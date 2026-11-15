# 共享服务契约

> 状态：**已于 2026-11-05 经用户确认定稿，尚未实现**。用户明确决定“按这三项建议定稿，同步正式规范”；决定记录见第 14 节。本文是 N2–N5 后续实现的服务契约依据，正式规范已同步。设计定稿不等于代码、协议发布或实机验收完成；本文不提升 porter 协议、profile 或备份库格式版本，不引入 mygo。
>
> 原 `docs/N1_SERVICE_CONTRACTS_2026-11-05.md`（2026-11-07 改名）。N1 于 2026-11-05 定稿第 1–15 节；2026-11-07 用户在 N2 实施中追加的决定已并入正文相应章节（标“2026-11-07 增补”），第 16 节保留决定与位置对照。实现映射与验收记录见 [acceptance/N2.md](acceptance/N2.md)。
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

在服务器配置目录保存独立私有身份记录：`identity/server.json` 和 `identity/agents/<instance_id>.json`，设计格式为 `valise-identity/1`（实现时引入，不属于备份库或 profile 格式）。后者包括 AgentRef、接受的 roots / profile 摘要、状态、创建时间和明确的接续记录引用；不含文件内容、环境值或凭据。目录 0700、文件 0600，写入采用锁、同目录临时文件、fsync 与 rename；只由主人权限的接入/重新接入/恢复收尾写入。日常读取不初始化这些文件。

身份独立于现有 dossier 文件，避免旧客户端重新保存 `valise-dossier/1` 时丢掉新字段。身份与 dossier 更新以固定的接入提交记录协调；未完成提交不发布新 generation，后续查询报告需要核实，不猜测成功。读取旧实例返回 `identity_state: legacy_unbound` 和本客户端的定位引用，不能伪造远端稳定身份。下一次用户明确接受接入时才建立身份。

`server_uid` 不能替代 SSH 主机密钥验证。同一远端账户经不同别名连接，核对主机信任与 server_uid 后才可去重；同 uid 出现在不相符的主机/用户上标为身份冲突，要求核实，不能自动合并克隆的系统盘。主机密钥变更沿用现有停止规则。

### 3.2 接入、恢复、迁移与创建副本

- 对已登记且未被取代的同一实例重新检测、升级 profile 或修改显示名，保留 generation；接受 roots / 来源变化仍须重新预检，所有旧执行计划失效。
- 明确作为新实例接入、清理记录后重新使用 id、重新接入已被取代的 id：创建新 generation。选择继续旧实例的特殊情况须展示已有身份与路径，不凭同名推断。
- 恢复当前实例保留目标 generation；记录“由 SnapshotRef 恢复”，不把时间点之前的世代重新写成当前世代。
- 成功迁移：目标拥有新的 AgentRef，以 `migration_completed` 边接续原旅程。创建副本：新 AgentRef 与独立旅程，仅有 `clone_from` 来源关系。失败、回退、仅暂存通过、仅文件恢复都不生成成功迁移边。
- 迁移关系字段：`relation_id`, `kind`, `source_agent?`, `source_snapshot`, `target_agent`, `operation_ref`, `verified_at`, `source_disposition`。source_disposition = stopped_verified / exclusive_omitted / unresolved；unresolved 不能宣称完整迁移完成。
- 成功目标端保存关系的权威记录；客户端缓存，原服务器可达时补记，补记失败不撤销已成功恢复。关系不进入备份库，源失联且没有任何可靠 AgentRef 证据时，只关联 source_snapshot，保留“来源实例身份未知”。

### 3.3 与快照的关联

不为新世代改写旧快照。新备份在服务器外部运行记录中保存 AgentRef → SnapshotRef 的明确绑定；旧快照无绑定时展示在该备份库历史中。仅靠同 instance_id / 同周期不能把旧世代全部挂到当前 Agent。丢失运行/迁移记录后仍可从完整备份库重建保存点和导出，只会缺少世代、原因、教学标记和迁移关联。

## 4. 保护计划：权威、版本与兼容


### 4.1 两部分状态

`ProtectionView` 包含 `server_plan`, `client_binding?`, `effective_summary`, `legacy_settings?`, `warnings`。两部分分别保存、分别版本化，不构造跨机器原子事务。

| 部分 | 字段 | 权威与限制 |
| --- | --- | --- |
| `ServerProtectionPlan` | `schema_version`, `agent_ref`, `revision`, `scope`, `schedule`, `period`, `server_copies`, `accepted_at` | 服务器独立执行需要的全部设置；新建 scope = preset/full；定时备份须明确启用 |
| `scope` | `kind: preset`, `tier` | 只接受现有 tier；N2 新建默认 full，显式旧范围保留；custom 返回 unsupported_scope |
| `schedule` | `enabled`, `daily_at`, `timezone`, `catch_up` | 首版新向导用每天 HH:mm + IANA 时区；旧非每日设置保留 legacy 表达，转换须显示差异。返回最近/下次触发及 linger 等实际条件；错过记录与可选补跑见 4.4 |
| `period` | `unit`, `timezone` | 复用现有 week/month/quarter 与周期边界规则；与 schedule 时区分开，修改只影响下一个叶子。旧 `.vault.json` 是兼容投影，不再作为第二个可独立覆盖的权威 |
| `server_copies[]` | `target_ref`, `enabled`, `minimum_interval` | 引用现有目标，不含密码；直传/证书/中转条件分别报告，配置开启不等于实际可更新 |
| `ClientProtectionBinding` | `client_id`, `agent_ref`, `revision`, `server_revision_seen`, `local_copies[]` | 本客户端私有；每项引用 repo_id 和是否随成功备份/客户端重新打开时补齐；路径仍取已有 repos 配置。执行规则与设置入口见 ARCHITECTURE.md 7.4（2026-11-08） |

持久化到服务器 `protection/<instance_id>.json`（`valise-protection/1`）和客户端 `protection-bindings.json`（`valise-protection-bindings/1`）；均在现有 Valise 配置目录内，0600，原子替换，不放备份库或活动 roots。

日程的解释由目标 systemd 校验；计划返回下一次触发时间及夏令时预览。无相应时区/日程能力就拒绝启用，不由客户端偷偷转成固定 UTC。首版不承诺 exactly-once 每天触发；漏执行按实际记录显示（4.4）。

### 4.2 读取与更改

- `GetProtection(AgentRef)`：只读；无新计划时返回 `mode: legacy`，明确现有 tier / timer / 复制规则，不能补写 full。
- 旧世代的计划（2026-11-10 用户决定）：实例重新接入成了新 generation 后，服务器上留下的旧 generation 计划已失效（3.2），它的 timer 拒绝运行。`GetProtection` 对当前 generation 返回 `mode: superseded` 与只读的 `superseded_plan`（旧计划原文），不再报 identity_conflict，也不统计错过与复制状态。重新设置由用户明确发起：以旧计划为初值，`expected_server_revision` 取旧计划的版本，经同一事务写入绑定当前 generation 的下一版并替换 timer；版本号继续递增，不归零。旧计划不自动迁移，引用其他 server_uid 的复制目标由用户在新设置里取舍；备份库不受影响。
- `PrepareProtectionChange(agent, expected_server_revision, expected_client_revision?, desired)`：计算服务/停机影响、读取成本、内容增减、复制条件及旧设置差异，返回 FrozenPlan；不修改 timer 或配置。
- `StartProtectionChange(plan_id, submission_key)`：登记任务，确认/授权缺失时返回 waiting，齐备后经第 8 节核实才执行。首次新建用 expected revision = 0；所有更新均比较 generation 与精确版本，冲突返回 revision_conflict，不做 last-writer-wins。
- 客户端部分更新成功但服务器部分失败，或相反：返回分项结果及需要重读的版本；不伪造整体回滚。组合执行先提交服务器，成功后提交本客户端绑定；本地提交失败保留服务器结果，补写须重新比较本地版本。

服务器提交采用**固定保护计划事务**：写入 intent（旧/新版本及受影响 timer、周期兼容投影配置）→ 阻止新的按计划任务启动 → 安装/检查新配置 → 原子发布新计划 → 核实 systemd 生效及周期投影一致 → 记录提交完成。提交前失败恢复旧 timer；提交后核实失败保留 committed 与 reconcile_required，不倒退版本或谎报仍为旧计划。进程崩溃时保持 pending 阻断，用户显式核实/恢复；不自动重复结果未知的 systemctl 步骤。已开始备份继续使用冻结的旧版本，不在中途切换。

### 4.3 新旧客户端与旧入口

- 未采用新计划的实例维持原逻辑。采用时展示 scope、timer 和复制差异，由主人确认；不能读取一次就接管。
- 新 timer 调用固定“读取已接受计划”的 porter 入口，不把 full 或整个计划内容固定写进 unit argv；计划 pending / 损坏 / generation 不符时拒绝运行，不回落 soul。
- 已接管实例的旧 timer_set、周期/同步自动设置等**写入口**必须被新版 porter 的统一边界检查拦下，要求支持版本比较的新入口。被计划引用的共享目标只能经 4.5 的版本化修改入口改动，列出受影响 Agent 并统一核对引用版本；删除被引用的目标拒绝，名称即标识、不支持改名；不能只拦 GUI。
- 旧显式 seal tier 与受管方 MCP soul / 频率限制保留；其返回标明是显式/旧入口，不暗中读取新默认。新日常备份读取同一服务器计划。
- 旧客户端仍能作其原有读取与显式备份；未知字段不能变成“没有计划”。新客户端连旧 porter 可读旧事实，但拒绝提交新计划，不静默模拟。
- 新版 porter 必须识别新配置标记并关闭无版本写入旁路。人为降级成不认识该标记的旧 porter 无法保证这些限制，N2 不支持此降级，更新/回退流程须先检查并拒绝；不能宣称新客户端可以约束任意旧程序或用户自行 SSH 改文件。
- 新计划的 per-Agent 复制规则只用于该实例；其他 legacy 实例保留旧 targets.auto 行为。同一次备份只选择一种策略，不重复触发新旧自动复制。

### 4.4 错过的定时备份与补跑（2026-11-07 增补）

- porter 读取保护计划时，按计划日程、接受时间与 timer 运行记录，计算最近 30 天内应触发而没有运行的时刻，记为“错过”；其后已有定时备份成功的，记为“已过期”。结果随 `protection_get` 返回，时间按服务器时钟，时钟异常时一并提示。
- 客户端在打开时提示新的错过记录（GUI 弹窗在 N4）；“已读”标记只存客户端。
- 计划新增 `schedule.catch_up`（默认关闭，用户在设置里打开）：开启后 timer 使用 `Persistent=true`，服务器开机或恢复运行后，若错过了上一次就补跑一次。新字段要求能力 `protection-plan/2`；客户端不向旧 porter 发送，旧 porter 读到也会拒绝，不会当作没有开关。
- **补跑留标记**：错过的时刻随后由补跑运行（开始时间晚于计划时刻 15 分钟以上、早于下一次）时，不再从记录中消失，而是记为 `caught_up` 并附实际运行时间；界面用很浅的小字显示“服务器从停机状态恢复”。

### 4.5 被计划引用的复制目标的版本化修改（2026-11-07 增补）

- `target_change_prepare`（daily）列出所有引用该目标的计划及其版本，冻结新目标定义；`target_change_apply`（owner）在保护计划锁内逐一核对这些版本，全部一致才原子写入 `targets.json`，并把每个引用计划的版本加一，让其他客户端看到变化；任何一个不一致则整次不写。
- 删除被引用的目标仍拒绝，须先修改计划不再复制；名称即标识，不支持改名。旧 `target_set` 对被引用目标继续拒绝。能力 `protection-plan/2`。

## 5. 时间轴与副本证据


### 5.1 读取方法

