// agent-doctor —— 声明式一致性检查器。
//
// 边界（见元仓 AGENTS.md「doctor 不做什么」）：
//
//	check      只读 · 离线 · 确定性
//	fix        写生成块与适配器，人手动跑，CI 不跑
//	bootstrap  下载与校验二进制，是独立脚本，不在本程序内
//
// 本程序不下载、不改文件（fix 子命令除外）、不执行 build/test/代码生成、
// 不解析自然语言、不读 git 历史。对同一份工作区快照，check 的输出必须完全相同。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/28257whoami/demisugar-doctor/internal/doctor"
)

const version = "v0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	root := fs.String("C", ".", "仓库根目录")
	cfgPath := fs.String("config", ".agent-doctor.yaml", "能力清单路径（相对仓库根）")
	todayStr := fs.String("today", "", "覆盖当前日期 YYYY-MM-DD，用于测试确定性")
	_ = fs.Parse(os.Args[2:])

	switch cmd {
	case "check":
		os.Exit(runCheck(*root, *cfgPath, *todayStr))
	case "fix":
		os.Exit(runFix(*root, *cfgPath))
	case "version":
		fmt.Println("agent-doctor", version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `agent-doctor `+version+`

用法:
  agent-doctor check [-C dir] [-today YYYY-MM-DD]   只读检查，CI 跑这个
  agent-doctor fix   [-C dir]                       重写生成块与适配器，人手动跑
  agent-doctor version

不做的事: 下载 · 执行 build/test/代码生成 · 解析自然语言 · 读 git 历史 ·
         验证 GitHub 分支保护
`)
}

func loadCfg(root, cfgPath string) (*doctor.Config, error) {
	p := cfgPath
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, cfgPath)
	}
	return doctor.LoadConfig(p)
}

func runCheck(root, cfgPath, todayStr string) int {
	cfg, err := loadCfg(root, cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-doctor:", err)
		return 1
	}

	today := time.Now()
	if todayStr != "" {
		t, err := doctor.ParseDate(todayStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agent-doctor: -today 格式应为 YYYY-MM-DD")
			return 2
		}
		today = t
	}

	rep := doctor.Run(root, cfg, today)
	rep.Write(os.Stdout, cfg.Repo)
	if rep.Failed() {
		return 1
	}
	return 0
}

func runFix(root, cfgPath string) int {
	cfg, err := loadCfg(root, cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-doctor:", err)
		return 1
	}
	changed, err := doctor.Fix(root, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-doctor fix:", err)
		return 1
	}
	if len(changed) == 0 {
		fmt.Println("agent-doctor fix: 无需改动")
		return 0
	}
	fmt.Printf("agent-doctor fix: 更新了 %d 个文件\n", len(changed))
	for _, c := range changed {
		fmt.Println("   ", c)
	}
	return 0
}
