# agent-kit：让 Agent 认识自己的保护

> 2026-11-19 收口（Valise Recovery v0.1 定稿）：受管 Agent 技能收口为极简二件套（`valise-onboard` + `valise-backup`），替代原四件套（`valise-enroll`、`valise-upkeep`、`valise-handoff`、`valise-checkup`）；`tools/list` 仅暴露 4 个单一职责工具（`valise_status`、`valise_seal`、`valise_onboard_context`、`valise_propose_dossier`），彻底移除首次调用隐式 `autoSeal`，旧工具名仅在 `Call()` 中静默向后兼容；`valise_seal` 在已确认保护计划时按计划档位（默认 `full`）封存；彻底取消 `valise_declare` 自报流程，审批状态只看 profile 确定性的 `approval_check`；记忆管理与跨会话交接整体延后至独立套件 ValiseMemo / v0.2+。界面使用“Agent 保护技能”。边界由代码执行，不靠技能文本。
>
> - **受管方**：被 Valise 管理的 harness / agent。技能与服务器上的 porter（`valise-porter mcp` 的受限工具）对接。本文第 1–8 节即受管方，**现有 agent-kit 全部属于这一侧**。
> - **管理方**：代表用户操作 Valise、管理服务器与 harness 的 agent。技能与用户侧的 Valise 客户端对接。**项目目前没有管理方技能**，见第 9 节，属于规划。
>
> 两侧可以组成官方套装，但不是同一套权限：受管方不因安装技能获得任何管理方权限；管理方不得借技能绕过客户端的确认、安装安全检查或凭据限制。边界由代码执行，不靠技能文本。

## 1. 定位与原则（受管方）

agent-kit 是可选的档案报备与操作前备份辅助，**不是灾难恢复的依赖**。用户接入后可明确选择“安装 Agent 保护技能”，再对自己的 agent 说一句：

> 用 valise-onboard 验收连接并报备你的目录布局。

此后，agent 在初次报备、查备份状态、执行高风险自我修改前可以按最小边界使用 Valise MCP 工具。

原则：

1. **执行交给代码，LLM 只做档案补充与沟通**：服务器探测、扫描、SQLite 魔数识别、备份校验、恢复与权限边界全部由 Go 代码执行；agent 不写档案、不执行恢复、不改动自己的常驻指令文件（如 `AGENTS.md`）。
2. **MCP 工具只开放最小权限**：不提供复活、删除、读取凭据或写入任意文件的能力；agent 禁止运行 shell 或读写未知目录去探查文件（零额外权限弹窗）。
3. **不越俎代庖**：高风险自修改前留底由用户发起或高风险条件明确触发；所有 Dossier 补充与排除建议均需用户在 Valise 桌面端或 CLI 确认后才落盘。
4. **不背书**：只做备份保护与布局覆盖排查，不替用户挑选或修改记忆框架。

## 2. 安装

客户端中的“安装 Agent 保护技能”会做两件事，并在执行前明确告诉用户：

- 把 `agent-kit/skills/` 下的二件套技能（`valise-onboard`、`valise-backup`）复制到 profile 中 `agent_kit.skills_dir` 指定的位置；同时安全检查并清理旧四件套残留目录（`valise-enroll`、`valise-upkeep`、`valise-handoff`、`valise-checkup`）：与历史官方发布内容一致的 `SKILL.md` 及 `.valise-bak-*` 自动清理并删除空目录，用户自行修改过的文件原样保留并列出。
- 接入 MCP Server（启动命令为 `valise-porter mcp --instance <id>`）：若 profile 声明了 `agent_kit.mcp_add_argv`，在安装前展开 `${PORTER}` 与 `${INSTANCE}` 并完整预览命令，默认在用户确认后经独立管理连接执行并记录审计（可选同时重启服务并复核健康）；未声明命令或用户不愿自动执行时，在折叠区提供可直接复制的配置片段、CLI 命令或文档指引。Valise 不擅自解析或静默改写 harness 配置文件。

技能目录为空（profile 未提供）的 harness，暂不支持安装技能，仍可单独接入 MCP。

## 3. MCP 工具（`valise-porter mcp`，stdio 模式）

一个 MCP 进程只服务于启动时 `--instance` 参数指定的那个实例。`tools/list` **仅暴露以下 4 个单一职责工具**以压低上下文开销（旧工具名 `valise_propose_enrollment`、`valise_checkup`、`valise_handoff_begin`、`valise_handoff_finish`、`valise_declare` 从 `tools/list` 隐藏，仅在 v0.1 `Call()` 中静默向后兼容，其中 `valise_propose_enrollment` 静默转发为 `kind: "classify"`）：

