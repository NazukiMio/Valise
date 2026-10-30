# Harness Profile 规范 `valise-profile/1`

Profile 是 Valise 了解某种 harness 的**唯一途径**。Go 代码里不允许出现任何 harness 的具体知识。新增一种 harness，只需要提交一份 profile。

## 1. 来源与信任

- 官方 profile 存放在仓库的 `profiles/` 目录下，经过 PR 审核后随 Release 签名发布。
- 用户可以在 `~/.config/valise/profiles/` 放置本地 profile。这类 profile 在 UI 中始终标记为"本地，未经审核"。
- `status: draft` 表示至少有一个条目尚未在靶机上核实。使用 draft profile 复活时，必须显式传入 `--allow-draft-profile`。
- 每一条无法从官方文档或真实安装中确认的规则，都必须标注 `verify: pending`。**宁可留空，不可猜测。**

## 2. Schema

```yaml
schema: valise-profile/1
id: example                      # 小写字母、数字和连字符，全局唯一
display_name: Example
profile_version: 1               # profile 内容每次变更都要加 1
status: draft                    # draft | verified
harness_versions_tested: []      # 例如 ["2026.10.3"]，填写核实时使用的版本
docs: https://…                  # 官方文档地址，只作参考，Valise 不会抓取

extends: null                    # 子 profile 填写父 profile 的 id，见第 4 节
when: null                       # 子 profile 的生效条件，例如 { exists: { root: state, glob: "x/y.json" } }

detect:
  binaries: [example]
  version_argv: [example, --version]
  state_dir_candidates: ["${env:EXAMPLE_HOME}", "${HOME}/.example"]   # 按顺序，取第一个存在的
  markers_any: [config.yaml]     # 状态目录中至少要存在其中一个

roots:                           # 除 state 之外的其他根目录
  - id: workspace
    discover_argv: null          # 可选：调用 harness 自己的 CLI 读出实际路径（只读命令）
    default: "${STATE_DIR}/workspace"

paths:                           # 按顺序匹配；凭据规则无论排在哪里都优先
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "memories/**", category: memory, verify: pending }

consistency:
  sqlite: [ { root: state, glob: "*.db" } ]
  quiesce: none                  # none | stop_service

provenance:
  kind: npm_global               # npm_global | git | uv_tool | pipx | binary
  npm: { package: example }
  git: { root: state, path: "example-agent" }

body:
  paths: [ { root: state, glob: "example-agent/**" } ]   # 状态目录中属于身体的部分
  bootstrap_argv: []             # 按 git 来源重建后执行的命令，以 argv 数组形式书写，在身体目录中运行
  fresh_install_doc: https://…   # 全新安装时的官方文档链接，只展示给用户，Valise 不执行

relocation: { env: EXAMPLE_HOME }   # 状态目录可以通过此变量改到别处；不支持时写 null（此时克隆只能到另一个用户或另一台服务器）

service:
  candidates: [ { scope: user, unit: example-gateway.service } ]   # 可以为空，表示前台运行

entries:
  - { id: main, kind: web, host: 127.0.0.1, port: 8080, path: "/" }

health:
  grace_seconds: 20
  checks:
    - { kind: systemd_active }
    - { kind: tcp, entry: main }
    - { kind: http, entry: main, expect_status: [200, 401] }
    - { kind: argv, argv: [example, doctor], ok_exit: [0] }

login: []                        # 见 LOGIN_RELAY.md

exclusive_secrets: [ { root: state, glob: "channels/**" } ]

native:
  export_argv: null              # 可选：调用 harness 自带的导出功能，产物作为附加文件存入金库（${OUT} 为输出路径）

checkup:
  sessions: { root: state, glob: "sessions/*.jsonl", format: jsonl, usage_fields: null }
  memory_files: [ { root: state, glob: "MEMORY.md", warn_lines: 800 } ]
  memory_mechanism_markers: [ { root: state, glob: "memories/*.md" } ]
  native_options_doc: https://…  # 官方文档中关于记忆、压缩、冷存档功能的说明页

handoff:
  targets: [ { root: state, glob: "MEMORY.md", mode: append_section, max_lines: 120 } ]
  new_session_hint: "在对话中发送 /new"

agent_kit:
  skills_dir: { root: workspace, path: "skills/" }
  mcp_config_doc: https://…      # 如何为该 harness 配置 MCP Server（Valise 只展示，不自动修改配置）
```

变量说明：`${HOME}` 表示用户主目录；`${STATE_DIR}` 表示解析后的状态目录；`${env:NAME}` 表示环境变量，变量未设置时这一项视为不存在；`${OUT}` 只在 `export_argv` 中使用。

