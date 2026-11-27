# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-27。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前阶段：Valise v0.1（Valise Recovery）发版收口（2026-11-27）

- **分支状态**：`v0.1.0-beta.1`（`56ddb6c`）/ `main`（`4e5fe6c`）在真实 Hermes Agent 服务器完成实机测试后，当前工作分支 **`feat/beta1-feedback-fixes`** 已完成 **WP0–WP6**、**Round 2–4 实机反馈修复**，以及基于 `stage-test-20261125-beta1-r4` 真机测试与 Agent 迁移体检报告开展的 **Round 5 第一批与第二批**（合入打包至 `stage-test-20261127-beta1-r5`）：
  - **Round 5 第一批（`07f6375` · WP1 / WP3 / WP5 / WP7 + 规范与 `GEMINI.md` 同步）**：
    - `GEMINI.md` 已退役 `Gemini 4 Argon` 并切至「Claude 5.5 + Gemini 4 统一池」双池路由；
    - 本批交付：一键重接与多选全选批量接入（单次 PIN 租约）+ 全仓去 AI 防御腔文案清理（WP1）；保护技能四态升级识别 + 安装基线与可视化 Diff 对比窗（WP3）；安装检查与脚本全文带滚动条子窗口（WP5）；跨机 `$HOME` 软链重映射、默认自动重接 Valise MCP（不覆盖技能且按服务状态拆行汇报）、脱敏配置旧 `$HOME` 路径扫描与暂存安全替换、空目录保留与外部软链前置告警、桌面「打开本地页面」(`ForwardPage` + 回环监听探测) 与 `hermes v15`（WP7）；经 `auditor-b5a-r3`（`AUDIT_PASS`）及全仓 `./testbed/selfcheck.sh` + `-race` 复测通过。
  - **Round 5 第二批（`21a3351` · WP2 / WP4 / WP6 + `SERVICE_CONTRACTS.md`/`ARCHITECTURE.md` 同步，已合入 R5 测试包待实机验收）**：
    - **WP2（单/多选快照删除 + 孤立 pack 安全清扫 + 自动清理跳过空周期）**：`vault.Index` 写入 `FullDeps: true`；`core/vault/prune.go` 免密钥抗崩溃删除指定快照并保护旧非 `FullDeps` 存档点依赖索引，清理零引用 `packs/*.pack` 且对最后一条快照删除幂等；`stationsync.Remote.DeleteFiles`（rsync/sftp/rclone 三实现校验与幂等）；`onlyAutomatic` 放行已手动删空的已结束周期供 `RetireExpiredAutoLeaves` 退役（不触及 `savepoints/`）；`ForgetSnapshotsSynced` 同步清理；`snapshot-delete/1`、`snapshot_delete`/`target_snapshot_delete` RPC；桌面端时间线与备份位置（`server_vault`/`server_target`/`station`/`local_repo`/`local_cloud` 区分与云盘去重）支持单/多选删除与组合影响预览。
    - **WP4（手动存档分离 + 两层智能周期 + 显式目标分发）**：`protection-plan/4`（`Schedule.FullCycle`: `""`/`"none"`/`"weekly"`/`"monthly"`）与 `backup-runs/4`（`BackupSelection.Targets` 绕过 `copyDue` 节流并按选定目标同步）；`needsFullCycle` 自动周期边界检查（覆盖全组 `BundleMembers`）自动升档 `full`；`CreateManualSavepoint`（含 `EnsureKeyRegistered`）从自动保护计划彻底拆出；桌面端「手动存档点」独立选档位/备注/去重后目标位置/同 Harness 组合兄弟与 `needKey` 门禁，`tabManage` 保持单屏至多 1 个 `ui.PrimaryButton`。
    - **WP6（同 Harness 平铺/分组 + 联动存档 + 组合恢复拦截 + Harness 顺序搬家）**：首页支持「平铺 / 分组」视图切换，分组视图按同服务器同 `ProfileID` 折叠子 Agent 并独立叠加子异常提示；主 Agent 新增「子 Agent」页签，子 Agent 详情提供所属主 Agent 跳转；`bundle-seal/1`（`seal_bundle` RPC）与 `Desired.BundleMembers` 严格校验同 `ProfileID`（否则 `bundle_cross_harness`），写入统一 `BundleID` 与 `BundleMembers`；单机恢复/迁移检出多成员组合备份即拦截返回 `bundle_not_standalone` 并引导 `PrepareBundleRestore`/`RunBundleRestore`（主 Agent 优先、子 Agent 跳过服务并清空 `StopSource`，`ModeMigrate` 自动重映射/推导命名目录与诚实展示 blockers/确认流）；新增 `/servers/{name}/move-all` 与 `InspectHarnessMove`/`PrepareHarnessMove`/`RunHarnessMove` 按 Harness（`ProfileID`）顺序逐组搬家（单组失败记录原因，不阻塞其余分组）。
    - **复审与全量验证**：经 `auditor-b5b`（发现 6 Must-fix + 2 Minor）返修并在 `auditor-b5b-r2` 获 `AUDIT_PASS`；`client/ui/{gui,tui}/*.go` 最大 399 行（`<= 400`）、`py_compile tools/standalone-restore/restore.py` 通过、`./testbed/selfcheck.sh` 输出 `SELFCHECK_PASS`，`core`/`porter`/`client`/`testbed`/`gui`/`tui` 及 Batch 2 定向 `service` 全量 `-count=1 -race` 通过。
