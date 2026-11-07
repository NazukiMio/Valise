# N1 共享服务契约（2026-11-05 定稿）

> 状态：**已于 2026-11-05 经用户确认定稿，尚未实现**。用户明确决定“按这三项建议定稿，同步正式规范”；决定记录见第 14 节。本文是 N2–N5 后续实现的服务契约依据，正式规范已同步。设计定稿不等于代码、协议发布或实机验收完成；本文不提升 porter 协议、profile 或金库格式版本，不引入 mygo。
>
> **2026-11-07 增补**：用户在 N2 实施中追加决定（服务器任务交给一次性 systemd 单元执行、错过的定时备份记录与可选补跑、被引用复制目标的版本化修改、来源未知快照的恢复确认、linger 与时钟的一键修复）。增补条款见 [N2 实施记录](N2_IMPLEMENTATION_2026-11-05.md) 的“增补决定”一节，与本文冲突处以增补为准；本文其余条款不变。
>
> 依据：PRODUCT.md 6.1–6.4、ARCHITECTURE.md 4.2 / 5.1 / 7.1–7.2 / 8.2.2 / 11、VAULT_FORMAT.md 10–11、INSTALL.md 4.2 / 5.2–5.3、UI.md 9–10、DEV_PLAN.md N1–N5。本文的类型、方法、文件名和能力名是**已定稿的实现设计约定**，不代表现有 API；线级协议字段、能力通告和发布版本在对应实现阶段按第 11 节映射，不能借映射改变已确认语义。

## 1. 交付范围与设计原则

N1 交付共享 Go 服务契约、持久化边界、兼容规则和验收矩阵；N2/N3 分别实现对应能力，N4 适配 GUI/TUI，N5a 适配结构化 CLI / 管理方 MCP。保持 `client/ui/service`，现有方法逐场景适配，不整体重写。

- 查询返回事实及证据；准备计划不执行服务器修改；计划确认与权限解锁分开；执行产生可查询任务。
- 服务层不依赖 mygo、Bubble Tea、MCP 或终端 stdout。原生 PTY、口令、机密输出不进入通用 DTO 或任务事件。
- 新类型按领域拆分至 service 子文件；可复用纯类型放 core，但不引入任何 harness 名称、路径或专用命令。
- 不改变 `valise-vault/1`、旧 tier、旧恢复日志或 B03 状态机，不新增万能执行器或任意方法转发接口。
- 本文设计新入口的边界；旧入口迁移前继续真实报告其能力，不能仅包装返回类型便宣布完成 N2/N3。

## 2. 公共类型与读取规则

以下是 Go 风格的类型约定，落地时所有传输字段带 snake_case JSON 标签。标识均不透明，客户端不得解析其前缀来推断权限；新随机标识用标准库 `crypto/rand` 生成 128 位随机值，已有 VaultID / SnapshotID 保留原格式。

| 类型 | 契约字段 | 语义 |
| --- | --- | --- |
| `ServerRef` | `server_uid`, `connection_id` | server_uid 是服务器 Valise 管理记录身份；connection_id 只定位本客户端的连接配置，不是服务器身份 |
| `InstallRef` | `server_uid`, `install_id` | 沿用一台服务器一个 profile 一个安装的现有限制，不借新 ID 宣称支持任意多安装 |
| `AgentRef` | `server_uid`, `install_id`, `instance_id`, `generation` | 引用一次被明确接受的实例世代；名字、路径、日历周期、加密 epoch 都不是 generation |
| `SnapshotRef` | `vault_id`, `snapshot_id` | 跨备份位置的快照身份；leaf、目录、原服务器别名是定位提示，不能替代它 |
| `TaskRef` | `executor_id`, `task_id` | executor_id 定位创建任务的客户端执行环境；远端 op_id/run_id 另外记录。跨进程只能读取或认领明确的待办，不能靠 task_id 获得执行权限 |
| `LocationRef` | `kind`, `id`, `authority_id` | kind = server_vault / local_repo / station / legacy_target / recovery_cache；同名位置不视为相同 |
| `Observation[T]` | `value?`, `status`, `observed_at?`, `last_attempt_at?`, `source`, `error?` | status = fresh / stale / unknown / not_applicable；失败不把旧 value 当作当前值，unknown 不等于不存在 |
| `Page[T]` | `items`, `next_cursor?`, `view_revision`, `observations`, `partial` | 分页不是事务快照；游标绑定查询条件与固定查询视图，不混入后续到达的节点 |
| `ServiceError` | `code`, `reason`, `suggestion`, `detail?`, `retry`, `task_ref?` | 复用 UserError 的四个展示字段；retry = safe_query / replan / reconcile / none，不返回秘密或原始远端 stderr |

时间使用 RFC3339 UTC；日程另外保存 IANA 时区，不能用固定 UTC 偏移代替。版本号是单调递增整数，对 JSON 以十进制字符串传输；内容摘要使用 SHA-256，仅作绑定/变更检测，不作签名或授权凭据。

读取方法统一 `(ctx context.Context, request) (response, error)`；读取 ctx 取消只结束本次查询。多服务器查询按服务器分别返回 Observation；一台失联不丢掉其他结果，不把离线缓存冒充实时清单。空列表只有在查询完整成功时才表示确实没有对象。

## 3. Agent 身份与旅程关联（方案 A，已确认）

### 3.1 权威记录与旧记录

在服务器配置目录保存独立私有身份记录：`identity/server.json` 和 `identity/agents/<instance_id>.json`，设计格式为 `valise-identity/1`（实现时引入，不属于金库或 profile 格式）。后者包括 AgentRef、接受的 roots / profile 摘要、状态、创建时间和明确的接续记录引用；不含文件内容、环境值或凭据。目录 0700、文件 0600，写入采用锁、同目录临时文件、fsync 与 rename；只由主人权限的接入/重新接入/恢复收尾写入。日常读取不初始化这些文件。

身份独立于现有 dossier 文件，避免旧客户端重新保存 `valise-dossier/1` 时丢掉新字段。身份与 dossier 更新以固定的接入提交记录协调；未完成提交不发布新 generation，后续查询报告需要核实，不猜测成功。读取旧实例返回 `identity_state: legacy_unbound` 和本客户端的定位引用，不能伪造远端稳定身份。下一次用户明确接受接入时才建立身份。