| 工具 | 输入 | 行为 | 限制 |
| :--- | :--- | :--- | :--- |
| `valise_status` | 无（`{}`） | 纯只读快查，**不做全盘扫描，不触发备份**：返回 `instance`、`protection_plan`（含是否已采用 `adopted`、定时、档位、周期、保留与复制目标）、精简 `last_seal`（`snapshot_id`、`created_at`、`tier`、`kind`）、由代码写成的一句话 `protection_summary`（agent 原样转述）、`pending_proposals`（计数）及必要 `notes`（`< 150 tokens`） | 毫秒级纯只读；写入不含路径与正文的轻量 `agent-mcp` 审计时间戳 |
| `valise_seal` | `{ "reason"?: string }` | 写入一条滚动周期备份（`kind: auto` 存入当前日期周期，参与周期保留策略，用户可在客户端按需晋升为永久存档点）。**已确认保护计划的实例按计划档位（默认 `full`）封存**；未采用保护计划的旧实例保守沿用 `soul` 档并在结果中附带 `plan_warning`。`reason` 可选（缺省记 `"agent mcp backup"`）写入审计记录 | 最短间隔 10 分钟；命中 `rate_limited` 时报错同时返回 `last_seal_id` 与 `last_seal_at` 供同一修改复用，不循环重试 |
| `valise_onboard_context` | 无（`{}`） | 纯只读收集档案上下文：**不触发任何备份**。返回 5 块信息 + `protection_summary` 与精简 `last_seal`：<br>1. `instance`（`instance_id`、`profile_id`、`profile_version`、`roots`）；<br>2. `protection_plan`；<br>3. `effective_rules`（Profile `paths` / `profile_body_paths` / `profile_sqlite` + Dossier `local_overrides` / `local_sqlite` / `local_roots` / `external_refs` / `tool_deps`）；<br>4. `layout_facts`（5 项：`unclassified_dirs`、`largest_dirs`〔仅统计会进入 `full` 常规备份的大目录，跳过 `body`/`cache`/`ignore`〕、`external_symlinks`、`churn_files`、**`untracked_sqlite`**）；<br>5. `pending_proposals`（当前待审批队列，便于去重） | 只返回路径聚合、字节计数与前 16 字节 SQLite 魔数识别线索（`untracked_sqlite`），**绝不读取任何表数据或文件内容**；写入轻量 `agent-mcp` 审计时间戳 |
| `valise_propose_dossier` | `{ "items": [ ... ] }` | 统一校验并入队四类结构化提案：<br>• `classify`：`{ kind, root, glob (或 path), category, reason }`（`category` 仅限 `identity \| memory \| workspace \| state \| skills \| config \| secrets \| cache \| ignore`；硬拒绝用非 `secrets` 覆盖 Profile `secrets`；改入 `cache`/`ignore` 标为降级并要求写明可重建理由）；<br>• `sqlite`：`{ kind, root, glob, secret_tables?, reason }`（硬拒绝削弱 Profile `secret_tables`）；<br>• `external_ref`：`{ kind, path, ref_kind: "external_dir" \| "symlink_target", suggested_category?, reason }`（校验绝对路径、非系统/根/家目录且不与任何既有 `roots` 重叠；入队时附带受限目录体积估算 `estimated_files` / `estimated_bytes`）；<br>• `tool_dep`：`{ kind, name, purpose?, reason }`（校验合法命令名；供迁移前只读检查目标机环境缺口，Valise 不自动安装也不背书任意第三方 CLI 工具） | 单批调用上限 ≤ 50 条（技能约束最多挑选 12 条高价值项）；禁止直写 Dossier，全部需人类在桌面「健康 › 归档建议」或 CLI `valise proposals` 确认后才生效 |

### 3.1 由 agent 补充档案边界（Dossier Proposals）