| 方法 | 请求 | 结果 |
| --- | --- | --- |
| `ListAgents` | 服务器筛选、是否含旧实例、cursor/limit、refresh_policy | AgentSummary：AgentRef/legacy 定位、安装归属、显示名、各自 Observation 的运行/保护/健康摘要、可用能力；部分失败独立列出 |
| `GetAgent` | AgentRef 或 legacy 定位 | 详情、来源、已接受布局版本、共享服务影响、入口元信息；敏感 URL 和环境内容不返回 |
| `ListTimeline` | subject（AgentRef 或一个已打开备份库集合）、from/to、tier、cursor/limit | TimelinePoint、缺口与查询完整性；不依赖连接原服务器 |
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
| period_material | 与参照备份库的快照/索引/pack/epoch/key 集合一致并已核对 | 明文能解、运行环境可用 |
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
- 证据的具体核对方法、pack 复用规则与核对时机见 ARCHITECTURE.md 7.1.1（2026-11-07 用户决定）。
- 接口展示的本机证据不被 porter 视为用户自报的删除许可。INSTALL.md 4.2 自担风险继续保留，但确认仅绑定本次具体操作；不能越过来源哈希错误、身份冲突或不允许的命令等硬阻断。

### 5.4 复制位置的检查与失败原因（2026-11-07 增补）

起因：服务器自己配置的复制位置（`targets.json` 中由用户写好 SSH 主机的目标）保存时客户端与服务器都不测试；服务器在 NAT 后连不上 NAS 时，自动复制只记 `copy_failed`，原因要人工登录服务器才能查到。

1. **失败原因留在记录里**：复制失败时，运行记录的组件除 `error_code` 外新增 `failure_cause`，取值固定：`host_unknown`（不认识主机名）、`connect_refused`（拒绝连接）、`connect_timeout`（连接超时、无路由或网络不可达）、`auth_denied`（拒绝登录）、`host_key_unverified`（主机身份未确认或已变化）、`not_writable`（目标目录不可写）、`tool_missing`（两端都没有可用的 rsync/sftp）、`other`。只由 SSH/rsync/sftp 的标准错误文本分类，不保存原始输出。
2. **计划视图给出每个复制位置的最近结果**：`protection_get`（protection-plan/2）新增 `copy_status`：计划引用的每个位置最近一次尝试的时间、结果与 `failure_cause`；只读、由运行记录计算。健康页在最近一次失败时给黄色项，按原因说明并给办法；连接超时或拒绝连接时说明“服务器可能在 NAT 后或另一个网络”，办法为经这台电脑中转（只在 Valise 打开时）、用 Tailscale 等组网工具，或换成服务器能访问的地址。
3. **保存前从服务器测试**：新增只读方法 `target_check`（daily，能力 `copy-check/1`）：由服务器按给定位置（已保存的名字或待保存的设置）做一次只读列目录，超时 20 秒，返回是否可达、所用方式与 `failure_cause`。不写文件，因此“目录不可写”只能在实际复制时发现。添加或修改服务器自己配置的复制位置时，客户端先测试；失败时说明原因，用户可以坚持保存，但带着警告。Valise 管理的位置沿用现有探测与中转。
4. **文案**：新写的界面文字使用 PRODUCT.md 第 8 节左列说法（备份位置、复制到其他位置，具体写 NAS、本机文件夹等实际位置），不再写“空间站”。既有界面文字已于 2026-11-07 统一替换，服务器上已有记录中的旧说法由客户端显示时映射（`service.PlainWording`）。

## 6. 恢复来源与冻结计划


### 6.1 读取来源

`OpenRecoverySource(location_ref, vault_locator)` 只建立可查询的来源 handle，不能直接将任意路径交给旧 PlanRevive。服务器备份库、用户明确添加/选择的本机目录、已配置远端位置分别用适配器；定位参数按各自安全规则验证。handle 不包含钥匙，不能授权写目标。

内部 `RecoverySource` 只提供 `InspectVault`、`ListSnapshots`、`ReadEncryptedObject`（允许的 VAULT/PERIOD/epoch/index/snapshot/pack 对象键）与 `Close`，不提供任意远端文件读取。大数据按流读取并验证；多位置补齐前核对 VaultID、SnapshotID、头摘要和 pack 内容哈希，冲突就停止。恢复缓存保留现有 partial 标记的原子写入和中断规则。

`PrepareRestore` 请求：`source_handle`, `snapshot_ref`, `target_agent_or_server`, `mode`, `include_secrets`, `root_mapping`, `profile_allowances`。profile_allowances 只对应既有显式允许（快照 profile、restore 能力未测试），不构造统一跳过检查开关。目标绑定主机信任身份，不能只绑定 SSH 别名。

从副本解密获取 profile_doc / provenance / roots / local_overrides；不先读来源服务器 dossier。选择本地已审核同 id/版本 profile 仍需核对内容及显示选择依据，不因 profile id 一样就合并不同命令。目标预检继续复用路径、空间、来源精确版本、共享服务、外部进程和 journal 规则。读原服务器仅为需要停源等明确动作，不是读快照的前提。

### 6.2 两次确认与源失联

恢复仍为：准备计划 → 第一次确认 → 准备材料及暂存校验 → 第二次确认 → 专用 journal 交换 → 复核。第二次确认绑定 op_id、暂存校验摘要、目标状态与同一冻结计划；不能以第一次确认自动交换。

源可达的迁移按既有规则停止原实例并核实，成功后才在目标启动。源不可达时只知道“无法核实”，不能当成 stopped。首版在独占身份无法核实隔离时仅提供“排除 profile 声明的独占凭据后恢复”或“仅导出/暂存、等待处理”；如果独占凭据内联于共享配置，说明被排除的整个文件及影响，不能扫描内容猜 token 或私自改写配置。首版不接受仅凭用户声明已关机/已撤销身份就带独占身份完整启动，也不提供风险确认绕过此条件。导出或暂存仍遵守凭据选择、权限与不启动的边界。

2026-11-11 问卷确认（待实现）：当前状态创建副本须先产生备份并绑定真实 SnapshotRef；旅程入口绑定所选已有时间点。副本允许原服务器，准备与启用是两个阶段；启用必须由当前人类进程在重新核实登录、共享服务和隔离条件后确认，不能复用旧确认、后台自动启动或把已准备记为已运行。普通恢复默认只保留旧目录，可选先备份当前状态；选择了先备份时，备份失败就不推进恢复，不修改保护计划。迁移成功后原实例自动转为已迁出，未成功不得提前隐藏或归档；备份库与旧目录不删除。

恢复测试首段是文件/暂存证据，不生成运行验证或迁移关系。测试索引保存原操作、实例、时间点和收尾结果，不保存文件、秘密或执行许可；运行中记录在重启后视为待核实，不重放。收尾仅允许核实原测试并丢弃仍为 planned 的暂存，已进入交换的日志拒绝当作测试清理。普通恢复的额外备份由当前人类进程选择，来源不明确认不能被该选择替代；已在来源确认中备份过时不再重复。

### 6.3 来源不明的旧快照用于恢复（2026-11-07 增补）

恢复计划在源身份未知时列出快照自带信息（记录的实例名、profile 与版本、来源服务器与周期、时间、范围），并明确说明来源无法核实。执行前用户必须二选一：“我清楚，继续”（受信的人类确认，不接受 JSON 自称）或“先备份当前状态再恢复”（对目标现有实例先做一次 full 备份）。交换前的旧状态仍按既有规则保留。

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
| `BackupRequest` | AgentRef、mode = accepted_plan / explicit_preset、expected_server_revision（按计划模式必填）、可选 explicit_tier（显式模式必填）、受限 purpose。reason 为可选用户文字，限长、不得写入备份库元数据 |
| `CopyRequest` | subject = SnapshotRef / 指定 vault 基准、source_location、destination_refs、expected_destination_revisions；全周期复制仍使用现有布局，不因只选时间点就把 partial 标完整 |
| `ActionRequest` | AgentRef、action_id、dossier/profile 摘要、操作前备份选择、允许的风险选择；实际命令只取远端已接受 profile，不接受 argv 输入 |
| `RestoreRequest` | 第 6 节请求字段；target_agent 时需 generation，迁移/副本的新目标需明确新 id/roots；不通过旧 host 字符串暗中指定恢复来源 |
| `ProtectionChangeRequest` | 第 4 节 desired 全量替换及两个 expected revision；缺失字段不能被默认解释为关闭原设置 |

内部人类交互接口为 `ReviewTask(task_ref) → ReviewView`、`RecordDecision(task_ref, expected_task_revision, plan_digest, phase_digest, choices)`、`SatisfyAccess(task_ref, requirement_id, interaction_adapter)`。这些接口只注册给受信的人类适配器，不能作为通用 JSON/RPC/MCP 方法导出；interaction_adapter 是进程内函数/句柄，不是可序列化口令参数。拒绝记录为取消请求；解锁后条件变化需要新的 review，不自动沿用旧确认。

新方法返回的 FrozenPlan/TaskView 都是纯 DTO；解密身份、打开的 Vault、transport、stage writer 仍由执行器进程内持有。进程退出后重新打开并核实，不从 JSON 恢复这些资源或确认回调。

### 7.5 健康项的内置一键修复（2026-11-07 增补）

- **linger**：计划开启了定时、linger 未开启时，健康页提示“退出登录或服务器重启后，定时备份不会运行”。一键修复执行 `loginctl enable-linger <用户>`；系统要求管理员权限时，经管理登录与高级模式用 sudo 执行；都不可用时显示命令，由用户自己执行。
- **时钟**：服务器与本机时间差超过 5 分钟，或 NTP 未同步时提示。一键修复执行 `timedatectl set-ntp true`；已开启 NTP 仍有偏差的，执行 `systemctl restart systemd-timesyncd` 后复核；均需管理员权限，规则同上。不从客户端时间手动改写服务器时钟。复核仍有偏差就如实说明，给出下一步。
- 两项都是 Valise 内置的固定系统动作，不是 harness 知识，不进 profile。确认页按 UI.md 健康一节列出实际命令与将改变的系统设置，用户再次同意后才执行；执行后复核并写审计。
- **接管模式**：设置里新增“接管模式”（默认关闭，按服务器保存在客户端）。开启后，TUI 在自己界面里执行需要 sudo 的修复：仍须用户在界面里通过两个口令开启高级模式并确认免责，确认页明确标注“这是 sudo 命令”；只在服务器允许免密 sudo（`sudo -n`）时于界面内执行，需要输入 sudo 密码的仍转到终端。关闭时沿用转到终端执行。
- **区分破坏性 sudo 命令**：每个内置修复步骤标明是否“破坏性”（会删除或覆盖数据、不可简单撤销）。只有含破坏性步骤的修复，执行前才再检查“别处有可恢复的备份”（INSTALL.md 4.2）；非破坏性步骤（开启 linger、开启时间同步、重启时间同步服务）不查。

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
| 远端备份/复制运行记录 | `~/.local/state/valise/runs/<run_id>.json` | 固定 backup/copy 执行器在副作用前记录 accepted，完成后记产物；分离执行时由一次性单元认领（9.4）；timer 与 MCP 也可映射为外部任务历史 |
| 保护计划提交 | 服务器 state `protection-changes/<change_id>.json` | 只协调固定设置/timer 发布；不能执行自定义步骤 |
| 恢复/B03 | 现有 journal / action-journal | 原状态机、锁、intent、恢复许可与损坏拒绝继续权威 |
| 迁移关系 | 目标端 identity 目录下按 relation_id 记录，客户端缓存 | 丢失关系只降级关联，不阻断读备份库 |

任务记录不存口令、环境、原生输出、快照文件内容或解密后的文件名清单。配置/目标路径等本来可见的元信息仍按 0600 保护；向 MCP 投影再删掉非必要的敏感字段。

任务索引损坏不能自动运行或抹掉远端日志；报告 record_corrupt 并通过已知服务器的 hello / 专用查询发现待处理记录。清理执行残留和删除备份继续需要显式命令，不因任务终态自动清理用户数据。

### 9.2 去重与断线

submission_key 标识一次用户意图。服务在发起副作用前持久记录 `key + kind + normalized_request_digest + task_ref`；同键同请求返回同任务，同键不同请求返回 request_conflict。新键表示新的意图，不因同日/相同 scope 自动合并备份。

