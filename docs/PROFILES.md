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
    discover_argv: null          # 可选：调用 harness 自己的 CLI 读出实际路径（只读命令；标准输出必须恰好是一个绝对路径，失败时退回 default 并警告）
    default: "${STATE_DIR}/workspace"

paths:                           # 按顺序匹配；凭据规则无论排在哪里都优先
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "memories/**", category: memory, verify: pending }

consistency:
  sqlite:
    - { root: state, glob: "*.db" }
    - { root: state, glob: "agent.sqlite", secret_tables: [auth_store] }   # 可选：该库中存放凭据的表，soul 档封存时在副本中清空（见 ARCHITECTURE.md 第 6 节）
  quiesce: none                  # none | stop_service

provenance:
  kind: npm_global               # npm_global | git | uv_tool | pipx | binary
  npm: { package: example }      # kind: npm_global 时必填
  git: { root: state, path: "example-agent" }   # kind: git 时必填
  uv: { package: example }       # kind: uv_tool 时必填
  pipx: { package: example }     # kind: pipx 时必填
  binary: { name: example }      # kind: binary 时必填，取 PATH 中的该命令计算哈希

body:
  paths: [ { root: state, glob: "example-agent/**" } ]   # 状态目录中属于身体的部分
  bootstrap_argv: []             # 按 git 来源重建后执行的命令，以 argv 数组形式书写，在身体目录中运行
  fresh_install_doc: https://…   # 全新安装时的官方文档链接，只展示给用户，Valise 不执行

relocation: { env: EXAMPLE_HOME }   # 状态目录可以通过此变量改到别处；不支持时写 null（此时克隆只能到另一个用户或另一台服务器）

service:
  candidates: [ { scope: user, unit: example-gateway.service } ]   # 可以为空，表示前台运行
  install_argv: [example, gateway, install]   # 可选：harness 自带的服务安装命令；复活到没有该服务的服务器时，经用户确认后执行

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
  sessions: { root: state, glob: "sessions/*.jsonl", format: jsonl, usage_fields: null, time_field: null }
  # format: sqlite 时改用 sqlite 子项（标识符只能是字母、数字、下划线）：
  # sessions: { root: state, glob: "state.db", format: sqlite,
  #             sqlite: { table: messages, session_column: session_id, time_column: timestamp, time_unit: s,
  #                       tokens_column: token_count, size_column: null, bytes_columns: [content] } }
  # 每行的大小取 size_column（非空时），否则为 bytes_columns 各列 length() 之和。
  # token 按会话汇总在另一张表时，加 usage: { table, session_column, tokens_columns: [...], time_column, time_unit }，
  # 用它代替每行的 tokens_column 与字节估算；每日用量按 time_column（最后更新时间）归日。
  thresholds: {}                 # 可选，覆盖体检默认阈值（AGENT_KIT.md 8.4）
  memory_files: [ { root: state, glob: "MEMORY.md", warn_lines: 800 } ]
  memory_mechanism_markers: [ { root: state, glob: "memories/*.md" } ]
  native_options_doc: https://…  # 官方文档中关于记忆、压缩、冷存档功能的说明页

handoff:
  targets: [ { root: state, glob: "MEMORY.md", mode: append_section, max_lines: 120 } ]
  new_session_hint: "在对话中发送 /new"

agent_kit:
  skills_dir: { root: workspace, path: "skills/" }
  mcp_config_doc: https://…      # 如何为该 harness 配置 MCP Server（Valise 只展示，不自动修改配置）
  mcp_add_argv: [example, mcp, add, valise, --command, "${PORTER}", --arg=mcp, --arg=--instance, "--arg=${INSTANCE}"]   # 可选：只打印给用户，Valise 不执行