- **Round 4 实机反馈修复（`stage-test-20261124-beta1-r3/REPORT.md` #1–#5 + `auditor-b4` `AUDIT_PASS`）**：
  1. **恢复/迁移表单清理（#1）**：`restore.origin` 移至页面顶部上下文条并从「放到哪里」表单卡片移除，单备份源改只读展示。
  2. **资料包预览自动清旧覆盖 & 迁移接入继承（#2）**：正式资料包预览支持默认勾选自动清理本地旧版覆盖；未装底座空壳页增加无需手动接入/启网关安抚提示；`planRecord` 与 `RunRestore` 自动继承 `DisplayName` 与完整 Dossier（`LocalOverrides`、`LocalSQLite`、`ExternalRefs`、`LocalRoots`、`ToolDeps`），迁移计划默认勾选「沿用原机保护计划」（仅按目标存在复制目标最佳实践写入），迁移结果页探测新机 `$HOME`/MCP 并提供一键重接。
  3. **Agent 服务状态双向实时同步（#3）**：每次进入 `/agents/<server>/<instance>` 单次刷新实时服务状态，并在 `agentServices` 与 `agents.value.Items` 之间双向同步。
  4. **迁移结果页收敛与明确导流（#4）**：终态（`succeeded`/`failed`/`cancelled`）隐藏误导性「查看原操作结果」按钮（仅 `reconciling`/`needs_recovery`/未知保留），查询时清空旧进度并展示「前往新机 Agent」与「迁移后收尾检查」卡片。
  5. **Dossier 闭环扩展（#5）**：`valise-dossier/1` 扩展支持用户确认「附加备份目录（`local_roots`）」与「伴生工具清单（`tool_deps`）」，新增 `dossier_update`（`TierOwner`，`dossier-roots/1`）并联动 `identity.SyncBoundRoots`；`seal` 与 `revive` 打包及 `${HOME}` 跨机重映射 `local_roots`，只读探测目标机缺失 CLI 工具输出环境差距清单，升级 `valise-onboard`（25 行）。
- **Round 3 实机反馈修复（`feat/beta1-feedback-fixes` + `auditor-b3` `AUDIT_PASS`）**：
  1. **服务控制与旧资料包恢复**：`agentservice.go` 有服务单元时常驻渲染「启动网关 / 重启网关 / 停止网关」三按钮并按状态启用/禁用；未安装服务单元时不再空白，区分「一键安装」「先更新实例资料」「本地旧覆盖告警与直达/一键恢复」「无安装命令跳转管理页」；「设置 › 资料包」新增横幅与一键「全部恢复为官方版」；`porter` 补齐 non-login SSH `XDG_RUNTIME_DIR` 回退、`installService` 使用 `profile.Detect.BinDirs` + `systemctl --user daemon-reload`。
  2. **迁移/分身来源与目标表单**：`restoresetup.go` 移除可编辑「源服务器」下拉菜单改为只读展示，`restore.go` 规范化 `origin` 解析；迁移严格排除当前源服务器，单目标自动选中、无其他服务器明确提示并禁用下一步；干净目标机不重复显示覆盖默认目录单选框。
  3. **干净目标与未启动/未安装网关兼容**：`InspectTargetPlacement` 对干净目标默认迁移不再强制推荐 `-copy` 命名后缀而是直落默认状态目录；`porter` `Inspect` 当自定义目录无结果时回退探测默认 `StateDir`；`planService` 在快照旧版本无 `install_argv` 且客户端有效资料同单元声明时深拷贝继承 `install_argv` 与 `bin_dirs`（交换后自动安装并启动），不再误报 `service_missing` 强制「仅迁入数据」。
  4. **分步引导迁移向导（`restore_env.go` / `restoresteps.go`）**：将原先堆砌的告警与阻塞收口为独立「准备环境」步骤与 6 步进度条，一次仅聚焦当前未决前置项（底座缺失/内联安装 -> 版本差异 -> 服务缺失）；前置满足后自动重检并跳过已就绪页（`envAutoSkipped` / `已就绪（已跳过）`），`核对计划` 仅保留简洁事实与风险确认。
