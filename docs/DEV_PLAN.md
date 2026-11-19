# Valise 开发计划

> 当前及后续工作按下列三幕产品路线与 N1–N5 交付依赖推进。每阶段须完成适用的自检（AGENTS.md 第 6 节）和具体验收才能标记 `Done`；文档更新不等于代码或实机验收完成，**不承诺日程或工期估时**。
> N1 契约设计已于 2026-11-05 定稿，现行契约见 [SERVICE_CONTRACTS.md](SERVICE_CONTRACTS.md)。N2（2026-11-07）与 N3（2026-11-08）已由用户确认为 Done；N4 GUI 核心流程与 Phase B1–B3.2 已在真实 Linux 靶机完成多轮验收，当前进入 `v0.1（Valise Recovery）` 最终收口；N5 未实施。当前进度与交接见 [STATUS.md](STATUS.md)。
> M1–M17 的原文与实测证据移到 [history/MILESTONES_M1-M17.md](history/MILESTONES_M1-M17.md)；旧 v0.1 CLI 发布设想为历史，CLI 可用不代表最终用户 GUI 已交付。

## 当前产品基线与三幕路线（2026-11-19 更新）

本轮复用 Go service、系统 OpenSSH、按需 `valise-porter`、age/zstd、分块周期备份库、暂存交换及恢复/配置事务各自的 journal。统一任务状态与前端契约，不重写为万能执行引擎。GUI 是最终用户交付，TUI 面向资源紧凑环境，CLI 面向自动化、诊断和高级用户，不作为普通用户必须学习的前置。

**产品形态与统一品牌承诺**：产品名、GitHub 仓库、数据目录与发行应用始终统一为 **Valise**（不拆分为两款独立应用），按三幕演进：

| 演进阶段 | 定位与核心目标 | 边界与冻结约定 |
| :--- | :--- | :--- |
| **第一幕：Valise v0.1（`Valise Recovery`）** | 围绕安装/接入、备份、多位置同步、原地恢复、跨机迁移、创建分身与基础照看的完整可用开源工具（`OpenClaw` 全链路零 CLI 闭环） | 补齐零 CLI 维护闭环（服务器空间清理、Agent 改名、跨机配置导入导出）与低门槛云盘（WebDAV / S3 兼容）及发布文档/QA。**v0.1 明确冻结**：TUI 与 GUI 1:1 功能对齐、主题文件扩展（`valise-theme/2` 与外部主题 `material`）、AI 对话集成、自定义范围与 N5 管理方 MCP |
| **第二幕：Valise Client（`v0.2–v0.3`）** | 深化通用 Agent 桌面客户端体验与高频管理能力 | 补全通用 `ForwardPage` 页面转发与 `RunLogin` OAuth 登录中继桌面入口、macOS 钥匙串/Touch ID 快捷解锁；在真机消除 `hermes.yaml` 剩余 `verify: pending` 并新增干净 `pi.yaml`；与兄弟记忆项目 **ValiseMemo** 集成（单 SQLite WAL 记忆文件纳入备份保护 + 通过 SSH 按需控制面在 Agent 页提供「记忆」标签页，等待 ValiseMemo 达成 M3/M4 后接入）；完成文案去「过度解释」专项审计 |
| **第三幕：Valise Harness（长期演进）** | 以现有 `profiles/*.yaml` 为基础，可选提供原生个人助手 Harness | 让用户“带着自己的个人 Agent 与记忆去旅行”，同时长期保持对 OpenClaw / Hermes / Pi 等通用开源 Harness 的一等兼容性 |