`server_uid` 不能替代 SSH 主机密钥验证。同一远端账户经不同别名连接，核对主机信任与 server_uid 后才可去重；同 uid 出现在不相符的主机/用户上标为身份冲突，要求核实，不能自动合并克隆的系统盘。主机密钥变更沿用现有停止规则。

### 3.2 接入、恢复、迁移与创建副本

- 对已登记且未被取代的同一实例重新检测、升级 profile 或修改显示名，保留 generation；接受 roots / 来源变化仍须重新预检，所有旧执行计划失效。
- 明确作为新实例接入、清理记录后重新使用 id、重新接入已被取代的 id：创建新 generation。选择继续旧实例的特殊情况须展示已有身份与路径，不凭同名推断。
- 恢复当前实例保留目标 generation；记录“由 SnapshotRef 恢复”，不把时间点之前的世代重新写成当前世代。
- 成功迁移：目标拥有新的 AgentRef，以 `migration_completed` 边接续原旅程。创建副本：新 AgentRef 与独立旅程，仅有 `clone_from` 来源关系。失败、回退、仅暂存通过、仅文件恢复都不生成成功迁移边。
- 迁移关系字段：`relation_id`, `kind`, `source_agent?`, `source_snapshot`, `target_agent`, `operation_ref`, `verified_at`, `source_disposition`。source_disposition = stopped_verified / exclusive_omitted / unresolved；unresolved 不能宣称完整迁移完成。
- 成功目标端保存关系的权威记录；客户端缓存，原服务器可达时补记，补记失败不撤销已成功恢复。关系不进入金库，源失联且没有任何可靠 AgentRef 证据时，只关联 source_snapshot，保留“来源实例身份未知”。

### 3.3 与快照的关联

不为新世代改写旧快照。新备份在服务器外部运行记录中保存 AgentRef → SnapshotRef 的明确绑定；旧快照无绑定时展示在该金库历史中。仅靠同 instance_id / 同周期不能把旧世代全部挂到当前 Agent。丢失运行/迁移记录后仍可从完整金库重建保存点和导出，只会缺少世代、原因、教学标记和迁移关联。

## 4. 保护计划：权威、版本与兼容

### 4.1 两部分状态

`ProtectionView` 包含 `server_plan`, `client_binding?`, `effective_summary`, `legacy_settings?`, `warnings`。两部分分别保存、分别版本化，不构造跨机器原子事务。

| 部分 | 字段 | 权威与限制 |
| --- | --- | --- |
| `ServerProtectionPlan` | `schema_version`, `agent_ref`, `revision`, `scope`, `schedule`, `period`, `server_copies`, `accepted_at` | 服务器独立执行需要的全部设置；新建 scope = preset/full；定时备份须明确启用 |
| `scope` | `kind: preset`, `tier` | 只接受现有 tier；N2 新建默认 full，显式旧范围保留；custom 返回 unsupported_scope |
| `schedule` | `enabled`, `daily_at`, `timezone` | 首版新向导用每天 HH:mm + IANA 时区；旧非每日设置保留 legacy 表达，转换须显示差异。返回最近/下次触发及 linger 等实际条件，不承诺错过后的重放 |
| `period` | `unit`, `timezone` | 复用现有 week/month/quarter 与周期边界规则；与 schedule 时区分开，修改只影响下一个叶子。旧 `.vault.json` 是兼容投影，不再作为第二个可独立覆盖的权威 |
| `server_copies[]` | `target_ref`, `enabled`, `minimum_interval` | 引用现有目标，不含密码；直传/证书/中转条件分别报告，配置开启不等于实际可更新 |
| `ClientProtectionBinding` | `client_id`, `agent_ref`, `revision`, `server_revision_seen`, `local_copies[]` | 本客户端私有；每项引用 repo_id 和是否随成功备份/客户端重新打开时补齐；路径仍取已有 repos 配置 |

持久化到服务器 `protection/<instance_id>.json`（`valise-protection/1`）和客户端 `protection-bindings.json`（`valise-protection-bindings/1`）；均在现有 Valise 配置目录内，0600，原子替换，不放金库或活动 roots。

日程的解释由目标 systemd 校验；计划返回下一次触发时间及夏令时预览。无相应时区/日程能力就拒绝启用，不由客户端偷偷转成固定 UTC。首版不承诺 exactly-once 每天触发；漏执行按实际记录显示。

### 4.2 读取与更改

- `GetProtection(AgentRef)`：只读；无新计划时返回 `mode: legacy`，明确现有 tier / timer / 复制规则，不能补写 full。
- `PrepareProtectionChange(agent, expected_server_revision, expected_client_revision?, desired)`：计算服务/停机影响、读取成本、内容增减、复制条件及旧设置差异，返回 FrozenPlan；不修改 timer 或配置。
- `StartProtectionChange(plan_id, submission_key)`：登记任务，确认/授权缺失时返回 waiting，齐备后经第 8 节核实才执行。首次新建用 expected revision = 0；所有更新均比较 generation 与精确版本，冲突返回 revision_conflict，不做 last-writer-wins。
- 客户端部分更新成功但服务器部分失败，或相反：返回分项结果及需要重读的版本；不伪造整体回滚。组合执行先提交服务器，成功后提交本客户端绑定；本地提交失败保留服务器结果，补写须重新比较本地版本。

服务器提交采用**固定保护计划事务**：写入 intent（旧/新版本及受影响 timer、周期兼容投影配置）→ 阻止新的按计划任务启动 → 安装/检查新配置 → 原子发布新计划 → 核实 systemd 生效及周期投影一致 → 记录提交完成。提交前失败恢复旧 timer；提交后核实失败保留 committed 与 reconcile_required，不倒退版本或谎报仍为旧计划。进程崩溃时保持 pending 阻断，用户显式核实/恢复；不自动重复结果未知的 systemctl 步骤。已开始备份继续使用冻结的旧版本，不在中途切换。

