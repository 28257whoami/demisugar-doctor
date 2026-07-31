package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// front matter 的 schema 按对象类型分开。
//
// 格式统一（都用 front matter）可以，生命周期统一不行——
// ADR、capability、交付记忆、长期记忆的状态词汇本就不同，
// 强行同构会逼着某一类用不合适的词。

// ADRFront —— 架构决策记录
type ADRFront struct {
	ID           string   `yaml:"id"`
	Status       string   `yaml:"status"` // proposed | accepted | superseded | retired
	Date         string   `yaml:"date"`
	Title        string   `yaml:"title"`
	Supersedes   []string `yaml:"supersedes"`
	SupersededBy string   `yaml:"superseded_by"`
}

var adrStatuses = map[string]bool{
	"proposed": true, "accepted": true, "superseded": true, "retired": true,
}

// DeliveryFront —— 交付记忆：一次需求的落地状态
type DeliveryFront struct {
	ID       string   `yaml:"id"`
	Status   string   `yaml:"status"` // open | closed
	Title    string   `yaml:"title"`
	ChangeID string   `yaml:"change_id"` // 跨仓关联，如 DS-2026-0017
	ClosedAt string   `yaml:"closed_at"`
	Commits  []string `yaml:"commits"`
}

var deliveryStatuses = map[string]bool{"open": true, "closed": true}

// LongTermFront —— 长期记忆：带失效条件，对付记忆腐坏
type LongTermFront struct {
	ID             string `yaml:"id"`
	Status         string `yaml:"status"` // valid | stale
	Title          string `yaml:"title"`
	Scope          string `yaml:"scope"`
	LastVerified   string `yaml:"last_verified"`
	InvalidateWhen string `yaml:"invalidate_when"`
}

var longTermStatuses = map[string]bool{"valid": true, "stale": true}

// splitFrontMatter 取出 --- 包裹的 YAML 头。
func splitFrontMatter(raw []byte) (string, bool) {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", false
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return "", false
	}
	return s[4 : 4+end], true
}

// validateFrontMatter 按类型校验单个文档的 front matter。
// 供 G 类独立调用，与索引生成解耦。
func validateFrontMatter(path, kind string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fm, ok := splitFrontMatter(raw)
	if !ok {
		return fmt.Errorf("缺少 front matter")
	}
	switch kind {
	case "adr":
		var a ADRFront
		if err := yaml.Unmarshal([]byte(fm), &a); err != nil {
			return err
		}
		if a.ID == "" || a.Title == "" || a.Date == "" {
			return fmt.Errorf("ADR front matter 缺少 id/title/date")
		}
		if !adrStatuses[a.Status] {
			return fmt.Errorf("非法 ADR 状态 %q（应为 proposed/accepted/superseded/retired）", a.Status)
		}
		if a.Status == "superseded" && a.SupersededBy == "" {
			return fmt.Errorf("状态为 superseded 但未填 superseded_by")
		}
	case "delivery":
		var d DeliveryFront
		if err := yaml.Unmarshal([]byte(fm), &d); err != nil {
			return err
		}
		if d.Title == "" {
			return fmt.Errorf("交付记忆缺少 title")
		}
		if !deliveryStatuses[d.Status] {
			return fmt.Errorf("非法交付状态 %q（应为 open/closed）", d.Status)
		}
		if d.Status == "closed" && d.ClosedAt == "" {
			return fmt.Errorf("状态为 closed 但未填 closed_at")
		}
	case "long_term":
		var l LongTermFront
		if err := yaml.Unmarshal([]byte(fm), &l); err != nil {
			return err
		}
		if l.Title == "" {
			return fmt.Errorf("长期记忆缺少 title")
		}
		if !longTermStatuses[l.Status] {
			return fmt.Errorf("非法长期记忆状态 %q（应为 valid/stale）", l.Status)
		}
		if l.LastVerified == "" {
			return fmt.Errorf("长期记忆缺少 last_verified（用于发现记忆腐坏）")
		}
		if _, err := time.Parse("2006-01-02", l.LastVerified); err != nil {
			return fmt.Errorf("last_verified 格式非法（要 YYYY-MM-DD）")
		}
		if l.InvalidateWhen == "" {
			return fmt.Errorf("长期记忆缺少 invalidate_when（避免记忆变成教条）")
		}
	default:
		return fmt.Errorf("未知的 front matter 类型 %q", kind)
	}
	return nil
}

// lastVerified 读出长期记忆的 last_verified 日期。
func lastVerified(path string) (time.Time, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	fm, ok := splitFrontMatter(raw)
	if !ok {
		return time.Time{}, false
	}
	var l LongTermFront
	if yaml.Unmarshal([]byte(fm), &l) != nil || l.LastVerified == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", l.LastVerified)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// renderBlock 按 kind 重算索引块内容。
// 这是 check 和 fix 共用的唯一实现——两者绝不能各算一遍。
func renderBlock(root string, gb GeneratedBlock) (string, error) {
	dir := filepath.Dir(filepath.Join(root, gb.File))
	switch gb.Kind {
	case "decisions-index":
		return renderDecisions(dir)
	case "delivery-index":
		return renderDelivery(dir)
	case "long-term-index":
		return renderLongTerm(dir)
	default:
		return "", fmt.Errorf("未知的生成块类型 %q", gb.Kind)
	}
}

func mdFiles(dirs ...string) ([]string, error) {
	var out []string
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".md") ||
				n == "README.md" || n == "TEMPLATE.md" ||
				strings.HasSuffix(n, "-index.md") {
				continue
			}
			out = append(out, filepath.Join(d, n))
		}
	}
	sort.Strings(out)
	return out, nil
}

