package doctor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// capIDs 返回排序后的 capability ID。
//
// 必须排序：Go 的 map 遍历顺序是【随机化】的，直接 range 会让同一份工作区
// 每次跑出不同的输出顺序，破坏「doctor 是纯函数」这条核心承诺。
// 同进程内跑两次也测不出来——随机化是跨进程的。
func capIDs(m map[string]Capability) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Run 执行全部 V1 检查。
//
// 边界（与元仓 AGENTS.md 的「doctor 不做什么」一致）：
//   - 只读工作区文件，不写任何东西
//   - 不联网、不执行 build/test/代码生成
//   - 不解析自然语言、不猜命令、不读 git 历史
//   - 对同一份工作区快照，输出必须完全确定（today 显式传入，便于测试）
func Run(root string, cfg *Config, today time.Time) *Report {
	r := &Report{}
	checkAdapters(r, root, cfg)
	checkLinks(r, root, cfg)
	checkGeneratedBlocks(r, root, cfg)
	checkFrontMatter(r, root, cfg, today)
	checkCIEntries(r, root, cfg)
	checkCapabilities(r, root, cfg, today)
	reportContext(r, root, cfg)
	return r
}

// checkFrontMatter 独立校验各文档集合的 front matter。
//
// 必须独立于 generated_blocks——否则没有索引的目录（如 long-term）
// 永远不会被检查，G 类的「front matter schema 合法」就是空头声明。
func checkFrontMatter(r *Report, root string, cfg *Config, today time.Time) {
	for _, set := range cfg.FrontMatter {
		dir := filepath.Join(root, set.Dir)
		files, err := mdFiles(dir)
		if err != nil {
			r.Add("G4", SevFail, set.Dir, "无法读取 front_matter 目录: %v", err)
			continue
		}
		if len(files) == 0 {
			r.Add("G4", SevSkip, set.Dir, "目录为空")
			continue
		}
		bad := 0
		for _, f := range files {
			rel, _ := filepath.Rel(root, f)
			if err := validateFrontMatter(f, set.Kind); err != nil {
				r.Add("G4", SevFail, rel, "%v", err)
				bad++
				continue
			}
			// 记忆腐坏检查：长期未验证的记忆可能已经失效，
			// 但它仍在被当成有效规则读取，这比没有记忆更危险。
			if set.Kind == "long_term" && set.StaleAfterDays > 0 {
				if lv, ok := lastVerified(f); ok {
					if age := int(today.Sub(lv).Hours() / 24); age > set.StaleAfterDays {
						r.Add("G5", SevWarn, rel,
							"距上次验证已 %d 天（阈值 %d），请确认是否仍然成立，或将 status 改为 stale",
							age, set.StaleAfterDays)
					}
				}
			}
		}
		if bad == 0 {
			r.Add("G4", SevPass, set.Dir, "%d 个文档的 front matter 合法（%s）", len(files), set.Kind)
		}
	}
}

// ── A 类：入口完整性 ────────────────────────────────────────────
//
// 只比对适配器与模板的 hash。不扫关键词、不设行数上限——
// 那类启发式既会误报，也能被改写绕过。
// 注意：模板只约束适配器，不约束各仓真正的 AGENTS.md 正文。
func checkAdapters(r *Report, root string, cfg *Config) {
	if cfg.RuleSource == "" {
		r.Add("A1", SevFail, "", "配置未声明 rule_source")
	} else if !exists(filepath.Join(root, cfg.RuleSource)) {
		r.Add("A1", SevFail, cfg.RuleSource, "规则真源文件不存在")
	} else {
		r.Add("A1", SevPass, cfg.RuleSource, "规则真源存在")
	}

	for _, a := range cfg.Adapters {
		got, err := os.ReadFile(filepath.Join(root, a.File))
		if err != nil {
			r.Add("A2", SevFail, a.File, "适配器文件缺失")
			continue
		}
		want, err := os.ReadFile(filepath.Join(root, a.Template))
		if err != nil {
			r.Add("A2", SevFail, a.Template, "模板文件缺失，无法校验适配器")
			continue
		}
		if hash(got) != hash(want) {
			r.Add("A2", SevFail, a.File,
				"适配器内容与模板 %s 不一致（适配器只应指向真源，不得承载规则正文；跑 agent-doctor fix 同步）", a.Template)
			continue
		}
		r.Add("A2", SevPass, a.File, "适配器与模板一致")
	}
}

