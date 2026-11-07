# Harness Profile 规范 `valise-profile/1`

Profile 是 Valise 了解某种 harness **通用规则的唯一途径**：发现、路径与分类、来源、安装、服务、管理页、登录、健康检查、备份、会话记忆检查、Agent 保护技能与管理动作。服务器 dossier 补足实际安装事实，不是第二套 harness 解释器。Go 代码不得硬编码具体 harness 知识；新增适配通过 profile 与必要的通用 schema 扩展，逐能力核实。

> 2026-11-05：产品默认备份收敛到保护计划，范围预设、凭据分类与本文件现有 schema 保持兼容。本轮没有新增布局、自定义范围或身份字段；实例世代与迁移关联按已定稿 SERVICE_CONTRACTS.md 保存于金库外的 Valise 身份记录，不向本 profile schema 添加身份字段；自定义详细语义仍留 N5b。界面用语以 PRODUCT.md 8.1 为准。

## 1. 来源与信任

- 官方 profile 存放在仓库的 `profiles/` 目录下，经过 PR 审核后打成**资料包**单独签名发布（1.1 节）。客户端本身不内置任何 profile。
- 用户可以在 `~/.config/valise/profiles/` 放置本地 profile。这类 profile 在 UI 中始终标记为"本地，未经审核"。本地 profile 与资料包中的 profile 同 id 时，本地的被拒绝并报告。

### 1.1 资料包（2026-10-31 用户决定）

- **内容**：一个 `tar.gz`，根目录下是 `PACK.json` 与全部官方 `*.yaml`（不含子目录）。`PACK.json`：`{"format": "valise-profile-pack/1", "version": "2026.10.31", "created_at": "…", "profiles": [{"file": "openclaw.yaml", "id": "openclaw", "profile_version": 8, "sha256": "…"}]}`。一个包包含全部主流 harness，整体安装、整体替换。
- **签名**：与 porter 使用同一把 Release minisign 钥匙，签名直接作用于压缩包本身（`valise-profiles.tar.gz.minisig`），因此离线拿到的包也能验签。
- **发布**：每个资料包是一个 GitHub Release，tag 为 `profiles-<version>`（例如 `profiles-2026.10.31`），资产为 `valise-profiles.tar.gz` 与 `.minisig`。客户端经 GitHub API 列出 Release，取最新的 `profiles-*`；也可以指定 tag。打包工具：`go run ./cmd/profilepack`（`client/` 下），签名用 minisign 命令行在发布时完成。
- **安装**（设置 › 更新，或 `valise profiles install`）：在线拉取，或选择本地文件。客户端用内置的 Release 公钥验签，再逐个核对 `PACK.json` 中的 sha256 并对整包运行 lint；任何一步失败都整包拒绝。通过后把压缩包与签名原样保存到 `<客户端数据目录>/profile-pack/`，**每次加载都重新验签**。
- **没有签名或签名不对的本地文件**：不作为资料包安装；经用户同意后把其中的 profile 作为"本地 profile"写入本地目录。同意之前列出每份 profile 中会被执行的命令（服务安装、身体重建、管理页令牌等 argv），界面标"未经审核"。
- **没有安装资料包时**：登记无法识别任何 harness，界面与 CLI 引导安装。已经登记的实例不受影响：服务器上保存着登记时的 profile，备份里也带着完整的 profile（`profile_doc`），因此恢复不依赖资料包。
- 开发构建没有 Release 公钥，只能把仓库的 `profiles/` 作为本地 profile 使用（演练脚本即如此）。
- `status: draft` 表示至少有一个条目尚未在靶机上核实。每一条无法从官方文档或真实安装中确认的规则，都必须标注 `verify: pending`。**宁可留空，不可猜测。**

### 1.2 按能力展示支持程度（2026-11-01 用户决定）

整体的 draft / verified 标签不足以说明"能做什么"。profile 新增 `capabilities`，逐项记录核实状态，界面按能力展示，并注明适用版本与环境：

```yaml
capabilities:
  install:  { status: tested, versions: ["2026.10.9"], env: "Ubuntu 26.04 arm64, npm 用户级前缀", date: 2026-11-xx }
  manage:   { status: tested, versions: [...], env: "...", note: "管理页连接测试通过" }
  backup:   { status: tested, ... }
  restore:  { status: untested, note: "恢复尚未在此版本演练" }
  login:    { status: partial, note: "OpenAI 回环登录已测；其他供应商未测" }
```