1. **代码提供线索**：`valise_onboard_context` 在不触碰文件内容的前提下提供 `unclassified_dirs`、`largest_dirs`（跳过已排除的 `body`/`cache`/`ignore`，仅统计会进入 `full` 常规备份的 `state` / `workspace` 大目录与插件垃圾）、`external_symlinks`、`churn_files` 与 `untracked_sqlite`（只检查前 16 字节 `"SQLite format 3\x00"`，排除已纳管库及 `body`/`cache`/`ignore`）。
2. **agent 筛选高价值提案**：`valise-onboard` 对照 `effective_rules` 和 `pending_proposals` 去重，单次只提最多 12 条高价值项：对关键的未分类人格/规则/核心配置/记忆小文件（如 `persona.md`）应主动提议归入 `identity`/`memory`/`config`（虽在 `full` 档会被兜底，但轻量 `soul` 备份与创建分身会丢弃 `unclassified`），并对 `roots` 外的个人资产/配置目录提交 `external_ref`（可带 `suggested_category`）、对依赖的伴生命令行工具提交 `tool_dep`，同时不对零散运行期噪声刷屏。
3. **用户拍板**：所有提案经由桌面「健康 › 归档建议」或 `valise proposals` 审批后，才分别写入实例档案的 `local_overrides`、`local_sqlite`、`external_refs`、`local_roots` 或 `tool_deps`；对于 `external_ref`，用户可二选一决定“仅登记提醒”或“纳入备份（附加目录）”（详见 8.2 节）。
4. **兜底与回流**：`raw` 档不受分类或降级规则影响。对同类 Harness 普遍成立的新模式仍通过 PR 进入官方 profile，不收集遥测。

## 4. 两个极简技能

每个技能只保留一个紧凑的 `SKILL.md`（正文英文、用用户语言回复；触发词兼容中英文）。两者均支持延迟工具发现（如 `tool_search` 先查带前缀的真实工具 ID），且明确要求找不到当前 v0.1 工具时立即停止并提示用户升级服务器 `valise-porter` 与重启网关、禁止回退调用旧工具。旧四件套（`valise-enroll`、`valise-upkeep`、`valise-handoff`、`valise-checkup`）已从发版套件移除。

### 4.1 `valise-onboard`（低频档案报备，≤ 34 行）

触发：刚安装完技能、新装插件或新增自定义记忆/数据目录、或用户要求检查备份覆盖与体积膨胀。**严格只报备，绝不触发备份**，且绝不执行 shell、读写磁盘文件或改写 `AGENTS.md` 等常驻指令，不在 `reason` 与回复中泄漏任何凭据或正文片段，失败不循环重试：

1. 调用 `valise_onboard_context`（仅此 1 次）。
2. 与 `effective_rules` / `pending_proposals` 去重，仅依据 `layout_facts` 和已知布局挑选至多 **12 条**高价值提案（大体积 ≥5 MiB 或明显可重建的插件缓存/依赖/日志降级 `cache`/`ignore`；未纳管 SQLite 提交 `sqlite`；新凭据、未分类的关键人格/规则/核心配置/记忆小文件〔如 `persona.md` 归入 `identity`，确保 `soul` 档备份与分身不丢失〕或成块核心业务/记忆目录提交 `classify`；`roots` 外个人资产/配置目录或外部软链提交 `external_ref`〔可带 `suggested_category`〕；依赖的伴生 CLI 工具提交 `tool_dep`〔带 `name` 与 `purpose`〕）。
3. 有高价值项则合并调 **1 次** `valise_propose_dossier`（无项则 0 次）。
4. 简洁汇报 3–4 点：保护计划/定时状态 + 原样转述 `protection_summary`；已提交的提案并引导去桌面「健康 › 归档建议」确认（含可选择仅登记提醒或纳入备份的 `external_ref` 与伴生工具 `tool_dep`，或说明无需补充）；列出 `roots` 外覆盖缺口与关键工具依赖。

### 4.2 `valise-backup`（日常查状态与高风险前留底，≤ 23 行）

触发：用户询问备份状态/要求先备份，或在 agent 进行高风险自我修改之前；**明文禁止**在普通聊天、只读查询、记入单条日常记忆、微调单个普通小文件时触发：

1. **查状态**：调用 `valise_status`（1 次），用 2 句短话交代定时保护开关、活跃档位、最近备份时间，并**原样转述** `protection_summary`。
2. **高风险修改前留底**：仅当用户要求备份，或即将改动核心配置（如 `openclaw.json`）、装卸技能/插件、大幅重写/清理人格/规则/记忆、批量改删工作区文件、升级 Harness 前，调用 `valise_seal`（可选短 `reason`）。
3. 汇报生成的滚动快照 ID（`kind: auto`）；若返回 `plan_warning`（未采用保护计划的旧 `soul` 实例），温和提醒去桌面端确认保护计划以覆盖配置与工作区；命中 `rate_limited` 时直接复用返回中的 `last_seal_id` 与时间并继续；其余错误立刻停下用一句话说明并询问用户，绝不循环重试。

## 5. Finding 的解读要点