## 3. profilelint 的校验规则

- 所有路径都必须相对于某个 root。禁止出现 `..`。除 `${HOME}` 和 `${env:…}` 展开得到的路径外，不允许出现其他绝对路径。
- 所有命令都必须写成 argv 数组，禁止使用 shell 字符串。
- category 必须是 ARCHITECTURE.md 第 5 节中列出的值。
- 声明了 `secrets` 的 glob 不得同时出现在 `body`、`cache` 或 `ignore` 中。
- `status: verified` 的 profile 中不允许出现任何 `verify: pending`，并且 `harness_versions_tested` 不能为空。
- 子 profile 不得删除父 profile 中的 `secrets` 规则。

## 4. 子 profile

用于覆盖同一 harness 下的不同插件形态，例如 DSH 的各种记忆插件。子 profile 通过 `extends` 继承父 profile，通过 `when` 设置生效条件，并只能**追加** `paths`、`consistency`、`checkup` 规则。子 profile 只用于正确地封存用户已经安装的东西，**不代表推荐**。

## 5. 贡献要求

提交 PR 时必须附上以下内容：harness 的版本；`valise enroll --report-json` 的输出（凭据路径可以打码）；在哪台靶机上验证过；以及每条 `verify: pending` 被移除的依据（官方文档链接，或真实安装的观察结果）。

---

## 6. 初稿

> 以下三份初稿根据 2026-10-30 时可查到的官方文档与社区资料整理。**凡是标注 `verify: pending` 的条目，都必须在 M2 阶段于靶机上逐条核实。**放进 `profiles/` 时，应拆成独立的 `.yaml` 文件。

### 6.1 `profiles/openclaw.yaml`

依据：官方 `openclaw backup` 文档说明，备份范围是状态目录（通常为 `~/.openclaw`）加上当前生效的配置文件；auth profile 位于 `agents/<agentId>/agent/auth-profiles.json`；每个 agent 的运行时状态保存在 `agents/<agentId>/agent/openclaw-agent.sqlite` 中。工作区内的文件名来自社区资料，需要核实。

```yaml
schema: valise-profile/1
id: openclaw
display_name: OpenClaw
profile_version: 1
status: draft
harness_versions_tested: []
docs: https://docs.openclaw.ai

detect:
  binaries: [openclaw]
  version_argv: [openclaw, --version]
  state_dir_candidates: ["${env:OPENCLAW_STATE_DIR}", "${HOME}/.openclaw"]   # 环境变量名 verify: pending
  markers_any: [openclaw.json]                                              # verify: pending

roots:
  - id: workspace
    discover_argv: null            # verify: pending —— 待确认是否有只读命令可以读出工作区路径
    default: "${STATE_DIR}/workspace"

paths:
  - { root: state, glob: "agents/*/agent/auth-profiles.json", category: secrets }
  - { root: state, glob: "credentials/**", category: secrets, verify: pending }
  - { root: state, glob: "openclaw.json", category: secrets, note: "配置中可能内联令牌，整个文件按凭据处理", verify: pending }
  - { root: workspace, glob: "SOUL.md", category: identity, verify: pending }
  - { root: workspace, glob: "IDENTITY.md", category: identity, verify: pending }
  - { root: workspace, glob: "USER.md", category: identity, verify: pending }
  - { root: workspace, glob: "AGENTS.md", category: identity, verify: pending }
  - { root: workspace, glob: "TOOLS.md", category: config, verify: pending }
  - { root: workspace, glob: "HEARTBEAT.md", category: config, verify: pending }
  - { root: workspace, glob: "MEMORY.md", category: memory, verify: pending }
  - { root: workspace, glob: "memory/**", category: memory, verify: pending }
  - { root: workspace, glob: "skills/**", category: skills, verify: pending }
  - { root: state, glob: "skills/**", category: skills, verify: pending }
  - { root: state, glob: "agents/*/sessions/**", category: sessions, verify: pending }
  - { root: state, glob: "agents/*/agent/*.sqlite", category: state }
  - { root: state, glob: "cron/**", category: config, verify: pending }
  - { root: state, glob: "logs/**", category: ignore, verify: pending }
  - { root: state, glob: "**/node_modules/**", category: cache }
  - { root: workspace, glob: "**", category: workspace }

consistency:
  sqlite: [ { root: state, glob: "agents/*/agent/*.sqlite" } ]
  quiesce: none

provenance:
  kind: npm_global
  npm: { package: openclaw }       # verify: pending

body: { paths: [], bootstrap_argv: [], fresh_install_doc: https://docs.openclaw.ai }

relocation: { env: OPENCLAW_STATE_DIR }   # verify: pending

service:
  candidates: [ { scope: user, unit: openclaw-gateway.service } ]   # verify: pending

entries:
  - { id: gateway, kind: web, host: 127.0.0.1, port: 18789, path: "/" }   # verify: pending

health:
  grace_seconds: 20
  checks:
    - { kind: systemd_active }
    - { kind: tcp, entry: gateway }
    - { kind: argv, argv: [openclaw, doctor], ok_exit: [0] }   # 需要确认是否存在非交互模式 verify: pending

login: []                          # M5 阶段补充

exclusive_secrets: [ { root: state, glob: "credentials/**" } ]   # verify: pending

native:
  export_argv: [openclaw, backup, create, --output, "${OUT}", --verify]

checkup:
  sessions: { root: state, glob: "agents/*/sessions/*.jsonl", format: jsonl, usage_fields: null }   # verify: pending
  memory_files: [ { root: workspace, glob: "MEMORY.md", warn_lines: 600 } ]
  memory_mechanism_markers: [ { root: workspace, glob: "memory/*.md" } ]   # verify: pending
  native_options_doc: null         # verify: pending

handoff:
  targets: [ { root: workspace, glob: "MEMORY.md", mode: append_section, max_lines: 120 } ]
  new_session_hint: null           # verify: pending

agent_kit:
  skills_dir: { root: workspace, path: "skills/" }   # verify: pending
  mcp_config_doc: null             # verify: pending
```