// ── B 类：引用有效性 ───────────────────────────────────────────
//
// 只解析【显式 Markdown 链接】和 capability: 引用。
// 绝不扫描反引号猜命令或路径——那会把示例代码、说明性路径全部误报。

var (
	reMDLink     = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	reCapability = regexp.MustCompile(`^capability:([a-z0-9_]+)$`)
)

func checkLinks(r *Report, root string, cfg *Config) {
	roots := cfg.DocRoots
	if len(roots) == 0 {
		roots = []string{"."}
	}
	seen := 0
	excluded := map[string]int{}
	for _, dr := range roots {
		_ = filepath.WalkDir(filepath.Join(root, dr), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
				if d != nil && d.IsDir() && skipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			skipped := false
			for _, ex := range cfg.DocExclude {
				if strings.HasPrefix(filepath.ToSlash(rel), strings.TrimSuffix(ex.Path, "/")) {
					excluded[ex.Path+"|"+ex.Reason]++
					skipped = true
					break
				}
			}
			if skipped {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			// 跳过围栏代码块：块内的链接是【语法演示】而非真实引用，
			// 扫进来就是误报——文档里教人怎么写 capability: 引用，
			// 反而会被报成引用了未登记的能力。
			inFence := false
			for i, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "```") {
					inFence = !inFence
					continue
				}
				if inFence {
					continue
				}
				for _, m := range reMDLink.FindAllStringSubmatch(line, -1) {
					target := m[1]
					where := fmt.Sprintf("%s:%d", rel, i+1)
					seen++

					if c := reCapability.FindStringSubmatch(target); c != nil {
						if _, ok := cfg.Capabilities[c[1]]; !ok {
							r.Add("B2", SevFail, where, "引用了未登记的 capability %q", c[1])
						}
						continue
					}
					if isExternal(target) {
						continue
					}
					clean := strings.SplitN(target, "#", 2)[0]
					if clean == "" {
						continue // 纯锚点
					}
					if !exists(filepath.Join(filepath.Dir(p), clean)) {
						r.Add("B1", SevFail, where, "链接目标不存在: %s", clean)
					}
				}
			}
			return nil
		})
	}
	r.Add("B1", SevPass, "", "已检查 %d 个显式链接", seen)

	// 排除清单必须可见——逃生口不能是暗的。
	keys := make([]string, 0, len(excluded))
	for k := range excluded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.SplitN(k, "|", 2)
		r.Excluded = append(r.Excluded,
			fmt.Sprintf("%-40s %3d 个文件   %s", parts[0], excluded[k], parts[1]))
	}
}

// ── C 类：索引一致性 ───────────────────────────────────────────
//
// 索引由 doctor 生成，不再人工维护两份真源后互相比对。
// check 只验证「重算结果 == 文件现状」，fix 负责写入。

var reBlock = regexp.MustCompile(
	`(?s)<!-- agent-doctor:begin\(([a-z0-9-]+)\) -->\n(.*?)<!-- agent-doctor:end -->`)

func checkGeneratedBlocks(r *Report, root string, cfg *Config) {
	for _, name := range capIDs(cfg.Capabilities) {
		cap := cfg.Capabilities[name]
		if cap.Status != StatusActive {
			continue
		}
		for _, gb := range cap.GeneratedBlocks {
			raw, err := os.ReadFile(filepath.Join(root, gb.File))
			if err != nil {
				r.Add("C1", SevFail, gb.File, "capability %s 声明的索引文件不存在", name)
				continue
			}
			cur, ok := extractBlock(string(raw), gb.Block)
			if !ok {
				r.Add("C1", SevFail, gb.File, "缺少生成块标记 agent-doctor:begin(%s)", gb.Block)
				continue
			}
			want, err := renderBlock(root, gb)
			if err != nil {
				r.Add("C1", SevFail, gb.File, "重算索引失败: %v", err)
				continue
			}
			if strings.TrimSpace(cur) != strings.TrimSpace(want) {
				r.Add("C1", SevFail, gb.File, "生成块 %s 已过期，跑 agent-doctor fix", gb.Block)
				continue
			}
			r.Add("C1", SevPass, gb.File, "生成块 %s 最新", gb.Block)
		}
	}
}

