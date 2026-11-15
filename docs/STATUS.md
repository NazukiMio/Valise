# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-15。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前工作与交接（2026-11-15 Stage 1 完成，待执行 Stage 2 & Stage 3）

**架构收敛与分段剥离计划**：
经 2026-11-15 代码审计与产品复盘，用户已确认**撤销** A/B 双槽位并行运行环境（`linux-bwrap` 容器接管、三档恢复保护模式、独立程序树采集与父子切换状态机），改为在新分支 `refactor/revoke-ab-slots-and-doc-audit` 上分阶段清理：
- **Stage 1（已完成）**：创建分支 `refactor/revoke-ab-slots-and-doc-audit`，完成 Prompt Audit 与规范文档重整；归档废止的 A/B 契约、提案与合成实验记录至 `docs/history/`；补充 MVP（`v0.1`）边界、工程减负计划与 5 项高阶用户痛点规划。
- **Stage 2（下一步，仅改 `client/ui`）**：剥离 `client/ui/gui` 与 `client/ui/service` 的 A/B 并行环境、三模式设置与隔离试运行代码；保留全部非 A/B 有效 N4 交付，验证 `client/ui` 测试、`vet`、`gofmt` 与 `go tool mygo vet`（单文件 `<= 400` 行，全 `T("key")`）后更新本文件并提交 Commit 2。
- **Stage 3（最后，改 `porter` / `core` / `profiles`）**：解除 `porter/internal/revive/direct_policy.go` 对直接恢复的拦截，剥离 `porter`、`core/environment`、`profiles/openclaw.yaml` 的 A/B 槽位代码，保留 `valise-snapshot/1` 与 `/2` 永久读取兼容，恢复新封存写入标准 `/1`；跑通 `AGENTS.md` 第 6 节全部五模块自检及 Linux ARM64/AMD64 交叉编译，更新本文件并提交 Commit 3。

### 本轮保留的非 A/B 有效成果（Stage 2/3 禁止误删）

1. **旅程范围化与增量加载**（`journey_stream.go`、`journey_scope.go`、`journeyread.go`、`journeylocations.go` 等）：按当前连接与明确迁移关系查询，支持分位置超时与重试。
2. **一次性备份进度与视图**（`backupfollow.go`、`backuponce.go`、`backupview.go`、`backupclientcopies.go` 及 `porter/internal/server/backuprun.go`）。
3. **直通恢复 / 失联恢复 / 导出 / 恢复前可选备份 / 恢复历史**（`restore.go`、`restoresetup.go`、`restoreprogress.go`、`restorehistory.go`、`restorelocations.go`、`lostsource.go`、`migrate.go` 及 `porter/internal/server/restore_review.go`）。
4. **健康页按时间点选择的文件与暂存恢复测试**（`healthrestore.go`、`healthtestselect.go`、`restoretest*.go`）：保留 `file` 与 `staging` 两档核对（`test_only` 写入暂存后 `discard`），移除 `environment` 隔离网关深度与来源确认弹窗。
5. **按实例原生终端**（`native.go`、`native_instance.go` 及 `porter` `native --instance <id>`）：保留 `--instance` 直接按 dossier/profile 解析 `native_cli`，去掉 A/B 运行环境守卫。
6. **快照与档案兼容**：`core/vault/` 与 `tools/standalone-restore/restore.py` 永久保持 `valise-snapshot/1` 与 `/2` 读取兼容；保留旧档案只读兼容逻辑（非 A/B 部分）。

### Stage 2（`client/ui` 剥离）待清理清单

- **待移除 `client/ui/gui/` 文件**（用 `git rm`）：
  - `environmentcleanup.go`, `environmentcleanup_test.go`, `environmentsettings.go`, `environmentsettings_test.go`
  - `healthtestenvironment.go`, `healthtestenvironment_test.go`, `healthtestmaterials.go`, `healthtestmaterials_test.go`
  - `restoreoldcleanup.go`, `restoreoldcleanup_test.go`
  - `restoreparallelconfirm_test.go`, `restoreparallelhistory_test.go`, `restoreparallelprepare.go`, `restoreparallelprepare_test.go`, `restoreparallelpreview.go`, `restoreparallelpreview_test.go`, `restoreparallelstatus.go`, `restoreparallelswitch.go`, `restoreparallelswitch_test.go`, `restoreparallelswitchview.go`
- **待适配 `client/ui/gui/` 保留文件**：`app.go`, `health.go`, `health_test.go`, `healthrestore.go`, `healthtestselect.go`, `healthtestselect_test.go`, `personalization.go`, `personalization_test.go`, `restore.go`, `restore_test.go`, `restoresetup.go`, `restoreprogress.go`, `pages_test.go` 等（另检查 `gui/i18n/zh-CN.json`、`TUI` / `CLI` 中残留的 A/B 运行环境调用）。
- **待移除 `client/ui/service/` 文件**（用 `git rm`）：
  - `direct_policy_test.go`, `environment_guard_test.go`, `environment_history_test.go`
  - `environment_old_cleanup.go`, `environment_old_cleanup_test.go`
  - `environment_plan.go`, `environment_plan_test.go`
  - `environment_prepare.go`, `environment_prepare_status.go`, `environment_prepare_stream.go`, `environment_prepare_test.go`, `environment_prepare_wire_test.go`
  - `environment_settings.go`, `environment_settings_test.go`
  - `environment_switch.go`, `environment_switch_test.go`
  - `restoretest_environment.go`, `restoretest_environment_test.go`, `active_approval_test.go`
- **待适配 `client/ui/service/` 保留文件**：移除对 `core/environment`、`environment_*` RPC 与 `RestoreTestDepthEnvironment` 的调用，确保直通恢复直接基于 `PlanRevive` / `PrepareRestore` / `RunRestore` 执行。
- **Stage 2 结束校验**：`export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3 && cd client/ui && go test ./... && go vet ./... && test -z "$(gofmt -l .)" && go tool mygo vet`。

### Stage 3（`porter` / `core` / `profiles` 剥离）待清理清单

- **待移除 `core/` 文件**：`core/environment/` 整包、`core/profile/environments.go`、`core/profile/environments_test.go`、`core/revive/environment_plan.go`、`core/revive/environment_preparation.go`、`core/revive/managed.go`（及对应测试）。清理 `core/profile/`、`core/revive/`、`core/vaultrpc/` 中对 `environments` / `environment-*` 能力的依赖。
- **待移除 `porter/` 包与文件**：
  - 整包：`porter/internal/activationrecord/`、`activeexec/`、`activeview/`、`environments/`、`isolated/`、`managedruntime/`、`runtimeguard/`
  - `porter/internal/revive/` 中 `direct_policy*.go`、`environment_*.go`、`managed_*.go`、`activation*.go`
  - `porter/internal/server/` 中 `environment_*.go` 及环境相关方法注册（保留 `backuprun`、`restore_review` 与 `native --instance`）
  - `porter/internal/seal/` 中 `active.go`、`active_program.go` 与运行期程序树采集（新封存写回 `valise-snapshot/1`），保留 `seal/journey.go`
- **待清理 `profiles/`**：移除 `profiles/openclaw.yaml` 中已废弃的 `environments:` 字段（及清理 `core/cmd/profilelint` 中的 `environments` 校验或将其标为已废弃）。
- **Stage 3 结束校验**：按顺序跑通 `AGENTS.md` 第 6 节全部 5 步（含 `profilelint`、Linux `arm64`/`amd64` 交叉编译与 `mygo vet`）。

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
