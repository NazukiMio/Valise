# agent-kit：让 Agent 认识自己的保护

> 2026-11-05 同步产品重整：两种 AI 角色和权限继续隔离。下文现有受管方 MCP 的 soul 范围保持兼容，保护计划迁移及管理方接口按 N1 / N5a 单独设计；不能因新默认 full 已定就声称两侧已经支持。界面使用“Agent 保护技能”，技能 ID 与命令名不变。
>
> - **受管方**：被 Valise 管理的 harness / agent。技能与服务器上的 porter（`valise-porter mcp` 的受限工具）对接。本文第 1–8 节即受管方，**现有 agent-kit 全部属于这一侧**。
> - **管理方**：代表用户操作 Valise、管理服务器与 harness 的 agent。技能与用户侧的 Valise 客户端对接。**项目目前没有管理方技能**，见第 9 节，属于规划。
>
> 两侧可以组成官方套装，但不是同一套权限：受管方不因安装技能获得任何管理方权限；管理方不得借技能绕过客户端的确认、安装安全检查或凭据限制。边界由代码执行，不靠技能文本。

## 1. 定位与原则（受管方）

agent-kit 是可选的保护与健康辅助，**不是灾难恢复的依赖**。用户接入后可明确选择“安装 Agent 保护技能”，再对自己的 agent 说一句：

> 我用 Valise 给你设置了备份保护，你学习一下。

此后，agent 就知道自己被保护着：做风险操作前会先封存，会话太长时会提议交接，用户问起"你还好吗"时可以做一次体检。

原则：

1. **执行交给代码，LLM 只做三件事**：补充登记信息、撰写交接内容、用自然语言解读体检结果。
2. **MCP 工具只开放最小权限**：不提供复活、删除、读取凭据或写入任意文件的能力。agent 修改自己的记忆文件时，使用它自己原有的文件工具；Valise 只负责告诉它"该写到哪里"，并在写完后用代码检查结果。
3. **不越俎代庖**：凡是修改记忆的操作，都必须先封存，经用户同意后才执行，并且可以撤销。
4. **不背书**：只推荐该 harness 官方文档中的原生选项，以及通用的选择标准。

## 2. 安装

客户端中的“安装 Agent 保护技能”会做两件事，并在执行前明确告诉用户：

- 把 `agent-kit/skills/` 下的四个技能复制到 profile 中 `agent_kit.skills_dir` 指定的位置。
- 展示该 harness 配置 MCP Server 的方法。MCP 的启动命令为 `valise-porter mcp --instance <id>`。Valise **不自动修改 harness 的配置文件**，而是给出可以直接复制的配置片段，或者给出一句可以直接转告 agent 的话。

技能目录为空（profile 未提供）的 harness，暂不支持安装技能。

## 3. MCP 工具（`valise-porter mcp`，stdio 模式）

一个 MCP 进程只服务于启动时 `--instance` 参数指定的那个实例。

| 工具 | 输入 | 行为 | 限制 |
| :--- | :--- | :--- | :--- |
| `valise_status` | 无 | 返回实例 id、profile 及其核实状态、最近一次封存的时间和档位、保护状态（2026-11-03，检测报告 F10：`configured_targets` 已配置的同步位置、`last_offsite` 别处最新的一份、`pods_waiting_sync` 还在等同步的个数，以及由代码写成的一句话 `protection_summary`，agent 原样转述，不从空列表推断"没有配置"）、当前的体检 finding id 列表、实例自己的记忆文件与交接目标位置（M6 验收中 DSH agent 因不知道自己的记忆在哪里，写进了同机 OpenClaw 的记忆文件），以及归类线索：未分类文件的路径、以及在本应稳定的类别（identity、config、skills）中连续多次封存都发生变化的文件路径（来自服务器端的变动统计，见 ARCHITECTURE.md 第 6 节） | 只读；只返回路径与计数，不返回任何文件内容 |
| `valise_checkup` | 无 | 运行代码体检（见 ARCHITECTURE.md 第 9 节），返回各项指标和 finding id | 只读，最短间隔 10 分钟 |
| `valise_seal` | `{ "reason": string }` | 封存一次 `soul` 档，返回快照 id | 只能封存 `soul` 档，最短间隔 10 分钟，`reason` 写入审计记录 |
| `valise_propose_enrollment` | `{ "items": [{ "root", "path", "category", "reason" }] }` | 由代码校验路径确实存在、没有路径穿越、类别合法，然后以"待确认"状态保存；确认后写入该实例的 `local_overrides` | 不得提议 `secrets` 以外的类别去覆盖已有的 `secrets` 规则；把文件移出 `soul` 档的提议（改为 `ignore`、`cache`、`body`）标记为"降级"；所有提议都需要用户在客户端确认 |
| `valise_declare`（2026-11-02 用户决定，M12 实现） | `{ "security_skills": [{ "name", "purpose" }], "approval_mode": "ask" \| "bypass" \| "unknown" }` | agent 申报自己装了哪些安全类技能、执行风险命令是否需要用户批准；声明连同时间保存在服务器上，界面一律标"agent 声明（未经核实）"；profile 有 `approval_check` 时以核实结果为准 | 只写声明本身；最短间隔 10 分钟；逃生舱技能在体检时提醒 agent 更新 |
| `valise_handoff_begin` | 无 | 先封存一次 `soul` 档（包含当前会话），再返回快照 id、当前会话的统计数据、profile 中的 `handoff.targets`、每个目标的行数上限，以及 `new_session_hint` | 每次交接只能调用一次 |
| `valise_handoff_finish` | `{ "snapshot_id" }` | 由代码检查交接目标文件确实被修改、没有超过行数上限，并且只有指定的区段发生了变化；写入审计记录，返回给用户看的下一步提示 | 检查失败时返回具体原因，原文件可以从快照中恢复 |