远端请求必须另有服务器可核对的 run_id 或现有 op_id。收到重试先查既有记录，不能仅依赖本机 task 索引去重；备份完成即将 SnapshotRef 和运行记录关联。旧 porter 无运行记录能力时，不对网络超时后的重试承诺去重；返回 reconciling，先核对快照/审计，无法唯一定位则要求用户明确发起新任务，说明可能多出一个时间点。

不得承诺网络环境下 exactly-once。跨“副作用已完成、完成记录尚未写入”的崩溃，只有对应固定执行器能以 op_id/快照发布记录证明结果；证据不足就 needs_recovery/reconciling，绝不靠重发猜测。后续复制失败的重试使用新的复制计划和既有 SnapshotRef，不重新备份。

### 9.3 互斥

沿用服务器 vault.lock、恢复和 B03 的现场互斥；新增任务索引锁不能替代它们。服务层按服务器、安装共享服务、Agent roots 和位置投影冲突，最终由 porter 在执行边界复检。多个资源按固定顺序取锁；冲突返回 resource_busy 与可公开的操作引用，不无限排队持有其他锁，也不在等待用户确认期间占用普通执行互斥。已持有专用日志保护的暂存操作沿用其 reservation，不允许相交操作闯入。

### 9.4 服务器任务的分离执行（2026-11-07 增补）

- **范围**：按计划备份及计划内服务器复制。RPC 先按现有规则持久接受运行记录，再用 `systemd-run --user --collect --unit=valise-task-<run_id>` 启动 `valise-porter task-run --id <run_id>`；该单元只读运行记录里冻结的请求，不接受其他参数，跑完即结束，不是常驻服务。客户端关闭或断网不影响执行。
- **退化**：用户 systemd 不可用或启动单元失败时，在当前连接内执行，记录 `execution: attached` 并告诉用户“关闭 Valise 会中断这次备份”。
- **进度与查询**：执行器把阶段写进运行记录；客户端按 run_id 查询或订阅。保护计划提交只需数秒，仍在连接内执行，客户端以后台任务等待。
- **分项时间**：运行组件可带 `started_at`（可选，兼容旧记录）与 `completed_at`；实际开始执行时记开始时间，复制成功或失败时都记结束时间。未开始、跳过或结果未知的组件不伪造执行时间。客户端不从相邻组件的完成时间推算旧记录的开始时间。这些是备份库外运行记录的元信息，不改变备份库格式或权限。
- **取消**：`backup_run_cancel`（daily）在记录里写入取消请求；执行器只在安全节点检查（封存提交前、每个复制目标之间），确认停下才记 cancelled，封存一旦提交就保留该快照。
- **本机任务**：拉回本机仓库、恢复上传暂存等必须经过本机的任务，在客户端进程内后台执行，任务索引落盘；关闭与强退按 N1 第 10 节。
- **服务接口**：Start 立即返回任务，另提供 List、Watch、Cancel；能力 `backup-runs/2`，旧 porter 退回连接内同步执行。
- **分离模式**：分离执行默认不启用，由设置里的“分离模式”开关决定（默认关闭，客户端保存）；开启且服务器声明 `backup-runs/2` 时才请求分离。关闭时，按计划备份在连接内执行，任务结果附提示“任务也可以在后台跑，查看「开启分离模式」”。

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
| 分离执行的服务器任务（9.4） | 取消只写请求；执行器在安全节点（封存提交前、每个复制目标之间）检查，确认停下才记 cancelled；封存已提交则保留快照，未开始的复制记 skipped |

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
| I3 | 成功迁移、失败回退、创建副本、身份关系丢失 | 仅成功迁移接续；副本独立；关系缺失不阻断备份库读取 | N2/N3 |
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

1. **身份与历史的关系**：采用第 3 节方案 A。每次新实例有独立世代，成功迁移用明确关系接续旅程，创建副本独立；身份/迁移关系保存在备份库外。关系记录丢失后仍可从完整副本找回保存点，但世代和迁移关联显示未知；本轮不引入必须随快照携带的永久 AgentID。
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

## 16. 增补决定记录（2026-11-07 用户确认，N2 实施中追加）

用户在 N2 实测后追加的决定已并入正文（2026-11-07 文档整理）。后端部分在 N2 实现，弹窗、按钮等界面在 N4 落地，TUI 沿用已有健康页修复模式（`f 修复 · 动作名`）。

| 决定 | 正文位置 |
| --- | --- |
| A 服务器任务交给一次性 systemd 单元执行（方案 C）；F4 分离模式开关 | 9.4、第 10 节 |
| B 错过的定时备份与补跑；F1 补跑留标记 | 4.1、4.4 |
| C 被计划引用的复制目标的版本化修改 | 4.3、4.5 |
| D 来源不明的旧快照用于恢复 | 6.3 |
| E linger 与服务器时钟的健康提示及一键修复；F2 接管模式；F3 区分破坏性 sudo 命令 | 7.5 |
| G 复制位置的服务器端检查 | 5.4 |

## 17. N4 反馈补充（2026-11-11 用户决定）

- 全局旅程读取通过 `JourneyListRequest.all_locations` 显式启用，GUI 的 Agent 页面改用下面的范围化增量读取。分页冻结合并后的查询视图；只读取描述、元数据和加密头，材料/解密/运行验证仍为独立事实。只读 porter 方法 `journey_list`、能力 `journey/1`，仅可选已配置目标名，不接受任意路径；未知能力标为未能读取，不能当作空库。
- 2026-11-11 范围澄清：上述全局查询保留给显式调用方，GUI 的 Agent 旅程使用显式连接范围。范围来自当前连接及已有明确 migration_completed 关系，不按同名或 clone_from 扩张；无关服务器不连接、不读其能力、不进入待处理数或错误列表。按位置重试不得扩大该范围，缓存结果与错误也须按范围筛选。首页启动只读本机索引，不以全局扫描作为打开单个 Agent 的前置。
- 一次性备份沿用已接受计划的身份、版本和复制位置，`BackupParams.selection` 为可选、可比较的固定选择（`tier`、`skip_copies`）；缺省保持原语义。仅本次执行使用，绝不写回保护计划。能力 `backup-runs/3`，准备与执行前都检查；旧 porter 不发送新选择。任务及 run 保存该选择，确认摘要和请求去重均绑定它；run 的 Plan 仍是原权威计划。范围为既有档位，不能借此开放 custom。

## 18. N4 恢复的人类适配（2026-11-11）

`PrepareRestore` / `RunRestore` 为进程内原生人类适配，复用 PlanRevive / ExecuteReviveConfirmed；尚不是对外通用 JSON 任务执行 API，不向 MCP 开放恢复执行。计划持有的解密资源不落盘。`restore-tasks/<op_id>.json` 仅记录来源/目标引用、连接信任摘要、阶段与脱敏结果；重启后原 running/waiting 视图为待核实，不恢复确认。重复使用同一 op_id 被拒绝并转查询，不建立第二次恢复。

`revive_review` / `revive_resume_checked` 属主人操作，需 `restore-review/1`；都在服务器互斥锁内进行。query 不执行恢复；checked resume 另验现场摘要与固定分支，摘要不构成授权。涉及按来源安装程序或服务时，porter 仍要求管理连接，客户端为本次操作建立独立连接。已开始交换则使用不可取消的执行 context；正常关闭等待，强退后读取原日志。文件恢复、服务启动步骤、本次健康检查、关系提交分别有证据才显示通过；退回不算原任务成功，核实停止且未交换才记 cancelled。

GUI 首段恢复来源限定服务器、本机仓库与本机登录云盘；其他位置只有元数据发现能力时不提供执行入口。备份位置可按已知时间点进入；不能把只读记录当作材料校验。恢复记录入口在设置的备份栏；只读列表不自动查询或重放远端操作。


## 19. 并行模式与可用性验收（2026-11-12 产品定案，技术实现待完成）

本节记录三轮用户问卷确认的目标语义；现行服务与 porter 尚无完整环境隔离/交替槽位契约，不能据此发送未协商的新执行参数。技术字段、RPC 与能力版本另行定稿；旧端不得静默把并行请求降级成直通。

### 19.1 模式、身份与目标

- 默认开启并行并保留旧环境；后续 Wizard 默认勾选并建议开启。设置提供并行（保留旧环境）、小占用并行、直通三种模式。模式不属于备份档位，“保留旧环境的并行”不等于 full 备份。
- 一个 Agent 只有一个激活环境，A/B 是它的运行环境位置，不是两个 AgentRef，不创建新世代、clone_from 或普通旅程分支。环境修订、来源与操作关系记录在备份库外。
- 当前 A 激活则准备 B，当前 B 激活则准备 A。目标必须明确为非活动且可复用；正常恢复计划显示将复用的旧环境，不要求先在设置中手动清空。
- 准备候选时当前环境继续使用；禁止为了腾空间提前释放当前激活环境。空间不足不自动降级、清理当前环境或追加云端保护流程。
- 关闭并行及可选的非活动槽位清理是设置动作；不得与占用该环境的恢复/测试互相破坏。执行中的恢复沿用已冻结的模式与资源范围。

### 19.2 测试与正式切换

- 测试先选择完整 SnapshotRef、来源与摘要，提供文件校验和隔离重建。隔离重建时原 Agent 继续运行，受测环境隔离对外连接；只验证到网关可打开，不建立正式恢复成功或外部账号已验证的证据。
- 候选就绪后才停止并封存当前环境，再激活候选、检查正式网关。启动/网关失败时先停止并核实候选，再回到原环境、检查并封存失败候选；不能核实停止时不并行启用两套独占身份。
- 网关就绪与用户确认能对话是独立事实。正式激活后进入待用户验收，不以启动退出码、HTTP 响应、使用时长或页面打开自动记为可用。
- 用户在原生界面实际对话成功后点“确认可用”。此确认由当前人类进程执行，绑定原操作、AgentRef 和当前新环境的修订；Valise 不读取聊天内容、不代发测试消息、不调用模型判断，也不把声明放入可由 MCP/JSON 请求冒充的通用确认字段。
- 待验收不伪装成持续执行，不占住人类会话等待；退出、重启、断线或超时均不代确认、不清理原环境，重开后查询原操作，不重放切换。
- 用户报告不能对话时可从原操作发起切回。切回后的活动环境、失败候选和运行证据分别记录；候选切换后新增的内容留在封存候选，不擅自合并进原 Agent 的记忆。

### 19.3 清理与再次恢复

- 小占用并行接受与并行模式相同的准备峰值。仅在用户已确认新环境可对话后，才开放清理旧环境的独立入口；“确认可用”不等于删除授权，不自动清理。
- 清理验证旧环境非活动、环境修订和操作归属未变、且未被下一次恢复/测试占用。不得删除当前环境、其他 Agent、共享程序或备份库。
- 保留旧环境的并行模式继续保留旧槽；下一次恢复按 A/B 交替复用明确非活动的另一槽位，不把历史备用槽的复用混称为提前释放当前环境。
- 清理失败独立显示待清理，不改写已经确认的运行结果，也不重跑恢复。失败候选或测试环境的收尾按其原操作与非活动状态核实，不把它们冒充已验收旧活动环境。

### 19.4 非全量内容的候选构建

- 默认数据底座为同一 Agent、且不晚于目标时间点的最近适用全量。关联须有明确身份/旅程证据，不凭同名或相同 instance_id 自动拼接。
- 没有适用全量时按目标记录的官方来源、版本重建。全量数据不保证包含程序或系统依赖，运行环境仍按记录准备，不自动使用最新程序版本。
- 所选非全量档位的整个范围以目标时间点为准，包括已删除内容；不能仅追加文件而让底座的旧内容残留。范围外的数据来源与时间点、组合清单和所需空间在计划中明确。
- 组合材料不伪装成新的历史全量快照，不改写公开备份或创建普通旅程分支。目录、程序、文件、网关、对话验收、回退与清理分别表达，现有单个 restore tested 状态不自动证明并行/隔离能力已核实。