### 4.3 新旧客户端与旧入口

- 未采用新计划的实例维持原逻辑。采用时展示 scope、timer 和复制差异，由主人确认；不能读取一次就接管。
- 新 timer 调用固定“读取已接受计划”的 porter 入口，不把 full 或整个计划内容固定写进 unit argv；计划 pending / 损坏 / generation 不符时拒绝运行，不回落 soul。
- 已接管实例的旧 timer_set、周期/同步自动设置等**写入口**必须被新版 porter 的统一边界检查拦下，要求支持版本比较的新入口。共享目标的改名/移除/auto 修改影响多个计划时，列出受影响 Agent 并统一核对引用版本；不能只拦 GUI。
- 旧显式 seal tier 与受管方 MCP soul / 频率限制保留；其返回标明是显式/旧入口，不暗中读取新默认。新日常备份读取同一服务器计划。
- 旧客户端仍能作其原有读取与显式备份；未知字段不能变成“没有计划”。新客户端连旧 porter 可读旧事实，但拒绝提交新计划，不静默模拟。
- 新版 porter 必须识别新配置标记并关闭无版本写入旁路。人为降级成不认识该标记的旧 porter 无法保证这些限制，N2 不支持此降级，更新/回退流程须先检查并拒绝；不能宣称新客户端可以约束任意旧程序或用户自行 SSH 改文件。
- 新计划的 per-Agent 复制规则只用于该实例；其他 legacy 实例保留旧 targets.auto 行为。同一次备份只选择一种策略，不重复触发新旧自动复制。

## 5. 时间轴与副本证据

### 5.1 读取方法

| 方法 | 请求 | 结果 |
| --- | --- | --- |
| `ListAgents` | 服务器筛选、是否含旧实例、cursor/limit、refresh_policy | AgentSummary：AgentRef/legacy 定位、安装归属、显示名、各自 Observation 的运行/保护/健康摘要、可用能力；部分失败独立列出 |
| `GetAgent` | AgentRef 或 legacy 定位 | 详情、来源、已接受布局版本、共享服务影响、入口元信息；敏感 URL 和环境内容不返回 |
| `ListTimeline` | subject（AgentRef 或一个已打开金库集合）、from/to、tier、cursor/limit | TimelinePoint、缺口与查询完整性；不依赖连接原服务器 |
| `GetTimelinePoint` | SnapshotRef + 可选 location_refs | 当时 tier、时间/大小、已知原因、每处材料/校验 Observation、已记录恢复动作 |
| `ListTaskHistory` | AgentRef / SnapshotRef、时间窗、cursor/limit | 操作记录；失败任务不混作恢复点 |

TimelinePoint 的核心字段：`snapshot_ref`, `created_at`, `recorded_instance_id`, `scope`, `metadata_state`, `agent_binding?`, `reason?`, `purpose?`, `copies[]`。scope 来自当时元数据，解密后核对头；元数据层发现的节点标 `metadata_only`，不承诺是可用恢复点。成功保存标记以完整发布的 `.age` + `.meta.json` 等记录核对，孤立 meta / 临时文件不作为已成功保存的点；同 ID 元数据或密文头不一致标 conflict，禁止混并。

索引是客户端 cache 目录内的可丢弃 `journey-index/`；保存源位置、文件身份/摘要和重建版本，不存文件列表明文。相同 SnapshotRef 在多个位置去重，source_locator 与来源 Observation 都保留。快照时间可能受服务器时钟影响，排序使用 `(created_at DESC, vault_id, snapshot_id)`，同时返回时钟警告，不靠 ULID 推断连续保护。

分页采用服务生成的查询视图 id + 位置游标；limit 默认 50、上限 200。查询视图过期/重建后返回 cursor_expired，前端重新查询并按 SnapshotRef 保持选中位置，不跳过或重复假装完整。条件改变旧游标失效；离线/读取失败时 partial = true，不能声称覆盖了整段时间。

### 5.2 独立证据类型

`CopyEvidence` 字段：`evidence_id`, `location_ref`, `subject`, `check`, `result`, `checked_at`, `source_revision`, `material_digest`, `verifier_version`, `error?`。subject 精确到 SnapshotRef / 整个 vault / 某目标环境；check 必须是以下一种：

| check | 证明什么 | 不能证明什么 |
| --- | --- | --- |
| metadata | 发现且解析了元数据与头文件位置 | 材料齐全、可解密 |
| snapshot_material | 已解密定位文件列表及引用，所有必要加密材料齐全 | 其他时间点或整周期齐全 |
| period_material | 与参照金库的快照/索引/pack/epoch/key 集合一致并已核对 | 明文能解、运行环境可用 |
| integrity | 指定范围内 pack 哈希、索引边界、头/meta/epoch 关系通过 | 明文正确、可启动 |
| decrypt | 指定快照文件全量解密并核对 SHA-256 | 暂存可写、服务健康 |
| staging | 指定目标 profile/版本/roots 的暂存与 SQLite 校验通过 | 已交换或启动 |
| runtime | 特定恢复操作实际交换后的服务检查通过 | 将来持续健康、任意其他目标能运行 |

result = passed / failed / unknown / stale / not_applicable；不得把其中一项 passed 折算成所有项通过。读取 encrypted manifest 要主人本机解锁；无钥匙时可以完成免钥匙校验，但单快照精确依赖闭包仍为 unknown，不用当前 index 文件误当全局索引。

整周期完整性必须带比较基准：结束周期的全量材料清单，或同一时刻冻结并验证的源清单；开放周期只能证明“齐至基准版本”。原服务器已丢失、没有可比较清单时，可证明**现有材料集合**完整/可恢复其所列快照，不能证明灾难前没有更多快照；无此证据不放宽删除权限。`.partial.json` 仍存在时不能标完整周期。

### 5.3 证据存储与失效

客户端 `verification-state.json`（0600）保存脱敏证据；服务器自身免钥匙校验可存其运行记录，客户端聚合但不升级权限。旧 repo-trial / restore-tests 迁移为带 `origin: legacy` 的历史结果；缺少对象/摘要/范围不作为新的 gate 放行证据。

