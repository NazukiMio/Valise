# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-21。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前阶段：Valise v0.1（Valise Recovery）发版收口（2026-11-21）

- **分支状态**：`feat/cloud-rclone-and-server-shell`（含 B3.1 与 B3.2 提交 `c098ce9`、`28b6696`，测试包 `stage-test-20261119-b3.2`）已通过全部真机验收并经 `--ff-only` 合入 `main`；当前工作分支 **`feat/v01-recovery-closure`** 已完成 v0.1 条目 1（`5152107`）、条目 2（`cef529f`）、`v01r2` 真机反馈修复（`4420a56`，已实机验收通过）、`v01r3`（`a4f1bd1`）、`v01r4`（`aabd97f`）及 `v01r5`；现已完成 **`v01r6`**（Profile 架构收口与编写套件），通过 `./testbed/selfcheck.sh`（`SELFCHECK_PASS`），并打好真机验收测试包 `stage-test-20261121-v01r6`。
- **B3 / B3.1 / B3.2 真机验收结论（2026-11-19，用户实机确认「全部通过」）**：
  1. **备份双轨与寿命闭环（B3）**：定时日期周期、永久手动存档点 `savepoints/`、主人级快照晋升、默认关闭的服务器自动退役与长旅程曲线视口虚拟化；原版恢复脚本验证源周期退役后仍可恢复。
  2. **服务器 `rclone` 与多服务器云盘登录（B3.1）**：`porter` 优先使用 `~/.local/bin/rclone` 并回退服务器 `PATH`；GUI 接通无 `sudo` Kit 安装（展示版本/URL/SHA-256 并显式确认）；云盘添加/重登录支持多选服务器串行登录（共享单次操作级 PIN 租约），`client_id`/`client_secret` 仅在当前页面内存会话保留并在离开/完成/关闭窗口时清零。
  3. **服务器命令行、Agent 原生对话与资料包更新（B3.2）**：服务器命令行移入服务器详情页并接通本地 PTY + `TIOCSWINSZ` 实时缩放与终端配色继承；Agent 页保留「原生对话」与「首次向导」，探测接入资料版本升级时主动提示并支持跳转「设置 › 资料包」下载或导入本地资料包且实时重载生效；清理云盘/位置页 11 处重复或过度防御性文案。
- **已通过真机验收（`feat/v01-recovery-closure`，`v01r2`）**：
  1. **条目 1 · 桌面零 CLI 维护收口**：Agent 详情页「改名…」（`SetAgentLabel`）、服务器设置「空间清理」（`PrevList`/`PrevDrop` 恢复前旧目录与 `OldList`/`OldCleanup` 已被取代旧实例；支持单项确认与**多选/全选单会话批量清理**，批量发起时统一弹出一次 PIN 或 Valise 口令验证、按 7 天/旧实例显示最高权限标签并诚实报告部分失败）及「设置 › 钥匙与口令 › 配置导入与导出」（`ExportConfig`/`ImportConfig`，不含私钥且支持缺通行证一键跳转重装）。
  2. **条目 2 · 低门槛云盘存储（WebDAV 与 S3 兼容）**：扩展 `kit/tools.yaml` 与 `core/kittools` 支持非浏览器凭证表单云盘（`browser: false`，不依赖回环端口 `53682`）与存储桶路径（`needs_bucket: true`，目标记为 `valise-<名>:<bucket>/valise-vault/`）；桌面与 CLI（`--bucket`）打通 WebDAV / S3 兼容云盘登录、长选项滚动与 `verify: pending` 显式确认试用，并在「设置 › 资料包」预览与已装卡片展示内置云盘清单、在「添加云盘」资料包缺失/未更新报错下提供直达「设置 › 资料包」按钮（同修 `v01r1` 仅安装到隔离 `$HOME` 导致真实宿主 `~/.config` / `~/Library/Application Support/valise` 仍停在旧资料包的问题）。