### 19.5 第一模块技术契约（2026-11-12，先实现底座，不开放执行入口）

- 独立环境登记 `valise-environments/1` 位于服务器 Valise 私有状态目录，按完整 AgentRef 的摘要分隔；不进入备份库/实例 dossier，不用软链表示激活。登记包含 revision、模式、A/B 环境修订与状态、当前激活槽、原操作及候选/原槽引用。运行时路径不接受远端请求传入，后续执行器只使用受管目录及已核实布局。
- 状态变更在副本上校验，失败不改变原对象。开始恢复冻结模式、当前环境和目标的修订；只复用明确非活动目标，拒绝复用仍占用的环境或在未完成原操作时开始新恢复。一个操作只有一次准备、一次切换，不用请求重试重放。
- 可信执行器才提交阶段证据：准备→就绪→停原环境意图→已停原环境→候选启动意图/网关通过→待人工验收→已确认可用。停止/启动意图须先持久，操作中断时运行状态为未知，不以命令退出码替代停止或健康核实。失败后只有候选确已停止才能启动原环境；退回仍保留原任务失败。该内存状态模型不是外部事件执行 API，不接受 JSON 自称网关通过或已停服。
- “确认可用”和清理各自绑定操作及环境修订。小占用的清理只在验收后开放；已复用的旧修订、当前激活环境、运行未知环境均不能被旧请求清理。模式变更不能修改执行中操作的冻结策略。
- 数据底座选择器输入已核实来源证据，只在同一精确源 AgentRef、相容 profile/来源/布局、时间不晚于目标的 full 中选最近者；跨迁移的兼容证据由调用层明确提供，不由选择器按名字推断。缺少时返回按记录来源重建。合成清单保留范围外底座文件，用目标范围的完整清单替换范围内内容，处理删除并拒绝根/路径/来源冲突，不生成新历史快照。
- 隔离底座首个实现使用 Linux 已存在的 bwrap：空白只读私有根、仅必要 OS 程序目录只读映射（不映射整份 /usr、/usr/local 或 /usr/share）、私有 HOME、清空继承环境、私有 PID/IPC/UTS/网络、私有 proc/dev/tmp/run，禁止嵌套用户命名空间。只读挂载仍可能允许访问 Unix socket，因此映射前检查元数据并拒绝特殊节点；私有材料拒绝到宿主文件的硬链接。有限任务必须带截止时间，取消时终止私有 PID 命名空间中的后台子进程。默认不共享宿主 HOME、会话 socket、SSH Agent 或网络。不能仅以 bwrap 存在判为能力可用；需实际探针与边界测试。没有工具或能力时拒绝，不修改宿主安全策略、不自动安装、不降级成裸进程。
- 这个隔离底座只负责运行经控制层准备的有限任务，不把 porter 做成守护进程或监听端口。实际 Gateway、私有运行时构建、稳定服务入口、槽位切换执行和对话验收还需后续模块；在闭环与 profile 核实之前不发布并行执行能力或画可执行按钮。


### 19.6 第二模块：私有候选构建与网关试运行（2026-11-12）

- 构建器只接收控制层已核实的资料、目标/底座清单及程序材料；不是远端任意路径/命令 API。私有 HOME 由构建器新建，完整源身份、profile、分类/布局和程序来源摘要必须匹配。首个实现只接收已核对 SHA-256、系统/架构、逐项清单的标准 tar 程序材料；材料的来源和精确版本由下载/来源适配器证明，不以“文件存在”推定。尚未接入的官方来源不能转而全局安装、借用当前程序或自动升级。材料获取/安装需要新的管理登录与人类计划确认；此内部构建器本身不安装系统依赖、不执行安装器、不开放 RPC。
- 先构建程序，再写数据。程序仅进入 profile 明确声明的私有程序目录；入口必须是这些目录内可执行程序，不使用共享全局命令。只支持核实可映射到私有 HOME 的根；外部根、路径冲突、缺失运行时材料与无适用布局均停止。数据从合成清单标明的源读取，逐项校验大小与 SHA-256；不能从活动环境补齐，不能覆盖程序目录。整个候选是新暂存，失败保留不完整材料归原操作处理，不进入就绪。
- 程序树与数据树在宿主上独立存放；隔离运行时把明确程序目录只读覆盖到新数据 HOME 的对应位置。数据目录交换不得连带搬走程序，也不得从活动 HOME 挂载程序。命名空间内 HOME 使用摘要绑定的来源 HOME，保留虚拟环境等依赖的绝对前缀；仅复用路径名字，不共享宿主该路径的内容。拒绝与系统程序、控制器、proc/dev/tmp/run 等保留路径重叠的 HOME。HOME 与全部程序目录在构建前验证，探针描述符与工作区必须一致。
- 源 HOME/根映射与分类记录以摘要绑定；文件和链接的映射在写入前整体检查，拒绝穿越、父子/跨根冲突、程序/数据冲突及逃逸链接。只创建普通文件与受限相对链接，不恢复设备/FIFO/socket/硬链接，不接受文件内容进日志。候选构建收据只记录来源、摘要、数量与范围，不代表启动或健康通过；试运行会产生自己的写入，收据也不能证明试运行后数据仍与原清单一致，正式激活需后续控制器重新构建或复核，不能自动把测试产物接成日常环境。
- 网关试运行只在既有独立网络/PID/文件隔离中执行。控制层只读挂入当前 porter 自身的探针入口与固定配置（不写入 harness 目录），探针启动固定私有程序、按 profile 已声明的回环 HTTP 入口及接受状态码核实；仅接受处于 bwrap 私有 PID 命名空间、作为其 init 直接子进程且可执行文件为固定只读 `/valise/porter` 的内部探针。探针自身关闭 dumpable，阻止候选通过同 UID 的 /proc 描述符或内存通道伪造结果。禁止重定向、代理、cookie 保存和读取/记录响应内容。探针不监听端口、不发送聊天或模型请求。程序退出、端口响应、HTTP 符合规则与隔离子进程已停止分别记录；全部通过也不等于正式激活或对话验收。
- 试运行结束先停止私有进程；只有确认位于本次 bwrap 的私有 PID 命名空间，才可清理其中的子进程，不扫描/结束宿主进程。超时/取消/异常退出留下未知或失败，不以缺少响应补记成功。退出后不能留下试运行实例占用独占身份。只有本模块的合成 profile 通过不改变官方 harness/命名实例能力声明。正式服务、首次接入、双环境切换、用户验收与界面仍在后续模块接入。

第二模块首版限制：程序材料获取/依赖/基础数据来源适配尚未实现；没有适用全量底座的非全量候选返回缺少基础数据，不假装已经按记录版本完成重建。私有根之外的布局暂不支持。程序材料链接的后续扩展只允许明确清单中的符号链接，记录目标文字及 SHA-256，目标必须解析到同一材料的明确私有程序目录内；拒绝指向数据/宿主/缺失项、循环、目录祖先循环和硬链接。普通文件全部验证写完、完整归档摘要通过后才建立链接。标准 tar 的长名称只接受与已核实条目一致的 path/linkpath 扩展，拒绝其他 PAX 元数据。本模块当时尚无构建/试运行 RPC 或 GUI；后续专用测试入口见第 19.8 节，正式服务切换仍未完成。不得因为内部接口参数含 verified 就接受远端 JSON 自称来源已核实。


### 19.7 到前端可测试阶段的产品范围（原 2026-11-12 用户 Goal）

2026-11-15 用户已取消长时自动 Goal，改为逐轮交付、用户参与测试。本节保留未完成的产品范围，不是自动继续执行或宣称已交付的依据。

目标为把本轮已确认计划推进到实际前端可测试；不能以内部模块或合成后端通过代替。验收至少覆盖：精确时间点与副本选择、文件/完整隔离测试分层、生产来源与独立运行时、三种 Agent 设置模式、A/B 交替与失败退回、重启仅核实、人工确认可用与独立清理、同机分身准备/人工启用及迁移收尾，以及真实适配与原生视图。分 profile 明确能力，不用未实现按钮画饼。

用户补充确认：Hermes 等没有网页入口的 harness，完整重建用该版本官方提供且可核实的离线就绪检查；缺少可靠检查就说明尚不支持完整测试。不能把进程存活、退出码零或共享服务 active 当作等价通过，也不自行添加网页或注入测试代码。原 Agent 持续运行、受测进程无外连，正式切换后人类实际对话与独立确认规则不变。具体已核实离线规则通过通用 profile 字段声明。

离线首个适配器是通用 Unix JSON 客户端，按 PROFILES.md 1.5 的固定请求与元数据断言执行，仍在候选的私有网络/PID/HOME 内，不创建监听器。拒绝宿主 socket、外部路径、公开或非本用户 socket、peer UID 不匹配、超长/未完整/重复字段 JSON、缺少修订绑定与模糊目标成员。记录 socket 可连接、收到有效 JSON、声明核对通过、进程停止四类结果，不能把离线通过填成 HTTP 通过；不读取聊天、不发模型消息、不返回原响应。

文件测试选择绑定完整 SnapshotRef、所选位置及加密头摘要，健康页选择、旅程页直接选中后使用同一服务入口。读取后再次核对实际 VaultID、SnapshotID、头摘要和实例；位置不支持/来源冲突时拒绝，不自动换最新或换副本。GUI 开始按钮前显示实际时间、范围与读取位置；已存在记录可先选择，继续扫描不清空选择。

2026-11-12 用户确认新备份升级格式、永久兼容旧格式：按 VAULT_FORMAT.md 第 12 节仅升级快照头为 `/2` 并同步独立恢复工具；运行时来源、来源 HOME/规范根和命名实例所属安装的登记记录进入加密头。没有运行时依赖证据时仍标未知，不凭父实例当前版本回填旧历史。

生产材料首个适配器按 VAULT_FORMAT.md 12.1 记录并匹配明确程序树，先覆盖 npm_global 的私有安装。只读备份采集摘要不执行安装/解释器，失败不阻断数据备份、不虚构部分证据。独立材料复制仍属于正式准备计划，使用管理连接；只有来源、树摘要、系统架构和布局完整吻合才可复制，不能把当前程序当旧快照未记录的运行时。材料复制后再次核对，实际程序位于独立目录，不引用活动目录。

### 19.8 隔离测试传输与日志（2026-11-12 实现映射）

- `environment-test/1` 只表示 porter 能接收专用隔离测试，不表示任意 profile、旧备份或系统已经支持。`environment_test` 为管理级方法，客户端每次另开管理连接；不开放到 MCP。目标只取既有测试 op_id，不接受命令、路径、程序归档或已通过的证据。
- 新文件测试在 stage_begin 时固定 `test_only`，完整测试另外冻结解密后的目标头、文件清单、VaultID 和所选加密头摘要。测试操作不能 swap、continue、携带活动数据、安装服务或建立恢复关系。旧日志缺少该字段仍按原契约读取；不能把旧正式恢复当测试清理。
- porter 用已接受 dossier/profile、真实根和明确的备份世代绑定核对冻结输入，再按记录摘要复制独立程序，读取暂存数据构建，最后运行隔离探针。不能接受请求里的 verified 声明。第一条传输路径仅覆盖同机默认实例、完整 full/raw 数据、精确匹配的已登记私有 npm 来源；命名实例、其他版本重建和非全量底座仍需适配，不静默借用活动环境或最新安装。
- 测试执行前保存 preparing；私有程序启动前保存 probing 意图；完成保存材料摘要及分项探针事实。重复执行请求和重启只能返回原记录，不能重放。未知运行收尾不能当作通过，也不能删除可能仍在使用的材料。清理由原测试的明确收尾动作执行，只删除固定 op_id 私有目录与对应测试暂存，不碰当前实例或备份库。
- 客户端保留文件校验与完整环境结果两层；HTTP、官方离线就绪、进程停止分别报告。完整测试未通过时，已经通过的文件校验仍是真实事实，不能把文件通过显示成完整通过。
- 完整测试选项同时检查 porter 传输能力及当前实例所用资料/版本和已接入的来源适配；只有 porter 更新而资料/来源尚未适配时，不渲染完整测试选择，说明仍可测试文件。所选具体备份的加密来源仍在解锁后重新核实，不凭当前资料推定旧备份可重建。