### 6.2 `profiles/hermes.yaml`

依据：社区迁移指南显示，`config.yaml`、存放密钥的 `.env`、`skills/`、`memories/`、`state.db` 都位于 `~/.hermes/` 下；Python 虚拟环境位于 `~/.hermes/hermes-agent/venv`；官方提供 `hermes backup` 和 `hermes import` 命令。另据 OpenClaw 的迁移文档，Hermes 的记忆文件为 `memories/MEMORY.md` 和 `memories/USER.md`。

**注意：**身体（`hermes-agent/`，包括其中的 venv）位于状态目录内部。venv 中大量使用绝对路径，因此**绝不封存、绝不复制，一律按来源记录重建**。

```yaml
schema: valise-profile/1
id: hermes
display_name: Hermes Agent
profile_version: 1
status: draft
harness_versions_tested: []
docs: https://hermes-agent.nousresearch.com

detect:
  binaries: [hermes]
  version_argv: [hermes, --version]          # verify: pending
  state_dir_candidates: ["${env:HERMES_HOME}", "${HOME}/.hermes"]   # 环境变量名 verify: pending
  markers_any: [config.yaml]

roots: []

paths:
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "auth.json", category: secrets, verify: pending }
  - { root: state, glob: "SOUL.md", category: identity }
  - { root: state, glob: "AGENTS.md", category: identity, verify: pending }
  - { root: state, glob: "memories/USER.md", category: identity }
  - { root: state, glob: "memories/**", category: memory }
  - { root: state, glob: "skills/**", category: skills }
  - { root: state, glob: "config.yaml", category: config, note: "需确认密钥不会内联在 config.yaml 中", verify: pending }
  - { root: state, glob: "cron/**", category: config, verify: pending }
  - { root: state, glob: "state.db", category: state }
  - { root: state, glob: "sessions/**", category: sessions, verify: pending }
  - { root: state, glob: "hermes-agent/**", category: body }
  - { root: state, glob: "backups/**", category: ignore }
  - { root: state, glob: "logs/**", category: ignore, verify: pending }

consistency:
  sqlite: [ { root: state, glob: "state.db" } ]
  quiesce: none                    # 如果存在向量库等二进制目录，可能需要改为 stop_service；verify: pending

provenance:
  kind: git
  git: { root: state, path: "hermes-agent" }

body:
  paths: [ { root: state, glob: "hermes-agent/**" } ]
  bootstrap_argv: []               # verify: pending —— 需要根据官方安装脚本确认 venv 的创建与依赖安装步骤，以及全局 hermes 命令的链接方式
  fresh_install_doc: https://hermes-agent.nousresearch.com

relocation: { env: HERMES_HOME }   # verify: pending

service:
  candidates: [ { scope: user, unit: hermes-gateway.service } ]   # verify: pending

entries: []                        # verify: pending

health:
  grace_seconds: 20
  checks:
    - { kind: systemd_active }
    - { kind: argv, argv: [hermes, doctor], ok_exit: [0] }   # verify: pending

login: []                          # M5 阶段补充

exclusive_secrets: [ { root: state, glob: ".env" } ]   # 其中包含社交平台的 Bot Token；更细的拆分 verify: pending

native:
  export_argv: null                # hermes backup 的输出路径参数 verify: pending

checkup:
  sessions: { root: state, glob: "state.db", format: sqlite, usage_fields: null }   # 表结构 verify: pending
  memory_files:
    - { root: state, glob: "memories/MEMORY.md", warn_lines: 600 }
    - { root: state, glob: "memories/USER.md", warn_lines: 300 }
  memory_mechanism_markers: [ { root: state, glob: "memories/MEMORY.md" } ]
  native_options_doc: null         # verify: pending

handoff:
  targets: [ { root: state, glob: "memories/MEMORY.md", mode: append_section, max_lines: 120 } ]
  new_session_hint: null           # verify: pending

agent_kit:
  skills_dir: { root: state, path: "skills/" }
  mcp_config_doc: null             # verify: pending
```