- **新增已完成并待真机验收（`v01r6`，Profile 架构收口与编写套件；基于 `v01r5` 全部收口项继续推进）**：
  1. **Linter 分层、全量命令审计与离线分类模拟器**：`core/profile` 与 `cmd/profilelint` 实现 Error/Warning 分层、全量 14 类可执行命令安全审计与离线分类模拟器；新增 `scripts/verify-profile.sh` 与 CLI `valise profiles lint --strict/--commands/--classify`（`--sample`）。
  2. **人类教程、AI 技能与 Profile 能力对齐**：新增人类教程 `docs/PROFILE_AUTHORING.md` 与纯文档/源码驱动 AI 技能 `skills/valise-profile-author/`；收口 `PROFILES.md` 并补齐 `hermes v12` / `dsh v5` 能力表。
  3. **本地 YAML 安全导入与恢复能力对齐**：`service` 支持单文件本地 YAML 安全导入（含官方 ID 拦截与命令审计）并对齐 `RestoreCapability`；同步更新 `onserver.go` 与桌面端「设置 › 资料包 › 导入本地资料 (.yaml)…」。
  1. **受管技能二件套与旧四件套安全清理**：移除 `agent-kit/skills/valise-{enroll,upkeep,handoff,checkup}/`，新增 `valise-onboard`（24 行，低频档案报备与覆盖缺口）与 `valise-backup`（21 行，状态查询与高风险改前留底）；`v01r5` 在两份 `SKILL.md` 补充**延迟工具发现**说明（如 OpenClaw `tool_search` 允许先查带前缀真实工具 ID；若找不到新版工具立即停止并提示用户升级服务器 `valise-porter` 与重启网关，绝不回退调用旧工具）；`porter/internal/kit` 内置 14 个历史版本 SHA-256 摘要，安装/卸载时自动清理未修改旧技能与 `.valise-bak-*` 并删除空目录、保留用户自行修改的文件。
  2. **4 工具 MCP 工具面与 `valise_onboard_context` 误报修复**：`tools/list` 仅暴露 `valise_status`、`valise_seal`、`valise_onboard_context`、`valise_propose_dossier`（旧工具名仅在 `Call()` 静默兼容）；移除隐式 `autoSeal` 并写入轻量 `agent-mcp` 审计时间戳；`valise_seal` 按已确认保护计划档位（默认 `full`）写 `kind: auto` 滚动周期叶（未确认旧实例走 `soul` + `plan_warning`）；**`v01r5` 修复 `porter/internal/mcp/onboard_context.go`：`collectLayoutFacts` 聚合 `largest_dirs` 时跳过已排除的 `CategoryBody` / `CategoryCache` / `CategoryIgnore`（仅统计会进入 `full` 常规备份的大目录），并在 `effective_rules` 补出 `profile_body_paths`，避免把已由 `body.paths` 排除的大目录误报为 `cache`**。
  3. **Dossier / Profile 契约与桌面端同步**（含 `2A` 与 Profile 工具链保留项）：`valise-dossier/1` 向后兼容扩展 `local_sqlite` 与 `external_refs`（人类在桌面端/CLI 确认后入档并在迁移/分身中随快照携带）；`valise-profile/1` 标记 `handoff`/`checkup` 为 `deprecated`，移除 `Declaration`（`valise_declare`）自报叠加，审批状态统一以确定性 `approval_check` 为准；保留 `PROFILES.md` 校验对齐、`valise profiles lint [--json]` CLI 与独立 `skills/valise-profile-author/`；桌面「Agent 保护技能」精简为 2 技能 + 3 句复制提示词（无 `Declaration` / 无 `AGENTS.md` 提醒），「健康 › 归档建议」与 CLI/TUI 支持三类提案审批（`classify` 常规 & `cache`/`ignore` 降级单独弹窗确认、`sqlite`、`external_ref`）。
- **明确冻结（延后至 v0.2+）**：主题引擎扩展（`valise-theme/2`、主题文件 `material` 字段）与 TUI 1:1 对齐在 `v0.1` 冻结；通用页面转发与 OAuth 中继完善、macOS Keychain/Touch ID、`hermes.yaml` 真机消 pending、`pi.yaml` 与兄弟项目 [ValiseMemo](../../ValiseMemo/docs/README.md) 集成（仅桌面侧只读浏览与按需控制面）归入 `v0.2–v0.3`（`Valise Client`）。
- **测试包打包触发标准（2026-11-19 用户约定）**：满足以下任一条件即打新 `stage-test-*` 测试包交由用户验收，否则先继续合并至下一批更新：
  1. 更新可能引发性能倒退或致命错误；
  2. 更新可能出现 UI/交互 Bug；
  3. 属于新内容附加，且对后端也进行了较多更改；
  4. 新内容涉及安全或风险边界变更。