- 能力项：`install`（官方方案）、`manage`（发现、状态、管理页、动作）、`backup`（登记与建立备份）、`restore`（恢复）、`login`（登录授权）、`checkup`（体检）、`kit`（Agent 保护技能）。状态：`tested` / `partial` / `untested` / `failed`；`tested` 必须写 `versions` 与 `env`。
- 官方列表区分"可安装"与"可管理"；列表用概览等级，详情按能力展示，例如"管理页连接测试通过""备份已测试""恢复尚未演练"。不因存在 YAML 就宣称全面支持。
- 用户本地 profile 不用笼统的"未验证"制造压力：展示来源（本地、未经审核）、已测试与未测试的能力、失败项及具体影响。
- **恢复保护按能力判断**（2026-11-02 用户决定）：只看 `capabilities.restore`。它是 `tested` 时直接恢复，不因整份 profile 仍是 `status: draft`（例如体检条目还有 `verify: pending`）而拦截；不是 `tested` 时，复活必须显式传入 `--allow-draft-profile`；技能安装同理只看 `kit`，且属于主人操作（ARCHITECTURE.md 2.2）。核实证据的细化（官方测试、在用户这里成功过、用户标记通过）另行讨论；界面写明"恢复尚未在此版本测试"与可能的影响，而不是只写"未核实"。旧 profile 没有 `capabilities` 时，按 `status` 推断：`verified` 视为全部 `tested`，`draft` 视为全部 `untested`。

- **审批状态检查**（2026-11-02 用户决定）：profile 可以声明 `approval_check`，用一条只读命令或一个配置键读出 harness 执行风险命令时是否需要用户批准，结果为"需要批准"或"无需批准（Bypass）"；与其他条目一样，未在靶机上核实时标 `verify: pending`。没有声明时，退回到 agent 经 MCP `valise_declare` 的申报（AGENT_KIT.md 第 3 节），界面标"未经核实"。
  - **schema**（M12 实现，2026-11-02 用户同意）：`approval_check: { argv, each, bypass_when, ask_when, verify }`。`argv` 是一条只读命令（只能引用 `${HOME}`），输出 JSON，由 porter 按服务器上保存的 profile 副本执行，走日常钥匙；`each` 可选，指向一个数组，逐项判断、取最差的结果；`bypass_when` 是"字段路径: 值"，全部相等才算无需批准；`ask_when` 是若干组条件，任一组全部相等就算需要批准；都不匹配为"不确定"。只做相等比较，不是解释器。读配置键的方式（不运行命令）留待需要时再加。
  - **出厂设置与修复**（2026-11-02 决定，2026-11-04 修订）：`default_when` 可选，形式同 `ask_when`（若干组“字段路径: 值”）；Bypass 结果命中时，界面注明“这是 harness 的出厂设置，不是你改的”。`remedy` 可选，是改为“执行前询问”的文字步骤，在没有可执行修复时继续用于手动引导。取消“不提供一键修改、不得登记审批修改动作”的旧限制：允许通过已核实的 profile 管理动作修复审批设置，遵循 INSTALL.md 第 5 节，执行前说明行为变化及必要的服务中断，执行后重新运行审批检查。健康发现与动作的关联字段、需要停止/重启时的编排与失败恢复方式，必须先明确 schema 和验收，再实现；本条不视为这些能力已经存在。
- **安装摘要**（2026-11-02 用户决定）：`install` 可以写 `where`（装到哪里）、`takes`（大概多久）与 `notes`（装之前要知道的事，例如 harness 出厂即为 Bypass），与 `summary` 一起组成方案开头的摘要（UI.md 9.6）。
- **agent 的名字**（2026-11-02 用户决定）：profile 可以声明 `identity: { argv, path, verify }`：一条只读命令（只能引用 `${HOME}` 与 `${STATE_DIR}`）输出 JSON，`path` 指向 harness 里给 agent 起的名字（例如 OpenClaw 的 `identity.name`）。只读这一项，不扫描、不解析其他文件内容。界面上的显示名优先用用户在 Valise 里起的（记在实例档案的 agent 条目 `display_name`），其次用这里读出的名字，都没有时只显示实例 id；实例 id 不随名字改变（UI.md 9 节"名字"）。
- **一个状态目录里的多个 agent**（2026-11-02 用户决定）：有的 harness 在一个状态目录里放多个 agent（OpenClaw 的 `agents.entries`，共用设置与网关）。暂时仍以一个状态目录为一个 Valise agent 备份与恢复，界面只列出其中的 agent 名单；是否拆开，等在靶机上研究清楚再定。
  - **与声明对照**：核实结果优先；agent 的声明与设置不符时两者都显示（设置为 Bypass 时红色，设置为需要批准而 agent 声称无需批准时黄色）。
  - **OpenClaw**：`openclaw exec-policy show --json` 的 `effectivePolicy.scopes[]`，`security.effective = full` 且 `ask.effective = off` 为无需批准。2026.10.9 默认即为此状态；预设 cautious（allowlist / on-miss）、deny-all（deny / off）以及 allowlist / off、full / always 都读作需要批准（均在 debian-vm 上读出，修改设置时需先停止网关）。