- 材料摘要变化、损坏/替换、DELETING、partial 标记、位置改绑、核验器规则变化，重新核验相应证据。大小和 mtime 仅用于发现变化，不能代替哈希证明；受控追加后可复用已核对不可变 pack，但新的集合仍需核对。
- 本机外盘未挂载或位置读取失败：保留最后通过时间，当前状态 stale/unknown。执行前从所选位置重新核对材料；不允许永久缓存的绿色结论跳过恢复前验证。
- staging/runtime 绑定目标主机、Agent generation、profile 内容摘要、身体版本、roots 与具体操作；目标改变不迁移成功结论。历史测试通过仍如实显示日期，不按时间自动变成测试失败。
- 接口展示的本机证据不被 porter 视为用户自报的删除许可。INSTALL.md 4.2 自担风险继续保留，但确认仅绑定本次具体操作；不能越过来源哈希错误、身份冲突或不允许的命令等硬阻断。

## 6. 恢复来源与冻结计划

### 6.1 读取来源

`OpenRecoverySource(location_ref, vault_locator)` 只建立可查询的来源 handle，不能直接将任意路径交给旧 PlanRevive。服务器金库、用户明确添加/选择的本机目录、已配置远端位置分别用适配器；定位参数按各自安全规则验证。handle 不包含钥匙，不能授权写目标。

内部 `RecoverySource` 只提供 `InspectVault`、`ListSnapshots`、`ReadEncryptedObject`（允许的 VAULT/PERIOD/epoch/index/snapshot/pack 对象键）与 `Close`，不提供任意远端文件读取。大数据按流读取并验证；多位置补齐前核对 VaultID、SnapshotID、头摘要和 pack 内容哈希，冲突就停止。恢复缓存保留现有 partial 标记的原子写入和中断规则。

`PrepareRestore` 请求：`source_handle`, `snapshot_ref`, `target_agent_or_server`, `mode`, `include_secrets`, `root_mapping`, `profile_allowances`。profile_allowances 只对应既有显式允许（快照 profile、restore 能力未测试），不构造统一跳过检查开关。目标绑定主机信任身份，不能只绑定 SSH 别名。

从副本解密获取 profile_doc / provenance / roots / local_overrides；不先读来源服务器 dossier。选择本地已审核同 id/版本 profile 仍需核对内容及显示选择依据，不因 profile id 一样就合并不同命令。目标预检继续复用路径、空间、来源精确版本、共享服务、外部进程和 journal 规则。读原服务器仅为需要停源等明确动作，不是读快照的前提。

### 6.2 两次确认与源失联

恢复仍为：准备计划 → 第一次确认 → 准备材料及暂存校验 → 第二次确认 → 专用 journal 交换 → 复核。第二次确认绑定 op_id、暂存校验摘要、目标状态与同一冻结计划；不能以第一次确认自动交换。

源可达的迁移按既有规则停止原实例并核实，成功后才在目标启动。源不可达时只知道“无法核实”，不能当成 stopped。首版在独占身份无法核实隔离时仅提供“排除 profile 声明的独占凭据后恢复”或“仅导出/暂存、等待处理”；如果独占凭据内联于共享配置，说明被排除的整个文件及影响，不能扫描内容猜 token 或私自改写配置。首版不接受仅凭用户声明已关机/已撤销身份就带独占身份完整启动，也不提供风险确认绕过此条件。导出或暂存仍遵守凭据选择、权限与不启动的边界。

## 7. 计划、任务与结果类型

### 7.1 冻结计划

`FrozenPlan` 契约字段：

- `plan_id`, `kind`, `created_at`, `expires_at`（首次执行有效期为 10 分钟）。kind 使用固定允许列表，不接受 shell、表达式、任意 RPC 名称或用户构造步骤。
- `targets[]`（主机信任身份、AgentRef / 安装影响集合）、`preconditions[]`（各记录版本/摘要）、`server_plan_revision?`, `client_binding_revision?`。
- `scope`, `sources[]`, `commands[]`, `profile_digest`, `impact`（停服/空间/共享实例/明文导出）、`required_access[]`, `confirmations[]`, `blockers[]`, `warnings[]`, `plan_digest`。
- `steps[]` 仅为固定实现提供的展示步骤和取消边界，不是可解释的工作流输入；服务端从真实 profile 与事实重新计算，不执行客户端回传的 steps。

摘要覆盖真实目标、模式、范围、来源、profile/命令/脚本哈希、版本、复制目标、共享影响、凭据选择、用户风险选择。计划 id / 摘要不是授权。目标路径身份、来源精确版本/哈希、dossier、收件人集合、保护计划和实际命令等变化，旧计划失效并返回 plan_stale；空间等动态前提在副作用前再检查。不把正在变化的 Agent 文件内容哈希当作备份计划固定前提。

只读准备允许创建客户端查询缓存；不写远端实例、timer、收件人或暂存目录。必须登录管理权限才能读到的预检事实，以待解锁步骤表达；解锁后补全事实若改变计划摘要，就重新展示确认。

首次执行前计划到期须重新准备；执行已开始后不因这 10 分钟到期中断，但每次新的副作用仍核对适用前提。恢复第二次确认以暂存核验结果另发阶段摘要和 10 分钟确认期限；到期保持未交换，复核现场后重新展示，不能重用过期同意。

### 7.2 任务状态

`TaskView`：`task_ref`, `kind`, `subject_refs`, `plan_id`, `plan_digest`, `state`, `wait?`, `stage`, `progress?`, `cancel`, `components[]`, `operation_refs[]`, `observed_at`, `revision`。