```

变量说明：`${HOME}` 表示用户主目录；`${STATE_DIR}` 表示解析后的状态目录；`${env:NAME}` 表示环境变量，变量未设置时这一项视为不存在；`${OUT}` 只在 `export_argv` 中使用。

## 3. profilelint 的校验规则

- 所有路径都必须相对于某个 root。禁止出现 `..`。除 `${HOME}` 和 `${env:…}` 展开得到的路径外，不允许出现其他绝对路径。
- 所有命令都必须写成 argv 数组，禁止使用 shell 字符串。
- category 必须是 ARCHITECTURE.md 第 5 节中列出的值。
- 声明了 `secrets` 的 glob 不得同时出现在 `body`、`cache` 或 `ignore` 中。
- `status: verified` 的 profile 中不允许出现任何 `verify: pending`，并且 `harness_versions_tested` 不能为空。
- 子 profile 不得删除父 profile 中的 `secrets` 规则。

实现说明：

- "未核实"标记有两种写法，profilelint 都会统计：规则中的 `verify: pending` 字段，以及任何包含 `verify: pending` 的 YAML 注释。`verify` 字段只允许取值 `pending`。
- "同时出现"按两种情况判定：glob 字符串完全相同；或者 `secrets` 的 glob 不含通配符，并且能被同一 root 下某条 `body`、`cache`、`ignore` 规则（包括 `body.paths`）匹配。
- glob 语法：以 `/` 分隔路径段，`**` 匹配零个或多个路径段，其余段使用 Go `path.Match` 的语法。
- `secret_tables` 中的表名只能包含字母、数字和下划线。
- profile 使用严格解析，出现 schema 中没有的字段即报错。子 profile 只能包含元信息字段（`schema`、`id`、`display_name`、`profile_version`、`status`、`harness_versions_tested`、`docs`、`extends`、`when`）以及 `paths`、`consistency`、`checkup`；它的父 profile 必须出现在同一次 lint 的文件集合中。

## 4. 子 profile

用于覆盖同一 harness 下的不同插件形态，例如 DSH 的各种记忆插件。子 profile 通过 `extends` 继承父 profile，通过 `when` 设置生效条件，并只能**追加** `paths`、`consistency`、`checkup` 规则。子 profile 只用于正确地封存用户已经安装的东西，**不代表推荐**。

## 5. 贡献要求

提交 PR 时必须附上以下内容：harness 的版本；`valise enroll --report-json` 的输出（凭据路径可以打码）；在哪台靶机上验证过；以及每条 `verify: pending` 被移除的依据（官方文档链接，或真实安装的观察结果）。

---

## 6. 核实记录（M2，2026-10-30）

初稿已经拆成 `profiles/*.yaml`，**以那里的文件为准**。每条规则的依据都写在文件注释里（官方文档页面或真实安装的观察结果）。三份 profile 的 `profile_version` 均为 2，状态仍为 `draft`。

| profile | 核实环境 | 版本 | 待核实条目 |
| :--- | :--- | :--- | :---: |
| `openclaw` | `ubuntu-vm`（arm64），npm 用户级前缀，`openclaw setup --baseline`，未配置凭据、未安装服务 | 2026.10.9 | 30 → 8 |
| `dsh` | `ubuntu-vm`（arm64），npm 用户级前缀，`dsh web --dump-config` 初始化，未配置凭据、未启动 | 0.2.1-rc.5 | 14 → 6 |
| `hermes` | `ubuntu-vm-x86`（x86_64，无 sudo），官方安装，systemd 用户服务运行中 | 0.22.5（commit bf0203a794） | 20 → 11 |

### 6.1 对初稿的主要更正

- **OpenClaw**：运行时状态库是 `state/openclaw.sqlite`，另有每个 agent 的 `agents/<id>/agent/openclaw-agent.sqlite`；后者同时存放模型凭据和会话转录，因此引入表级凭据剥离（`secret_tables`，见 ARCHITECTURE.md 第 6 节）。会话不再是 JSONL，体检改为读 SQLite。工作区标准文件中没有 `TOOLS.md`、`HEARTBEAT.md`，新增 `BOOT.md`。补充凭据文件 `.env`、`secrets.json`、`auth.json`、`codex-home/`、`openclaw.json.bak`。日志默认在 `/tmp/openclaw/`，不在状态目录里，删除 `logs/**` 规则。服务单元名、端口 18789、`OPENCLAW_STATE_DIR` 均已由官方文档核实。工作区路径改由只读命令 `openclaw config get agents.defaults.workspace` 读出。健康检查删除 `openclaw doctor`（没有找到退出码有文档的只读命令）。
- **DSH**：凭据在 `.credentials.yaml` 和 `.env`，不在 `credentials/`；会话为 `sessions/` 下的 JSONL；存储在 `storages/`；profile 目录下的 `node_modules/` 由 pnpm 按 `package.json` 安装，归为缓存。状态目录的标记文件为 `profiles`。`DSH_HOME` 和默认地址 `127.0.0.1:3080` 已核实。
- **Hermes**：`config.yaml` 的 schema 中有 `api_key` 字段，可以内联密钥，整体改为凭据。按官方文档补充凭据存储（`.op.env`、`npmrc`、`mcp-tokens/`、`vault/`、`proxy/`、`platforms/` 等）。身体除 `hermes-agent/` 外还包括安装程序管理的 `tools/`（约 1 GB 的 node、python、uv、chromium 等运行时）和 `installs/`（约 0.5 GB 的安装环境）。新增 `kanban.db`、`cron/executions.db` 两个 SQLite。健康检查删除 `hermes doctor`（会联网探测模型提供商）。

### 6.2 仍未核实的条目与原因

| profile | 条目 | 原因 | 何时能核实 |
| :--- | :--- | :--- | :--- |
| openclaw | `config-journal-fingerprint.key` 归为凭据 | 文档没有说明用途，只按文件名保守处理 | 需要 OpenClaw 文档或源码说明 |
| openclaw | `state/openclaw.sqlite` 中按列名补充的凭据表（`secret_store_entries`、设备令牌等） | 只看到表结构，没有观察到实际行（`auth_profile_store` 已在 M5 验收中核实） | 配对设备、使用 secret store 后 |
| openclaw | `cron/**` 归为配置 | 未观察到该目录 | 创建一个定时任务后 |
| openclaw | `openclaw.json` 中内联的渠道令牌是否属于独占凭据 | 需要接入渠道才能观察 | M4（克隆时） |
| dsh | `profiles/*/pnpm-lock.yaml` | 未观察到 | 安装一个插件后 |
| dsh | 会话文件布局、独占凭据、交接目标、技能目录 | 未配置凭据，没有运行过会话；web profile 禁用了文件系统技能 | 配置凭据并运行后；交接与技能属于 M6 |
| hermes | `shared/nous_auth.json` 归为凭据 | 文档没有说明，只按文件名保守处理 | 需要 Hermes 文档或源码说明 |
| hermes | `plugins/**` 归为技能、`image_cache/**` 归为缓存 | 文档提到目录，但没有说明是否可再生 | 安装一个插件后 |
| hermes | `bootstrap_argv`（重建 `tools/`、`installs/`） | 属于身体重建 | M4 |
| hermes | `native.export_argv`、`quiesce` | 属于封存 | M3 |
| 全部 | 体检的会话表结构、`native_options_doc`、`new_session_hint`、`mcp_config_doc` | 属于 agent-kit | M6 |

**用户决定（2026-10-30）**：三份 profile 保持 `draft`，上表各条目在对应的里程碑（或配置凭据实际运行 harness 后）再核实，不阻塞 M2。

`ubuntu-vm-x86` 上的 Hermes 还有 14 个文件没有任何规则（`shared-state.db`、`source-checks/`、`gateway_state.json`、`state/`、`spawn-ledger.json`、`gateway-starts.log`、`context_length_cache.yaml`、`install_id`、`plugin-update-checks/`、`kanban/`、`runtime/` 等，合计约 158 KiB）。它们在登记报告中显示为未分类，不会被封存；没有文档依据，就不为它们编写规则。**用户决定**：保持未分类，由用户登记时用 `--assign` 自行归类。

### 6.3 额外样本：`ubuntu-vm` 上的旧版 Hermes

这份 Hermes 由旧版 Fathom 部署：`~/.hermes` 是指向 `/slots/active → /slots/b` 的符号链接（AB 槽位），目录中还有 `FATHOM_RESTORE_MARKER.txt`。它只用于只读观察：登记报告能穿过根目录的符号链接正确扫描，旧项目的文件都落在"未分类"中。它不作为核实依据，也不在其上写入实例记录。

### 6.4 子 profile 机制示例（仅用于演示机制，不代表推荐）

社区中存在多种 DSH 记忆插件，存储方式各不相同：有的为每个项目存一个 JSONL 文件，有的使用单个 JSON 文件，有的采用向量加图谱的组合。下面的示例只演示"检测到用户已经装了某种存储形态时，如何把它正确归类"：

```yaml
schema: valise-profile/1
id: dsh-memory-json-store
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