### 19.9 非全量测试的组合传输（2026-11-13）

- `environment-test/2` 在 v1 专用测试上增加单个明确的全量底座，不改变公开备份格式。客户端只扫描当前 Agent 所在连接及已配置副本，按时间倒序核实完整头、资料/分类/来源/布局与备份世代绑定；不以名字判断同一 Agent，也不连接无关服务器。目标点、副本和头摘要始终不变。不可读位置记为不完整，不宣称找到所有位置中的最近点。
- 底座与目标均保留各自 SnapshotRef、头摘要与清单；目标必须非 full/raw，底座必须同一世代且 full、不晚于目标，来源/资料/依赖/布局相容。不能把更新全量、未知世代或当前目录当作底座。尚无可用底座时仍需按记录官方来源重建；该适配未完成之前明确报告，不能悄悄空白填充。
- 客户端在本次人类进程中展示所选时间点、完整底座时间/位置、范围外来源与最终文件量，确认后才创建测试暂存。此确认不是跨进程票据、JSON 布尔授权或恢复/删除授权。取消不启动程序、不改当前 Agent。
- core.Compose 生成全量候选的组合清单；整个目标档位范围取目标点（包括删除），范围外才读底座。客户端按条目的确切来源解密并传输，porter 再核实两个头的绑定/兼容、组合范围和逐项哈希。不得只把两份清单或目录叠加。组合只属于本次测试，不生成旅程点、不修改旧备份；结果单列底座来源，不能把范围外内容称为目标时间点的数据。
- 含底座的输入在开始暂存与新管理连接执行前都要求 v2，旧端不允许忽略新字段执行。


### 19.10 正式受管运行入口（2026-11-13 技术映射，尚未开放并行执行）

- 正式运行与隔离测试使用不同的内部编译入口。测试没有开启网络的参数；正式入口由已核实候选、接受的 roots 和交换日志构建，只共享网络，保留私有 PID/HOME/proc/run、清空宿主环境与只读独立程序。数据只绑定已登记且本用户拥有的稳定活动根，程序覆盖在其上，不能让数据挂载盖掉只读程序。root 自身/父级别名、冲突和程序内数据根拒绝。
- 稳定入口为完整 AgentRef 摘要命名的 `valise-agent-<sha256>.service` 用户单元，systemd 直接运行已编译的 `/usr/bin/bwrap` argv；porter 不常驻、不监听。不生成脚本、不在 harness 放代码，不使用旧槽位软链。单元只由管理连接中的正式执行器安装，不能从 RPC 提供任意 unit/argv。输出不采集，用户单元仅使用 NoNewPrivileges，不写 CapabilityBoundingSet/AmbientCapabilities。
- 单元定义按固定 systemd 字符规则转义，并以内容摘要绑定。创建只接受不存在的名称；更新只接受当前定义摘要吻合、无 drop-in、确已停止的本用户单元。查询只读，不初始化、不修复、不重启；命令退出、单元运行、进程停止和网关结果分别记录。
- 整个切换事务在停服前保存原来源/模式/环境与单元修订及启用状态，先关闭原启动入口的开机启动，再停服、核实并交换数据；受管 unit 定义更新必须发生在数据交换之后、启动之前。只有网关核实后才重新启用开机启动。这样重启不会把旧程序与已交换的新数据错误组合，也不重放切换。失败先停并核实候选，再按日志恢复原数据、独立程序和启动入口。
- 首次接入还须冻结原环境的独立程序与启动事实、检查共享服务/其他实例，并把旧原生入口停用纳入人类计划。备份和原生界面须跟随活动环境及独立程序绑定，不能让它们继续读取封存的旧环境。此闭环及三模式/人工验收/清理未完成前，不广告正式并行恢复能力或渲染相应执行按钮。

受管查询核对已记录的定义摘要、systemd fragment、无 drop-in/待重载/排队任务，以及本用户 cgroup 和进程内核启动标识；停止必须另查 cgroup 为空。HTTP 先核实监听 socket 属于该 cgroup，再核对固定回环入口，防止把其他进程的成功网页算为候选就绪。正式网络入口仅复制公开 DNS 配置及经过标准 X.509 解析的公开 CA 证书，不挂宿主 /etc 或私钥目录。此内部模块已过真实 Linux 合成用户服务验证，但未接正式恢复 RPC。

### 19.11 受管环境的数据交换子日志（2026-11-13）

- 复用目录交换机制，但由正式执行器在首次写日志时绑定完整 AgentRef、父操作、SnapshotRef、原/候选环境修订、计划和完整数据清单摘要，以及原/候选根的设备与 inode。公开 Plan 无此授权字段，不能通过 JSON 自称受管操作。子日志只放置数据，不安装/移动程序，不控制服务，不建立新身份或旅程。
- 准备和传输发生在 building/prepared，原环境仍运行；实际交换只允许父操作已核实 original_down。操作、世代、修订、登记根和清单变化均拒绝。数据放置完成不是正式恢复成功；后续服务启动、网关与人工对话验收由父事务分别记录。
- 退回只允许父事务已核实 candidate_down（或已保存 resuming 意图）。先将已完成的数据子日志原子移回待处理目录并同步目录，再按原日志退回；每次重命名同步父目录，跨多个根中途断开按各根身份核实，不只看上次写下的阶段。原目录丢失/被替换时拒绝冒报退回成功。候选停止未知时不移动数据，不启动原环境。失败候选和原目录保留，不作为备份或新旅程点。
- 普通 swap/resume、恢复分支检查与旧目录清理不能执行或清除受管子日志。查询仍可读；清理归父操作的独立授权。初始内部适配只支持互不重叠的真实登记根及普通文件数据清单；旧暂存清单没有链接目标字段，故拒绝未绑定链接，而不是默默忽略。共享/嵌套实例不猜停止范围。调用方必须持有服务器恢复互斥锁；在父事务和入口绑定完成前不开放新 RPC 或按钮。

### 19.12 正式切换的父事务（2026-11-13，内部实现）

- 执行器把已准备的环境登记、数据子日志及原/候选受管单元定义绑定为一个私有持久操作。单元定义仅由内部编译器产生，按完整 AgentRef 和内容摘要私有保存，读取不创建文件或执行命令；不能用 JSON 提供单元内容或 argv。父记录冻结身份、SnapshotRef、两边修订、数据操作、资料与原开机启动策略。
- 数据子计划与清单摘要也进入父绑定。独立程序的每边材料引用按环境修订冻结，可信来源适配器必须在准备、停原环境前、启动候选前与退回启动原环境前核对完整私有程序清单；没有适配器就拒绝执行，不用单元内容摘要代替程序校验。此适配器是内部 Go 代码，不是请求提供的回调、verified 布尔值或确认票据。
- 准备、正式切换及明确退回自己持有服务器恢复互斥锁，和备份/安装/目录操作串行。每个外部改动先保存意图，再分别保存命令结果和核实证据；准备完成前原单元继续运行。正式入口一次执行，重复请求/重启只返回记录，不重发未知步骤。
- 顺序为关闭开机启动、停并核实 A、放置 B 数据、更新受管定义、记录候选启动意图、启动 B、核实拥有的网关，最后按原策略恢复开机启动并进入待人工验收。网关通过不是对话验收，父事务不调用确认或清理。
- 失败保存原因并进入退回；先关闭已装定义的开机启动，停并核实这个精确单元及 cgroup，才记候选已停、退回原数据及定义、记录启动原环境意图，启动并检查 A。原恢复保留失败，B 的写入留在失败目录。定义/进程/停止状态不能核实时保留待核实，不启另一环境。
- 查询返回原持久事实与当前可核实的单元/网关状态；查询不初始化、修复、启动、切换、确认或删除。明确退回可在互斥锁下根据日志核实部分步骤；它不是重开页面时自动重放。旧环境修订已复用或原操作已被后续操作取代时不允许旧命令修改当前运行。
- 准备阶段取消且原数据/定义/进程仍可核实，保持原 PID，不为退回而重启原环境。关闭过的开机启动按冻结策略恢复；原先未开启的也不擅自开启。查询前后环境修订变化则不拼接运行证据，提示重新核实。切回中的问题和原失败原因分别保留；问题处理后的明确重试不重做已经完成的数据退回。
- 首次内部适配先连接已受管、正在运行的 A 与已有数据子日志，使用明确 HTTP 规则。原生入口的首次接入、活动程序/备份/原生界面绑定、正式离线规则和完整入口仍需接入；未闭环前不广告正式执行能力或新增 GUI 恢复按钮。此阶段限制不替代第 19.7 节的完整交付目标。

### 19.13 独立程序的持久材料绑定（2026-11-13）

- 程序绑定属于服务器私有状态，按完整 AgentRef、槽位和修订分隔；每个修订不可覆盖。程序在该修订的固定私有目录下由来源适配器的标准 tar/完整清单构建，不引用活动目录，也不接受 RPC 任意宿主路径或程序包。快照来源保留完整源身份/时间点；首次接入的原环境只记录实测程序来源，不伪造一份备份。
- 绑定保存接受的 profile/布局、完整程序清单/来源/归档摘要、实际私有工作区、固定启动描述与构建收据。绑定摘要进入父事务；只有可信程序构建入口能新建，读取和核对不创建或修复文件。私有格式不改变公开备份库格式。
- 每次正式启动前按绑定重查程序目录、文件类型/所有者/权限/大小/SHA-256及链接目标，拒绝硬链接、额外文件、外部/循环链接和路径别名。链接在材料图内解析，不在宿主跟随来源 HOME 的绝对目标。普通文件内容仅用于哈希，不输出或解析凭据。
- 校验绑定必须仍属于当前 Agent 世代和所请求的槽位修订；历史/已复用的修订不能启动或清理新环境。读取原记录不证明程序此刻完整；完整校验也不证明网关、对话或正式恢复成功。原环境的程序构建不复制/封存活动数据，原数据只在后续带日志交换时保留。
- 首次接入、备份与原生界面的活动绑定、正式服务传输和 GUI 继续接入；仅增加程序绑定不开放执行能力，也不缩减第 19.7 节目标。

### 19.14 Agent 恢复模式设置（2026-11-13，前端接入）

- `environment-settings/1` 只广告恢复模式的读取与保存，不代表正式并行执行已接通。读取为日常级，保存为本人级；两者绑定完整 AgentRef，不开放 MCP。界面位于 Agent「管理与设置」，默认并行，另外提供小占用并行及直通；更改后由用户明确保存。
- 尚未接入受管运行入口时，模式存在独立私有策略文件，不创建环境登记、不声称已有激活 A。受管登记建立后以登记中的模式为准；首次建立时继承已保存策略。读取不创建文件。保存比较包含策略来源与修订的摘要，拒绝过期页面、世代变化和进行中的环境操作；服务器互斥锁与环境 CAS 共同串行化。保存模式不启停服务、不交换或清理目录。
- 恢复当前实例显示实际读取的模式。正式并行执行尚未适配时说明原因并链接 Agent 设置，不能沿用直接恢复冒充并行。显式选择直通才可走既有直接流程；已经受管的实例仍不得绕回旧流程。准备和执行前重新核对模式，不因持有旧计划而忽略设置变化。旧 porter 无此能力时提示更新，不显示可保存但服务器不认识的设置。
- 关闭并行后的旧环境清理仍是独立动作，设置保存本身不授权删除；清理和正式切换按钮仅在对应执行能力实际接通后显示。