| state | 进入条件 | 可执行下一步 |
| --- | --- | --- |
| waiting | 计划准备好但缺用户确认、解锁、风险选择或第二次交换确认 | 按具体 wait requirement 由本地用户处理；拒绝可取消 |
| running | 固定执行器已取得互斥，正在执行可核实阶段 | 查询/订阅；按 cancel 声明请求取消 |
| reconciling | SSH 断开、执行响应丢失、进程退出，当前结果不明 | 查询真实运行记录与专用 journal；不能自动重跑 |
| needs_recovery | 有已确认的残留/未完成专用日志，需要用户选恢复分支 | 返回经过现场校验的 recovery_actions |
| succeeded | 本任务承诺的全部必要分项有成功证据 | 查询结果；不推断未请求的运行验证 |
| partial | 必要分项有成功也有失败/未完成，执行已停止且结果已知 | 保留成功产物和失败明细，重新准备后续任务 |
| failed | 必要目标未完成且没有尚不明确的执行结果 | 原因/下一步；回退结果独立列出 |
| cancelled | 已核实停止，且无未解决的结果不明阶段 | 保留已发生副作用及残留说明，不自动清理 |

wait.kind = confirmation / owner_unlock / admin_login / risk_ack / stage_confirmation；断连不伪装成 waiting。存在未知远端结果优先 reconciling，不用 partial 吞掉未知。只有全部事实确定后才能进入终态；回退成功不等于原任务 succeeded。

分项 `ComponentResult`：`kind`, `target_ref`, `status`（not_started / running / succeeded / failed / skipped / unknown）, `artifact_refs`, `evidence_refs`, `error?`。例如“服务器备份成功、复制失败”返回 partial，并保留可浏览 SnapshotRef；“配置已发布、服务启动失败”保留 publish 成功和 start 失败；原本 inactive 的服务保持停止，不制造 runtime passed。

### 7.3 方法集合

| 方法 | 契约 |
| --- | --- |
| `GetCapabilities` | 返回本客户端及各服务器的已实现版本能力、允许操作和限制；不以 UI 功能存在推定远端能力 |
| `PrepareBackup` / `PrepareCopy` / `PrepareRestore` / `PrepareProtectionChange` / `PrepareAction` | 类型化请求返回 FrozenPlan，保留旧方法适配；无通用 Execute(method, args) |
| `StartBackup` / `StartCopy` / `StartRestore` / `StartProtectionChange` / `StartAction` | 只接受 plan_id + submission_key，登记并返回 task_ref；缺确认/授权时返回 waiting，受信人类交互补齐后重新核实才异步执行。不能向 MCP 整体暴露这一组方法 |
| `GetTask` | 返回当前已核实视图；重启后从运行记录与专用日志核实，不从旧 UI state 恢复 |
| `ListTasks` | 按服务器/Agent/时间/状态筛选，包含需要恢复的远端操作 |
| `WatchTask(task_ref, after_sequence?)` | 先返回当前完整视图，再推送增量；事件丢失返回 resync_required。取消订阅不取消任务 |
| `RequestCancel(task_ref, expected_revision)` | 返回 accepted / deferred / rejected 及实际任务状态；不是清理、回退或撤销接口 |
| `PrepareRecovery(task_ref, recovery_action)` | 依据 revive / action-journal 支持的分支重新准备计划，现场不符停止 |

安装/原生终端/登录/隧道仍使用自己的服务适配与会话生命周期，只将脱敏摘要映射到任务。N1 不将 `exec.Cmd`、PTY 字节、令牌 URL、剪贴板秘密转换为 JSON 任务参数或事件。

### 7.4 关键请求与人类交互接口

| 请求类型 | 必需选择及版本约束 |
| --- | --- |
| `BackupRequest` | AgentRef、mode = accepted_plan / explicit_preset、expected_server_revision（按计划模式必填）、可选 explicit_tier（显式模式必填）、受限 purpose。reason 为可选用户文字，限长、不得写入金库元数据 |
| `CopyRequest` | subject = SnapshotRef / 指定 vault 基准、source_location、destination_refs、expected_destination_revisions；全周期复制仍使用现有布局，不因只选时间点就把 partial 标完整 |
| `ActionRequest` | AgentRef、action_id、dossier/profile 摘要、操作前备份选择、允许的风险选择；实际命令只取远端已接受 profile，不接受 argv 输入 |
| `RestoreRequest` | 第 6 节请求字段；target_agent 时需 generation，迁移/副本的新目标需明确新 id/roots；不通过旧 host 字符串暗中指定恢复来源 |
| `ProtectionChangeRequest` | 第 4 节 desired 全量替换及两个 expected revision；缺失字段不能被默认解释为关闭原设置 |

内部人类交互接口为 `ReviewTask(task_ref) → ReviewView`、`RecordDecision(task_ref, expected_task_revision, plan_digest, phase_digest, choices)`、`SatisfyAccess(task_ref, requirement_id, interaction_adapter)`。这些接口只注册给受信的人类适配器，不能作为通用 JSON/RPC/MCP 方法导出；interaction_adapter 是进程内函数/句柄，不是可序列化口令参数。拒绝记录为取消请求；解锁后条件变化需要新的 review，不自动沿用旧确认。

新方法返回的 FrozenPlan/TaskView 都是纯 DTO；解密身份、打开的 Vault、transport、stage writer 仍由执行器进程内持有。进程退出后重新打开并核实，不从 JSON 恢复这些资源或确认回调。

## 8. 授权、确认与自动化边界

- `CallerContext` 由进程启动/适配层注入，值为 human_gui / human_tui / human_cli / manager_mcp；请求 JSON 不得自称 human。受管方 MCP 仍是另一套服务器接口，完全不接入此身份。
- `AccessRequirement` 按目标列 daily / owner / admin、原因、是否需要独立连接及 gate 检查。复用原 owner challenge、Valise 内存解锁窗口、ssh-agent 与临时管理登录；每次管理操作新连接，不能将 admin 窗口看作复用 SSH 会话许可。
- `ConfirmationReceipt` 只存在服务内部，绑定 plan_id、plan_digest、具体阶段、调用来源、本地交互会话、过期时间、一次性消费状态及明确选择。通用 API 不接受 `confirmed: true` 或用户提供 receipt 文件作为同意证明。
- 用户点击确认或在受信 CLI/TUI 中交互回答后，适配层通过内部人类交互接口记录决定；口令经专用内存通道/askpass，不存进 receipt。危险名称确认、风险自担、凭据导出和恢复第二次确认各自绑定对应计划，不复用其他操作的同意。
- 既有 CLI 的 `--yes` 等显式自动化语义不被新包装擅自更改；不能用这条兼容规则把 MCP 请求转成带 `--yes` 的 CLI。新 CLI 可用哪些非交互确认项在 N5a.1 逐项公布。
- 确认与解锁顺序由缺项决定，已有有效窗口可复用，确认不能代替 owner 证明；窗口到期等下一安全边界暂停再解锁，不能为等口令挂住交换中的原子恢复段。
- 恢复交换前必须重新验证足以完成该次交换的会话/许可条件；进入交换后按专用 journal 完成或回退。高级模式按 INSTALL.md 4.1 逐步检查、到期暂停，不能被统一任务状态放宽。

