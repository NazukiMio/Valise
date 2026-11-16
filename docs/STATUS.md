# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-16。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前工作与交接（2026-11-16 A/B 槽位剥离 Stage 1–3 与前端整固 Phase F1 完成；Phase F2 待用户审批后开始）

**架构收敛与三阶段剥离完成状态**：
经 2026-11-15 代码审计与产品复盘，用户已确认**撤销** A/B 双槽位并行运行环境（`linux-bwrap` 容器接管、三档恢复保护模式、独立程序树采集与父子切换状态机）。分支 `refactor/revoke-ab-slots-and-doc-audit` 上三个阶段均已完成、验证并提交：
- **Stage 1（已完成，`bc3d917`）**：创建分支 `refactor/revoke-ab-slots-and-doc-audit`，完成 Prompt Audit 与规范文档重整；归档废止的 A/B 契约、提案与合成实验记录至 `docs/history/`；补充 MVP（`v0.1`）边界、工程减负计划与 5 项高阶用户痛点规划。
- **Stage 2（已完成，`c9de8b9`：`core` / `profiles` / `porter`）**：
  - 已剥离 `core/environment/`、`core/profile/{environments*,readiness*,runtime_executable_test}.go`、`core/revive/{environment_plan,environment_preparation,managed}.go`、`core/rpc/tier.go` 中 `environment_*` 方法、`profiles/openclaw.yaml` 的 `environments:` 段；
  - 已剥离 `porter/internal/{activationrecord,activeexec,activeview,environments,isolated,managedruntime,runtimeguard}/`、`revive/` 策略与 A/B 槽位代码、`server/` 环境探测/计划接口、`seal/` 运行期程序树叠加采集（新封存写回 `valise-snapshot/1`，`core/vault` 与 `tools/standalone-restore` 永久保持 `/1` 与 `/2` 读取兼容）；
  - 保留 `restore-review/1`、`backuprun`、`seal/journey`、`legacy` 只读兼容及 `porter/internal/install/native.go` 的非 A/B `native --instance` 入口。
- **Stage 3（已完成，本提交：`client/ui`）**：
  - 已移除 `client/ui/{service,tui,gui}` 中的 50 个 A/B 专用文件（`environment_*`、`active_approval_test.go`、`direct_policy_test.go`、`health_runtime*`、`restoretest_{baseline,capability_test,composition_test,environment*,support*}`、`tui/runtimecards_test.go`、`gui/{environment*,healthruntime*,healthtestenvironment*,healthtestmaterials*,restoreoldcleanup*,restoreparallel*,restorepolicy}.go`）；
  - 已清理 `client/ui/{service,cmd,tui,gui}` 保留文件中对 `core/environment`、`RuntimeState`/`Runtime`、`EnvironmentTestSupported`、`RestoreKindEnvironment`、`TestMaterialReview` 及 `isolated` 隔离深度的依赖；已从 `client/ui/gui/i18n/zh-CN.json` 移除 184 个失效 A/B 词条，`gui/` 与 `tui/` 全部 `.go` 文件均为 `<= 400` 行（最高 395 行）；
  - 已通过 `AGENTS.md` 第 6 节全部 5 步检查：`core`（含 `profilelint`）、`porter`（含 `linux/arm64` 与 `linux/amd64` 交叉编译）、`client`、`testbed`、`client/ui`（含 `go vet`、`gofmt`、`go tool mygo vet` 及 `go test ./...` 全部通过；其中本机 `127.0.0.1` 回环、`ssh-add`、`~/.ssh/config` 与 askpass 相关测试需在放开沙箱后运行）。

### 已保留的非 A/B 有效成果

1. **旅程范围化与增量加载**（`journey_stream.go`、`journey_scope.go`、`journeyread.go`、`journeylocations.go` 等）：按当前连接与明确迁移关系查询，支持分位置超时与重试。
2. **一次性备份进度与视图**（`backupfollow.go`、`backuponce.go`、`backupview.go`、`backupclientcopies.go` 及 `porter/internal/server/backuprun.go`）。
3. **直通恢复 / 失联恢复 / 导出 / 恢复前可选备份 / 恢复历史**（`restore.go`、`restoresetup.go`、`restoreprogress.go`、`restorehistory.go`、`restorelocations.go`、`lostsource.go`、`migrate.go` 及 `porter/internal/server/restore_review.go`）。
4. **健康页按时间点选择的文件与暂存恢复测试**（`healthrestore.go`、`healthtestselect.go`、`restoretest*.go`）：保留本地与服务器暂存两步文件核对（`test_only` 写入暂存后 `discard`），移除 `isolated` 隔离网关重建深度与来源弹窗。
5. **按实例原生终端**（`native.go`、`native_instance.go` 及 `porter` `native --instance <id>`）：保留 `--instance` 直接按 dossier/profile 解析 `native_cli`，去掉 A/B 运行环境守卫。
6. **快照与档案兼容**：`core/vault/` 与 `tools/standalone-restore/restore.py` 永久保持 `valise-snapshot/1` 与 `/2` 读取兼容；保留旧档案只读兼容逻辑（非 A/B 部分）。

### 前端整固 Phase F1（已完成，2026-11-16，待用户审批）

依据 [UI.md](UI.md) 第 4、7.10/7.11、9.5、11 节与 [DEV_PLAN.md](DEV_PLAN.md) 第 5 项，只改 `client/ui/gui` 与 `client/ui/service` 的一处判断：

