# AGENTS.md — agent-doctor

声明式一致性检查器。**验证**被声明的语义契约，**不定义**任何业务格式。

本仓是通用工具，与具体产品无关 —— 它从 `demisugar-workspace` 拆出来，
就是因为工具和产品机密不该放在一起（那边有 800+ 行产品定稿）。
本仓可以公开发布二进制，消费方无需任何凭据即可下载。

## 三个入口，职责互斥

```
agent-doctor check      只读 · 离线 · 确定性 · CI 跑这个
agent-doctor fix        写生成块与适配器 · 人手动跑 · CI 不跑
agent-doctor-bootstrap  下载 + SHA256 校验 · 消费方仓库里的脚本 · 唯一联网环节
```

生成物走 gofmt 模式：**开发者本地跑 `fix` 并提交，CI 跑 `check` 发现 diff 就失败。**

## 明确不做的事

只列检查项，工具一定会膨胀 —— 每次有人说「顺手也检查一下 X」都很难拒绝，
因为单看每条都合理。所以非目标与目标同等地位：

| 不做 | 该由谁做 |
|---|---|
| 下载任何东西 | 消费方仓库的 `tools/agent-doctor-bootstrap` |
| 修改任何文件 | `agent-doctor fix`（人手动跑） |
| 执行 build / test / 代码生成 | 独立 CI job |
| 解析自然语言、猜测意图 | 不做。可验证承诺必须在清单中显式声明 |
| 扫描反引号猜命令或路径 | 不做。只解析显式 Markdown 链接与清单声明 |
| 解析任意 shell 命令 | 只看 step 的 run/uses 是否出现稳定入口 |
| 验证 GitHub 分支保护状态 | 独立的 GitHub 配置审计 |
| 记录、推断或管理项目计划状态 | 人写进 `deferrals`，doctor 只验证 |
| 读 Git 历史 | 不做 |

**判据：给定同一份工作区文件，`check` 的输出必须完全相同**，与 git 状态、
网络、时间无关。唯一例外是 `review_by` 需要当前日期，通过 `-today` 显式传入以便测试。

> 曾经踩过：三处直接 `range` map 导致跨进程输出顺序每次不同。
> Go 的 map 遍历是随机化的，**同进程跑两次测不出来**。所有遍历必须先排序。

## 检查项

| 类 | 检查 | 边界 |
|---|---|---|
| A 入口 | 规则真源存在；**适配器** == 模板 hash | 只约束适配器，不约束消费方 `AGENTS.md` 正文 |
| B 引用 | 显式 Markdown 链接目标存在；`capability:` 引用已登记 | 不解析反引号；跳过围栏代码块 |
| C 索引 | 生成块重算无 diff | 生成，不维护两份真源 |
| E 门禁 | 解析 workflow YAML，确认 `jobs.<ci_job>.steps` 真实执行了 `entry` | 只看 run/uses，不理解 shell 语义 |
| F 上下文 | 按 `context_profiles` 报告规模 | 只报告，不设阈值 |
| G 清单 | `review_by` 未过期；`deferrals` 字段完整；front matter schema 合法 | |
| G5 腐坏 | 长期记忆 `last_verified` 超过 `stale_after_days` 则告警 | 仅告警，不阻断 |

**未实现**：D 类（OpenAPI 源 hash / lock 匹配）。需要消费方的 OpenAPI 与
lock 都就位，登记为消费方的 `contract_consistency` capability。

D 类的「重新生成无 diff」需要真正执行生成器，属于独立 CI job，不在本工具内 ——
不能一边声称不执行命令，一边让它跑生成。

> **纪律**：未实现的检查必须在此标注，并在消费方能力清单中登记。
> 声明与实现分叉是本工具最致命的失信方式 —— 它的全部价值建立在「声明 == 行为」上。

## capability 生命周期

```
planned | active | not_applicable | retired
```

`planned` 必须带 `review_by`。到期即 FAIL，含义是**必须重新决策**，
不等于必须立刻实现。消解方式两条：转 `active`，或追加一条 `deferrals`
写明 `reason` 与 `decision_ref`。

推迟历史由人显式声明，doctor 只验证和告警，**不替人记录** ——
它对工作区快照必须是纯函数，不能自己攒状态。

## schema 边界：格式统一，生命周期不统一

| 对象 | 状态词汇 | 特有字段 |
|---|---|---|
| ADR | `proposed \| accepted \| superseded \| retired` | `supersedes` / `superseded_by` |
| capability | `planned \| active \| not_applicable \| retired` | `review_by` / `deferrals` / `entry` |
| 交付记忆 | `open \| closed` | `closed_at` / `change_id` / `commits` |
| 长期记忆 | `valid \| stale` | `scope` / `last_verified` / `invalidate_when` |

强行同构会逼着某一类用不合适的词。`active` 在 capability 里表示「已启用」，
交付记忆若也用它表示「进行中」，就是同一个词两种含义。

## 输出格式

结论行刻意**不用 `PASS`** —— 那容易被读成「项目准备完成」：

```
CONSISTENT
capability: active 2 / planned 7
```

`CONSISTENT` 只断言「当前声明与实现自洽」。完成度看 active/planned 比例。

## doc_exclude 是逃生口，受约束

容易被滥用成「哪个目录报错就排除哪个」，所以：

- 必须是非空相对路径，禁止 `.` / `..` / 绝对路径
- 必须写 `reason`
- 报告末尾列出排除了哪些目录、各多少文件 —— 逃生口不能是暗的

## 测试

`internal/doctor/testdata/` 下两组 fixture：

- `green/` —— 一切正常，锁定「不应误报」
- `red/` —— 每类各埋一个故障，锁定「不应漏报」

覆盖：A2 适配器偏离、B1 坏链接、B2 未登记能力、C1 索引过期、
E1 job 未执行 entry（写在注释里不算）、G2 review_by 过期、
G3 deferral 字段缺失、G4 front matter 非法、G5 记忆腐坏。

**漏报比误报危险**：绿色的 CI 会让人以为规则还在生效。
新增检查类时必须同步补 red fixture。

## 发布

打 tag 触发 GoReleaser，产出四平台静态二进制（linux/darwin × amd64/arm64，
`CGO_ENABLED=0`）与 `checksums.txt`。

消费方在 `.agent-doctor-version` 里 pin 版本 + SHA256，升级是显式 PR。
代价：加一条检查 = 各消费仓一次 PR。这是「升级必须可审计」的必然成本。

```bash
git tag v0.2.0 && git push origin v0.2.0
```

## 本地开发

```bash
go build ./...
go test ./...
go run ./cmd/agent-doctor check -C /path/to/repo -today 2026-07-31
go run ./cmd/agent-doctor fix   -C /path/to/repo
```

`-today` 让检查结果可复现，测试必须使用它。