### 8.1 管理方跨进程请求（已确认，N5a.2 实现）

不跨进程传递解锁身份、ssh-agent 凭据或通用确认票据。管理方 MCP 只提交脱敏、类型化请求到本地私有待办箱，GUI/TUI 人类进程在打开时读取，重新准备冻结计划、展示并确认，**由该人类进程执行**；MCP 只能查询许可范围的结果。

待办箱使用客户端 state 目录 `management-requests/`（0700/0600），字段为 request_id、origin、允许的 operation_kind、类型化选择、submission_key、状态及结果 task_ref；不是步骤/命令文件。每项用文件锁和原子 revision 更新认领，执行者崩溃转核实，不因 lease 时间到了就让另一进程重跑。

没有可用人类会话时返回 waiting_for_user，CLI 不在后台冒充 GUI 弹出秘密输入；用户主动打开后处理。重开后重新确认，不凭磁盘上 approved 字段恢复执行授权。过期、取消或参数变化使旧请求无效。客户端配置目录不是对同 uid 恶意程序的隔离边界，不能宣称文件箱保护能防住本机已被控制。

允许的管理方工具清单仍留 N5a.2 逐项评审；恢复、删除、读秘密、任意写、原生终端和高级模式不得通过请求箱间接执行。N2–N4 不需要提前实现请求箱。

## 9. 执行、持久化与重复请求

### 9.1 权威与存储

| 数据 | 设计位置 | 权威与恢复 |
| --- | --- | --- |
| FrozenPlan | 客户端进程内；必要远端计划按固定执行器保存摘要 | 进程退出后不复用旧确认；重开需重新核实/准备 |
| Task 任务索引 | 客户端 state `tasks/<task_id>.json` | 只存脱敏目标、提交键、分项、远端操作引用和最后核实状态；不是执行权威 |
| 远端备份/复制运行记录 | `~/.local/state/valise/runs/<run_id>.json` | 固定 backup/copy 执行器在副作用前记录 accepted，完成后记产物；timer 与 MCP 也可映射为外部任务历史 |
| 保护计划提交 | 服务器 state `protection-changes/<change_id>.json` | 只协调固定设置/timer 发布；不能执行自定义步骤 |
| 恢复/B03 | 现有 journal / action-journal | 原状态机、锁、intent、恢复许可与损坏拒绝继续权威 |
| 迁移关系 | 目标端 identity 目录下按 relation_id 记录，客户端缓存 | 丢失关系只降级关联，不阻断读金库 |

任务记录不存口令、环境、原生输出、快照文件内容或解密后的文件名清单。配置/目标路径等本来可见的元信息仍按 0600 保护；向 MCP 投影再删掉非必要的敏感字段。

任务索引损坏不能自动运行或抹掉远端日志；报告 record_corrupt 并通过已知服务器的 hello / 专用查询发现待处理记录。清理执行残留和删除备份继续需要显式命令，不因任务终态自动清理用户数据。

### 9.2 去重与断线

submission_key 标识一次用户意图。服务在发起副作用前持久记录 `key + kind + normalized_request_digest + task_ref`；同键同请求返回同任务，同键不同请求返回 request_conflict。新键表示新的意图，不因同日/相同 scope 自动合并备份。

远端请求必须另有服务器可核对的 run_id 或现有 op_id。收到重试先查既有记录，不能仅依赖本机 task 索引去重；备份完成即将 SnapshotRef 和运行记录关联。旧 porter 无运行记录能力时，不对网络超时后的重试承诺去重；返回 reconciling，先核对快照/审计，无法唯一定位则要求用户明确发起新任务，说明可能多出一个时间点。

不得承诺网络环境下 exactly-once。跨“副作用已完成、完成记录尚未写入”的崩溃，只有对应固定执行器能以 op_id/快照发布记录证明结果；证据不足就 needs_recovery/reconciling，绝不靠重发猜测。后续复制失败的重试使用新的复制计划和既有 SnapshotRef，不重新备份。

### 9.3 互斥

沿用服务器 vault.lock、恢复和 B03 的现场互斥；新增任务索引锁不能替代它们。服务层按服务器、安装共享服务、Agent roots 和位置投影冲突，最终由 porter 在执行边界复检。多个资源按固定顺序取锁；冲突返回 resource_busy 与可公开的操作引用，不无限排队持有其他锁，也不在等待用户确认期间占用普通执行互斥。已持有专用日志保护的暂存操作沿用其 reservation，不允许相交操作闯入。

## 10. 取消与生命周期

| 事件/阶段 | 契约 |
| --- | --- |
| 切页 / 关闭详情 | 结束页面订阅；已确认任务由当前存活进程继续，重新进入先 GetTask |
| 显式取消，尚未副作用 | 取消已核实后 cancelled；不再接受该计划的旧确认 |
| 拉取/复制中 | 在可中断 I/O 边界停止，保留 partial/只追加材料和已成功产物；报告残留，不谎称撤销 |
| 备份一致性停服/服务恢复中 | 请求可 deferred，先按固定执行器恢复原服务并核实，再报告结果；不能强杀而遗留停服 |
| 暂存完成，未交换 | 可停止推进；丢弃暂存是独立显式恢复/清理动作，权限不变 |
| 恢复已进入交换 | rejected，或仅取消后续订阅；继续完成/回退并写专用 journal，不因页面关闭中断 |
| B03 | 严格按其阶段、执行者存活与恢复分支；不将通用 Cancel 翻译为危险的 discard/replay |
| 关闭最后窗口 / 正常退出进程 | 关闭 PTY/隧道按会话规则；对任务展示仍在执行的阶段，退出前协调到安全边界。若交换已开始则等待其完成/回退或转专用恢复状态；不承诺 UI 消失后进程永远存活 |
| 强制结束 / 断电 / SSH 断连 | 查询记录后 reconciling/needs_recovery；不自动重新执行，不写“已取消” |
| 服务器 timer / 已有有限 autosync 子进程 | 可按既有机制执行后退出；不新增常驻服务；客户端复制因客户端退出停止，并如实记录 |