- **核实证据**（2026-11-02 用户决定，细节随 profile 专题与 M17 定稿）："已核实"按能力看三种证据：官方测试过（维护者在靶机上的记录，随资料包签名）、在用户这里成功过（Valise 自动记录这个 profile 版本与 harness 版本下的成功操作，如恢复或恢复演练）、用户标记通过（用户亲自试过后手动确认，与前两种区分显示）。官方 profile 与第三方 profile 同样适用，后者只是没有官方签名。

### 1.3 多实例（M12，2026-11-02 用户决定）

- **叫法**（2026-11-02 用户决定）：profile 可以写 `terms: { agent: "profile" }`，即 harness 自己对一个 agent 的叫法；界面按它显示（如"Hermes · 3 个 profile"），内部统一称 agent。按 agent 的事实记在实例档案里（ARCHITECTURE.md 4.1）。

- **发现**：`state_dir_candidates` 中第一个有标记文件的目录是默认实例；`detect.named` 每条 glob 匹配到、并且有标记文件的目录各是一个命名实例，名字取 `*` 匹配到的部分（小写字母、数字、连字符）。一个目录只算一个实例（按真实路径去重，真实目录优先于指向它的软链）。没有 `markers_any` 时，只有 `skills/`、日志、缓存的目录不算实例。
- **实例 id**：默认实例为 profile id，命名实例为 `<profile id>-<名字>`（如 `openclaw-work`），接入时可改。
- **选择变量**：`named[].env` 必须写明怎样让 harness 自己的命令针对这个实例（值只能引用 `${NAME}`、`${STATE_DIR}`）。登记时记入实例记录（`name`、`env`）与备份头；之后读取目录、管理动作、审批检查、登录都带上。必须在靶机上核实（OpenClaw 2026.10.9 只认 `OPENCLAW_STATE_DIR`，不认环境变量 `OPENCLAW_PROFILE`）。
- **命名实例共用安装**：按 agent 的有效 profile（`ForAgent`，由通用 profile 与实例档案现算；存的 profile 副本保持原样）去掉 `entries`、`body`、`provenance`（身体由默认实例负责）。`service`：`named[].service: shared` 表示命名实例运行在默认实例的服务里（一个网关服务所有实例），保留；不写时服务单元尚未核实（profile 里写的是默认实例的），去掉，Valise 不显示、不控制，恢复时也不停止或启动任何服务。恢复共用服务的命名实例会短暂停止这个服务，同一安装里的所有实例一起停下，计划里写明。
- **嵌套**：一个实例的状态目录里如果嵌着其他实例（Hermes 的 bot 都在 `~/.hermes/profiles/` 下；也包括其他已接入实例），它们不装进这个实例的备份。
  - 嵌套的都是本 profile 共用服务的命名实例时（2026-11-02 用户决定先核实服务名；Hermes v0.22.5 已核实），交换照常进行：停止共用服务即停下它们，交换窗口内把它们的目录原样改名搬进新活目录（计划的 `keep`，与身体目录同一套交换日志，失败时搬回），不复制、不替换，回滚复制档位外内容时跳过它们。
  - 其他情况（服务未核实的命名实例、别的 profile 的实例）仍拒绝原地交换，porter 在交换前再检查一次；恢复测试、单独恢复嵌在里面的实例、恢复到另一台服务器不受影响。
- **迁移或克隆命名实例**：Valise 不猜目标目录（默认目录属于另一个 agent），必须用 `--root state=<路径>` 指定；路径型的选择变量随新目录改写。

### 1.4 通用规则、本机事实与保护范围（2026-11-05）