func renderDecisions(dir string) (string, error) {
	files, err := mdFiles(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("| 编号 | 决策 | 状态 | 日期 |\n|---|---|---|---|\n")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		fm, ok := splitFrontMatter(raw)
		if !ok {
			return "", fmt.Errorf("%s 缺少 front matter", filepath.Base(f))
		}
		var a ADRFront
		if err := yaml.Unmarshal([]byte(fm), &a); err != nil {
			return "", fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if !adrStatuses[a.Status] {
			return "", fmt.Errorf("%s: 非法 ADR 状态 %q（应为 proposed/accepted/superseded/retired）",
				filepath.Base(f), a.Status)
		}
		note := a.Status
		if a.SupersededBy != "" {
			note = fmt.Sprintf("%s（被 %s 取代）", a.Status, a.SupersededBy)
		}
		fmt.Fprintf(&b, "| %s | [%s](%s) | %s | %s |\n",
			a.ID, a.Title, filepath.Base(f), note, a.Date)
	}
	return b.String(), nil
}

func renderDelivery(dir string) (string, error) {
	files, err := mdFiles(filepath.Join(dir, "open"), filepath.Join(dir, "closed"))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("| 变更号 | 标题 | 状态 | 关闭时间 |\n|---|---|---|---|\n")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		fm, ok := splitFrontMatter(raw)
		if !ok {
			return "", fmt.Errorf("%s 缺少 front matter", filepath.Base(f))
		}
		var d DeliveryFront
		if err := yaml.Unmarshal([]byte(fm), &d); err != nil {
			return "", fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if !deliveryStatuses[d.Status] {
			return "", fmt.Errorf("%s: 非法交付状态 %q（应为 open/closed）", filepath.Base(f), d.Status)
		}
		rel := filepath.Join(filepath.Base(filepath.Dir(f)), filepath.Base(f))
		fmt.Fprintf(&b, "| %s | [%s](%s) | %s | %s |\n",
			dash(d.ChangeID), d.Title, rel, d.Status, dash(d.ClosedAt))
	}
	return b.String(), nil
}

func renderLongTerm(dir string) (string, error) {
	files, err := mdFiles(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("| 记忆 | 范围 | 状态 | 最后验证 | 失效条件 |\n|---|---|---|---|---|\n")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		fm, ok := splitFrontMatter(raw)
		if !ok {
			return "", fmt.Errorf("%s 缺少 front matter", filepath.Base(f))
		}
		var l LongTermFront
		if err := yaml.Unmarshal([]byte(fm), &l); err != nil {
			return "", fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if !longTermStatuses[l.Status] {
			return "", fmt.Errorf("%s: 非法长期记忆状态 %q（应为 valid/stale）", filepath.Base(f), l.Status)
		}
		fmt.Fprintf(&b, "| [%s](%s) | %s | %s | %s | %s |\n",
			l.Title, filepath.Base(f), dash(l.Scope), l.Status, dash(l.LastVerified), dash(l.InvalidateWhen))
	}
	return b.String(), nil
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// Fix 重写全部生成块与适配器。
// 与 check 严格分离：CI 只跑 check，fix 由人手动跑并提交（gofmt 模式）。
func Fix(root string, cfg *Config) ([]string, error) {
	var changed []string

	for _, a := range cfg.Adapters {
		tpl, err := os.ReadFile(filepath.Join(root, a.Template))
		if err != nil {
			return changed, fmt.Errorf("读取模板 %s: %w", a.Template, err)
		}
		dst := filepath.Join(root, a.File)
		if cur, err := os.ReadFile(dst); err == nil && hash(cur) == hash(tpl) {
			continue
		}
		if err := os.WriteFile(dst, tpl, 0o644); err != nil {
			return changed, err
		}
		changed = append(changed, a.File)
	}

	for _, cap := range cfg.Capabilities {
		if cap.Status != StatusActive {
			continue
		}
		for _, gb := range cap.GeneratedBlocks {
			p := filepath.Join(root, gb.File)
			raw, err := os.ReadFile(p)
			if err != nil {
				return changed, fmt.Errorf("读取 %s: %w", gb.File, err)
			}
			want, err := renderBlock(root, gb)
			if err != nil {
				return changed, err
			}
			cur, ok := extractBlock(string(raw), gb.Block)
			if !ok {
				return changed, fmt.Errorf("%s 缺少生成块标记 agent-doctor:begin(%s)", gb.File, gb.Block)
			}
			if strings.TrimSpace(cur) == strings.TrimSpace(want) {
				continue
			}
			old := fmt.Sprintf("<!-- agent-doctor:begin(%s) -->\n%s<!-- agent-doctor:end -->", gb.Block, cur)
			new := fmt.Sprintf("<!-- agent-doctor:begin(%s) -->\n%s\n<!-- agent-doctor:end -->", gb.Block, strings.TrimSpace(want))
			if err := os.WriteFile(p, []byte(strings.Replace(string(raw), old, new, 1)), 0o644); err != nil {
				return changed, err
			}
			changed = append(changed, gb.File)
		}
	}
	return changed, nil
}
