# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-16。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前工作与交接（2026-11-16 A/B 槽位剥离 Stage 1–3 全部完成，待真机端到端回归测试）

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

### 下一步待办

1. **前端审计整固（Phase F1 → Phase F2，由 Claude 分阶段执行后交回审批）**：
   - **2026-11-16 前端审计已确认的 4 项设计决定**（详见 [UI.md](UI.md) 第 4、7.4.3、7.10/7.11、9.5 与 11 节及 [DEV_PLAN.md](DEV_PLAN.md)）：
     1. 保持「现在备份 / 备份一次 / 保存保护计划」在 GUI 中的一键确认体验，同时将全局 `confirmations` 收归 `App` 字段，并消除 `i18n.go` 全局 `words` 的并发读/写竞态；
     2. GUI 健康页修复动作在无管理钥匙时直接复用 `ServerPassword` askpass 弹窗（不再提示去终端执行 `valise action`），并接入内置 `linger` 一键开启；时钟修复与健康项“忽略（Dismiss）”状态机在 GUI 中归入 `v0.2`；
     3. GUI“创建副本”目标服务器允许选择并默认选中原服务器（解锁单服务器同机克隆），在自动布局核实前（`v0.2+`）同机直接展开目录映射输入框；
     4. 遵循“内容语义与视觉排版分离”的三层权宜之计（`docs/UI.md` 7.4.3）：`shell`/`home` 维持现有 `theme.json` 布局校验，其余页面采用“粗粒度区块插槽 + 默认流式回退”、CSS Class 式语义样式函数（`card`/`subPanel`/`cardTitle`/`fact`）与面向 i18n 的弹性宽度令牌。
   - **Phase F1（优先，v0.1 缺陷修复与功能打通）**：修改 `i18n.go`、`backupnow.go`、`app.go`、`restoresetup.go`、`health.go` 及对应测试；遵守单文件 `<= 400` 行与 `TestNoInterfaceWordsInCode` / `TestCatalogMatchesCode`。
   - **Phase F2（Phase F1 验收通过后推进，模板化底座与性能收敛）**：修改 `theme.go`、`layout.go`、`themefile.go`、`agent.go`、`journey.go`（弹性宽度令牌、粗粒度区块流式辅助与常用面板语义样式函数），并限制 `installpage.go` 降级文本缓冲上限、按业务域逐步收敛 `App` 字段。
2. **真机端到端闭环验证与新测试包**：旧 `stage-test-20261114/Valise Test.app` 构建于撤销 A/B 前；需在 Phase F1 落地后重新构建测试包，并在 macOS 桌面 + Linux 靶机上实测「添加服务器 → 接入 → 保护计划 → 手动备份 → 文件/暂存恢复测试 → 原生界面 → 直通恢复/迁移/克隆」。
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