事件结构：`task_ref`, `sequence`, `task_revision`, `type`, `at`, `payload`；payload 只为脱敏状态/进度/分项。事件可丢弃，慢订阅者不阻塞执行；sequence 仅在该任务可用事件流内单调，跨进程重启可返回 resync_required，GetTask 是恢复视图入口。total 未知时不给百分比，阶段完成不代表任务完成。

## 11. 能力协商与错误

GetCapabilities 返回本客户端服务契约版本以及远端支持的 `identity`, `protection_plan`, `run_status`, `recovery_source` 等版本化能力；具体 hello 扩展字段待 N2 实现时核对现有协议并定版。已有 action_transactions 能力和 pending_actions 继续保留，不能用协议 minor 数字替代逐能力判断。

修改方法在第一步和执行前都验证必要能力；不认识 schema/tier/mode 拒绝写入，读取可展示“版本不支持”但不能按默认值解释。不得在本文仅凭新方法名就发布协议 1.4 或其它版本。

必需错误码：unsupported_capability、unsupported_schema、unsupported_scope、identity_conflict、identity_unbound、revision_conflict、plan_stale、plan_expired、confirmation_required、access_required、resource_busy、source_conflict、material_missing、integrity_failed、profile_not_allowed、exclusive_identity_unresolved、cursor_expired、request_conflict、result_unknown、record_corrupt。

错误包含失败原因及下一步；无法证明“没有”时用 unknown。外部命令仅返回允许的退出码/固定错误分类，不能原样附 stdout/stderr、token URL、env 或文件内容。实际命令展示来自 profile 的可公开 argv，敏感参数只可在专用用户流程使用。

## 12. 自定义范围的冻结边界（N5b，不阻塞 N2）

N1 先固定：scope 与分类分开；secrets 优先级不可削弱；不自动扩 roots / 跟未知链接；SQLite 数据库及附属文件作为一致性单位；读失败不当作文件不存在；范围外携带与范围内删除必须能区分。

N2/N3 的 scope 只接受 preset。N5b 必须另行定稿匹配规则、快照完整范围记录、回滚携带/删除、最低操作前保护与旧工具在交换前拒绝策略；如果改变公开格式就升版本并同步 standalone。不能先塞可选 custom 字段然后让旧工具按 full 误恢复。

B03 操作前仍使用已确认标准备份要求；新日常 full 不擅自改变 B03 的退路规则。新动作的最低覆盖设计须保证待修改配置与恢复所需内容已保存，不能仅凭 tier 排序认定覆盖；不能通过自定义排除项降低既有要求。

## 13. 实施映射与验收矩阵

| 编号 | 场景与输入 | 必须观察到的结果 | 验证阶段 |
| --- | --- | --- | --- |
| I1 | 两台服务器同 profile/id/显示名 | 两个 Agent，不能合并 | N2 fake-ssh |
| I2 | 旧实例只读；接受接入；profile 升级；重用已清理 id | 只读不写身份；明确建代；升级保代；重用新代 | N2 fake-ssh + 实机 |
| I3 | 成功迁移、失败回退、创建副本、身份关系丢失 | 仅成功迁移接续；副本独立；关系缺失不阻断金库读取 | N2/N3 |
| P1 | 新建计划/未确认/旧 timer | 默认 full 尚未启用；未确认不落盘；旧 soul 保持 | N2 |
| P2 | 两客户端同时改同 revision；修改中崩溃；本地绑定写失败 | 拒绝陈旧写入；pending 阻断/核实；分项结果真实 | N2 fake-ssh + systemd 实机 |
| P3 | 接管后旧客户端改 timer/targets.auto；旧 seal / MCP | 无版本变更被拒；旧显式范围与 MCP 限频不变 | N2 |
| P4 | 客户端关闭、退出登录、重启、时区/DST、linger 不满足 | 真实 timer 行为与预览/条件一致；不能保证的运行显示限制 | N2 实机 |
| T1 | 原服务器与本地索引均丢失，只保留完整副本和恢复钥匙 | 可重建快照列表并导出；原因/世代未知不编造 | N3 + standalone |
| T2 | 孤立 meta、同 ID 不同头、同数量不同集合、过期分页 | 不伪造恢复点、不混并副本、拒绝旧游标 | N2/N3 |
| V1 | 缺 pack/index/epoch/key、损坏、partial、开放周期继续追加 | 区分材料/校验；不伪造整周期完整或删除许可 | N3 |
| V2 | 试解通过但目标 profile/版本改变；盘未挂载；旧证据无摘要 | 历史结果保留，当前 gate 重新检查，不转成运行保证 | N3 |
| R1 | 来源完全不可达、从本机/NAS 读取，目标正常 | 不读源 dossier；核对材料、来源、权限后走原恢复日志 | N3 实机 |
| R2 | 源不可达且独占身份未处理；快照 profile 未允许 | 暂存/导出可独立设计；不擅自同时启动，不自动信任命令 | N3 |
| A1 | plan 参数/profile/目标变化、等待到期、请求自称 human | 旧确认无效；不执行；不能从 JSON 伪造人类来源 | N2/N3 |
| A2 | 窗口有效/到期、第二次确认、管理连接隔离 | 不重复无意义解锁；到期安全暂停；交换仍按专用机制 | N3/N4 实机 |
| J1 | 请求超时后同 key 重试、同 key 不同请求、完成记录前崩溃 | 同任务/冲突/待核实；结果未知不自动重放 | N2/N3 故障注入 |
| J2 | 备份成功复制失败；配置已发布启动失败；成功回退 | 保留成功产物/发布；partial/failed 与回退独立表达 | N2/N3 + B03 |
| L1 | 切页、取消订阅、显式取消、最后窗口关闭、强制退出 | 按第 10 节逐项验证，不合并为取消或后台永久运行 | N3/N4 |
| M1 | 没有人类会话、拒绝/过期、执行者崩溃、MCP 请求禁用操作 | 明确等待/拒绝/核实；不传秘密、不自动确认、不绕过禁区 | N5a.2 |
| C1 | custom scope 或未知新语义传给旧读取/恢复端 | 在写入/交换前拒绝；现阶段未实现 custom | N5b |