### 3.1 由 agent 补充归类（M6）

运行时文件（心跳、锁、临时状态）因 harness、因用户而异，不写进官方 profile，而是作为每个实例的本地覆盖处理：

1. **代码提供证据**：`valise_status` 给出未分类文件，以及在 identity、config、skills 中连续多次封存都发生变化的文件。
2. **agent 解释并提议**：agent 结合它对自身 harness 的了解，用 `valise_propose_enrollment` 提出归类和理由。
3. **用户拍板**：客户端列出全部提议；"降级"提议（会让文件离开 `soul` 档）单独列出、单独确认，防止 agent 误判或被注入后让记忆从备份中消失。
4. **兜底**：`raw` 档不受任何覆盖影响，始终包含实例目录下的全部文件。
5. **回流**：如果某个模式对所有用户都成立，应以 PR 的形式带证据修改官方 profile。Valise 不收集遥测。

在 M6 之前，用户可以用 `valise enroll --assign` 手动写入本地覆盖。

## 4. 四个技能

每个技能都是一个标准的 `SKILL.md`，用 frontmatter 写明 `name` 和 `description`，正文用平实的语言写给 agent 看。

### 4.1 `valise-enroll`（认识保护）

触发：用户让 agent 了解 Valise 的备份保护；旧“逃生舱”说法继续识别，不能只接受旧品牌词。

步骤：

1. 调用 `valise_status`，了解自己是哪个实例、受到什么保护。
2. 用一两句话向用户说明：哪些东西已经受到保护（灵魂，以及是否包含凭据），最近一次封存是什么时候，是否已经同步到异地。
3. 如果 agent 知道自己还使用了 profile 没有覆盖的目录（例如用户让它自建的笔记目录），就调用 `valise_propose_enrollment` 提出来，并提醒用户去客户端确认。
4. 记住 4.2 中的习惯规则。如果 harness 支持，把规则写进自己的常驻指令，但只写一行引用，例如"风险操作前遵循 valise-upkeep 技能"。

### 4.2 `valise-upkeep`（日常习惯）

在以下操作之前，先调用 `valise_seal`，并在 `reason` 中写明原因：

- 修改自己的人格、规则或记忆文件；
- 安装或删除技能和插件；
- 修改配置；
- 升级 harness；
- 批量编辑或清理文件。

如果封存失败，就告诉用户失败原因，并询问用户是否继续。**不得在用户不知情的情况下跳过封存。**

### 4.3 `valise-handoff`（正式的"转生"）

触发：用户提出想切换会话；或者体检出现 `long_session` 或 `cost_growth`，用户同意交接。

步骤：

1. 调用 `valise_handoff_begin`。此时旧会话已经完整封存，要明确告诉用户："旧对话已经封存，不会丢失。"
2. 撰写交接内容，写入 `handoff.targets` 指定的原生记忆文件。写入方式为追加一个带日期标题的区段，不得改动该区段以外的内容，并且不超过行数上限。内容只包括：正在进行的任务与下一步、近期确定的事实与决定、未解决的问题。不写寒暄，不写排错过程，不写任何凭据。
3. 调用 `valise_handoff_finish`。
4. 把 `new_session_hint` 转告用户，告诉用户如何开启新会话；新会话会自动加载交接内容。

### 4.4 `valise-checkup`（体检）