旧直接恢复的模式约束也由服务器执行：建立正式暂存及首次停服/交换前，按服务器接受的身份、实际目标根与将操作的服务读取设置，只有明确保存为直通且尚未受管的已有 Agent 可进入。未绑定的当前实例须先重新接入；换实例 ID、把请求标为迁移/副本或使用路径别名，均不能绕过实际目标 Agent 的设置。只做文件测试不要求直通。已停服的旧交换日志继续按原日志处理；仅更改模式不会阻断退回、丢弃或独立清理（已有运行环境占用检查仍生效），也不把它们变成恢复许可。尚未停服的旧计划在模式改变后不再提供继续，但保留退回入口；再次确认和真正执行都重新检查。

### 19.15 首次从原生服务切换（2026-11-13，执行适配）

- 首次恢复不要求先把运行中的 A 重启成受管服务。准备期间原生入口保持运行；控制器核实该入口及当前程序，保存原程序的独立材料，然后把真实运行的原环境登记为 A。B 仍在另一私有目录准备，不复制活动数据作为临时备份，不产生旅程点。
- 原生入口只能来自接受的 profile 的明确用户服务，独立记录定义文件摘要、实际解释器摘要、PID/内核启动标识、cgroup 与开机启动状态；拒绝系统级服务、歧义候选、额外 drop-in、未重载/排队任务及不明所有权。单元文件仅用于哈希，不输出内容或解析凭据；不读取进程参数/环境。停服前重查精确原进程，停止后核实该 cgroup 为空，不能把 systemctl 退出码当成停止证明。
- 父事务冻结原生描述符和独立 A 材料。正式执行先关闭原生开机启动、停止并核实原生入口，才交换数据和安装/启动受管 B。原生单元文件保留，B 与原生入口不能同时运行。首次安装受管单元必须确认该名字尚不存在，不能假装已安装了 A。
- B 失败或明确切回时，先核实 B 已停止，再退回原数据并以独立程序的受管 A 启动；不依赖联网重装或重新读取可能变化的原安装。若在停原生入口之前取消，保留原 PID，只恢复原开机启动策略。查询/重启只读，不自动重发停服、交换或启动。未知原生停止/定义/进程身份均保留待核实，不启动另一环境。
- 原生事实和程序来源仍须由可信准备控制器交叉核实，不能通过 RPC 提交任意单元、路径、命令或“已经停止”的布尔值。此内部适配与前端正式入口分开广告；活动绑定及旧入口冲突保护接通前不开放执行按钮。

### 19.16 同一 Agent 的嵌套内容根（2026-11-13）

- 一个 Agent 的 workspace 等逻辑内容根可以位于其状态根内部，不能因此视为共享 Agent 或要求用户搬目录。控制器依据完整登记根生成唯一映射：内部根始终指向最外层物理根，不串联多个嵌套交换；同一路径的别名根、未规范路径和请求自行改写的映射拒绝。
- 文件清单仍使用原逻辑 root ID 和各自分类。只有最外层物理根创建暂存、捕获交换 inode、交换及回退；暂存预建全部已登记内部根（包括空根）。正式运行也只挂载最外层根，内部相对位置必须和来源布局一致，不能以重叠挂载覆盖其他内容或程序。
- 除物理根身份，嵌套布局还在首次日志中记录原逻辑根的目录身份。交换前和回退时核对其在原环境中的具体位置，不跟随内部目录别名，不把丢失/被替换的内部根冒充保留完好的原环境。旧的独立根日志继续读取，不要求补写字段。其他 Agent 的重叠根与共享服务仍须独立核实，不因本条自动放行。
- 此映射不改变备份库格式或分类，不从活动数据补齐非全量备份，不增加旅程点，也不更改未核实的 profile 能力声明。

### 19.17 真实来源的只读恢复条件预览（2026-11-13）

- `environment-restore-plan/1` 仅声明日常级只读 `environment_restore_plan`。客户端先解锁并核对精确时间点/副本的加密头，提交完整 AgentRef、设置摘要及所选材料元数据；服务器重新核实当前身份、接受的资料、真实根、备份世代绑定和组合来源。不接受请求指定服务、路径、程序或“已核实”布尔值，不开放 MCP。
- 预览从接受的 profile 选择唯一原生用户服务，读取真实定义/进程证据，扫描明确私有 npm 来源；所选记录的完整依赖、安装来源和当前程序必须吻合，原生实际可执行文件也必须属于同一程序材料并匹配摘要。先后重读身份/设置/原生事实，拒绝过程中变化。原生进程运行、程序来源吻合、网关就绪、正式恢复和人工对话验收仍分别表达；预览不启动网关探针。
- 返回内容范围、逻辑/物理根数、独立程序量和各文件系统的保守额外空间预算。预算按已确定的准备布局计算：一个程序归档、原/正式候选/独立探针三份程序，正式暂存/候选/探针三份数据及元数据余量；已占用的活动环境不重复计入额外需求，空间不足不自动删除或降级。预览不写日志、建登记、复制文件、创建暂存、启停服务或安装任何内容。
- 前端显示“查看并行恢复条件”与真实预览结果，完整来源清单和可用空间不代替执行许可。准备、切换、确认和清理尚未接通时不显示对应按钮，明确本构建只能预览；未来执行须另经人类计划确认并重新核实原条件。只读预览 capability 不能被当作正式准备/激活 capability。



### 19.18 首次准备控制器的持久输入（2026-11-14，内部接入）

- 首次准备由可信控制器重做 19.17 的完整来源检查，比较人类所看预览的摘要，并核实各文件系统空间。只能从接受资料与精确备份输入派生路径/命令/清单，不接受请求提供程序包、原生描述符或已通过标记。管理连接及原操作的明确授权仍是未来传输入口的前提；内部控制器本身不广告新执行 capability。
- 在复制任何程序前，先独占保存该 Agent 世代的准备记录，绑定输入、预览、原生事实、原目录身份及固定数据操作 ID。记录先于归档、独立 A、登记 A/预留 B 和数据暂存；重复请求与重新打开只读原记录，不重放中断步骤，不覆盖部分程序修订。失败保留原步骤及固定错误码，不把未知意图算完成。
- 原程序标准 tar 与独立 A 都只含程序，原 Agent 持续运行；复制后再次核实原服务、完整来源、设置和目录身份，然后才建立真实 A 并预留 B。正式数据子日志从首次写入即带父绑定，不把测试日志升级为正式恢复。非全量按原组合清单传输，不能复制活动数据补齐；尚未支持的条目类型在任何写入前拒绝。
- 准备收尾在原互斥锁下核对数据清单、原来源及身份，建立未运行的正式 B，再单独构建/探测另一套私有程序和数据。探针写入不得进入正式 B 或交换暂存。探针核实停止后才记录 prepared 并绑定父激活事务；正式切换、对话确认和清理仍为独立动作。
- 尚未完成部分准备的明确取消/清理、所有旧执行入口互斥、活动备份/原生界面/健康绑定及对应传输前，不开放 GUI 准备或切换按钮。此内部接入不替代 19.7 的完整前端交付范围。

首次准备的父绑定还包含完整原程序来源材料摘要；正式停原生入口之前重新观察全部声明程序树及实际解释器，与独立 A 的来源和文件清单逐项核对。仅原 PID、单元或解释器没有变化不能证明程序未更新。此源检查只在原生入口仍活动时要求，停服后的失败退回使用独立材料，不依赖已失效的原安装。旧内部私有父记录缺少此可选字段时仍可读取，生产准备控制器始终生成它；不从 RPC 接收或省略这一绑定。

### 19.19 取消首次准备与独立材料清理（2026-11-14，内部接入）

- 取消准备只结束尚未正式切换的原操作，原生入口、原 PID、活动目录与开机策略保持原样；不把取消当作网关健康或对话通过，也不默认删除材料。已经开始停服、交换或激活时必须走原父事务的明确切回，不能冒充准备取消。
- 复制/构建的每个顶层私有目录及数据暂存，在首次写内容前由控制器登记设备/inode 与固定资源种类；请求不能提供清理路径。目录创建到所有权记录之间中断时，只可移除仍为空的固定目录，不递归删除未知内容。记录保存意图早于删除，失败保留已完成项；明确重试只核实并完成原清理，不另认领目录。
- 隔离探针的私有生命周期锁在启动前由控制器持有，并通过固定 `--sync-fd` 交给 bwrap 的私有 namespace init，harness 不持有该描述符。取消/清理取得该固定锁的独占句柄才证明没有仍使用材料的探针；不因 porter 已退出、计时结束或旧日志缺少通过结果而猜测停止。旧无生命周期锁的未知探针仍保留材料并说明无法核实。
- 清理是另一次明确授权：先返回完整 Agent/操作、固定材料清单与当前可核实状态的摘要，执行时在互斥锁下重新比较。只清理已取消、未开始正式切换的首次准备材料；不删除活动数据、备份、其他 Agent 或已投入日常运行的程序。改过的目录身份、共享/重叠登记、已安装的受管入口、未知探针或过期摘要都拒绝。
- 清理完尚未接管原生服务的首次准备后，可在 CAS 下解除仅由本次建立的初始 A/B 登记，保留最新恢复模式及单调增加的策略修订，让下一次准备重新核实当前原生来源。不复用旧修订的程序、不覆盖部分 A。原准备记录作为已取消/已清理的只读记录保留；受管实例日后的候选取消/复用另按活动绑定办理，不允许解除真实活动环境。
- 状态查询只读，分别返回准备阶段、已取消、尚留材料、清理中/完成及原生/隔离进程当前事实；未知意图不自动重放。管理连接、人类确认、旧入口冲突保护和活动绑定未闭环前，仍不广告新准备/清理 RPC 或执行按钮。

### 19.20 旧入口的运行环境占用保护（2026-11-14）

- 原生准备记录在第一次复制之前即占用完整 Agent、登记根、原程序/解释器、原生服务与固定私有材料；已有环境登记持续占用其当前资源。取消但尚未清理不释放所有权，已清理的旧记录不阻止新的修订。保护只查当前服务器的私有记录，不扫描其他服务器。
- 普通登记/资料改写、直接恢复/继续/旧目录清理、服务控制、管理动作/事务、技能安装/分类决定及旧登录/原生执行入口，必须在原互斥锁内核对实际实例、目录和服务范围。其他实例只在目录或服务确有交集时受影响；不能用另一实例 ID、路径别名、先前生成的计划、旧日志或通用 exec 绕开保护。
- 同一安装的命名实例共用资料副本。登记另一个不重叠的命名实例可以保留完全相同的资料；改写正在被环境记录使用的共享资料属于冲突。旧登录会话的准备不构成之后的执行许可，启动时重新检查；命令运行期间 porter 保留互斥，子进程也继承锁引用。
- 检查只读，不迁移旧档案、不初始化环境、不修复损坏记录；无法核实所有权时返回原因与原操作处理建议。已有服务/准备记录、原始文件内容及命令参数不进入错误输出。所有权保护不代替本人确认、管理登录或任务授权。
- 尚无环境记录时，不把未来私有环境所需的目录权限强加给原有操作；发现环境记录后，才核实其完整目录链、属主与私有文件。测试 HOME 显式设为私有权限，不放松已有记录的检查来迁就测试夹具。
- 普通实例列表/记录/profile 也不在读取中自动导入旧档案。已提交记录优先；未转换旧记录仍可只读查看，按原安装的旧通用 profile 投影，不建立身份或把旧资料升级写入已接受档案。兼容转换仅在既有明确写操作的原互斥内，对目标所属安装执行；先核对全部转换范围的占用，再写资料/档案并保留旧文件。重复旧副本不覆盖已接受事实，显式移除记录后不从它复活。此转换不产生新旅程或备份。
- 只做文件测试时按实际暂存目标检查，不因来源属于受管 Agent 就禁止读取备份。父绑定的数据传输仅在原 building/prepared 阶段允许；正式交换/退回仍只由父执行器在对应已停证据下调用。普通用户不能提交“忽略保护”字段。
- 已受管实例的日常备份、健康、管理页/原生程序须另从实际活动绑定派生，不能因为旧入口被拦住就永久关闭日常能力。无可核实实例/路径范围的旧通用 exec 在服务器存在占用时拒绝；可用的原生入口须走固定 profile 与当前独立程序的专用适配，不按 argv 文本猜涉及哪个 Agent。完成这些适配与全部入口检查后才开放正式 GUI 准备/切换。