| 研发主阶段 | 状态 | 交付依赖与当前关系 |
| --- | --- | --- |
| N1 规范与服务契约 | `Done`（2026-11-05，契约设计与规范同步） | 冻结身份/关系记录、恢复安全边界、任务契约与人机确认边界 |
| N2 Agent 汇总、保护计划与时间轴 | `Done`（2026-11-07，后端与实机验收；界面随 N4） | 承接跨服务器汇总、权威保护计划（默认 `full`）与时间轴 |
| N3 严格副本、自动同步与失联恢复 | `Done`（2026-11-08，后端与实机验收；界面随 N4） | 承接严格副本校验、云盘直传/中转与原服务器失联恢复 |
| N4 mygo v0.9.2 GUI 主流程与 v0.1 收口 | `In progress`（主流程 B1–B3.2 已真机验收；正在做 `v0.1` 零 CLI 收口与 WebDAV/S3 云盘） | GUI 主流程已完成从零、接入、双轨备份、云盘、直接恢复/失联恢复/导出与原生物流；TUI 1:1 对齐自 `v0.1` 冻结至后续版本 |
| N5 管理方自动化、自定义与可选扩展 | `Planned`，延后至 `v0.2+` | 管理方 MCP、结构化 CLI、自定义范围与记忆扩展按各自契约独立推进 |

### N1：正式规范与共享服务契约

- **状态（2026-11-05）**：`Done`（设计）。用户确认身份/关系保存在备份库外、源失联且独占身份无法核实隔离时不凭声明完整启动、管理方请求由人类 GUI/TUI 进程确认并执行。已定稿 [具体服务契约](SERVICE_CONTRACTS.md)，同步 PRODUCT / ARCHITECTURE / VAULT_FORMAT / PROFILES / INSTALL / AGENT_KIT / UI 与开发指令。
- **交付**：同步产品定位、GUI/TUI/CLI 职责、Agent 首页及旅程术语，保留内部 ID、旧参数、档位与备份库兼容性。对共享 service 的读取、计划、执行、进度、等待确认、取消、结果及重连查询逐项定义契约，对接专用 journal，不将前端内存作为任务事实。
- **已定稿**：身份及迁移关联；服务器/客户端权威计划的版本、并发冲突和提交失败处理；timeline/task API；确认与冻结计划、对象、阶段绑定及变化/过期失效；任务持久化、重复请求、取消/断连核实与跨进程请求边界。自定义范围只冻结原则，仍在 N5b 开工前定稿，不成为默认 `full` 备份的前置。

### N2：Agent 跨服务器汇总、权威保护计划与时间轴

- **状态（2026-11-07）**：`Done`。fake-ssh 与真实 systemd 靶机验收见 [acceptance/N2.md](acceptance/N2.md)。
- **交付**：共享 service 跨服务器汇总 Agent 和诚实的离线/过期状态；实现明确迁移记录关联，不能凭同名合并，创建副本保持独立；服务器权威保护计划（默认 `full`，自动/一次性任务分离模式，错过的定时记录与可选补跑）与严格事件时间轴；`linger` 与时钟健康检查及一键修复。

### N3：严格副本、跨前端自动同步与源服务器失联恢复

- **状态（2026-11-08）**：`Done`（用户确认）。fake-ssh、真实 rclone 与靶机验收见 [acceptance/N3.md](acceptance/N3.md)。
- **交付**：基于清单一览与内容哈希核对的严格副本判定；本机仓库自动同步收敛进共享 service；源服务器完全不可达时从完整本机/异地副本独立读取时间轴与恢复至可达目标；基于独立 `rclone` 程序的云盘“服务器登录 / 这台电脑登录”双路径与只追加同步。已知待实机核实的新增云盘包括 `OneDrive`、`Dropbox` 及正在补齐的 `WebDAV`、`S3 兼容存储`（在 `kit/tools.yaml` 标明 `verify: pending`）。

### N4：mygo v0.9.2 GUI 主流程与 v0.1 收口