| finding | 触发依据（由代码计算，阈值写在 profile 或全局配置中） | 建议方向 |
| :--- | :--- | :--- |
| `long_session` | 单个会话持续的天数或 token 量超过阈值 | 交接（4.3） |
| `cost_growth` | 每日 token 增长率持续上升 | 交接；检查接入社交平台的渠道是否在把大量噪声送进上下文，例如考虑只响应 @ 或私聊（以 harness 的原生配置为准） |
| `memory_bloat` | 记忆文件的行数超过阈值 | 提醒用户；仅在用户同意时，做一次久远记忆的抽象化整理 |
| `session_pileup` | 会话的总体积很大，并且冷会话占比很高 | 建议在 harness 中启用会话冷存档与索引（原生方案优先）。同时说明：这样做也能让封存更快、更省空间 |
| `no_memory_mechanism` | 没有检测到任何记忆机制 | 解释记忆机制的作用，按第 6 节的标准帮用户挑选 |

## 6. 选择标准（用于代替点名推荐）

帮用户挑选记忆类或会话管理类插件时，引导用户按以下标准判断：

1. **数据以人类可读的文本形式保存在本地**（Markdown 或 JSONL），用户可以自己查看和修改。
2. **不依赖外部云服务或数据库进程**，或者明确说明了依赖了什么。
3. **区分长期记忆与短期记忆**，并且有某种淡出或归档机制，不会无限增长。
4. **旧会话可以冷存档，并且有索引**，而不是简单删除。
5. **会过滤凭据**，不会把 API Key 写进记忆。
6. **仍在活跃维护**，有明确的许可证，与当前的 harness 版本兼容。
7. **修改或删除记忆之前有备份或回收站机制。**

## 7. 伴生插件（独立仓库，后期）

Valise 团队可以在独立的仓库中提供长期记忆、短期记忆、冷存档 + 索引、程序性记忆等小插件，规则如下：

- Valise 的核心功能在没有任何伴生插件时必须完整可用。
- 伴生插件和其他第三方方案在推荐时**地位相同**，同样只能通过第 6 节的标准被选择，不得在 UI 中获得特殊位置。
- 冷存档插件与 Valise 之间只约定一份索引格式。Valise 能读到这份索引就利用它（例如在体检中显示哪些会话已经归档），读不到就降级处理，不影响任何功能。

## 8. 实现细节（M6 确定）

### 8.1 MCP 服务端

- `valise-porter mcp --instance <id>`：stdio 上的 JSON-RPC 2.0，一行一条消息。只实现 `initialize`、`notifications/initialized`、`ping`、`tools/list`、`tools/call`，不引入第三方 MCP 库。协议版本：客户端请求的版本在支持列表（`2025-06-18`、`2025-03-26`、`2024-11-05`）内就照用，否则回复最新的。
- `tools/list` 仅暴露 4 个工具（`valise_status`、`valise_seal`、`valise_onboard_context`、`valise_propose_dossier`）；`Call()` 对隐藏的旧版工具名保留 v0.1 静默兼容，并移除连接首次调用自动触发 `autoSeal` 的机制。
- 工具结果同时放在 `structuredContent`（JSON 对象）和一段 `text` 中；用户错误（频率限制、校验失败等）以 `isError: true` 返回"原因 + 建议"，不中断会话。
- 审计记录的发起者为 `agent-mcp`；`valise_seal`（含 `rate_limited` 与实际封存）、`valise_status`、`valise_onboard_context`、`valise_propose_dossier` 均写入不含任何内容/敏感路径的轻量审计时间戳，保证桌面端准确统计「最近 MCP 调用时间」。频率限制保存在 `~/.local/state/valise/mcp/<实例>.json`。
- MCP 进程以实例在服务器上的 profile 副本与实例档案为准，不需要客户端在线。

### 8.2 归类与档案扩展提议