// ── E 类：门禁真实性 ───────────────────────────────────────────
//
// 解析 workflow YAML，定位 jobs.<ci_job>.steps，确认其中真实执行了稳定入口。
//
// 不能用「整个 workflow 文本包含 entry 字符串」——那样写在注释里也会 PASS，
// ci_job 会变成死字段，E 类形同虚设。
//
// 但也不解析任意 shell：只看 step 的 run/uses 字段是否出现 entry，
// 不理解 shell 语义。
func checkCIEntries(r *Report, root string, cfg *Config) {
	jobs := loadWorkflowJobs(filepath.Join(root, ".github", "workflows"))

	for _, name := range capIDs(cfg.Capabilities) {
		cap := cfg.Capabilities[name]
		if cap.Status != StatusActive || cap.CIJob == "" {
			continue
		}
		steps, ok := jobs[cap.CIJob]
		if !ok {
			r.Add("E1", SevFail, ".github/workflows/",
				"capability %s 声明 ci_job=%q，但没有任何 workflow 定义这个 job", name, cap.CIJob)
			continue
		}
		if cap.Entry == "" {
			r.Add("E1", SevPass, "", "capability %s 的 job %s 存在", name, cap.CIJob)
			continue
		}
		found := false
		for _, s := range steps {
			if strings.Contains(s, cap.Entry) {
				found = true
				break
			}
		}
		if !found {
			r.Add("E1", SevFail, ".github/workflows/",
				"job %q 的 steps 中没有执行 capability %s 的稳定入口 %s", cap.CIJob, name, cap.Entry)
			continue
		}
		r.Add("E1", SevPass, "", "capability %s 的稳定入口已在 job %s 中执行", name, cap.CIJob)
	}
}