- **Round 2 实机反馈修复（`TESTRESULT.md` #1–#15 + `auditor-b2` `AUDIT_PASS`）**：
  1. **失效实例移除与目录缺失检测（#1 / #7 / #14）**：新增 `instance_forget`（`TierOwner`，`instance-forget/1`）、`MissingRoots` 探测与 `AgentReader` 缓存失效联动，并在 GUI/CLI 提供缺失实例告警与一键移除。
  2. **安装终端本地 PTY 与实时缩放（#2）**：安装终端接入本地 PTY 与 `TIOCSWINSZ` 尺寸同步。
  3. **Agent 概览服务控制与本地旧资料告警（#3 / #10）**：Agent 概览页提供服务控制（`start`/`restart`/`stop`/`install_argv`）与快捷 `profile.actions`；当旧版 `.yaml` 本地覆盖官方资料包时展示 `LocalStale` 提示并支持恢复官方版。
  4. **`profiles/hermes.yaml` 升级至 `v14`（#10）**：补充 `cache/{pnpm,uv,partials,scratch}/**`、`source-checks/**`、lockfiles 等规则，通过 `--strict` 校验。
  5. **保护计划档位切换与说明澄清（#4 / #8）**：支持保护计划档位切换（`full`/`soul-secrets`/`soul`），`tier.full` 明确标注「不含程序本体」，并在 `PRODUCT.md` 记入 `v0.2+` 轻量沙箱心跳演练规划。
  6. **`identity` 分类与 `soul` 未分类跳过提示（#6）**：`valise_propose_dossier`、`valise-onboard` 与 GUI 新增 `identity` 分类，并针对 `soul` 未分类条目增加跳过提示。
  7. **迁移文案、结构化目录选择器与内联自定义安装（#8 / #11 / #13 / #15）**：优化迁移安抚文案；新增结构化 `InspectTargetPlacement` 目录选择器（无 `state=`/`--root` CLI 语法）；支持内联自定义安装与用户预设保存（`custom-installs.json`，`0600`）。
  8. **可复制错误/路径/确认句（#9）**：错误详情、路径与确认框文本接入 `.Selectable()`，确认句附带一键复制按钮。
- **`feat/beta1-feedback-fixes` 已完成修复（WP0–WP6 + Stage 4 审计）**：
  1. **WP1 · 迁移身体与服务解耦**：支持 `AllowVersionMismatch`、`SkipService`/`--data-only`、`BinaryFound` 探测、同提交 `git` 仓库修复、Dossier 可见性及 `external_refs` 缺口告警。
  2. **WP2 · 安装脚本两级安全检查**：修复 Hermes `install.sh` L332 单字节 `\xHH` 误报，补充行号与代码片段预览；对破坏性、禁用安全机制及二进制脚本保持永久拒绝（无绕过），将二次下载管道 / `eval` / 解码命令降级为 `RiskSentence` 明确确认。
  3. **WP3 · 接管后内联保护计划与一键 Agent Kit 接线**：接管后内联展示保护计划卡片并在 Agent Kit 页增加缺失计划横幅；支持用户确认后一键执行 MCP `mcp_add_argv`/`mcp_remove_argv` 接线与可选服务重启，并独立汇报三段结果。
  4. **WP4 · `protection-plan/3` 多周期调度与复制目标闭环**：支持 `off`/`hourly`/`daily`/`weekly`/`monthly` 周期（向后兼容旧版 porter）、允许关闭定时保存、完整展示与保存复制目标（服务器 + 本机 + 客户端云盘如 `gdrive`），并增加按周期联动的保留份数预览。
  5. **WP5 · 本地资料覆盖与编写套件升级**：支持本地 `.yaml` 覆盖同 ID 官方资料（含徽章与一键恢复官方）、结构化能力差异预览、导入后过期实例同步（修复 `RecordFrom` 保留 `Label` 与 `LocalOverrides`），升级 `skills/valise-profile-author/` 与 `docs/PROFILE_AUTHORING.md`，并将 `profiles/hermes.yaml` 升至 `v13`。
  6. **WP6 · 诊断日志与 GitHub 限流兜底**：`beta`/`rc` 预发布构建默认开启 `valise-debug.log`，并在 GitHub API 403/429 限流时自动回退至 `releases.atom`。
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

- **当前待验收测试包**：`/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261127-beta1-r5/Valise Test.app`（`0.1.0-beta.2-dev+stage-test-20261127-r5`，Beta 公钥 `RWTZ1EJhJqLr+89l9EhFambSn5K8QWguYmn9x2mGlC5aQfJzcMyWA15L`，资料包 `2026.11.27.1` 含 `dsh v5` / `hermes v15` / `openclaw v24`，构建自 `feat/beta1-feedback-fixes`，含 `darwin/arm64` 客户端、`valise` CLI 与 `valise-porter_linux_{arm64,amd64}`；具体提交见包内 `BUILD.json`）。打包验收门禁中执行 `profiles install` / `profiles status` / `cloud list` 必须使用 `--config-dir "$TMP"` 隔离配置目录。历史包 `stage-test-20261125-beta1-r4`、`stage-test-20261124-beta1-r3`、`stage-test-20261123-beta1-r2`、`stage-test-20261123-beta1-fixes`、`stage-test-20261121-v01r6`、`stage-test-20261120-v01r5`、`v01r4`、`v01r3`、`v01r2`、`b3.2` 等均保留未动。
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