- **后续专项待办**：全仓 UI 文案二次审计（平衡精简其余「过度防御 / 过度解释」说明）。

### 已完成阶段摘要（架构收敛 + F1/F2 + B1–B3.2）

- **架构收敛与 A/B 剥离（2026-11-15，`refactor/revoke-ab-slots-and-doc-audit`）**：撤销 A/B 双槽位并行运行环境与 `bwrap` 容器接管，统一回归「暂存校验 → 带交换日志原子交换 → `.valise-prev-<op>` 保留与回退」；永久保持 `valise-snapshot/1` 与 `/2` 读取兼容；保留范围化旅程加载、`restore-review/1`、`backuprun` 与非 A/B `native --instance`。
- **前端整理与整固（2026-11-16/17，Phase F1 `d43f228` / F2 `2177be0` / 界面整理三批）**：消除 `gui` 全局状态竞态、`confirmations` 收归 `App`、打通同机创建副本与无管理钥匙 `linger` 弹窗修复；落地三层样式/区块分离、沉浸式标题栏、旅程横向虚拟化与「经典 / 手账 / 夜航」材质主题，验收记录见 [acceptance/N4.md](acceptance/N4.md)。
- **真机闭环修复（2026-11-16–19，Phase B1–B3.2）**：落地保护计划「按原设置重新生效」、移除位置一键清理障碍、引导前可选暂停受管服务、官方脚本上游哈希变更容错与粘贴安装地址/指令、单次 Valise 主口令 + 30 分钟窗口 + 本机 6 位 PIN 快捷解锁、操作级 PIN 临时租约、结构化 Debug 日志、离线 `nas` 复制秒级取消、本地 PTY 与资料包热更新。

### 必须保留的环境与基线事实

- **当前待验收测试包**：`/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261121-v01r6/Valise Test.app`（`0.0.0-dev+stage-test-20261121-v01r6`，构建自 `feat/v01-recovery-closure`，含 `darwin/arm64` 客户端、`valise` CLI 与 `valise-porter_linux_{arm64,amd64}`；具体提交见包内 `BUILD.json`）。打包验收门禁中执行 `profiles install` / `profiles status` / `cloud list` 必须显式指定真实宿主 `HOME=/Users/nazukimio`（避免仅写入隔离沙盒 `$HOME`）。历史包 `stage-test-20261120-v01r5`、`v01r4`、`v01r3`、`v01r2`、`b3.2` 等均保留未动。
- **默认资料包与演练公钥**：当前开发资料包位于 `~/.cache/valise-drill/devpack`（`2026.11.21`：`dsh v5`、`hermes v12`、`openclaw v24`，`kit-tools.yaml` 含 `webdav`/`s3`，公钥 `RWQCRlUzO1yCpR4+m1KbYsJO7OIYDC2OpeB3j+JTZ2cPj37DcSZuoCF1`）；演练配置 `~/.cache/valise-drill/drillpack` 独立保留，不得混用或覆盖。
- **构建与检查环境**：macOS 上始终执行 `export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3`；构建发布/测试包时 `-ldflags` 必须同时注入 `main.version` 与 `valise/client/porter.ReleasePublicKey`；禁止执行 `rm -f`（清空用 `: > file`，测试用 `t.TempDir()`）；未经用户明确要求不执行 `git push`。

---

## 1. 历史阶段摘要（N1–N4 主体闭环，N5 延后）

| 阶段 | 状态 | 说明与记录位置 |
| :--- | :--- | :--- |
| N1 规范与服务契约 | `Done`（2026-11-05） | [SERVICE_CONTRACTS.md](SERVICE_CONTRACTS.md) 第 1–16 节 |
| N2 汇总、保护计划、时间轴与 B03 | `Done`（2026-11-07） | [acceptance/N2.md](acceptance/N2.md)、[acceptance/B03.md](acceptance/B03.md) |
| N3 严格副本、云盘与失联恢复 | `Done`（2026-11-08） | [acceptance/N3.md](acceptance/N3.md) |
| N4 mygo GUI 主流程与 v0.1 收口 | `In progress`（v0.1 收口；TUI 1:1 对齐冻结） | [acceptance/N4.md](acceptance/N4.md) |
| N5 管理方自动化、自定义与扩展 | `Planned`（v0.2+） | [DEV_PLAN.md](DEV_PLAN.md) |
