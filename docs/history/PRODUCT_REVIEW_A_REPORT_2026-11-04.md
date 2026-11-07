# 产品复审 A 批整改报告（2026-11-04）

> **DEPRECATED（2026-11-07）**：本文已移入 `docs/history/`，只作历史记录保留，不再更新，不作为现行依据。A 批整改的验收记录。原文未改；文内链接、旧术语与当时的状态描述按写作时理解。

## 1. 范围与版本

依据 `PRODUCT_REVIEW_PROPOSAL_2026-11-04.md` 的 A01–A04 与 `QA_REVIEW_PROPOSAL_2026-11-04.md` 的 A-T01–A-T03 完成整改。两份提案另补充了 B 的中断恢复要求，以及 C 的匹配冲突、扫描遗漏与实际旧客户端兼容验收要求；B/C 执行能力没有在本批实现。

开发基线为 `701cdb6d19927033e4e1fe713dcffee1bc6dce38`，验收使用该基线加本批未提交修改的客户端。构建环境为 macOS ARM64、Go 1.27.3；Bubble Tea v2.1.1、Bubbles v2.2.1、Lip Gloss v2.0.6，未升级依赖。真实目标为 Debian 13 ARM64（SSH 靶机 `debian-vm`，Valise 别名 `debian`），只读检查隔离实例 `openclaw-test`。

## 2. 修改与任务对应

| 任务 | 结果 | 主要文件 |
| --- | --- | --- |
| A01 / A-T01 | 概览与健康页共用合并发现、忽略与排序；会话发现不再追加到备份行；忽略数量包含会话发现；健康分栏号随布局计算 | `tui/health.go`、`tui/healthstate.go`、`tui/instance.go` |
| A02 / A-T02 | 健康子检查独立报告覆盖状态，失败不吞掉其他成功结果；审批未知不退成绿色；页面显示加载、失败、上次结果时间；过期与已离开页面的响应丢弃 | `service/health.go`、`service/health_read.go`、`tui/healthstate.go`、`tui/tui.go`、`cmd/valise/health.go` |
| A03 / A-T03 | 人类入口统一称“健康”，只读 `checkup` 称“会话与记忆检查”；规则页导航、给 agent 的提示与首次引导更新；审批说明如实表明当前没有执行入口 | `tui/rules.go`、`tui/chat.go`、`tui/checkup.go`、`tui/guide.go`、`tui/iset.go`、`service/health.go`、`cmd/valise/kit.go` |
| A04 / A-T03 | 备份选择页说明轻装/标准/完整/原样的内容，明确凭据与工作区；设置称“备份分组”，说明整组删除和下一组生效；CLI 帮助按用途分组 | `tui/pods.go`、`tui/iset.go`、`cmd/valise/main.go` |

`HealthReport` 新增可选 `checks` 字段，逐项记录 `ok`、`failed` 或 `unsupported` 及安全说明。发现严重性与检查完整性分别计算；忽略问题不会消除检查失败。错误只使用本地生成的原因与建议，不回显远端原始错误或未知审批命令的输出。

原有 CLI 子命令、RPC、技能 ID 和已有 JSON 字段保持兼容。CLI `health` 仍报告综合健康，`checkup` 单独提供会话与记忆指标；TUI 健康页合并两者。本批未改变金库格式、备份选择语义或恢复路径。

为满足单个 TUI Go 文件不超过 400 行的要求，将终端执行辅助代码移至 `tui/terminal.go`，将现有图标测试移至 `tui/icon_test.go`，没有引入新执行能力。

## 3. 自动验证

新增状态测试覆盖：

- 备份、服务、定时器、技能、审批任一读取失败，其他检查与本地恢复测试记录仍保留；错误中的模拟凭据不进入 JSON。
- 审批能力未声明与读取失败分开；已声明但无法判定时报告检查未完成，不能把 agent 的“需要批准”声明显示为绿色通过。
- 首次读取失败、失败后恢复、成功后刷新失败；旧结果保留并标出上次成功时间；无结果或未完成时不能显示正常结论。
- 两次刷新乱序返回时，旧成功与旧失败均不能覆盖新结果；父页面在导航栈中仍收到自己的结果，已退出页面不再接收。
- 对话入口开/关与单实例/安装子页四种布局，合并发现只出现一次，忽略与取消忽略影响摘要及数量，健康入口编号正确。

