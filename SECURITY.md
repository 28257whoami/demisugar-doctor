# 安全说明

## 本仓公开的边界

本仓是通用一致性检查工具，**不含任何产品内容或密钥**：

- 不含业务代码、产品设计、需求文档
- 不含凭据、token、连接串
- `internal/doctor/testdata/` 里的故障是**故意埋的测试夹具**，
  不是真实配置

产品相关内容在私有仓 `demisugar-workspace`，与本仓严格分离。

**公开是不可撤回的**：即使将来转私有，已有的 commit 历史、Release
和 fork 仍然存在于外部。因此任何提交前必须确认不含上述内容。

## 报告漏洞

通过 GitHub Security Advisory 私下报告，不要开公开 issue。

## 依赖

- 依赖极少（仅 `gopkg.in/yaml.v3`），刻意保持
- Dependabot 每周检查
- 发布前 CI 跑 `go build` / `go vet` / `go test`

## 二进制完整性

Release 产出四平台静态二进制与 `checksums.txt`。
消费方在 `.agent-doctor-version` 中 pin 版本 + SHA256，
`agent-doctor-bootstrap` 下载后强制校验，不匹配即丢弃。

**升级必须是显式 PR**，不存在自动拉取最新版的路径。