1. 调用 `valise_checkup`，用通俗的语言解释每一个 finding（见第 5 节）。
2. 只给建议，不主动修改任何东西。涉及记忆的整理（例如把久远的记忆抽象成摘要），必须满足以下条件：用户明确同意 → 先 `valise_seal` → 在原文件中只修改用户同意的部分 → 告诉用户可以从哪个快照撤销。
3. 推荐方案时，先给出该 harness 官方文档中的原生选项（profile 的 `native_options_doc`），再给出第 6 节的选择标准。**不点名推荐任何第三方插件。**

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
- 工具结果同时放在 `structuredContent`（JSON 对象）和一段 `text` 中；用户错误（频率限制、校验失败等）以 `isError: true` 返回"原因 + 建议"，不中断会话。
- 审计记录的发起者为 `agent-mcp`；被频率限制挡回的封存尝试也记一条（结果 `rate_limited`，附原因）。频率限制与交接状态保存在 `~/.local/state/valise/mcp/<实例>.json`。
- MCP 进程以实例在服务器上的 profile 副本为准（与封存相同），不需要客户端在线。
- **代码兜底**（用户决定，2026-10-31）：同一个 MCP 连接中，agent 第一次调用任何工具（`valise_seal` 与 `valise_handoff_begin` 本身会封存，除外）时，如果距上次封存已超过 10 分钟，先自动封存一次 `soul` 档，审计原因为 `auto: first tool call (<工具名>)`，并计入频率限制。原因：M6 验收中观察到，模型在写记忆、改人格前并不可靠地调用 `valise_seal`。局限：只覆盖调用了 Valise 工具的会话；不依赖 agent 的定时封存属于 M8。

### 8.2 归类提议

- `valise_propose_enrollment` 的 `path` 是相对 root 的路径：指向文件时生成精确规则，指向目录时生成 `<path>/**`。代码校验：路径存在、不含 `..` 与绝对路径、不经过符号链接逃出 root、类别合法；与已有 `secrets` 规则冲突的非 `secrets` 提议直接拒绝。
- 提议保存在 `~/.config/valise/instances/<实例>.proposals.json`（0600），每条有 id、规则、理由、时间、是否"降级"，以及它会把多少个目前未分类的文件移出凭据层。同一规则重复提议时只更新理由。
- 未分类文件按"未知即敏感"随凭据层封存；把它们归入 soul 档类别的提议，客户端会注明"接受后这些文件将从凭据层进入 soul 档"，提醒用户确认其中没有凭据（M6 验收中观察到 agent 会把未知的数据库提议为 `state`）。
- 客户端：`valise proposals <服务器> <实例>` 列出全部待确认提议，降级提议单独成组；`--accept <id,…|all>` 只接受非降级提议；降级提议必须用 `--accept-demotion <id,…>` 单独接受，执行前再次说明"这些文件将不再进入 soul 档"；`--reject <id,…|all>` 拒绝。接受的提议写入实例记录的 `local_overrides`，从下一次封存起生效。

### 8.3 交接

- `valise_handoff_begin`：封存一次 `soul` 档（不受 `valise_seal` 的 10 分钟限制），记录每个交接目标文件当时的 SHA-256 与长度，返回快照 id、最近活跃会话的统计、目标、行数上限与 `new_session_hint`。同一时间只能有一个未完成的交接；新的 begin 会作废旧的。
- `valise_handoff_finish`：`append_section` 模式下逐个检查目标文件：原内容必须原样保留为前缀；追加部分不超过 `max_lines` 行，第一行非空行必须是带日期（`YYYY-MM-DD`）的 Markdown 标题。至少一个目标被修改才算成功。失败时返回具体原因，并说明可以从 begin 的快照恢复原文件。

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
  2. **交给 agent**：一段可以复制给 agent 的话，请它用已安装的 `valise-checkup` 技能解读结果并提出整理方案，由用户确认后再执行；没装技能时提示先安装。
  3. **记忆整理技能**：说明 Valise 计划提供自己的 `valise-memory` 技能（整理、压缩、归档记忆），目前处于规划中。只写文字，不渲染按钮，不点名任何第三方方案。
- 归类提议在界面上称为"归档建议"，放在体检页中（UI.md 9.4），不再单独成页。

### 8.4.1 Valise Kit 的三级（2026-11-02 用户决定）

原“加强套装”并入 Valise Kit。每一级只能安装签名资料包中列出、审核过的条目（来源锁定版本与 SHA-256，执行的命令全部展示），不是插件市场；只在用户选择后安装，逐项卸载。

