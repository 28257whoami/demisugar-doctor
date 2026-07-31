package doctor

import (
	"strings"
	"testing"
	"time"
)

// fixedToday 让检查结果可复现。doctor 对同一份工作区快照必须是纯函数，
// 唯一的外部输入就是「今天」，所以测试里必须固定它。
var fixedToday = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

func load(t *testing.T, dir string) *Config {
	t.Helper()
	cfg, err := LoadConfig(dir + "/.agent-doctor.yaml")
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	return cfg
}

// TestGreenFixture 锁定「一切正常时必须通过」。
// 这条防止 doctor 变得过度敏感，动不动就误报。
func TestGreenFixture(t *testing.T) {
	rep := Run("testdata/green", load(t, "testdata/green"), fixedToday)
	if rep.Failed() {
		var b strings.Builder
		rep.Write(&b, "green")
		t.Fatalf("green fixture 应当通过，实际失败:\n%s", b.String())
	}
}

// TestRedFixture 锁定「每类故障都必须被抓到」。
// 这条防止某个检查悄悄失效——那比没有检查更危险，
// 因为绿色的 CI 会让人以为规则还在生效。
func TestRedFixture(t *testing.T) {
	rep := Run("testdata/red", load(t, "testdata/red"), fixedToday)
	if !rep.Failed() {
		t.Fatal("red fixture 应当失败，实际通过了")
	}

	got := map[string]bool{}
	for _, f := range rep.Findings {
		if f.Severity == SevFail {
			got[f.Check] = true
		}
	}

	// 每一类都必须有故障被抓到。新增检查类时同步补 fixture。
	want := []struct {
		check string
		desc  string
	}{
		{"A2", "适配器偏离模板"},
		{"B1", "Markdown 链接指向不存在的文件"},
		{"B2", "引用了未登记的 capability"},
		{"C1", "生成块过期"},
		{"G2", "planned 的 review_by 已过期"},
		{"G3", "deferral 缺 reason / decision_ref"},
		{"G4", "front matter 非法"},
		{"E1", "ci_job 的 steps 未执行稳定入口（写在注释里不算）"},
	}
	for _, w := range want {
		if !got[w.check] {
			t.Errorf("检查 %s（%s）没有报出故障", w.check, w.desc)
		}
	}
}

// TestStaleMemoryWarning 锁定记忆腐坏告警。
// 长期未验证的记忆仍在被当成有效规则读取，比没有记忆更危险。
func TestStaleMemoryWarning(t *testing.T) {
	rep := Run("testdata/red", load(t, "testdata/red"), fixedToday)
	found := false
	for _, f := range rep.Findings {
		if f.Check == "G5" && f.Severity == SevWarn {
			found = true
		}
	}
	if !found {
		t.Error("last_verified 为 2020 年的长期记忆应触发 G5 腐坏告警")
	}
}

// TestExcludeValidation 锁定 doc_exclude 的约束。
// 它是逃生口，不加约束会被滥用成「哪个目录报错就排除哪个」。
func TestExcludeValidation(t *testing.T) {
	bad := [][]ExcludeRule{
		{{Path: "", Reason: "x"}},
		{{Path: ".", Reason: "x"}},
		{{Path: "/abs", Reason: "x"}},
		{{Path: "a/../b", Reason: "x"}},
		{{Path: "ok/", Reason: ""}},
	}
	for i, rules := range bad {
		if err := validateExcludes(rules); err == nil {
			t.Errorf("第 %d 组非法排除规则未被拒绝: %+v", i, rules)
		}
	}
	if err := validateExcludes([]ExcludeRule{{Path: "node_modules/", Reason: "第三方依赖"}}); err != nil {
		t.Errorf("合法规则被拒绝: %v", err)
	}
}

// TestStatusValidation 锁定四态词汇，防止有人悄悄加一个状态绕过检查。
func TestStatusValidation(t *testing.T) {
	valid := []Status{StatusPlanned, StatusActive, StatusNotApplicable, StatusRetired}
	for _, s := range valid {
		if !s.valid() {
			t.Errorf("%q 应当是合法状态", s)
		}
	}
	for _, s := range []Status{"", "enabled", "done", "todo"} {
		if s.valid() {
			t.Errorf("%q 不应当是合法状态", s)
		}
	}
}

// TestDeterminism 锁定纯函数性质：同一份快照跑两次结果必须一致。
func TestDeterminism(t *testing.T) {
	cfg := load(t, "testdata/green")
	var a, b strings.Builder
	Run("testdata/green", cfg, fixedToday).Write(&a, "x")
	Run("testdata/green", cfg, fixedToday).Write(&b, "x")
	if a.String() != b.String() {
		t.Fatal("同一份工作区两次 check 的输出不一致，doctor 不是纯函数")
	}
}

// TestFrontMatterSchemas 锁定三类对象的状态词汇互不混用。
// 格式统一可以，生命周期统一不可以——ADR 的 accepted 和
// capability 的 active 不是一回事。
func TestFrontMatterSchemas(t *testing.T) {
	cases := []struct {
		kind    string
		status  string
		wantErr bool
	}{
		{"adr", "accepted", false},
		{"adr", "active", true}, // active 是 capability 的词，ADR 不许用
		{"delivery", "open", false},
		{"delivery", "active", true}, // 避免与 capability 的 active 撞词
		{"long_term", "valid", false},
		{"long_term", "closed", true},
	}
	for _, c := range cases {
		var ok bool
		switch c.kind {
		case "adr":
			ok = adrStatuses[c.status]
		case "delivery":
			ok = deliveryStatuses[c.status]
		case "long_term":
			ok = longTermStatuses[c.status]
		}
		if ok == c.wantErr {
			t.Errorf("%s 的状态 %q: 合法性判断错误", c.kind, c.status)
		}
	}
}