主要测试：`TestHealthPartialSources`、`TestHealthUnsupportedAndUnknownApproval`、`TestHealthRefreshStateAndLateResponses`、`TestMergedHealthDismissAndNavigation`。既有概览、首次引导与备份选择测试同步更新。

定向命令：

```sh
go test ./service ./tui ./cmd/valise -run 'TestHealthPartialSources|TestHealthUnsupported|TestHealthRefreshState|TestMergedHealth|TestOverviewNeeds|TestChecklist' -count=1
```

结果：退出码 0。服务层故障分支是纯聚合依赖注入测试；生产成功路径仍由既有 fake-ssh 测试覆盖，不将前者描述为真实 SSH 故障验收。

首轮 `testbed/selfcheck.sh` 退出码 0；随后调整了一句审批说明并重建最终客户端。最终再次运行 `testbed/selfcheck.sh`，退出码 0：core、porter、client、testbed、client/ui 的测试、vet、gofmt 检查，以及 profilelint、Linux ARM64/AMD64 porter 交叉编译均通过。依照当前 M7b 前阶段，脚本跳过 mygo generate、bun typecheck 和 bun build:web。最后一次客户端构建及 `--help` 也退出 0。

本地证据：`/tmp/valise-A-final-selfcheck-20261104.log`、`/tmp/valise-A-ui-live.log`、`/tmp/valise-A-health-live.txt`、`/tmp/valise-A-checkup-live.txt`。这些是本次运行的临时日志；可长期保留的结论与覆盖范围记录在本报告中。

## 4. 实际 CLI 与 TUI 验收

真实 CLI 执行 `health debian openclaw-test` 与 `checkup debian openclaw-test` 均退出 0。健康输出保留已知 Bypass 红色告警，显示备份及既有恢复测试记录；会话检查显示新标题与“没有记忆机制”发现。这里展示的是已有恢复测试记录，本批未重新执行恢复或交换目录。

实际 TUI 通过 PTY 驱动运行真实客户端并连接 Debian，操作链为：主页 → Debian → 安装 → 隔离测试 agent → 健康 → `r` 刷新 → 概览 → `b` 备份内容选择 → 缩为 80×24 → 恢复 110×34 → 取消选择 → 设置。

已验证：

- 健康页同时出现 agent 健康与会话、记忆发现；刷新时出现正在读取状态。
- 概览存在统一摘要，没有“体检有 N 个提醒（见体检）”旧重复内容。
- 备份选择明确写“选择内容”与“不含凭据和工作区”；两种窗口尺寸下选择页仍存在，缩放后页面保持。
- 设置页出现“备份分组”。

驱动退出码 0，记录两条 PASS：实际健康/刷新/概览/备份/缩放/分组通过，以及临时配置与拷贝钥匙清理完成。驱动仅使用临时配置，关闭本机仓库后台复制；没有确认创建备份或执行管理动作。

实机证据为终端去 ANSI 文本断言，不是截图或像素级视觉验收。走查时使用的二进制与最终代码只差一句审批引导的措辞调整，随后已重建最终二进制。

## 5. 覆盖边界与后续

- 实机覆盖健康、概览、备份选择、分组设置和窗口缩放；规则、对话提示、首次引导及 CLI 帮助的全部路径未逐页进行实机操作，采用代码检查和相关自动测试。
- 首次失败、旧结果、子检查失败与迟到响应以确定性测试验收；本批未在真实 SSH 会话中注入断线或延迟。
- 未改动审批设置、systemd 服务或活动状态目录，未执行恢复/交换；本批不能作为 B 修复或服务停启验收。
- 没有进行 Oracle 或其他靶机的本批验收。
- B02 可继续复用已核实的固定管理动作；B03 仍须先定稿持久化进度、原服务状态、中断后继续/恢复与分别报告配置/服务结果的 schema。C 仍按独立备份范围功能推进。