2026-11-05 产品重整：新工具箱按“接入 AI 管理、让受管 Agent 了解保护、辅助传输、记忆与会话管理”等用途组织，基础/增强/专家保留为底层风险与安装方式，不要求用户先理解三级套装。每项说明装在哪里、影响谁、不装会怎样、权限与卸载办法。规划中的技能、pending 工具不能显示为可安装，工具箱与记忆扩展不阻塞默认保护和恢复闭环。

| 级别 | 内容 | 安装方式 | 访问级别 |
| :--- | :--- | :--- | :--- |
| 基础 | 四个逃生舱技能与 MCP 接入（8.5 节） | 默认提供，在管理页一键安装 | 主人 |
| 增强 | 官方记忆技能（`valise-memory`，规划中）、官方辅助技能、不需要 root 的工具（如 rclone） | 逐项选择、逐项安装 | 主人 |
| 专家 | harness 插件、需要 root 的工具（如经系统包管理器装 rsync） | 逐项安装，先说明风险、用户确认知晓 | 开发者模式 › 高级模式，并受 INSTALL.md 4.2 的前提约束 |

每个工具一行展示：这台服务器上有没有、用于哪些功能、不装会怎样（如"rsync 没有时改用 sftp"）、安装方式与完整命令；也可以"打开终端窗口"由用户自己安装。

**初版清单**（2026-11-02 用户确认，"不求多求精"）：清单在仓库的 `kit/tools.yaml`，打进签名资料包时名为 `kit-tools.yaml`（资料包是扁平结构，`PACK.json` 以 `kit_tools` 记录它的 SHA-256），与 profile 分开；每项写明 `id`、级别、名称、用途、不装会怎样、检测方式（argv）、安装方式（增强级：各架构的下载地址与 SHA-256，装到 `~/.local/bin`；专家级：各发行版的包名或固定 argv）与卸载方式。

| id | 级别 | 用途 | 不装会怎样 | 安装方式 |
| :--- | :--- | :--- | :--- | :--- |
| `rclone` | 增强 | 云盘（M13） | 不能上云到网盘 | 官方二进制，锁定版本 |
| `rsync` | 专家 | 上云、仓库同步更快 | 改用 sftp，速度慢一些 | 系统包管理器（apt / dnf），需要 sudo |
| `linger` | 专家 | 用户登出或服务器重启后，定时封存、自动上云与以 systemd 用户服务运行的 harness 继续运行 | 这些会在登出或重启后停下，不报错 | `loginctl enable-linger <用户>`，需要 sudo；卸载为 `disable-linger` |

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


### 8.5 安装 Agent 保护技能

- `valise kit install <服务器> <实例>`：先列出将要写入的文件，用户确认后由 porter 把 `agent-kit/skills/` 下的四个技能写到 profile 的 `agent_kit.skills_dir`（每个技能一个子目录，已存在且内容不同时先备份为 `SKILL.md.valise-bak-<时间>`）。技能文件随客户端分发。
- 然后打印 MCP 的接入方法：profile 有 `agent_kit.mcp_add_argv` 时，展开 `${PORTER}`、`${INSTANCE}` 后打印 harness 自己的命令供用户执行；没有命令、只有 `agent_kit.mcp_config_snippet` 时打印展开后的配置片段；同时打印一句可以转告 agent 的话。
- `format: jsonl` 的会话文件以 `.zst` 或 `.zstd` 结尾时先解压；大小按磁盘上的字节计，token 估算按解压后的字节计。Valise 不执行这条命令，也不修改 harness 配置。
- 技能正文用英文书写，并要求 agent 用用户的语言与用户交流；触发说明同时列出中英文说法。
- `valise kit status <服务器> <实例>`（M7a）：逐个列出四个技能在技能目录中的状态（与本客户端发布的版本一致 / 已被修改 / 未安装）以及 Valise 留下的备份数；MCP 方面只报告最近一次 agent 经 MCP 调用的时间（来自审计记录）。Valise 不读取 harness 的配置，因此不声称"MCP 已配置"。
- `valise kit uninstall <服务器> <实例> [--force]`（M7a）：先列出将删除的文件，用户确认后由 porter 在 Go 代码中删除：内容与当前发布版本一致的 `SKILL.md`、Valise 写的 `SKILL.md.valise-bak-*` 备份，以及因此变空的技能目录。被修改过的 `SKILL.md` 保留并列出，加 `--force` 才删除。然后打印撤除 MCP 的方法：profile 的 `agent_kit.mcp_remove_argv`（harness 自己的命令，Valise 不执行），或提示从配置片段所在文件删除相应条目；并提醒 agent 可能在常驻指令里加过一行提及 `valise_seal` 的提醒，需要用户或 agent 自行删除。MCP 状态、审计记录与金库不受影响。

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