- `valise_propose_dossier` 的四类提案保存在 `~/.config/valise/instances/<实例>.proposals.json`（0600），每条含 id、kind（`classify` / `sqlite` / `external_ref` / `tool_dep`）、规则或库/外联/工具定义、理由与时间：
  - `classify`（含旧 `valise_propose_enrollment` 静默转发）：`category` 只允许 `identity | memory | workspace | state | skills | config | secrets | cache | ignore`，`root` 允许基础 `state`、profile 声明的 root 以及已纳入档案的 `local_roots`。路径经代码校验存在、相对 `root`、不含 `..` 且不经符号链接逃逸；命中既有 `secrets` 规则的非 `secrets` 提议直接拒绝；将文件改为 `cache` 或 `ignore`（离开 `soul` 档）标为"降级"并强制要求原因；命中当前 `unclassified` 移入 `soul` 档时标注未分类计数提醒用户不含凭据。
  - `sqlite`：校验 `root` 与 `glob` 在实例 `roots` 内，并校验不削弱同匹配 Profile `consistency.sqlite[].secret_tables`。
  - `external_ref`：校验 `ref_kind` 为 `external_dir` 或 `symlink_target`，可选附带 `suggested_category`（仅限 `identity | memory | workspace | state | skills | config | secrets`），并在入队时以受限遍历（最多 5,000 个普通文件或 2 秒截止，跳过符号链接与非普通文件）估算 `estimated_files` 与 `estimated_bytes`。路径必须为规范化绝对路径，禁止 `/`、`$HOME`、系统目录（`/etc`、`/usr`、`/var`、`/bin`、`/sbin`、`/lib`、`/lib64`、`/boot`、`/proc`、`/sys`、`/dev`、`/root`、`/tmp`、`/run` 及其软链解析目标），且不得与任何既有 `roots` 重叠（含相等、祖先或子目录关系，词法与 `EvalSymlinks` 双重校验）。
  - `tool_dep`：携带 `name`（合法命令名 `^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`，不含 `/` 或空格）、可选 `purpose` 与 `reason`。审批后写入实例档案的 `tool_deps`，迁移计划阶段经 `revive_inspect.extra_tools` 在目标机登录 `PATH` 与 `~/.local/bin` 上执行纯只读存在性检查并展示环境缺口清单；Valise **绝不自动安装**，也不背书任意第三方 CLI 工具。
- 客户端审批（桌面「健康 › 归档建议」与 CLI `valise proposals <服务器> <实例>`）：列出全部待确认提议，降级提议单独成组且仅可通过 `--accept-demotion` / 桌面逐条确认弹窗接受。对于 `external_ref`，人类可二选一处置：
  1. **仅登记提醒**（普通 `accept`）：写入 `external_refs`，作为覆盖缺口保留提示，不扩封；
  2. **纳入备份（附加目录）**（`promote_external_roots`）：校验该路径当前在磁盘上作为真实目录（或解析至合法非禁用目录的软链）存在、不与任何既有 `roots` 重叠，且类别仅限 `identity | memory | workspace | state | skills | config | secrets`（严禁 `cache`、`ignore`、`body`）；验证或自动派生唯一 `root_id`（`ext-<slug>`，满足 `^[a-z0-9]+(-[a-z0-9]+)*$`、长度 ≤ 48 且非 `state`/`body_patch`），写入 `local_roots` 与 `roots[id]`，并移除同路径的 `external_refs` 提醒。附加目录的默认类别在分类优先级中位于 profile `paths` 之后（`secrets` > `local_overrides` > `body.paths` > `paths` > `local_roots[root]`），从下一次封存起按类别随对应档位封存。
- **直接档案管理（`dossier_update`）**：porter 经能力 `dossier-roots/1` 暴露 `dossier_update`（`TierOwner`）RPC，允许人类在客户端直接增删 `local_roots`（仅允许移除 `local_roots` 登记的附加目录，严禁移除 `state` 或 profile 声明的 root；新增适用与上文完全相同的安全路径与类别校验）与 `tool_deps`。

### 8.3 旧交接工具兼容（v0.1 静默保留，功能归 ValiseMemo）

- `valise_handoff_begin` / `valise_handoff_finish` 与 `valise_checkup` / `valise_declare` 已从新安装技能与 `tools/list` 下线，仅在 `Call()` 中保持 v0.1 静默兼容以便已接旧技能的存量代理平稳过渡。

### 8.4 体检

- 会话来源由 profile 的 `checkup.sessions` 声明：`format: jsonl` 时每个文件是一个会话，每行一个事件，`usage_fields` 为要求和的 token 字段（点分路径），`time_field` 为事件时间字段（RFC 3339 或 Unix 秒/毫秒）；`format: sqlite` 时由 `sqlite` 子项声明表名、会话列、时间列与单位、token 列、大小列（见 PROFILES.md）。没有 token 字段时按字节数除以 4 估算，并标注为"估算"。
- 默认阈值（全局，profile 可在 `checkup.thresholds` 中覆盖）：

| finding | 默认条件 |
| :--- | :--- |
| `long_session` | 单个会话持续 ≥ 14 天，或 token ≥ 500,000 |
| `cost_growth` | 最近 3 个自然日（UTC）的 token 量逐日上升，且最后一天 ≥ 第一天的 1.5 倍、≥ 50,000 |
| `memory_bloat` | 记忆文件行数超过 profile 的 `warn_lines` |
| `session_pileup` | 会话总体积 ≥ 100 MiB，且最后活动在 90 天前的会话占体积 ≥ 50% |
| `no_memory_mechanism` | profile 声明的记忆机制标记一个都不存在（没有声明标记时也视为不存在） |

