package main

import "testing"

// TestVersionIsInjectable 锁定 version 可被 ldflags 覆盖。
//
// 背景：曾经写成 const，导致 GoReleaser 的 -X main.version 静默失效——
// 注入 v9.9.9 构建出的二进制仍然输出 v0.1.0。const 在编译期就被内联，
// linker 无从替换。
//
// 这个测试本身无法验证 ldflags（那需要真正构建），
// 但能锁住「默认值是 dev」这个信号：如果有人把它改回硬编码版本号，
// 测试会失败，提示他 -X 已经失效。
func TestVersionIsInjectable(t *testing.T) {
	if version != "dev" {
		t.Fatalf("version 默认值应为 %q，实际 %q。\n"+
			"若被改成具体版本号，说明可能又写成了 const 或硬编码，"+
			"ldflags -X main.version 将静默失效。", "dev", version)
	}
}