- 按 profile 发现通用结构，由用户接受的 dossier 核对真实 roots、来源、共享服务及安装布局；剩余未知项继续按既有分类优先级处理。分类优先级仍以 ARCHITECTURE.md 第 4 节为准，不能把“先看档案”解释为覆盖 secrets 声明。
- 官方 profile 不枚举每台机器的绝对链接或安装路径。布局探测只提供证据，用户接受不等于已验证管理命令的暂存隔离性；未知外部目标不自动跟随。具体 dossier 扩展及 B03 链接处置仍待定稿，不能把候选提案当已实现能力。
- 新计划 full 范围包含既有 roots 内的 unclassified，并按敏感层保存；用户可以确认接入而不逐文件归类。不可读项、roots 外内容与外部引用单列为覆盖缺口，不能把已保存链接文本称为已保存目标。
- 保护计划、自定义包含/排除与类别是不同模型。`local_overrides` 仍只补充分类；不得为实现“自定义备份”伪造类别、放宽 secrets 优先级或改写 raw 的原含义。
- N1 / N5b 在引入新范围前定稿匹配、SQLite 单位、快照范围记录、回滚携带/删除语义及旧工具拒绝策略；本文件 schema 和 lint 未扩展前不在 YAML 中使用假定字段。
- full / raw 不等于可重建所有程序。来源记录与能力核实继续逐项检查，目录外程序、版本不可得与架构限制如实报告；通过备份测试不自动将 restore 标为 tested。

## 2. Schema