- 体检只读：SQLite 以只读方式打开，不修改任何文件。

### 8.6 体检的呈现与处理建议（2026-10-31 用户决定）

- 每个发现除了代码给出的数字，还要附**处理建议**，分三层，按顺序展示：
  1. **harness 自己的做法**：profile 的 `checkup.remedies.<finding>`（文字，只展示，Valise 不执行）；没有时用 Valise 内置的通用说明（例如长会话：开新会话前先交接）。
  2. **交给 agent**：涉及目录布局与备份体积优化时，可复制引导词请 Agent 用 `valise-onboard` 报备目录与排除可重建缓存（旧 `valise-checkup` 技能已随 v0.1 收口移除，深度记忆与会话整理交由后续 ValiseMemo / v0.2+ 承接）。
  3. **记忆整理技能**：说明 Valise 计划提供自己的 `valise-memory` 技能（整理、压缩、归档记忆），目前处于规划中。只写文字，不渲染按钮，不点名任何第三方方案。
- 归类提议在界面上称为"归档建议"，放在体检页中（UI.md 9.4），不再单独成页。

### 8.4.1 Valise Kit 的三级（2026-11-02 用户决定）

原“加强套装”并入 Valise Kit。每一级只能安装签名资料包中列出、审核过的条目（来源锁定版本与 SHA-256，执行的命令全部展示），不是插件市场；只在用户选择后安装，逐项卸载。

2026-11-05 产品重整：新工具箱按“接入 AI 管理、让受管 Agent 了解保护、辅助传输、记忆与会话管理”等用途组织，基础/增强/专家保留为底层风险与安装方式，不要求用户先理解三级套装。每项说明装在哪里、影响谁、不装会怎样、权限与卸载办法。规划中的技能、pending 工具不能显示为可安装，工具箱与记忆扩展不阻塞默认保护和恢复闭环。

| 级别 | 内容 | 安装方式 | 访问级别 |
| :--- | :--- | :--- | :--- |
| 基础 | Agent 保护技能二件套（`valise-onboard`、`valise-backup`）与 MCP 接入（8.5 节） | 默认提供，在管理页一键安装 | 主人 |
| 增强 | 官方记忆技能（`valise-memory`，规划中）、官方辅助技能、不需要 root 的工具（如 rclone） | 逐项选择、逐项安装 | 主人 |
| 专家 | harness 插件、需要 root 的工具（如经系统包管理器装 rsync） | 逐项安装，先说明风险、用户确认知晓 | 开发者模式 › 高级模式，并受 INSTALL.md 4.2 的前提约束 |

每个工具一行展示：这台服务器上有没有、用于哪些功能、不装会怎样（如"rsync 没有时改用 sftp"）、安装方式与完整命令；也可以"打开终端窗口"由用户自己安装。

**初版清单**（2026-11-02 用户确认，"不求多求精"）：清单在仓库的 `kit/tools.yaml`，打进签名资料包时名为 `kit-tools.yaml`（资料包是扁平结构，`PACK.json` 以 `kit_tools` 记录它的 SHA-256），与 profile 分开；每项写明 `id`、级别、名称、用途、不装会怎样、检测方式（argv）、安装方式（增强级：各架构的下载地址与 SHA-256，装到 `~/.local/bin`；专家级：各发行版的包名或固定 argv）与卸载方式。

| id | 级别 | 用途 | 不装会怎样 | 安装方式 |
| :--- | :--- | :--- | :--- | :--- |
| `rclone` | 增强 | 云盘（M13） | 不能同步到网盘 | 官方二进制，锁定版本 |
| `rsync` | 专家 | 同步、仓库同步更快 | 改用 sftp，速度慢一些 | 系统包管理器（apt / dnf），需要 sudo |
| `linger` | 专家 | 用户登出或服务器重启后，定时封存、自动同步与以 systemd 用户服务运行的 harness 继续运行 | 这些会在登出或重启后停下，不报错 | `loginctl enable-linger <用户>`，需要 sudo；卸载为 `disable-linger` |

不放进 Kit：zstd 与 age（porter 自带，独立恢复在用户电脑上进行）、harness 的运行时（由 profile 的 `requires` 与高级模式负责）、与 Valise 功能无关的运维工具。

**schema 与安装流程**（M12，2026-11-02）：

