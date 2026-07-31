package doctor

import (
	"fmt"
	"io"
	"sort"
)

type Severity int

const (
	SevPass Severity = iota
	SevSkip
	SevWarn
	SevFail
)

func (s Severity) mark() string {
	switch s {
	case SevPass:
		return "✓"
	case SevSkip:
		return "-"
	case SevWarn:
		return "⚠"
	default:
		return "✗"
	}
}

type Finding struct {
	Check    string // A1 / B2 / C1 ...
	Severity Severity
	Where    string // 文件[:行]
	Message  string
}

type Report struct {
	Findings []Finding
	// Info 是纯信息输出（F 类的上下文规模），不参与通过与否的判定。
	Info []string

	// 能力统计。PASS 只代表「当前声明自洽」，不代表「项目准备完成」，
	// 所以结论行必须同时给出 active/planned 比例，避免被误读成后者。
	NActive, NPlanned, NOther int
	Excluded                  []string
}

func (r *Report) Add(check string, sev Severity, where, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{
		Check:    check,
		Severity: sev,
		Where:    where,
		Message:  fmt.Sprintf(format, args...),
	})
}

func (r *Report) Failed() bool {
	for _, f := range r.Findings {
		if f.Severity == SevFail {
			return true
		}
	}
	return false
}

var checkGroups = map[byte]string{
	'A': "入口完整性",
	'B': "引用有效性",
	'C': "索引一致性",
	'D': "契约一致性",
	'E': "门禁真实性",
	'F': "上下文规模",
	'G': "清单合法性",
}

func (r *Report) Write(w io.Writer, repo string) {
	fmt.Fprintf(w, "agent-doctor  %s\n\n", repo)

	byGroup := map[byte][]Finding{}
	for _, f := range r.Findings {
		g := f.Check[0]
		byGroup[g] = append(byGroup[g], f)
	}

	groups := make([]byte, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })

	var nFail, nWarn, nSkip int
	for _, g := range groups {
		items := byGroup[g]
		worst := SevPass
		for _, f := range items {
			if f.Severity > worst {
				worst = f.Severity
			}
		}
		fmt.Fprintf(w, "[%c] %-12s %s\n", g, checkGroups[g], worst.mark())
		for _, f := range items {
			switch f.Severity {
			case SevFail:
				nFail++
			case SevWarn:
				nWarn++
			case SevSkip:
				nSkip++
			case SevPass:
				continue // 通过项不逐条列出，避免刷屏
			}
			if f.Where != "" {
				fmt.Fprintf(w, "    %s  %-4s %s\n        → %s\n", f.Severity.mark(), f.Check, f.Where, f.Message)
			} else {
				fmt.Fprintf(w, "    %s  %-4s %s\n", f.Severity.mark(), f.Check, f.Message)
			}
		}
	}

	if len(r.Info) > 0 {
		fmt.Fprintf(w, "\n[F] 上下文规模（仅报告，V1 不设阈值）\n")
		for _, s := range r.Info {
			fmt.Fprintf(w, "    %s\n", s)
		}
	}

	if len(r.Excluded) > 0 {
		fmt.Fprintf(w, "\n[排除] 以下路径未参与链接检查\n")
		for _, e := range r.Excluded {
			fmt.Fprintf(w, "    %s\n", e)
		}
	}

	fmt.Fprintln(w)
	if nFail > 0 {
		fmt.Fprintf(w, "INCONSISTENT  %d 个错误", nFail)
	} else {
		// 刻意不用 PASS：它容易被读成「项目准备完成」。
		// CONSISTENT 只断言「声明与实现自洽」。
		fmt.Fprintf(w, "CONSISTENT")
	}
	if nWarn > 0 {
		fmt.Fprintf(w, "，%d 个警告", nWarn)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "capability: active %d / planned %d", r.NActive, r.NPlanned)
	if r.NOther > 0 {
		fmt.Fprintf(w, " / 其他 %d", r.NOther)
	}
	fmt.Fprintln(w)
	if nSkip > 0 {
		fmt.Fprintf(w, "（%d 项因 planned 或目录为空而跳过）\n", nSkip)
	}
}