N1 的设计验收包括用户确认第 14 节决定、契约与现有方法映射、状态/错误案例矩阵及正式规范一致性检查；这些设计交付完成后在 DEV_PLAN.md 标记 N1 Done（仅设计）。本轮未执行上表产品测试，未修改执行代码；N2–N5 实现后仍须运行适用自检及对应实机验收，不用文档审阅替代测试。

## 14. 用户决定记录（2026-11-05）

用户明确确认：“按这三项建议定稿，同步正式规范。”

1. **身份与历史的关系**：采用第 3 节方案 A。每次新实例有独立世代，成功迁移用明确关系接续旅程，创建副本独立；身份/迁移关系保存在金库外。关系记录丢失后仍可从完整副本找回保存点，但世代和迁移关联显示未知；本轮不引入必须随快照携带的永久 AgentID。
2. **原服务器失联、独占身份不能核实隔离时**：首版只提供排除声明的独占凭据后恢复，或导出/暂存并等待处理。不把“连不上”视为已关机，不增加仅凭用户声明已关机/撤销身份就带独占身份完整启动的分支。
3. **管理方跨进程确认**：采用第 8.1 节的人类进程执行方案。MCP 只留允许的请求，用户打开 GUI/TUI 后重建计划并确认，由该人类进程执行；不跨进程共享解锁身份、ssh-agent 凭据或通用确认票据，不由 MCP 进程凭 GUI 许可自行执行。

本次确认批准服务设计及正式规范同步，不表示执行代码、公开协议或新格式已经发布，不批准 B03 尚待设计的链接展开机制，也不新增管理方 MCP 恢复/删除执行权限。

## 15. 与现有代码的逐项映射（只读核对）

| 现有入口/类型 | 保留内容 | N2/N3 所需适配，不计作现状 |
| --- | --- | --- |
| `Service` / `AdminAuth` / `OwnerAuth` | 共享配置与认证路由 | 增加类型化 caller 与任务会话；将进程级 RiskAccepted 收敛为单次具体计划选择，兼容旧显式入口但不能向其他任务泄漏 |
| `PlanRevive` / `RevivePlan` | 原预检、blockers、来源/profile 约束 | 新 PrepareRestore 从 RecoverySource 准备 DTO；旧计划包含内存钥匙/Vault/transport，不能 JSON 往返后执行 |
| `ExecuteRevive` / `ReviveResume` | 暂存校验、二次确认、不可取消交换、continue/back | 将同步确认回调适配为 waiting；将 SwapResult 原状态和日志投影为脱敏分项，不直接序列化服务日志 |
| `PlanAction` / `RunAction` / `ActionStatus` / `ResumeAction` | B03 digest、Phase、发布/服务恢复事实及专用日志 | 将每项结果映射 TaskView；保留 continue/restore-services 的真实分支，不与 revive 的 continue/back 混用；验收前不开放入口 |
| `AdvancedGate` / `RequireGate` / `WithRiskAccepted` | 既有适用对象、过旧提醒和本次自担风险 | Gate 的试解/解锁移入明确步骤；旧缓存不等于新证据，不将 Service 字段当作通用批准 |
| `RepoSync` / `RepoStatuses` / `RepoStatus.Has` | 只追加复制、无钥校验、原仓库路径和 LastOK/LastError | 触发下沉 service；meta 存在不当作完整；增加材料基准、证据和 run 查询，不能用 LastOK 代替任务 |
| `TimerSet` / `TimerStatus` | 旧间隔至少 1h、OnActiveSec/OnUnitActiveSec、legacy soul 语义 | 新计划新增日历/时区/范围与版本事务；旧 enabled 当前只看 unit 文件，新状态需查询实际 systemd，不宣称文件存在即运行 |
| `PeriodSet` / `PeriodSetting` | unit/tz 和下个叶子生效规则 | 纳入新计划版本与兼容投影；周期时区和日程时区独立，旧无版本写入接管后拒绝 |
| `WithProgress(Stage, Done, Total)` | 非阻塞回调 | 包装成可丢弃事件；请求 id 不是任务 id，当前无持久重放能力 |
| SSH `Call` 的 ctx 取消与会话关闭 | 本地停止等待/关闭连接的事实 | 不直接映射 cancelled；重新查 run / journal；交换阶段继续使用既有独立 context |
| `hello` 的 PendingOps / PendingActions、`action-transactions/1` | 协议 1.3 及 B03 逐能力检查 | 缺少字段表示能力未知/不支持，不表示没有操作；新的 run/plan 能力先设计再定协议版本 |

关键定位：

- [service.go](../client/ui/service/service.go)、[access.go](../client/ui/service/access.go)、[gate.go](../client/ui/service/gate.go)。
- [revive_plan.go](../client/ui/service/revive_plan.go)、[revive_run.go](../client/ui/service/revive_run.go)、[core/revive/types.go](../core/revive/types.go)。
- [action_transactions.go](../client/ui/service/action_transactions.go)、[core/action/transaction.go](../core/action/transaction.go)。
- [repo.go](../client/ui/service/repo.go)、[cloud.go](../client/ui/service/cloud.go)、[porter timer.go](../porter/internal/timer/timer.go)、[core/vaultrpc/types.go](../core/vaultrpc/types.go)。
- [progress.go](../client/ui/service/progress.go)、[rpcclient.go](../client/ssh/rpcclient.go)、[core/rpc/types.go](../core/rpc/types.go)。

以上为设计时工作树现状核对，不是对现有问题的代码修复或验收通过声明。