### 19.21 当前环境的只读绑定（2026-11-14）

- 当前绑定共用正式父记录的严格读取与摘要规则，不从 unit 名称或登记的 Active 字段单独推断。核对完整 AgentRef、活动修订、原操作与数据子日志、已接受资料/根、程序材料摘要及实际活动目录身份，再读取已归档且当前安装的服务定义和进程事实。查询不编译服务定义、不创建 DNS/CA 材料、不初始化记录或执行未完成意图。
- 原生首次准备尚未切换时单独核实原 NativeRef，不能把私有 A 的程序副本当作已经运行的程序；正式切换后用当前受管定义与独立程序。运行、停止、网关监听归属、HTTP 检查和人类确认可对话仍为独立事实。历史已保存成功不是本次查询的运行证明。
- 原生阶段还须按独立 A 的来源记录重新核实完整原程序，不能仅检查解释器/PID。原服务合法重启后，日常查询可在完整来源、定义和可执行文件仍匹配时观察新的 PID/启动标识；不回写冻结的切换计划，不让原切换确认重新生效。缺少完整来源证据的旧内部原生记录不能冒充已核实的生产绑定。
- 停止、交换、启动或退回中的记录不提供可供日常执行使用的稳定绑定；查询返回具体未核实状态。查询前后重读登记和父记录，修订或事实改变就丢弃混合观察，不沿用旧服务结果。再次准备期间仅在当前活动环境与先前已完成操作的修订仍一致时沿用其绑定，不要求未使用的旧环境仍存在。
- 已确认候选的非活动旧环境清理不改变当前运行绑定；历史对话确认按完整环境修订保留，取消下一次准备不抹掉仍激活环境的确认。该读取规则不实现清理或再次恢复执行器。
- 绑定只为当前 Agent 读取本服务器数据，不连接其他服务器；未知绑定不能回落到旧 dossier 的原程序或共享服务。管理登录、本人确认和专用动作边界保持不变，只读绑定本身不授予启动、切换、对话确认或删除权限。现阶段新增读取适配不代表正式执行入口已经交付。

### 19.22 活动环境的备份读取（2026-11-14）

- 备份在原有跨进程互斥内绑定稳定活动修订和数据目录身份。数据可读与程序来源可核实是两项事实；程序、网关或服务不健康不应阻止可核实数据的加密备份，来源证据缺失写入 runtime.unresolved，不以旧程序补齐。停止/交换/退回中或数据身份无法核实时拒绝混合备份。
- 已绑定的备份及嵌套实例读取只查询已接受档案，不在日常备份中导入旧档案或更新共享资料。当前登记缺失但仍有运行环境归属时不能回落到旧原生来源；完成前的首次原数据以已保存的数据身份为准。
- 首次接入在独立 A 完成、创建环境登记之前保存只读的原生数据绑定：完整 AgentRef、A 修订、本次准备操作、独立程序摘要、原生入口及所有逻辑根的设备/inode。它不授予切换权限；仅在尚未发生正式切换、原生 A 仍为活动修订及目录身份吻合时支持日常读取。准备失败/取消但材料保留，不因此永久关闭原数据备份。重新准备仅在初始登记已明确解除后更新该绑定，不自动清理材料。
- 已受管时，普通数据档位读取活动数据根；runtime 按当前独立程序的记录与实际材料生成，不能读取封存原程序来伪造来源。raw 按运行时的程序覆盖关系读取私有程序在已声明备份根内的文件/链接，排除数据根中被覆盖而不可见的旧程序；不新增 roots、不扩展普通档位、不把根外解释器自动塞进备份。无法完整核实 raw 程序覆盖时给出失败原因与数据档位建议。
- 本模块不改变公开快照头 /2、周期、age 或独立恢复语义，永久兼容旧头 /1。备份来源绑定本身不是恢复成功或对话确认，也不修改保护计划。

### 19.23 当前环境的管理页令牌与原生界面（2026-11-14）

- 已接入 Agent 的原生界面按实例调用，服务器只读取该 Agent 已接受资料的 native_cli；不接受请求中的 argv、宿主程序位置、环境或挂载。安装向导尚未接入实例的旧入口保留，但不能绕过已有环境归属。原生界面继续使用每次新开的管理连接，不向日常钥匙或 MCP 开放。
- 管理页令牌保持现有主人级操作；受管时只允许已接受资料对应入口的固定命令，不能用客户端传入的命令替换它。尚未受管的实例保留原客户端资料契约。令牌及完整地址只在内存经过，不进入错误、审计、会话文件或终端记录。
- 两类执行都在服务器原互斥内重新绑定完整身份、稳定活动修订、接受资料、真实数据根及独立程序。原生首次准备期间须另验整个原来源与原生入口；尚未切换不把独立 A 副本当作已运行的服务。材料不全、正在交换或身份改变时不回落宿主 PATH 或旧安装。
- 受管执行从当前私有程序清单解析固定 argv，只在已核实程序路径中找入口，不用宿主 PATH；使用相同程序/数据映射和网络策略，独立 PID/run/tmp，程序只读。日常执行只读取已准备的公开 DNS/CA 材料，不编译服务、创建网络材料、启停网关或重放恢复。用户在原生界面的输入与输出只转交，既不解析也不保存。
- porter 持有互斥直至所启动的终端/令牌命令结束；命名空间 init 继承生命周期锁引用，命令本身不持有或读取锁。断开或取消只结束本次命令的私有进程，不停止当前网关。退出码不代替网关或对话确认；原生会话不能自动确认可用或清理旧环境。
- 原生界面查询只返回已接受的命令说明/提示和实际传输能力，不声称网关已运行。GUI/TUI/CLI 共用按实例入口；通用服务器 shell 仍受旧占用保护，不把它当作活动绑定的绕过方式。此消费者接入不代表完整 A/B 准备/切换/确认/清理已交付。

### 19.24 当前环境的审批读取（2026-11-14）

- 既有日常级 approval_check 仅接收实例 ID。已有环境时在原互斥内从严格当前绑定选择实际私有程序的 approval_check，不接收请求 argv、挂载或“已核实”字段；未知/正在转换的绑定不回落到旧安装。没有环境归属的旧实例保留原读取兼容，查询不隐式转换旧档案。
- 检查使用固定私有程序、当前真实数据根及已准备的网络材料。数据和程序只读，HOME 为命名空间内临时目录，保留记录中的绝对 HOME 名字；私有 /tmp、/run 可写但不落到持久材料。程序覆盖处的现有数据目录在启动前核实，不为一次查询向活动目录补建挂载点。缺失或别名不符就报告未核实，不改变原数据/程序或自动修复。
- 输出仅在有界内存中按已声明 JSON 规则读取模式和默认值，stderr 丢弃，错误不带原输出；超限、超时和退出失败均不能产生通过状态。检查前后核实当前绑定未改变，不使用上次读取补绿。父进程及命名空间生命周期持有实际锁引用，有限检查结束/取消后停止本次私有进程；不启停当前网关、不代发模型请求、不执行恢复或确认对话。
- 此读取不为尚未适配的配置修改事务授予当前环境写权限，也不开放通用执行接口或新的并行恢复能力。

### 19.25 首次并行准备的人类传输（2026-11-15，按 19.7 接入）

- 专用能力 `environment-restore-prepare/1` 只覆盖首次原生环境的准备、只读查询、取消与独立材料清理，不表示正式切换、人工对话验收、再次交替或任意来源已适配。来源和布局继续由 19.18 的可信准备控制器核实；不接受请求中的 programme、目录、挂载、服务定义、执行证据或活动槽位。
- `environment_restore_prepare_begin` 为管理级，输入为完整 AgentRef/设置摘要/精确目标与可选底座的已解密清单、已查看条件摘要、操作 ID，以及针对未核实环境能力的明确允许。父操作沿用环境登记的 32 位小写十六进制 ID，与固定数据子操作的 `op_` ID 分开；本机任务索引以 `kind: environment` 区分，不把父 ID 送入旧 swap/resume。独立管理连接上重新派生全部执行事实；未明确允许时拒绝未核实能力。收据只返回操作/快照/状态、固定数据子操作 ID 与绑定摘要，不返回私有原始记录、解密清单或程序材料。程序捕获与数据暂存仍不停止当前 Agent。
- 上传沿用同一管理操作的 `stage-write --op <服务器派生的数据子操作 ID>`，服务器在互斥内核对父归属、开放阶段和固定暂存目录；不为请求路径签发通用写入许可。客户端在内存中从各条目明确的备份来源解密传输，不取活动数据补齐。
- `environment_restore_prepare_finish` 仅接收 AgentRef 和原操作 ID，管理级；只消费父绑定的精确暂存并启动隔离探针。重复 begin/finish 返回原状态而不重新复制、构建或试运行；断线/重启时客户端仅查询原操作，不因请求超时重复上传或执行。
- `environment_restore_prepare_status` 为日常只读，将已有准备记录投影为身份/快照/模式、阶段、材料与探针状态、历史分项探针事实、原环境运行事实及可取消/可审阅清理的条件；不创建记录、不推进或重放。缺少记录仍是结果待核实，不据此重发。条件标志不是确认、删除许可或通过证据。
- `environment_restore_prepare_cancel` 为管理级，只接受 AgentRef 与原操作 ID，使用原控制器核实未正式接管和私有进程生命周期，取消不删除材料。`environment_restore_prepare_cleanup_review` 为日常只读，返回固定材料类型、root 标识、存在与否及审阅摘要；`environment_restore_prepare_cleanup` 为另一次明确的管理级操作，输入同一身份/操作和审阅摘要，重新核实非活动及目录身份后清理。不能用取消或查询代替删除确认。
- 客户端首次执行前在本机保存脱敏的操作索引（含选定时间和范围）及服务器信任摘要，不保存解密材料、身份或人类确认。准备确认绑定当前人类进程、所选副本与条件，十分钟后重新读取；新管理登录完成后、首次发送前再次核对期限和连接信任。已经执行的准备不因确认期限中断。凭据恢复、未核实能力和非全量底座的影响分别明确展示。新增能力尚未发布之前不显示执行按钮；接通准备也不能显示尚未实现的切换/验收/旧环境清理按钮。

### 19.26 正式切换的审阅与人类适配（2026-11-15，接入中）

- `environment-restore-switch/1` 对应固定的 switch_review / switch_status / switch / return 方法，名称前缀均为 `environment_restore_`。审阅/状态为日常只读，切换/切回为单独管理操作。只输入完整 AgentRef、原父操作 ID 与审阅摘要，切换另外接受未核实环境能力的具体允许；不输入路径、单元、程序、阶段证据或通用 confirmed 字段。不开放到 MCP。此能力在人工验收、独立旧环境清理和正常后续恢复闭环前不广告，GUI 不根据仅准备能力开放切换。
- 切换前审阅在原互斥中重新核对父/子绑定、环境修订、独立原/候选程序清单、原生完整来源、精确原进程及开机策略、数据暂存目录身份和完整清单。暂存文件只读打开，核对普通文件/本用户/单链接/大小/内容摘要及敏感权限，拒绝额外文件或特殊节点；不调用会改权限或改 journal 的 StageVerify。SQLite 暂存已经完整性通过且字节摘要未变，按相同字节复核，不产生 WAL 或修改数据库。活动数据仍可变化，不把活动数据的内容/mtime 加入切换确认。
- 审阅摘要绑定父/子记录、登记修订、实际暂存文件身份与权限、原进程/定义/启动策略和核实的材料引用。审阅不复制、写候选、启停、改记录或自动修复。结果只返回脱敏引用、固定阶段/失败码、分项条件、文件量、开机策略和可选动作条件；历史探针通过不冒充这次审阅通过。没有原记录时不创建任何状态。
- 客户端以当前人类进程中的单次审阅对象承接第二次确认，十分钟有效；管理登录后再核对期限和连接信任。服务器执行器在同一互斥内重新派生审阅并比较摘要，变化即停止，不先停 Agent。摘要不是执行票据或通过标记。正式动作开始后使用原有有界父事务完成/退回，不因页面切换或正常关闭打断；命令结果不明只查询原父操作，不重新提交切换。
- 明确切回使用另一份新审阅和人类确认，仍由原父事务先核实候选停止、再按原数据子日志退回；不让通用旧 resume 接手。中断意图不自动重发，审阅条件不保证外部步骤一定成功，实际分项结果另行显示。状态查询仅观察当前运行/网关与原记录，不提交对话确认或删除，待人工对话验收不显示为持续执行。