```yaml
schema: valise-kit-tools/1
tools:
  - id: rclone                # 小写字母、数字、连字符
    tier: enhanced            # enhanced | expert
    name: rclone
    purpose: 同步到网盘
    without: 不能同步到网盘
    check: { binary: rclone } # 或 { linger: true }
    binary:                   # 只用于增强级：官方二进制，装到 ~/.local/bin/<check.binary>
      version: "x.y.z"
      archives:
        - { arch: amd64, url: "https://…", sha256: "…", member: "压缩包内的路径" }
    install:                  # 只用于专家级：按发行版（os-release ID）的固定 argv，只能引用 ${HOME}、${USER}
      - { os_id: [debian, ubuntu], argv: [sudo, apt-get, install, -y, rsync] }
    uninstall:                # 可选；增强级的二进制由 porter 删除，不需要写
      - { os_id: [debian, ubuntu], argv: [sudo, loginctl, disable-linger, "${USER}"] }
    verify: pending           # 在靶机上核实前保持 pending
```

- **检测**：porter 的 `install_check` 按登录 PATH 与 `~/.local/bin` 查找 `check.binary`，并报告 linger 是否开启；走日常钥匙。
- **增强级安装**（主人）：客户端从签名清单里的地址下载压缩包，核对 SHA-256，取出 `member`，经一次性许可（`kit_tool_begin` 返回，绑定工具名与文件的 SHA-256）由 `valise-porter tool-put <名称>` 写到 `~/.local/bin/<名称>`（先写临时文件、核对 SHA-256、0755、改名）。目标已存在且不是 Valise 装的，拒绝覆盖。porter 在 `~/.local/state/valise/kit-tools.json` 记下它装的文件及其 SHA-256。
- **增强级卸载**（主人）：`kit_tool_remove` 只删除记录中、且内容未被改动的文件。
- **专家级**：需要开发者模式下的高级模式与 INSTALL.md 4.2 的前提；命令先过第 7 节检查，展示后确认，在新的管理连接的交互终端里执行（sudo 的密码由用户在终端里输入）。没有为这台服务器发行版写命令时，说明原因并给出"打开服务器终端自己装"。rsync 不提供卸载（系统包，可能被别的程序使用）。
- 清单中 `verify: pending` 或缺少 SHA-256 的条目只展示，不能安装。
- **云盘与本机下载**（N3，2026-11-07 用户决定，ARCHITECTURE.md 7.3）：rclone 条目另有 `clouds`（每项 `id`、`name`、`rclone_type`、`params`、`access: folder|account`、`access_note`、可选 `loopback: false`、可选 `ask_app: true` 与 `app_help`（rclone 自建应用说明的 https 地址）、`verify`；`params` 不能出现 token、client_secret 等授权内容）与 `client_archives`（`os: darwin|linux`、`arch`、`url`、`sha256`、`member`），后者只用于“这台电脑登录”时下载到客户端配置目录，不安装到服务器。


### 8.5 安装 Agent 保护技能

- **安装流程**（桌面「管理与设置 › Agent 保护技能」或 `valise kit install <服务器> <实例>`）：
  1. 先列出将要写入 `agent_kit.skills_dir` 的二件套技能文件（`valise-onboard`、`valise-backup`；已存在且内容不同时先备份为 `SKILL.md.valise-bak-<时间>`），并提示会按历史发版哈希自动清理未修改的旧四件套残留（用户改过的保留并列出）。
  2. 若 profile 声明了 `agent_kit.mcp_add_argv`，同时展开 `${PORTER}`、`${INSTANCE}` 预览实际命令，**默认勾选经用户确认后通过独立管理连接执行**（`admin` 权限，写入审计记录，可取消勾选仅安装技能文件）；若实例托管了后台服务，同时提供可选的「重启服务并复核健康」步骤。
  3. **分项诚实汇报结果**：分别展示「技能文件写入结果」「MCP 注册命令退出码」与「服务重启及健康复核结果」，绝不把命令退出码 0 合并冒充服务健康或 MCP 已就绪。
  4. **折叠区手动兜底**：无论是否执行过自动注册，展开后的 `mcp_add_argv`、`mcp_config_snippet`、`mcp_config_doc` 与 3 句可直接复制对 Agent 说的话（首次报备布局、查询备份状态、高风险修改前留底）均保留在折叠面板中，随时可直接查看复制，无需解锁 Valise 口令。