```yaml
schema: valise-profile/1
id: example                      # 小写字母、数字和连字符，全局唯一
display_name: Example
profile_version: 1               # profile 内容每次变更都要加 1
status: draft                    # draft | verified
harness_versions_tested: []      # 例如 ["2026.10.3"]，填写核实时使用的版本
docs: https://…                  # 官方文档地址，只作参考，Valise 不会抓取
display:                         # 可选：界面上的实例卡片图标（2026-11-01 用户决定新标准）
  color: "#E8603C"               # 主色，#RRGGBB；图标只用这一种颜色
  icon: |                        # 标准图标：最多 12 行 × 24 列（显示宽度），按 UI.md 8.1 原样显示，不缩放
    ...
  icon_compact: |                # 可选，紧凑图标：最多 6 行 × 12 列；用于窄终端与实例页标题
    ...
  icon_source: "…"               # 出处：据哪个官方图案改画、其许可证；未核实时写 verify: pending

capabilities: {}                 # 按能力的核实状态，见 1.2 节

extends: null                    # 子 profile 填写父 profile 的 id，见第 4 节
when: null                       # 子 profile 的生效条件，例如 { exists: { root: state, glob: "x/y.json" } }

detect:
  binaries: [example]
  version_argv: [example, --version]
  state_dir_candidates: ["${env:EXAMPLE_HOME}", "${HOME}/.example"]   # 按顺序，取第一个存在的
  markers_any: [config.yaml]     # 状态目录中至少要存在其中一个
  home_env: EXAMPLE_HOME          # 可选：harness 自己的家目录变量，设置时代替候选与 named 中的 ${HOME}
  named:                          # 可选：同一份安装里的其他实例（多实例，见下）
    - { glob: "${HOME}/.example-*", env: { EXAMPLE_STATE_DIR: "${STATE_DIR}" } }

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

install:                         # 可选：官方方案（INSTALL.md 第 3 节），只写已核实的版本
  id: npm-user-prefix
  summary: "安装 Example 1.2.3，约 300 MB，不需要管理员权限"
  requires:
    - binary: node
      version: ">=22"
      install:                   # 可选：只在高级模式下由 Valise 代为执行（INSTALL.md 4.1），按发行版区分
        - { os_id: [ubuntu, debian], argv: [sudo, apt-get, install, -y, nodejs] }
  steps:
    - { kind: npm_global, package: example, version: "1.2.3", integrity: "sha512-…" }
    - { kind: script, url: "https://example.com/install.sh", sha256: "…", interpreter: [bash], args: ["--user"], runtime_downloads: "会从 example.com 下载 Node 运行时" }
    - { kind: argv, argv: [example, setup, --baseline] }
    - { kind: path_add, dir: "${HOME}/.example/bin", optional: true, id: path,
        skip_effect: "以后自己 ssh 登录时直接输入 example 会提示找不到命令，需要写完整路径或用 valise shell <服务器> --profile example" }
    # 任何步骤都可以 optional: true（必须同时写 id 与 skip_effect），可选步骤默认不执行（INSTALL.md 第 3 节）

native_cli: [example, onboard]   # 可选：原生界面的命令（INSTALL.md 第 2 节）
native_hints:                    # 可选：打开原生界面前显示的提示（INSTALL.md 第 2 节），只是文字
  - "引导最后会等待控制台登录：另开终端运行 valise open <服务器> <实例>"

actions:                         # 可选：管理动作（INSTALL.md 第 5 节；M12 定稿）
  - id: status                   # 小写字母、数字和连字符，profile 内唯一
    name: 查看状态                # 界面上的名称
    explain: "显示服务是否在运行"   # 对用户的解释：会发生什么
    risk: read                   # read | manage | config | danger
    argv: [example, status]      # 只能引用 ${HOME}
    output: text                 # text | none | secret
  - id: copy-token
    name: 复制控制台令牌
    explain: "把控制台登录用的令牌复制到剪贴板（不显示）"
    risk: read
    argv: [example, dashboard, --json]
    output: secret
    secret: { json_field: url, url_param: token }   # 从输出取出机密本身；都不写时取第一行
  - id: reset-token
    name: 生成新令牌
    explain: "生成新的控制台令牌"
    undo: "无法撤销；旧令牌立即失效"  # 可选：能否撤销、怎样撤销
    risk: config
    argv: [example, token, --rotate]
    output: none
    terminal: false              # 可选：true 时在独立交互终端运行，输出不经 Valise，不能是 secret

relocation: { env: EXAMPLE_HOME }   # 状态目录可以通过此变量改到别处；不支持时写 null（此时克隆只能到另一个用户或另一台服务器）

service:
  candidates: [ { scope: user, unit: example-gateway.service } ]   # 可以为空，表示前台运行
  install_argv: [example, gateway, install]   # 可选：harness 自带的服务安装命令；复活到没有该服务的服务器时，经用户确认后执行
  repair_action: restart          # 可选：指向本 profile 已核实的 actions[].id；服务异常时提供该动作入口（B02）

entries:
  - id: main
    kind: web
    host: 127.0.0.1
    port: 8080
    path: "/"
    auth:                        # 可选：管理页需要令牌时，如何由 harness 自己的命令取得（LOGIN_RELAY.md 4.1）
      url_argv: [example, dashboard, --print-url]   # 打印一个带令牌的管理页地址；与 token_argv 二选一
      url_field: null            # 可选：url_argv 输出 JSON 时，地址所在的点分字段（例如 browserUrl）
      token_argv: null           # 只打印令牌本身；配合 url_suffix 使用
      url_suffix: null           # 例如 "#token=${TOKEN}"，只能引用 ${TOKEN}
      hint: "…"                  # 没法自动取得时给用户看的说明（例如"运行 example token --show，粘贴到页面的密钥栏"）
    port_change:                 # 可选：本机端口被占用时"两边一起改端口"（LOGIN_RELAY.md 4.2）
      steps:                     # harness 自己的改端口命令，按顺序运行；argv 只能引用 ${PORT} 与 ${HOME}
        - [example, config, set, port, "${PORT}"]
      restart_service: true      # 改完是否还需要 Valise 重启服务（命令自己会重装/重启服务时填 false）
    # 改过端口后，新端口记在实例记录的 entry_ports 里（按入口 id），打开管理页、健康检查都用它；
    # profile 中的 port 只是默认值。

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
  # token 在 JSON 列里时，用 tokens_json: { column, path }（点分路径，经 json_extract 读取），与 tokens_column 二选一。
  # token 按会话汇总在另一张表时，加 usage: { table, session_column, tokens_columns: [...], time_column, time_unit }，
  # 用它代替每行的 tokens_column 与字节估算；每日用量按 time_column（最后更新时间）归日。
  thresholds: {}                 # 可选，覆盖体检默认阈值（AGENT_KIT.md 8.4）
  remedies:                      # 可选：每种发现对应的 harness 原生处理方式，只展示给用户（AGENT_KIT.md 8.6）
    long_session: ["在对话中发送 /new 开始新会话；想保留上下文就先让 agent 做一次交接"]
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
  mcp_config_snippet: null       # 可选：harness 没有添加命令时，打印给用户粘贴的配置片段（可用 ${PORTER}、${INSTANCE}）
  mcp_remove_argv: [example, mcp, remove, valise]   # 可选：kit uninstall 时打印的撤除命令，Valise 不执行（可用 ${INSTANCE}）
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
- `display.icon` 最多 12 行、每行显示宽度不超过 24；`display.icon_compact` 最多 6 行、每行不超过 12。只允许单宽字符（推荐盲文 U+2800–U+28FF，空白用 `⠀`；也可用框线与 ASCII），不得包含全角字符、emoji、制表符、控制字符或 ANSI 转义；`display.color` 必须是 `#RRGGBB`。图标原样显示、不在运行时缩放；只有标准图标时，紧凑场景按点缩放兜底（UI.md 8.1）。
- `install.steps` 中 `script` 必须有 `sha256` 与 `runtime_downloads`（没有时写空字符串表示已确认没有）；`url` 必须是 `https`；所有命令都是 argv 数组。
- `capabilities` 中状态为 `tested` 的项必须有 `versions` 与 `env`。
- `actions`：`id` 唯一且合法；`name`、`explain` 不能为空；`risk` 与 `output` 只能取规定的值；`argv` 只能引用 `${HOME}`；`output: secret` 必须写 `secret`（`url_param` 只能与 `json_field` 一起出现），其他 `output` 不能写 `secret`；`terminal: true` 不能与 `secret` 同时出现；`config` 与 `danger` 必须写 `undo`。
- `service.repair_action` 非空时必须有服务候选，引用本 profile 中存在的动作；B02 只允许非交互、非机密的 `manage` 动作。关联本身表示维护者已核实此动作可处理所声明服务的问题；尚未核实的动作不得填入关联。命名实例的有效 profile 去掉服务时，一并去掉此入口，不通过动作名猜测服务关系。客户端资料与服务器登记版本不同时，先重新接入，再确认执行；不根据新资料的说明执行旧版本命令。
- `entries[].auth` 中 `url_argv` 与 `token_argv` 最多出现一个；`url_suffix` 只能与 `token_argv` 一起出现，并且只能引用 `${TOKEN}`；`url_field` 只能与 `url_argv` 一起出现，必须是点分字段路径。
- profile 使用严格解析，出现 schema 中没有的字段即报错。子 profile 只能包含元信息字段（`schema`、`id`、`display_name`、`profile_version`、`status`、`harness_versions_tested`、`docs`、`extends`、`when`）以及 `paths`、`consistency`、`checkup`；它的父 profile 必须出现在同一次 lint 的文件集合中。