- **状态**：`In progress`（核心闭环 B1–B3.2 已于 2026-11-16–19 完成多轮真实 Linux/macOS 验收；当前执行 `v0.1` 发行收口）。
- **界面技术约定**：桌面界面使用 mygo v0.9.2（2026-11-17 自 v0.7.3 升级）原生 GPU UI（`client/ui/gui/`，单文件 `<= 400` 行，全量走 `zh-CN.json` 词表），后续升级 mygo 仍须用户同意。
- **2026-11-15 架构收敛结果**：已撤销 A/B 双槽位并行运行环境及三档模式方案（归档见 [history/RESTORE_ENVIRONMENTS_DEPRECATED_2026-11-15.md](history/RESTORE_ENVIRONMENTS_DEPRECATED_2026-11-15.md) 与 [acceptance/N4.md](acceptance/N4.md) 6.8–6.39），统一回归单一“暂存校验 → 带交换日志原子交换 → `.valise-prev-<op>` 保留与一键回退”架构。

---

## MVP（v0.1 首个可发布闭环）目标与边界（2026-11-19 更新）

为确保个人用户能尽早用上可靠、零门槛可独立验证的桌面发行版（`Valise v0.1`），明确发版边界如下：

| 维度 | v0.1 Must-Have（必达闭环） | 延后与明确冻结项（Post-v0.1 / v0.2+） |
| --- | --- | --- |
| 目标平台与 Harness | macOS / Linux 客户端 + Linux VPS（Ubuntu/Debian ARM64/x86_64）+ **OpenClaw** 完整端到端闭环 | `hermes.yaml` 真机消 pending 后开放完整恢复；`pi.yaml`；更多 Harness |
| 恢复架构与测试 | 直通暂存校验（`.valise-stage-<op>`）+ journal 原子交换 + `.valise-prev-<op>` 保留与一键回退；健康页文件与暂存校验（`test_only`） | ~~A/B 双槽位与 `bwrap` 容器接管~~（**已永久撤销**） |
| 核心 GUI 零 CLI 闭环 | 添加/接入服务器、双轨定时与手动存档点备份、自动退役、晋升、本机/SFTP/云盘同步、旅程曲线、恢复/迁移/创建副本、失联恢复与导出、Agent 改名、服务器空间清理（prev/old）、跨机配置导入导出、资料包更新、服务器命令行、原生对话与向导 | 通用页面转发（`ForwardPage`）与 OAuth 登录中继完善；macOS 钥匙串/Touch ID 解锁；主题扩展（`valise-theme/2`、外部主题 `material`）；TUI 1:1 功能对齐 |
| 云盘存储支持 | Google Drive（已核实）+ `WebDAV` / `S3 兼容存储` 凭证表单接入（`verify: pending`，支持用户在界面显式勾选/CLI `--allow-pending` 试用并核实） | 其余自建协议或复杂企业云盘 |
| 独立兜底承诺 | `tools/standalone-restore/restore.py` 永久兼容 `valise-snapshot/1` 与 `valise-snapshot/2` 离线恢复（含源周期退役后） | 新快照格式扩展（留待 N5b 自定义范围再按需升级） |
| 自动化与扩展 | 基础 CLI 可用（`status`/`seal`/`revive`/`doctor`）；受管方技能保持现有边界 | N5a 结构化 CLI / 管理方 MCP server；N5b 自定义范围；ValiseMemo 集成；全仓文案“过度防御/过度解释”审计；第三幕 Valise Harness |

## 工程改进与交付批次记录（2026-11-15–19）