- **保护计划引导**：接入新实例后默认引导创建保护计划（默认 `full` 档位，定时备份按需开关）；若在 Agent 保护技能页检测到当前实例尚未启用保护计划，页面明确提示并给出前往设置的入口（避免 `valise_seal` 回退到旧 `soul` 档）。
- `format: jsonl` 的会话文件以 `.zst` 或 `.zstd` 结尾时先解压；大小按磁盘上的字节计，token 估算按解压后的字节计。
- 技能正文用英文书写，并要求 agent 用用户的语言与用户交流；触发说明同时列出中英文说法。
- `valise kit status <服务器> <实例>`（M7a）：逐个列出当前技能在技能目录中的状态（与本客户端发布的版本一致 / 已被修改 / 未安装）以及 Valise 留下的备份数；MCP 方面只报告最近一次 agent 经 MCP 调用的时间（来自审计记录）。Valise 不读取 harness 的配置，因此不声称"MCP 已配置"，也不展示 agent 自报安全状态。
- **卸载流程**（`valise kit uninstall <服务器> <实例> [--force]`）：先列出将删除的文件，并在 profile 声明了 `agent_kit.mcp_remove_argv` 时展开 `${INSTANCE}` 预览撤除命令（默认勾选经用户确认后通过独立管理连接执行并记录审计，可选同时重启服务并复核健康）。用户确认后，porter 在 Go 代码中删除内容与当前发布版本一致的 `SKILL.md`（含匹配历史哈希的旧四件套残留）、Valise 写的 `SKILL.md.valise-bak-*` 备份，以及因此变空的技能目录；被修改过的 `SKILL.md` 保留并列出，加 `--force` 才删除。结果同样分别展示技能文件删除、MCP 撤除退出码与服务复核状态，并在折叠区保留手动撤除命令或配置片段清理指引。MCP 状态、审计记录与备份库不受影响。

## 9. 管理方接口与技能（N5a，规划未实现）

- **用途**：让用户委托的另一个 AI 经用户侧 Valise 客户端查询 Agent、保护与旅程，准备计划，发起允许且已授权的任务，并取得进度、部分结果与下一步。未知 harness 的 profile 辅助属于这一侧的后续扩展，不使草稿自动成为可执行资料。
- **架构**：管理方 MCP 与结构化 CLI 都是共享 Go 服务层的薄适配，不模拟终端，不解析人类输出，不给 porter 增加监听端口。跨进程请求采用 [已定稿 N1 契约](SERVICE_CONTRACTS.md) 第 8.1 节：MCP 只向客户端私有待办箱提交允许的类型化请求；用户打开 GUI/TUI 后重新准备冻结计划、展示并确认，由该人类进程执行，MCP 查询脱敏结果。业务权限、安装检查和保护规则不变，不新增常驻授权服务。
- **硬边界**：
  - 接口不能取得 Valise 口令、管理口令、服务器密码、加密身份或 SSH 私钥，也不能代理用户输入原生界面的模型凭据、读取机密输出或开启高级模式。
  - AI 可以准备允许的计划和发起请求，不能替用户确认。没有人类会话或缺少权限/确认时返回明确等待状态；用户在 GUI/TUI 人类进程内完成确认与执行。只在该进程内复用适用的有效解锁窗口，不要求每个只读步骤重输口令，窗口不代替计划确认。确认绑定计划摘要、目标及阶段，变化或过期失效；不跨进程共享解锁身份、ssh-agent 凭据或通用确认票据，不把磁盘 approved 字段当成授权。执行者中断先核实，不让 MCP 接替重跑。
  - 当前不开放 MCP 恢复、删除、读取凭据或写任意文件的执行接口。未来管理方若需请求恢复/删除，必须单独评审工具清单与客户端人工确认机制后更新规范；本轮计划不构成执行授权，不能借“转发到客户端”绕过边界。
  - 管理方与受管方接口、实例范围和身份隔离。受管方仍是服务器单实例 MCP，不因客户端新增管理方入口扩大能力。
- **首批设计范围**：读取状态、实际范围与缺口，准备允许的计划，提交已允许且经授权的任务，查询进度和中断结果。确切工具名、参数、结构化结果、取消语义与版本能力在 N5a 定稿，不在技能中虚构命令或 API。重连按持久任务/专用 journal 查询，不自行重跑不明结果的操作。
- **默认保护迁移**：管理方允许的备份任务读取用户确认的保护计划；现有受管方 `valise_seal`、交接和首次工具调用的 soul 备份保持当前范围与频率限制。是否让它们跟随新计划须另行明确兼容和资源影响，不能通过改技能 prompt 扩大。
- **交付与验收**：依赖已稳定的服务契约；缺少确认条件时停在正确节点，任何返回不含秘密；查询、允许任务、部分失败、授权过期、断线查询及重复请求均须验证。用户不必靠临时 SSH 命令绕过 Valise。MCP 可执行接入与自定义、可选扩展的具体排期见 DEV_PLAN.md N5a–N5c；未实现前不渲染可用入口。