1. **全局状态**：`gui/i18n.go` 的当前词表 `words` 改为 `atomic.Pointer[catalog]`（后台 goroutine 调 `T()` 与主线程 `chooseLocale()` 不再竞态）；包级 `confirmations` 收进 `App.confirms`（带锁的实例字段），“现在备份 / 备份一次 / 保存保护计划”仍是一键确认。测试：`gui/concurrency_test.go`（`-race` 下并发 `T()` + `chooseLocale()`；两个 `App` 的确认互不可见、各只生效一次）。
2. **创建副本**：`gui/restoresetup.go` 只在“迁移”时把原服务器排除出目标列表；切到“创建副本”默认选中原服务器，同机时直接展开目录映射输入区并给一句提示（新词条 `restore.same_server_hint`）。测试：`restore_test.go` `TestCloneOnTheOnlyServer`（单服务器可选中原服务器并发起副本计划）。
3. **健康页修复的服务器密码**：根因在服务层——`adminTransport` 早已支持 askpass 转交，但 `service/system_fixes_run.go` 的 `adminLoginAvailable` 只看 `NoTerminalLogin`、没看 `canPrompt()`，桌面上没有管理钥匙的密码登录服务器因此拿不到 linger 一键修复（被判成只能手动）。已补 `|| s.canPrompt()`（本阶段唯一的 `service` 改动）。GUI 删除 `fixError` 里“去终端执行 `valise action …`”的退回与 3 个词条 `health.need_password*`；`health.admin_login` 改为同时说明管理口令与服务器密码；UI.md 7.3 一处旧表述同步。
4. **linger 一键开启**：新文件 `gui/healthsysfix.go`：点击后先 `PlanSystemFix`，确认框直接列出命令、改变的系统设置、撤销办法与需要的管理登录；确认后 `RunSystemFix`（摘要绑定计划），结果分别显示每条命令的退出码与复核结论，未通过时可重试；服务层判为不能代为执行时只说明原因并列出命令。时钟修复与“忽略”不在 GUI 里（v0.2）。
5. **验证（均为实际运行）**：`gui/healthfix_test.go` 经 fake-ssh（ssh 替身按 OpenSSH 的方式通过 `SSH_ASKPASS` 询问密码）跑通“无管理钥匙 → 窗口弹出服务器密码 → linger 修复 / 需管理登录的 profile 动作执行”，含取消密码后不执行、可重试；撤掉服务层修复后该测试与 `TestAdminLoginAvailableNeedsAPrompt` 均失败。`go test -race ./gui/...`、`go tool mygo vet`、AGENTS.md 第 6 节 5 步自检全部通过；`gui/*.go` 最大 381 行。**未做**：真实 OpenSSH + sshd 靶机上的 askpass 与 `loginctl`（polkit）行为，fake-ssh 不能代替，留给下面的真机闭环。

### 下一步待办

1. **Phase F2（等用户审批 Phase F1 后开始；模板化底座与性能收敛）**：修改 `theme.go`、`layout.go`、`themefile.go`、`agent.go`、`journey.go`（弹性宽度令牌、粗粒度区块流式辅助与常用面板语义样式函数），并限制 `installpage.go` 降级文本缓冲上限、按业务域逐步收敛 `App` 字段。2026-11-16 前端审计确认的 4 项设计决定见 [UI.md](UI.md) 第 4、7.4.3、7.10/7.11、9.5 与 11 节。
2. **真机端到端闭环验证与新测试包**：旧 `stage-test-20261114/Valise Test.app` 构建于撤销 A/B 前；Phase F1 已落地，需重新构建测试包，并在 macOS 桌面 + Linux 靶机上实测「添加服务器 → 接入 → 保护计划 → 手动备份 → 文件/暂存恢复测试 → 原生界面 → 直通恢复/迁移/克隆」。
3. **合并与后续演进**：真机闭环通过后，将分支 `refactor/revoke-ab-slots-and-doc-audit` 整理合并，并按 `docs/DEV_PLAN.md` 推进 MVP `v0.1` 收口与 5 项用户痛点规划。

### 必须保留的环境与基线事实

- **当前用户构建**：`/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261114/Valise Test.app`（`0.0.0-dev+stage-test-20261114`）。A/B 剥离完成后应重新打出干净 `v0.1` 测试包交用户验证。
- **默认资料包与演练公钥**：默认资料包为 `2026.11.10`（OpenClaw 22、Hermes 11）；默认配置公钥 `~/.cache/valise-drill/devpack/key.pub`，演练配置公钥 `~/.cache/valise-drill/drillpack/key.pub`，不得混用或覆盖。
- **构建环境**：macOS 上始终执行 `export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3`；禁止执行 `rm -f`（用 `git rm` 或 `t.TempDir()`）；禁止自动 `git push`。
- **原基线**：分叉前分支 `n2-agent-protection-journey` 位于 `f243bcf`（含 `07d7952`、`4bdd0fb`、`3e9f124`、`4d890c2`、`f243bcf`）；工作树中的 `references/` 与 `.DS_Store` 保留不提交。

---

## 1. 历史阶段摘要（N1–N3 已验收，N4 进行中）

| 阶段 | 状态 | 说明与记录位置 |
| :--- | :--- | :--- |
| N1 规范与服务契约 | `Done`（2026-11-05） | [SERVICE_CONTRACTS.md](SERVICE_CONTRACTS.md) 第 1–16 节 |
| N2 汇总、保护计划、时间轴与 B03 | `Done`（2026-11-07） | [acceptance/N2.md](acceptance/N2.md)、[acceptance/B03.md](acceptance/B03.md) |
| N3 严格副本、云盘与失联恢复 | `Done`（2026-11-08） | [acceptance/N3.md](acceptance/N3.md) |
| N4 mygo GUI 完整流程与 TUI 对齐 | `In progress`（架构减负中） | [acceptance/N4.md](acceptance/N4.md) |
| N5 管理方自动化、自定义与扩展 | `Planned` | [DEV_PLAN.md](DEV_PLAN.md) |