### 19.27 人工对话确认与确认后的保留环境切回（2026-11-15，接入中）

- `environment_restore_confirm` 为原父操作的专用管理方法，沿用尚未广告的 `environment-restore-switch/1`，不开放 MCP。只接受 AgentRef、原父操作 ID 和新审阅摘要，不接受 confirmed/对话/网关证据、命令或目录。人类界面先引导在原生界面实际对话，再明确勾选“我已实际对话成功”，由本进程单次审阅对象提交；不读取聊天、不代发消息、不用时长或 HTTP 代替人的确认。
- 只读审阅新增 CanConfirm 条件：原操作仍待人工验收、当前精确候选修订和独立程序/实际受管进程可核实且正在运行。当前网关结果另列，不把 HTTP 成功当作对话事实；也不把回退程序损坏当作已验证的候选无法接受。专用 confirmation_digest 绑定现有父/子/环境记录、当前定义与实际运行身份，作为 confirm 请求的 digest；不绑定活跃数据内容、当前 HTTP 结果或独立回退程序的就绪标记，避免把非确认条件变成隐含的过期条件。正常对话写入和 HTTP 状态变化不会单独使人类确认失效；切换/切回仍使用原完整审阅摘要。旧环境暂不能用于切回时明确显示，仍可单独记录新环境的对话验收。
- 确认在原恢复互斥内重新读取并比较摘要，以环境登记的原子 CAS 提交该操作/候选修订的 ConfirmUsable；不启停、交换、删除、生成快照或旅程分支。父记录只记固定 human_acceptance 的意图/完成时间，不含聊天或用户自由文本，且不能作为执行或删除授权。CAS 已提交而回包/后续记账失败时以登记事实为准；重复提交已接受操作只查询，查询不补确认。
- 确认不删除旧环境。并行/小占用模式下，只要原操作尚未被后续操作取代、旧修订未清理或复用且独立原程序/数据仍可核实，允许另一次新审阅和明确切回。Accepted 只能由显式 user_revert 分支进入退回，禁止自动健康回滚；先核实候选停止，再沿原数据日志退回。开始切回时清除当前候选的已确认标志，父记录中的历史人工动作保留；不能把已经不再激活的候选继续显示成当前对话已确认。旧环境已清理时拒绝切回。
- 确认与切回各自十分钟单次审阅，管理登录后再核对；响应丢失只查原操作，不重发。人工确认、清理、后续正常 A/B 交替的完整执行能力仍按 19.10/19.26 的门槛开放；本节不提前广告能力或宣称前端完整可测。

### 19.28 已验收旧环境的独立清理（2026-11-15，接入中）

- 首个正式旧环境清理适配于原操作冻结为小占用并行、当前仍为该候选修订且已人工确认的环境。固定 old_cleanup_review（日常只读）/old_cleanup（单独管理）方法前缀为 environment_restore_，只接受原 Agent/父操作及新审阅摘要；不接收路径、目标槽位、命令或已通过证据，不开放 MCP。清理是另一项明确人类动作，不沿用确认可用的同意；完整执行能力的广告门槛不变。关闭并行后的清理和正常后续交替继续接入，不能以本适配缩小 19.7。
- 只清理原数据子日志固定 prev 目录、原独立程序的确切修订目录及本次准备已结束的归档/隔离测试临时材料。数据身份同时匹配原准备记录和子日志；程序归属匹配创建前保存的目录设备/inode 声明，从对应准备记录及明确历史候选关系查找，不按名称扫描或认领陌生目录。拒绝路径链别名、替换目录、当前/其他已登记（含停用）的数据根或程序范围冲突。元数据遍历限定在旧资源目录内，不跟随符号链接读取外部内容，不读取 Agent 文件内容；仅解析公开 VAULT 描述元数据以保护嵌套备份库，并保护已知备份根。已结束的隔离重建数据副本是私有临时材料，不冒充完整备份位置。审阅列实际种类、根、文件量和逻辑字节，不把它说成保证释放的物理空间。
- 当前候选的独立程序、精确服务/数据绑定及原启动入口停止状态须可核实；候选正在运行或已明确停止均可，网关结果另列，不要求新的对话/HTTP 检查。清理不启停服务、不更改当前数据或备份。原互斥内重新核对范围与审阅摘要后，先进入 Cleaning，阻止冲突的恢复/设置，再保存固定资源及身份；每个资源在删除前保存意图，重新核对目录链/身份/注册冲突，才由 Go 的 os.RemoveAll 删除，随后同步父目录并记完成。
- 私有父记录新增可选 cleanup 进度，旧记录永久可读；不改变公开备份格式。临时测试 namespace 的生命周期锁须证明已停止；此事实在删除前持久保存。中断时只有该停止证据加临时目录删除意图才能解释随后缺失的跟踪文件，不能用缺少文件猜进程已停。资源缺失只有已保存删除意图或完成事实时才是已删除；完成后重新出现的目录不被旧命令删除。中断/失败保持独立的待继续状态和已确认候选，不改写原恢复成功/对话确认；查询不续删或收尾。重新清理须新的十分钟单次人类审阅和管理登录，响应丢失只查询原操作。
- 所有资源完成后先保存进度，再原子提交 OriginalCleaned/旧槽 Empty。原候选在 Cleaning 和完成后仍能按自身根/程序绑定读取、备份及打开已有原生入口；不能为允许缺失旧目录放松正常交换/退回的目录验证。完成后不把预期已删除的旧程序显示成损坏，也不开放切回。正常模式继续保留旧环境；此适配不自动删除、不删除单个备份或周期。

### 19.29 关闭并行后的独立清理入口（2026-11-15，接入中）

- 保存直通设置不删除任何文件、不停止或迁出当前受管环境。Agent 设置中的“查看旧环境清理范围…”是另一项只读动作，随后仍须独立勾选及管理登录才删除；不以保存设置作为删除授权。
- 首适配复用 19.28 的已验收旧环境清理：原操作可为保留旧环境的并行或小占用，登记的当前模式须为直通，原候选仍为当前且已人工验收。固定 `environment_restore_old_cleanup_current` 为日常只读，只接受完整 AgentRef，从已有登记查找确切原操作，再执行同一范围审阅；不初始化记录，不按名称扫描或猜操作。原 review/delete 方法同时允许此情形，仍绑定原操作冻结模式；新增 policy_mode 表示当前设置，不改写历史模式。正常保留旧环境模式不因此获得删除入口，设置变化会使旧摘要失效。
- GUI 设置入口不依赖本机曾执行恢复或保存过索引；可从服务器返回的完整身份、原操作及快照派生本机脱敏查询路由。该路由不是确认；只有当前人类进程的新审阅对象可执行。入口同样按未广告的正式执行能力显示。请求丢失只查询原操作，清理期间模式保存被阻止；结束后仍是直通，当前程序、数据、人工验收及备份保留。
- 本节首适配不冒充已完成所有槽位处置：明确退回/取消后的非活动候选清理、直通受管实例的后续恢复及正常再次 A/B 交替继续接入 19.7；不要求用户先手动清空目录，不回落到旧未受管交换器。

### 19.30 受管环境作为后续恢复来源（2026-11-15，接入中）

- 后续恢复不重新接管原生服务，不把固定 A1/B1 当作当前环境。只读预览从严格活动绑定核实当前修订、私有程序、服务定义/实际运行及实际数据根，按登记 Active 派生下一非活动修订；保留当前已运行来源，准备不停止它。首个来源适配继续要求目标备份记录与现有私有程序的来源/版本/依赖/布局精确相容，不用原宿主安装代替当前受管程序。只复制程序清单材料，不从活跃数据补齐快照。
- 后续准备借用当前独立程序作为回退来源，不创建或认领第二份“原程序”。私有准备记录增加可选的受管来源引用，固定原操作、原修订、材料、定义及观察；旧原生记录及摘要保持兼容。源程序标准 tar 在本次独立工作区构建，逐文件核对已登记哈希/大小/权限和链接，构建前后复核私有程序；不安装、不执行解释器、不修改原目录。新候选及探针各自独立，仍使用原数据子日志和正式父事务。
- 新正式父记录尚未建立时，后续准备的持久来源锚定只证明原环境仍归属明确历史操作；不授予启停、交换、删除或人工验收。仅在新操作 Building 或取消且没有正式父记录时，按此锚定读取明确历史绑定；现有损坏或带意图的父记录不能回落。读取继续核对真实根身份、程序和登记，正常对话写入不改变来源身份。取消后不丢失当前运行和历史验收，清理本次材料不能删除借用的原程序；历史记录的旧格式永久可读。
- 锚定绑定父记录的固定计划/定义/数据清单摘要，不把旧清理进度当作来源身份；历史取消记录同样必须有明确来源才能越过缺失的正式父记录。新父事务绑定该来源摘要，并继承原生入口已停止/禁用的约束；下一轮不因 NativeOriginal 为空丢失该约束。显式清理已取消且未切换候选后，仅把其确切非活动修订标为空，保留当前原环境及确认，不重置槽位修订。
- 此模块先接非活动槽已按独立动作清理的后续准备，接着实现正常计划内的保留旧槽/取消材料复用；这只是实现顺序，不缩小 19.7。最终普通恢复不得要求用户预先清空非活动槽，正式执行能力仍须在正常复用/失败退回/取消与后续交替闭环后广告。直通受管后续恢复、其他来源及分身/迁移范围不变。

### 19.31 非活动环境与准备材料的计划内复用（2026-11-15，接入中）

- 后续并行恢复的普通预览列出将复用的确切旧槽及固定材料种类、内容根和逻辑量，说明旧内容会覆盖、当前环境继续运行、备份保留。仍在同一准备确认页完成，不要求先去手动删除。只复用明确已停止/取消/退回、归属可核实的非活动材料；未知或正在使用的资源停止，不凭目录名认领。
- 范围从已有登记、准备创建声明、数据日志和明确历史关系派生。已验收旧环境含原 prev、确切旧程序和已结束临时材料；取消/退回含该候选实际保留的数据位置、独立程序及本次材料。此前中断复用的剩余资源继续携带原归属、删除意图和停止证明，不丢弃已删除证据或删除后来重建的目录。保护当前程序/数据、其他登记（含停用/别名）、控制器及备份；不读取 Agent 文件内容计算预览，仅读取目录元数据和公开备份描述。
- 独立归档当前程序并重新核实来源后，先保存新操作的固定复用清单和来源锚定，再预留下一修订，随后逐项删除非活动资源；每次删除前持久保存意图、复核目录设备/inode/路径链/注册冲突和当前来源，使用 Go os.RemoveAll，完成后同步父目录并记进度。当前环境不停止、不释放。最后才建立新的正式数据暂存与候选。
- 中断只读重连不续删。用户可取消本次准备；独立材料清理须显示本次已开始复用的剩余旧材料并另行确认，或下一次普通计划重新审阅复用。原资源的进度和已停止探针证据持久携带，删除跟踪文件不被当作进程已停。完成的旧历史只更新清理事实，不改快照、身份、当前对话验收或原操作冻结模式。
- 新操作取代旧操作后，旧恢复记录展示历史结果与已被后续操作取代，不把缺失已复用目录当作当前恢复失败，不提供旧操作的切换/确认/清理动作。历史确认与当前确认分开。首次原生取消后的复用、受管直通及其余 19.7 仍按完整目标继续接入，不因首个受管适配提前广告完整能力。