### 6.3 `profiles/dsh.yaml`

依据：DSH 于 2026-08-13 开源，基于 Node.js，通过 npm 包 `@deepseek-ai/dsh` 分发，Web UI 默认地址为 `127.0.0.1:3080`。插件按 profile 安装（`dsh plugin --profile web add …`），会话日志采用只追加设计。社区插件的数据位于 `~/.dsh/` 下。DSH 目前处于开发者预览阶段，官方明确提示可能出现破坏性变更，所以 `harness_versions_tested` 必须写到精确版本。

**注意：**DSH 通常以 `npx` 或前台 `dsh web` 的方式运行，可能没有 systemd 单元。如果用户没有全局安装，就无法记录稳定的来源；此时登记报告应提示用户先进行全局安装（用户级前缀）。

```yaml
schema: valise-profile/1
id: dsh
display_name: DeepSeek Harness
profile_version: 1
status: draft
harness_versions_tested: []
docs: https://github.com/deepseek-ai/deepseek-harness

detect:
  binaries: [dsh]
  version_argv: [dsh, --version]   # verify: pending
  state_dir_candidates: ["${env:DSH_HOME}", "${HOME}/.dsh"]   # 环境变量名 verify: pending
  markers_any: []                  # verify: pending

roots: []

paths:
  - { root: state, glob: "credentials/**", category: secrets, verify: pending }
  - { root: state, glob: "profiles/*/package.json", category: config, verify: pending }
  - { root: state, glob: "profiles/*/*.yml", category: config, verify: pending }
  - { root: state, glob: "profiles/*/node_modules/**", category: cache, verify: pending }
  - { root: state, glob: "sessions/**", category: sessions, verify: pending }
  - { root: state, glob: "logs/**", category: ignore }

consistency: { sqlite: [], quiesce: none }

provenance:
  kind: npm_global
  npm: { package: "@deepseek-ai/dsh" }

body: { paths: [], bootstrap_argv: [], fresh_install_doc: https://github.com/deepseek-ai/deepseek-harness }

relocation: null                   # verify: pending

service: { candidates: [] }

entries:
  - { id: web, kind: web, host: 127.0.0.1, port: 3080, path: "/" }

health:
  grace_seconds: 15
  checks:
    - { kind: tcp, entry: web }

login: []

exclusive_secrets: []              # verify: pending

native: { export_argv: null }

checkup:
  sessions: { root: state, glob: "sessions/**", format: jsonl, usage_fields: null }   # verify: pending
  memory_files: []
  memory_mechanism_markers: []     # 由各记忆插件的子 profile 追加
  native_options_doc: null

handoff:
  targets: []                      # 没有记忆插件时，交接写入的目标 verify: pending
  new_session_hint: null

agent_kit:
  skills_dir: null                 # verify: pending
  mcp_config_doc: null             # verify: pending
```

### 6.4 子 profile 机制示例（仅用于演示机制，不代表推荐）

社区中存在多种 DSH 记忆插件，存储方式各不相同：有的为每个项目存一个 JSONL 文件，有的使用单个 JSON 文件，有的采用向量加图谱的组合。下面的示例只演示"检测到用户已经装了某种存储形态时，如何把它正确归类"：

```yaml
schema: valise-profile/1
id: dsh.memory-json-store
display_name: DSH 记忆存储（单 JSON 文件形态）
profile_version: 1
status: draft
extends: dsh
when: { exists: { root: state, glob: "memory-plugin/memories.json" } }
paths:
  - { root: state, glob: "memory-plugin/**", category: memory }
checkup:
  memory_mechanism_markers: [ { root: state, glob: "memory-plugin/memories.json" } ]
```