### 3.1 B03 配置事务（2026-11-05 用户确认）

动作的 `argv` 与 `steps` 互斥。`steps` 为 1–16 个 `{id, argv}` 固定步骤，必须同时声明 `transaction: {root: state, env: {变量名: "${STAGE_DIR}[/相对路径]"}, writes: [相对文件或目录]}`；只允许非交互、`output: none`、`risk: config` 且有 `undo` 的动作。`verification: {versions: [精确版本], env, date}` 记录完整事务证据；未核实时写 `verify: pending`，不提供执行入口。重定向与 writes 必须覆盖所有副作用，不把声明当成系统沙箱。

`approval_check` 可声明 `repair_action` 与 `repair_expect`（字段路径到目标值的 map），两者同时存在；动作必须是已核实事务，审批读法不可 pending，当前 harness 精确版本必须命中证据。每个非空 scope 必须满足 repair_expect 的全部值，空 scope、缺字段、unknown 不通过。暂存与发布后的复核都使用该目标，不能仅凭 ask 分类确认成功。步骤只能在暂存环境运行，首错停止，不自动重跑结果不明的步骤。

服务启停、目录交换、失败恢复与中断记录由固定引擎执行，profile 不提供条件、循环或任意 cleanup 命令。原本 inactive 的服务不启动；命名实例仍按 ForAgent 处理。完整执行、兼容、恢复与验收语义以用户确认的 [B03 设计](history/B03_DESIGN_PROPOSAL_2026-11-05.md) 第 3–7 节为本次实现依据。

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

`ubuntu-vm-x86` 上的 Hermes 还有 14 个文件没有任何规则（`shared-state.db`、`source-checks/`、`gateway_state.json`、`state/`、`spawn-ledger.json`、`gateway-starts.log`、`context_length_cache.yaml`、`install_id`、`plugin-update-checks/`、`kanban/`、`runtime/` 等，合计约 158 KiB）。这是 2026-10-30 的观察与当时 soul 范围下的处理记录。当前这些内容保持 unclassified，不凭文件名编造分类；soul-secrets / full / raw 按既有规则将其放入敏感层，新默认 full 包含它们。用户可自愿用 `--assign` 补充归类，接入不再要求先逐文件归类。

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