// workflowFile 只反序列化我们需要的部分。
type workflowFile struct {
	Jobs map[string]struct {
		Steps []struct {
			Run  string `yaml:"run"`
			Uses string `yaml:"uses"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// loadWorkflowJobs 返回 job 名 -> 该 job 各 step 的 run/uses 文本。
func loadWorkflowJobs(dir string) map[string][]string {
	out := map[string][]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		var wf workflowFile
		if yaml.Unmarshal(raw, &wf) != nil {
			continue
		}
		for job, spec := range wf.Jobs {
			var cmds []string
			for _, s := range spec.Steps {
				if s.Run != "" {
					cmds = append(cmds, s.Run)
				}
				if s.Uses != "" {
					cmds = append(cmds, s.Uses)
				}
			}
			out[job] = cmds
		}
	}
	return out
}

// ── G 类：清单合法性 ───────────────────────────────────────────
func checkCapabilities(r *Report, root string, cfg *Config, today time.Time) {
	if len(cfg.Capabilities) == 0 {
		r.Add("G1", SevWarn, ".agent-doctor.yaml", "未声明任何 capability")
		return
	}
	for _, name := range capIDs(cfg.Capabilities) {
		cap := cfg.Capabilities[name]
		if !cap.Status.valid() {
			r.Add("G1", SevFail, ".agent-doctor.yaml", "capability %s 的 status %q 非法", name, cap.Status)
			continue
		}

		switch cap.Status {
		case StatusPlanned:
			if cap.ReviewBy == "" {
				r.Add("G2", SevFail, ".agent-doctor.yaml",
					"capability %s 是 planned 但没有 review_by，会变成永久跳过", name)
				continue
			}
			due, err := ParseDate(cap.ReviewBy)
			if err != nil {
				r.Add("G2", SevFail, ".agent-doctor.yaml", "capability %s 的 review_by 格式非法（要 YYYY-MM-DD）", name)
				continue
			}
			if today.After(due) {
				// 到期意味着「必须重新决策」，不等于必须立刻实现。
				// 消解方式：转 active，或追加一条 deferrals 说明理由。
				r.Add("G2", SevFail, ".agent-doctor.yaml",
					"capability %s 的 review_by %s 已过期，需重新决策（转 active，或追加 deferrals 并写明理由）",
					name, cap.ReviewBy)
				continue
			}
			// 校验推迟记录的完整性：缺 reason / decision_ref 的推迟
			// 等于没有理由的延期，那正是 review_by 想防的事。
			for i, d := range cap.Deferrals {
				switch {
				case d.From == "" || d.To == "":
					r.Add("G3", SevFail, ".agent-doctor.yaml",
						"capability %s 的第 %d 条 deferral 缺 from/to", name, i+1)
				case d.Reason == "":
					r.Add("G3", SevFail, ".agent-doctor.yaml",
						"capability %s 的第 %d 条 deferral 没写 reason", name, i+1)
				case d.DecisionRef == "":
					r.Add("G3", SevFail, ".agent-doctor.yaml",
						"capability %s 的第 %d 条 deferral 没写 decision_ref（推迟要有据可查）", name, i+1)
				default:
					if _, err := ParseDate(d.From); err != nil {
						r.Add("G3", SevFail, ".agent-doctor.yaml",
							"capability %s 的第 %d 条 deferral 的 from 日期格式非法", name, i+1)
					}
					if _, err := ParseDate(d.To); err != nil {
						r.Add("G3", SevFail, ".agent-doctor.yaml",
							"capability %s 的第 %d 条 deferral 的 to 日期格式非法", name, i+1)
					}
				}
			}
			if len(cap.Deferrals) >= 3 {
				r.Add("G3", SevWarn, ".agent-doctor.yaml",
					"capability %s 已推迟 %d 次，建议重新评估是否该转 not_applicable", name, len(cap.Deferrals))
			}
			r.Add("G2", SevSkip, "", "capability %s 计划中（复查 %s）", name, cap.ReviewBy)

		case StatusActive:
			missing := 0
			for _, f := range cap.Files {
				if !exists(filepath.Join(root, f)) {
					r.Add("B3", SevFail, f, "capability %s 已启用，但声明的文件不存在", name)
					missing++
				}
			}
			if cap.Entry != "" && !exists(filepath.Join(root, cap.Entry)) {
				r.Add("B3", SevFail, cap.Entry, "capability %s 已启用，但稳定入口不存在", name)
				missing++
			}
			if missing == 0 {
				r.Add("B3", SevPass, "", "capability %s 的声明文件齐全", name)
			}

		case StatusNotApplicable, StatusRetired:
			r.Add("G1", SevSkip, "", "capability %s: %s", name, cap.Status)
		}

		switch cap.Status {
		case StatusActive:
			r.NActive++
		case StatusPlanned:
			r.NPlanned++
		default:
			r.NOther++
		}
	}
}

// ── F 类：上下文规模 ───────────────────────────────────────────
//
// V1 只报告，不设阈值。行数可以靠写长行绕过，token 又依赖 tokenizer，
// 所以同时给出文件数 / 字节数 / 估算 token，并标注估算方式。
// 将来定预算时再固定 tokenizer 及其版本。
const tokenEstimatorNote = "估算方式 v1: CJK 字符 ≈ 1 token，其余 ≈ 4 字节/token"

func reportContext(r *Report, root string, cfg *Config) {
	for _, p := range cfg.ContextProfiles {
		var bytes, tokens, files int
		var missing []string
		for _, f := range p.Files {
			raw, err := os.ReadFile(filepath.Join(root, f))
			if err != nil {
				missing = append(missing, f)
				continue
			}
			files++
			bytes += len(raw)
			tokens += estimateTokens(string(raw))
		}
		r.Info = append(r.Info, fmt.Sprintf(
			"%-16s %d 文件  %d 字节  ~%d tokens", p.Name, files, bytes, tokens))
		for _, m := range missing {
			r.Add("F1", SevFail, m, "context_profile %q 引用的文件不存在", p.Name)
		}
	}
	if len(cfg.ContextProfiles) > 0 {
		r.Info = append(r.Info, tokenEstimatorNote)
	}
}

func estimateTokens(s string) int {
	var cjk, other int
	for _, ru := range s {
		if ru >= 0x4E00 && ru <= 0x9FFF || ru >= 0x3000 && ru <= 0x303F {
			cjk++
		} else {
			other += utf8.RuneLen(ru)
		}
	}
	return cjk + other/4
}

// ── 工具 ──────────────────────────────────────────────────────

func hash(b []byte) string {
	// 归一化行尾后再算 hash，避免 CRLF 导致误报
	n := strings.ReplaceAll(string(b), "\r\n", "\n")
	sum := sha256.Sum256([]byte(strings.TrimSpace(n)))
	return hex.EncodeToString(sum[:])
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isExternal(t string) bool {
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") ||
		strings.HasPrefix(t, "mailto:") || strings.HasPrefix(t, "#")
}

func skipDir(name string) bool {
	switch name {
	case "node_modules", ".git", "dist", "build", "vendor", ".bin":
		return true
	}
	return false
}

func extractBlock(content, block string) (string, bool) {
	for _, m := range reBlock.FindAllStringSubmatch(content, -1) {
		if m[1] == block {
			return m[2], true
		}
	}
	return "", false
}
