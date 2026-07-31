package doctor

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Status 是 capability 的生命周期状态。
// 注意：capability / ADR / memory 三者格式统一，但生命周期各不相同，
// 不要强求同构（见元仓 AGENTS.md「schema 边界」）。
type Status string

const (
	StatusPlanned       Status = "planned"
	StatusActive        Status = "active"
	StatusNotApplicable Status = "not_applicable"
	StatusRetired       Status = "retired"
)

func (s Status) valid() bool {
	switch s {
	case StatusPlanned, StatusActive, StatusNotApplicable, StatusRetired:
		return true
	}
	return false
}

// Deferral 是一次显式推迟。doctor 只验证，绝不代写——
// doctor 对工作区快照必须是纯函数，不能自己记状态。
type Deferral struct {
	From        string `yaml:"from"`
	To          string `yaml:"to"`
	Reason      string `yaml:"reason"`
	DecisionRef string `yaml:"decision_ref"`
}

type Capability struct {
	Status    Status     `yaml:"status"`
	ReviewBy  string     `yaml:"review_by"`
	Milestone string     `yaml:"milestone"`
	Deferrals []Deferral `yaml:"deferrals"`

	// Entry 是仓内稳定入口。CI 只调用它，命令细节封在里面，
	// 避免 .agent-doctor.yaml 和 workflow 各写一份命令产生漂移。
	Entry string `yaml:"entry"`
	CIJob string `yaml:"ci_job"`

	Files           []string         `yaml:"files"`
	GeneratedBlocks []GeneratedBlock `yaml:"generated_blocks"`
}

type GeneratedBlock struct {
	File  string `yaml:"file"`
	Block string `yaml:"block"`
	Kind  string `yaml:"kind"` // decisions-index | delivery-index | long-term-index
}

// FrontMatterSet 声明一组需要独立校验 front matter 的文档。
//
// 与 generated_blocks 分开：即使某个目录不参与索引生成，
// 它的 front matter 也必须合法。否则 G 类只会在生成索引时顺带校验，
// 没有索引的目录就永远漏检。
type FrontMatterSet struct {
	Dir  string `yaml:"dir"`
	Kind string `yaml:"kind"` // adr | delivery | long_term

	// StaleAfterDays 仅对 long_term 生效：last_verified 超过这个天数
	// 就告警，用于发现记忆腐坏。0 表示不检查。
	StaleAfterDays int `yaml:"stale_after_days"`
}

// ContextProfile 声明某类任务的必读上下文集合。
// V1 只报告规模，不设阈值——token 数依赖 tokenizer，先测真实数字。
type ContextProfile struct {
	Name  string   `yaml:"name"`
	Files []string `yaml:"files"`
}

type Adapter struct {
	File     string `yaml:"file"`
	Template string `yaml:"template"`
}

type Config struct {
	SchemaVersion int    `yaml:"schema_version"`
	Repo          string `yaml:"repo"`
	RuleSource    string `yaml:"rule_source"`

	Adapters        []Adapter             `yaml:"adapters"`
	Capabilities    map[string]Capability `yaml:"capabilities"`
	ContextProfiles []ContextProfile      `yaml:"context_profiles"`
	FrontMatter     []FrontMatterSet      `yaml:"front_matter"`

	// DocRoots 限定 B 类链接检查的扫描范围，避免误扫 node_modules。
	DocRoots []string `yaml:"doc_roots"`

	// DocExclude 排除路径前缀。测试 fixture 里的故障是【故意】埋的，
	// 扫进来会让 doctor 报出自己的测试数据。
	//
	// 这是个逃生口，容易被滥用成「哪个目录报错就排除哪个」，所以：
	//   - 必须是非空相对目录，禁止 "." / ".." / 绝对路径
	//   - 必须写 reason
	//   - 报告中列出排除了哪些目录、各多少文件
	DocExclude []ExcludeRule `yaml:"doc_exclude"`
}

// ExcludeRule 一条排除规则。必须写理由——无理由的排除等于关掉检查。
type ExcludeRule struct {
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
}

func validateExcludes(rules []ExcludeRule) error {
	for i, e := range rules {
		p := strings.TrimSpace(e.Path)
		switch {
		case p == "":
			return fmt.Errorf("doc_exclude[%d]: path 不能为空", i)
		case p == "." || p == "./" || p == "..":
			return fmt.Errorf("doc_exclude[%d]: 禁止排除 %q——那等于关掉整个 B 类检查", i, p)
		case strings.HasPrefix(p, "/"):
			return fmt.Errorf("doc_exclude[%d]: 必须是相对路径，收到 %q", i, p)
		case strings.Contains(p, ".."):
			return fmt.Errorf("doc_exclude[%d]: 路径不得包含 ..", i)
		case strings.TrimSpace(e.Reason) == "":
			return fmt.Errorf("doc_exclude[%d] (%s): 必须写 reason", i, p)
		}
	}
	return nil
}

func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	if c.SchemaVersion != 1 {
		return nil, fmt.Errorf("%s: 不支持的 schema_version %d（本 doctor 支持 1）", path, c.SchemaVersion)
	}
	if err := validateExcludes(c.DocExclude); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// ParseDate 统一日期格式，避免各处自己解析。
func ParseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}