1. **分阶段剥离 A/B 槽位冗余代码（`Done`，2026-11-16）**：已在分支 `refactor/revoke-ab-slots-and-doc-audit` 完成 Stage 1–3 并通全仓自检。
2. **治理高耗时测试套件与文档规范化维护（持续进行）**：开发期坚持定向 `-race` 内循环与提交前 `./testbed/selfcheck.sh` 完整门禁；正式规范只保留终态，阶段验证流水统一写进 [acceptance/](acceptance/)。
3. **前端审计整固与界面整理（`Done`，2026-11-16/17）**：Phase F1（`d43f228`）、Phase F2（`2177be0`）与 `design/gui-polish-phase2` 三批界面精修全部通过用户窗口复核并合入 `main`。
4. **真机实测闭环（`Done`，2026-11-16–19）**：
   - **Phase B1（`cc13a31`）**：保护计划按原设置重生效、移除位置接口、`RequireGate` 本机仓库快速放行、`native --shell`、已有 Agent 下保留官方安装入口。
   - **Phase B1.1（`f328c4a`）**：移除位置汇总全服阻碍并一键清理、原生入口三分与引导前暂服、官方脚本哈希变化容错与自定义官方安装粘贴、`linger` 失败后 `sudo` 重试、`userTransport` 私钥口令 askpass 转交。
   - **Phase B2（`Done`，已真机验收）**：单一 Valise 口令统管、默认 30 分钟免输窗口、本机 6 位 PIN 码快捷解锁与错 5 次自毁包裹、已验仓库零私钥放行。
   - **Phase B3（`Done`，已真机验收）**：双轨日期周期/永久 `savepoints/`、主人晋升、服务器自动退役、长旅程虚拟化曲线、操作级 PIN 租约、云盘重登录、首页按服务器分组。
   - **Phase B3.1 & B3.2（`Done`，2026-11-19 `28b6696` `stage-test-20261119-b3.2` 真机「全部通过」并合入 `main`）**：服务器 `rclone` Kit 安装与 `PATH` 回退；云盘多服务器串行批量登录与会话内保留 `client_secret`；「服务器命令行」移回服务器详情页；本地 PTY 与实时窗口缩放 + 配色继承；Agent 页保留「原生对话」与「首次向导」及资料更新提示；设置页「资料包」在线更新与本地签名导入；清理 11 处重复/过度解释文案。
5. **当前批次 · v0.1 收口实施（`feat/v01-recovery-closure`，进行中）**：
   - 条目 1：桌面端接入服务器空间清理（`prev`/`old`）、Agent 改名与跨机配置导入导出；
   - 条目 2：打通 WebDAV 与 S3 兼容存储的表单登录与存储桶目标规则。
6. **后续专项（`v0.2+`）**：全仓 UI 文案二次审计（平衡精简其余「过度防御 / 过度解释」说明）、`ForwardPage`/`RunLogin` 桌面完善、Keychain/Touch ID、实机消除 `hermes.yaml` `pending` 与接入 ValiseMemo。

### N5：管理方 MCP、结构化 CLI、自定义范围与可选扩展（`v0.2+`）

各子项均为 `Planned`、未实现；可提前设计，各自完成验证后交付，不阻塞 `v0.1`：

| 子项 | 具体交付 | 依赖 | 验收 |
| --- | --- | --- | --- |
| N5a.1 结构化 CLI | 稳定结构化输入/输出、错误、等待确认及任务查询；保留旧命令参数 | N1 契约，复用 N2/N3 已验证 service | 自动化消费成功、失败、等待和断连结果；秘密不输出 |
| N5a.2 管理方 MCP | 首批只读查询、准备允许的计划、提交类型化请求及结果查询；由用户 GUI/TUI 进程确认并执行，不跨进程共享解锁身份或通用票据；不开放恢复/删除执行接口 | N1 授权/任务契约与桌面交互 | 实机验证 AI 发起与用户确认全链路、拒绝/过期和重连 |
| N5b 自定义范围（原 C） | 可保存范围方案、目录选择覆盖未来文件、范围预览和回滚语义；不自动扩大 roots 或跟随未知链接 | N1 scope/兼容、N2 计划、N3 校验恢复 | 真实数据备份/恢复及 GUI 选择验收 |
| N5c 可选扩展 | ValiseMemo 记忆保护与桌面只读视图、M14 更多 harness 覆盖（`hermes` 消 pending / `pi`）、M17 profile 编辑与 M8 显式 raw 能力 | 各自明确契约、权限、来源和能力核实 | 逐项确定交付和实机验收后开工 |

---

## 贯穿全程：`testbed/drill.sh`

每个里程碑都往演练脚本中追加对应的步骤，覆盖部署 → 登记 → 三个档位的封存 → 同步到 NAS → 脱离 Valise 恢复 → 回滚 → 跨架构迁移 → 故障与崩溃注入 → 克隆，最终输出 `ALL_PASS (failures=0)`。
