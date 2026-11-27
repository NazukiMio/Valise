# Harness Profile 规范 `valise-profile/1`

Profile 是 Valise 了解某种 harness **通用规则的唯一途径**：发现、路径与分类、来源、安装、服务、管理页、登录、健康检查、备份、会话记忆检查、Agent 保护技能与管理动作。服务器 dossier 补足实际安装事实，不是第二套 harness 解释器。Go 代码不得硬编码具体 harness 知识；新增适配通过 profile 与必要的通用 schema 扩展，逐能力核实。

> 2026-11-05：产品默认备份收敛到保护计划，范围预设、凭据分类与本文件现有 schema 保持兼容。本轮没有新增布局、自定义范围或身份字段；实例世代与迁移关联按已定稿 SERVICE_CONTRACTS.md 保存于备份库外的 Valise 身份记录，不向本 profile schema 添加身份字段；自定义详细语义仍留 N5b。界面用语以 PRODUCT.md 8.1 为准。

## 1. 来源与信任

- 官方 profile 存放在仓库的 `profiles/` 目录下，经过 PR 审核后打成**资料包**单独签名发布（1.1 节）。客户端本身不内置任何 profile。
- 用户可以在客户端数据目录下的 `profiles/` 放置本地 profile（与 `os.UserConfigDir()` 对齐：macOS 为 `~/Library/Application Support/valise/profiles/`，Linux 为 `${XDG_CONFIG_HOME:-~/.config}/valise/profiles/`）。这类 profile 在 UI 中始终标记为"本地，未经审核"。本地 profile 与官方资料包中的 profile 同 `id` 时，**允许本地文件显式覆写同 `id` 的官方 profile**（便于调试新版本或抢先适配上游变更）；界面始终醒目标注为"本地覆写，未经审核"，展示与官方版本的能力差异，并提供一键移除本地覆写以立即恢复官方资料包版本。

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

- **审批状态检查**（2026-11-02 用户决定；2026-11-19 收口）：profile 可以声明 `approval_check`，用一条只读命令或一个配置键读出 harness 执行风险命令时是否需要用户批准，结果为"需要批准"或"无需批准（Bypass）"；与其他条目一样，未在靶机上核实时标 `verify: pending`。没有声明 `approval_check` 时在健康页诚实标为不支持该项检查，不再采信 agent 经 `valise_declare` 的自报声明。
  - **schema**（M12 实现，2026-11-02 用户同意）：`approval_check: { argv, each, bypass_when, ask_when, verify }`。`argv` 是一条只读命令（只能引用 `${HOME}`），输出 JSON，由 porter 按服务器上保存的 profile 副本执行，走日常钥匙；`each` 可选，指向一个数组，逐项判断、取最差的结果；`bypass_when` 是"字段路径: 值"，全部相等才算无需批准；`ask_when` 是若干组条件，任一组全部相等就算需要批准；都不匹配为"不确定"。只做相等比较，不是解释器。读配置键的方式（不运行命令）留待需要时再加。
  - **出厂设置与修复**（2026-11-02 决定，2026-11-04 修订）：`default_when` 可选，形式同 `ask_when`（若干组“字段路径: 值”）；Bypass 结果命中时，界面注明“这是 harness 的出厂设置，不是你改的”。`remedy` 可选，是改为“执行前询问”的文字步骤，在没有可执行修复时继续用于手动引导。取消“不提供一键修改、不得登记审批修改动作”的旧限制：允许通过已核实的 profile 管理动作修复审批设置，遵循 INSTALL.md 第 5 节，执行前说明行为变化及必要的服务中断，执行后重新运行审批检查。健康发现与动作的关联字段、需要停止/重启时的编排与失败恢复方式，必须先明确 schema 和验收，再实现；本条不视为这些能力已经存在。
- **安装摘要**（2026-11-02 用户决定）：`install` 可以写 `where`（装到哪里）、`takes`（大概多久）与 `notes`（装之前要知道的事，例如 harness 出厂即为 Bypass），与 `summary` 一起组成方案开头的摘要（UI.md 9.6）。
- **agent 的名字**（2026-11-02 用户决定）：profile 可以声明 `identity: { argv, each, pick, name, verify }`：一条只读命令（只能引用 `${HOME}` 与 `${STATE_DIR}`）输出 JSON，`each`（可选，点分路径）指向对象数组，`pick`（可选，字段相等匹配）选中当前实例所属条目，`name`（必填，点分路径）指向 harness 里给 agent 起的名字（例如 OpenClaw 的 `identityName`）。只读这一项，不扫描、不解析其他文件内容。界面上的显示名优先用用户在 Valise 里起的（记在实例档案的 agent 条目 `display_name`），其次用这里读出的名字，都没有时只显示实例 id；实例 id 不随名字改变（UI.md 9 节"名字"）。
- **一个状态目录里的多个 agent**（2026-11-02 用户决定）：有的 harness 在一个状态目录里放多个 agent（OpenClaw 的 `agents.entries`，共用设置与网关）。暂时仍以一个状态目录为一个 Valise agent 备份与恢复，界面只列出其中的 agent 名单；是否拆开，等在靶机上研究清楚再定。
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
- **迁移或创建命名副本（2026-11-11 问卷确认，待实现）**：GUI 按已核实的 profile 创建/命名规则与目标布局安排位置，普通用户只填分身名称，需要时再展开换服务器或自定义位置；不盲填、不把默认实例目录当作副本位置。CLI 的 `--root state=<路径>` 继续兼容；当前执行器仍要求显式指定，GUI 自动安排尚待适配。路径型选择变量随新目录改写，同机副本还需正确的新名称、选择变量、服务/端口及登录隔离。`detect.named` 的发现规则本身不证明创建、启用已核实；具体创建/启用适配 schema 尚待设计与靶机验证，不在本次问卷中宣称已有。

### 1.4 通用规则、本机事实与保护范围（2026-11-05）

- 按 profile 发现通用结构，由用户接受的 dossier 核对真实 roots、来源、共享服务及安装布局；剩余未知项继续按既有分类优先级处理。分类优先级仍以 ARCHITECTURE.md 第 4 节为准，不能把“先看档案”解释为覆盖 secrets 声明。
- 官方 profile 不枚举每台机器的绝对链接或安装路径。布局探测只提供证据，用户接受不等于已验证管理命令的暂存隔离性；未知外部目标不自动跟随。具体 dossier 扩展及 B03 链接处置仍待定稿，不能把候选提案当已实现能力。
- 新计划 full 范围包含既有 roots 内的 unclassified，并按敏感层保存；用户可以确认接入而不逐文件归类。不可读项、roots 外内容与外部引用单列为覆盖缺口，不能把已保存链接文本称为已保存目标。
- 保护计划、自定义包含/排除与类别是不同模型。Dossier 的 `local_overrides`、`local_sqlite` 与 `external_refs` 仅在人类审批 `valise_propose_dossier` 提案或显式 CLI 参数时更新（详情见 ARCHITECTURE.md 第 4.1 节）；不得为实现“自定义备份”伪造类别、放宽 secrets / `secret_tables` 优先级或改写 raw 的原含义。
- N1 / N5b 在引入新范围前定稿匹配、SQLite 单位、快照范围记录、回滚携带/删除语义及旧工具拒绝策略；本文件 schema 和 lint 未扩展前不在 YAML 中使用假定字段。
- full / raw 不等于可重建所有程序。来源记录与能力核实继续逐项检查，目录外程序、版本不可得与架构限制如实报告；通过备份测试不自动将 restore 标为 tested。
- **Profile 与 Dossier（`valise-onboard`）分工边界**：Profile 只描述跨用户、跨机器稳定的通用规则（探测、roots、显式 `secrets`/`exclusive_secrets` 白名单、`body.paths`、安装来源 `provenance`、核心 SQLite 与 `secret_tables`、高置信 `cache`/`ignore`、服务/入口/健康检查/命令行与向导/Agent 保护技能）。单机特有的自装插件、个人工作区子目录或零散小文件不要硬塞进 Profile——`full` 默认已按敏感层兜底保存 roots 内未分类文件，用户可在接入后通过 `valise-onboard` 报备进 Dossier。完整的编写心智模型、排查坑点与离线校验流程见 [PROFILE_AUTHORING.md](PROFILE_AUTHORING.md)。

### 1.5 已废弃：并行运行环境声明（`environments`，2026-11-15 撤销）

> **DEPRECATED（2026-11-15）**：`environments`（`engine: linux-bwrap`、`runtime_executable`、`offline_readiness` 等 A/B 并行槽位字段）已随 A/B 双槽位架构于 2026-11-15 正式撤销，官方与用户 profile 中不再配置该字段，并从 `profilelint` / schema 中移除。恢复与健康页恢复测试统一使用标准的 `detect`、`paths`、`consistency`、`provenance`、`service`、`entries` 与 `health` 声明。历史说明见 `docs/history/RESTORE_ENVIRONMENTS_DEPRECATED_2026-11-15.md`。

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
  logo:                          # 可选，桌面界面用的官方图标（2026-11-08 用户决定）；TUI 不用
    svg: |                       # 官方发布的 SVG 原文，随签名资料包分发，界面不联网抓取
      <svg …>…</svg>
    source: "https://…"          # 取自哪里（官方仓库或品牌资源页的固定地址）
    license: "…"                 # 许可或品牌使用条款；不确定时写 verify: pending，不分发

capabilities: {}                 # 按能力的核实状态，见 1.2 节

extends: null                    # 子 profile 填写父 profile 的 id，见第 4 节
when: null                       # 子 profile 的生效条件，例如 { exists: { root: state, glob: "x/y.json" } }

detect:
  binaries: [example]
  version_argv: [example, --version]
  bin_dirs: ["${HOME}/.example/bin"]   # 可选：私有安装目录下的可执行文件夹，在登录 PATH 前查找
  state_dir_candidates: ["${env:EXAMPLE_HOME}", "${HOME}/.example"]   # 按顺序，取第一个存在的
  markers_any: [config.yaml]     # 状态目录中至少要存在其中一个
  home_env: EXAMPLE_HOME          # 可选：harness 自己的家目录变量，设置时代替候选与 named 中的 ${HOME}
  named:                          # 可选：同一份安装里的其他实例（多实例，见下）
    - { glob: "${HOME}/.example-*", service: shared, env: { EXAMPLE_STATE_DIR: "${STATE_DIR}" } }   # service 可选 shared 或省略

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
  npm:
    package: example             # kind: npm_global 时必填
    roots: ["${HOME}/.example/node_modules"]   # 可选：私有安装器存放 node_modules 的目录列表（${HOME} / ${STATE_DIR}）
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
  where: "${HOME}/.example"      # 可选：展示安装位置摘要
  takes: "约 1–3 分钟"            # 可选：展示大致耗时
  notes:                         # 可选：装前需知（例如出厂审批默认模式）
    - "默认开启独立配置目录"
  requires:
    - binary: node
      version: ">=22"
      why: "运行主程序"           # 可选：说明依赖用途
      install:                   # 可选：只在高级模式下由 Valise 代为执行（INSTALL.md 4.1），按发行版区分
        - { os_id: [ubuntu, debian], argv: [sudo, apt-get, install, -y, nodejs] }
  steps:
    - { kind: npm_global, package: example, version: "1.2.3", integrity: "sha512-…" }
    - { kind: script, url: "https://example.com/install.sh", sha256: "…", interpreter: [bash], args: ["--user"], runtime_downloads: "会从 example.com 下载 Node 运行时" }
    - { kind: argv, argv: [example, setup, --baseline] }
    - { kind: path_add, dir: "${HOME}/.example/bin", optional: true, id: path,
        skip_effect: "以后自己 ssh 登录时直接输入 example 会提示找不到命令，需要写完整路径或用 valise shell <服务器> --profile example" }
    # 任何步骤都可以 optional: true（必须同时写 id 与 skip_effect），可选步骤默认不执行（INSTALL.md 第 3 节）

native_cli: [example, onboard]   # 可选：原生引导命令，用于首次初始化与配置向导（INSTALL.md 第 2 节）
native_session_cli: [example, tui]   # 可选：原生会话命令，用于日常连接运行中的实例交互（INSTALL.md 第 2 节）
native_hints:                    # 可选：打开原生界面前显示的提示（INSTALL.md 第 2 节），只是文字
  - "引导最后会等待控制台登录：另开终端运行 valise open <服务器> <实例>"

actions:                         # 可选：管理动作（INSTALL.md 第 5 节；M12 定稿；B03 补配置事务）
  - id: status                   # 小写字母、数字和连字符，profile 内唯一
    name: 查看状态                # 界面上的名称
    explain: "显示服务是否在运行"   # 对用户的解释：会发生什么
    risk: read                   # read | manage | config | danger
    argv: [example, status]      # 单步动作只能引用 ${HOME}；与 steps/transaction 互斥
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
  - id: enable-approval          # B03 配置事务示例（1–16 步；config + none + undo）
    name: 开启危险命令前确认
    explain: "写入要求确认的审批配置（暂存核对后切换，支持回退）"
    undo: "恢复前保留旧状态目录，失败可直接回退"
    risk: config
    output: none
    steps:
      - { id: write, argv: [example, config, set, approval, ask] }
    transaction:
      root: state
      env: { EXAMPLE_HOME: "${STAGE_DIR}" }
      writes: [config.yaml]
      scratch: [logs]
    verification:
      versions: ["1.2.3"]
      env: "Debian 13 arm64"
      date: "2026-11-05"
    verify: pending              # 未在当前精确版本实机核实时标 pending（有 verify: pending 时不开放入口）

approval_check:                  # 可选：读取运行时的风险命令审批模式（PROFILES.md 1.2）
  argv: [example, exec-policy, show, --json]
  each: scopes                   # 可选：点分路径指向对象数组，逐项检查取最坏结果
  bypass_when: { security: full, ask: off }
  ask_when:
    - { security: allowlist, ask: on-miss }
  default_when:                  # 可选：出厂设置即为 Bypass 的条件组合
    - { security: full, ask: off }
  remedy: ["运行 example config set approval ask"]
  repair_action: enable-approval # 可选：关联本 profile 已核实的 B03 配置事务动作
  repair_expect: { security: allowlist, ask: on-miss }
  verify: pending

identity:                        # 可选：从只读 JSON 读取 harness 给 agent 起的名字
  argv: [example, agents, list, --json]
  each: agents                   # 可选：列表字段名；输出本身就是 JSON 数组时省略
  pick: { isDefault: "true" }    # 可选：相等匹配字段
  name: name                     # 必填：名字字段的点分路径
  verify: pending

terms: { agent: agent }          # 可选：harness 对单个 agent 的自有称呼（如 profile），不超过 20 字

relocation: { env: EXAMPLE_HOME }   # 状态目录可以通过此变量改到别处；不支持时写 null（同机副本需另有已核实的命名实例/隔离方案，否则只能到另一用户或服务器）

service:
  candidates: [ { scope: user, unit: example-gateway.service } ]   # 可以为空，表示前台运行
  install_argv: [example, gateway, install]   # 可选：harness 自带的服务安装命令；复活到没有该服务的服务器时，经用户确认后执行
  repair_action: restart          # 可选：指向本 profile 已核实的 actions[].id；服务异常时提供该动作入口（B02）

entries:
  - id: main
    display_name: "管理面板"        # 可选：界面展示的入口名称（缺省时显示 id）
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
      steps:                     # harness 自己的改端口命令，按顺序运行；argv 只能引用 ${PORT}
        - [example, config, set, port, "${PORT}"]
      restart_service: true      # 改完是否还需要 Valise 重启服务（命令自己会重装/重启服务时填 false）
    prepare:                     # 可选：入口按需启用/构建准备（LOGIN_RELAY.md 4.3）
      summary: "启用管理面板配置并重启服务"
      check_paths: []            # 可选：相对实例 state 根的检查路径列表（禁止 .. 与 / 开头绝对路径）
      check_argv: [example, config, get, web.enabled]   # 可选：只读检查命令（daily）
      expect_output: "true"      # 可选：配合 check_argv，去首尾空白后精确匹配
      steps:                     # 必填：按顺序直调的准备命令列表（admin，无 shell 包装）
        - [example, config, set, web.enabled, "true"]
      restart_service: true      # 可选：准备完成后是否重启实例 systemd 服务
      verify: pending            # 未实机核实时标 pending
    start:                       # 可选：入口脱离终端的按需后台启动与显式停止（LOGIN_RELAY.md 4.3）
      summary: "在服务器后台启动管理面板"
      argv: [example, dashboard, --no-open]   # 必填：后台启动命令（owner）
      stop_argv: null            # 可选：显式停止命令（owner）；未声明时直接停止 Valise 记录的单元或进程组
      wait_seconds: 15           # 可选：启动后轮询端口监听的超时秒数（默认 15）
      verify: pending            # 未实机核实时标 pending
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
  export_argv: null              # 可选：调用 harness 自带的导出功能，产物作为附加文件存入备份库（${OUT} 为输出路径）

# checkup 与 handoff 自 2026-11-19 起标为 deprecated 可选兼容字段：记忆与跨会话治理交由独立套件 ValiseMemo / v0.2+；
# 已发布 profile 与历史资料包保留向后兼容解析，新编写 profile 不再要求配置这两节。
checkup:                         # deprecated (可选兼容)
  sessions: { root: state, glob: "sessions/*.jsonl", format: jsonl, usage_fields: null, time_field: null }
  # format: sqlite 时改用 sqlite 子项（标识符只能是字母、数字、下划线）：
  # sessions: { root: state, glob: "state.db", format: sqlite,
  #             sqlite: { table: messages, session_column: session_id, time_column: timestamp, time_unit: s,
  #                       tokens_column: token_count, size_column: null, bytes_columns: [content] } }
  # 每行的大小取 size_column（非空时），否则为 bytes_columns 各列 length() 之和。
  # token 在 JSON 列里时，用 tokens_json: { column, path }（点分路径，经 json_extract 读取），与 tokens_column 二选一。
  # token 按会话汇总在另一张表时，加 usage: { table, session_column, tokens_columns: [...], time_column, time_unit }，
  # 用它代替每行的 tokens_column 与字节估算；每日用量按 time_column（最后更新时间）归日。
  thresholds: {}                 # 可选，覆盖体检默认阈值
  remedies:                      # 可选：每种发现对应的 harness 原生处理方式
    long_session: ["在对话中发送 /new 开始新会话"]
  memory_files: [ { root: state, glob: "MEMORY.md", warn_lines: 800 } ]
  memory_mechanism_markers: [ { root: state, glob: "memories/*.md" } ]
  native_options_doc: https://…  # 可选

handoff:                         # deprecated (可选兼容)
  targets: [ { root: state, glob: "MEMORY.md", mode: append_section, max_lines: 120 } ]
  new_session_hint: "在对话中发送 /new"

agent_kit:
  skills_dir: { root: workspace, path: "skills/" }
  mcp_config_doc: https://…      # 如何为该 harness 配置 MCP Server（参考文档链接）
  mcp_add_argv: [example, mcp, add, valise, --command, "${PORTER}", --arg=mcp, --arg=--instance, "--arg=${INSTANCE}"]   # 可选：kit install 预览并默认经用户确认后在独立管理连接执行，同时保留在折叠区手动复制
  mcp_config_snippet: null       # 可选：harness 没有添加命令时，展示给用户粘贴的配置片段（可用 ${PORTER}、${INSTANCE}）
  mcp_remove_argv: [example, mcp, remove, valise]   # 可选：kit uninstall 预览并默认经用户确认后在独立管理连接执行，同时保留在折叠区手动复制（可用 ${INSTANCE}）
```

变量说明：`${HOME}` 表示用户主目录；`${STATE_DIR}` 表示解析后的状态目录；`${env:NAME}` 表示环境变量，变量未设置时这一项视为不存在；`${OUT}` 只在 `export_argv` 中使用。

## 3. `profilelint` 的校验规则

`profilelint`（及 `valise profiles lint`、`./scripts/verify-profile.sh`）将检查结果分为**硬错误（Errors）**与**软警告（Warnings）**，并可经 `--commands` 输出全部可执行入口的命令审计清单。

### 3.1 硬错误规则（Errors，阻断打包 / 加载 / 导入）

出现任意硬错误时，资料包打包、客户端加载与本地导入一律拒绝：

- **严格解码**：YAML 启用 `KnownFields(true)`，出现 schema 未定义的字段名即报错（例如 `identity` 只有 `name` 没有 `path`）。
- **路径与根目录**：所有路径必须相对于某个已声明的 `root`，禁止出现 `..`；除 `${HOME}` 与 `${env:…}` 展开得到的路径外，不允许出现绝对路径。glob 以 `/` 分隔路径段，`**` 匹配零个或多个路径段，其余段使用 Go `path.Match` 语法。
- **分类与冲突边界**：
  - `category` 必须是 ARCHITECTURE.md 第 5 节中列出的值。
  - 声明为 `secrets` 的 glob 不得与同一 root 下的 `body`、`cache`、`ignore`（含 `body.paths`）冲突（同字符串，或 `secrets` 为字面路径且能被对方匹配）。
  - `exclusive_secrets` 不得与同一 root 下的 `body`、`cache`、`ignore`（含 `body.paths`）冲突。
  - `consistency.sqlite` 不得与同一 root 下的 `body`、`cache`、`ignore`（含 `body.paths`）冲突；`secret_tables` 表名仅允许字母、数字与下划线。
- **`body.paths` 与 `paths` 的 body 约束**：
  - `body.paths[].glob` 必须写成 `<字面目录>/**` 形式（不含通配符的非空目录前缀加 `/**`，与恢复期目录搬动前置条件一致）。
  - `paths` 中若声明 `category: body`，同一 `(root, glob)` 必须同时出现在 `body.paths` 中，否则恢复时无法搬动身体目录（推荐仅在 `body.paths` 声明，不再重复写入 `paths`）。
- **命令与变量安全**：
  - 所有命令必须写成 argv 数组，禁止 shell 字符串，且只能使用各自字段允许的变量占位符。
  - 所有可执行 `argv` 禁止以 shell 包装器（如 `sh -c`、`bash -lc`、`zsh -ec` 等，含经 `env`/`sudo`/`nohup` 等前缀包裹的形式）执行命令字符串，也禁止任意参数包含命令替换 `$(` 或反引号 `` ` ``（`install.steps[].interpreter: [bash]` 本身不含 `-c` 属于合法解释器声明）。
- **核实状态与能力**：
  - `verify` 字段只允许取值 `pending`；`profilelint` 同时统计字段与 YAML 注释中的 `verify: pending`。
  - `status: verified` 禁止存在任何 `verify: pending`，且 `harness_versions_tested` 不能为空。
  - `capabilities` 中状态为 `tested` 的项必须同时填写 `versions` 与 `env`。
- **视觉标识**：
  - `display.logo.svg` 只接受不超过 64 KiB 的纯 SVG 文本（禁止 `<script>`、事件属性、外部引用与 `<foreignObject>`），且必须写明 `source` 与 `license`（不构成背书，AGENTS.md 7.6）。
  - `display.icon` 最多 12 行 × 24 列、`display.icon_compact` 最多 6 行 × 12 列，仅允许单宽字符（推荐盲文 U+2800–U+28FF、框线与 ASCII），禁止全角字符、emoji、制表符、控制字符与 ANSI 转义；`display.color` 必须为 `#RRGGBB`。
- **安装、入口与动作约束**：
  - `install.steps` 中 `script` 必须有 `https://` `url`、64 位十六进制 `sha256`、`interpreter` 与 `runtime_downloads`（确认无额外下载时写 `""`）。
  - `entries[].auth` 中 `url_argv` 与 `token_argv` 最多出现一个；`url_field`（点分路径）仅可与 `url_argv` 同用；`url_suffix`（仅允许 `${TOKEN}`）仅可与 `token_argv` 同用。
  - `entries[].prepare`：`check_paths` 必须为相对实例 `state` 根的相对路径（禁止 `..`、`/` 开头绝对路径与变量占位符）；`steps` 非空；`expect_output` 仅可与 `check_argv` 同用；`check_argv` 与每条 `steps[]` 均须通过可执行命令与 shell 包装器安全审计（仅允许 `${HOME}`、`${STATE_DIR}`）；`verify` 只允许 `pending` 或省略。
  - `entries[].start`：`argv` 必填，`stop_argv` 可选，均须通过可执行命令与 shell 包装器安全审计（仅允许 `${HOME}`、`${STATE_DIR}`）；`wait_seconds` 不能为负；`verify` 只允许 `pending` 或省略。
  - `entries[].port_change`：`steps` 非空、必须引用 `${PORT}`，且全部通过可执行命令与 shell 包装器安全审计。
  - `actions`：`id` 唯一且合法；`name`、`explain` 非空；`risk`、`output` 取值合法；单步 `argv` 仅允许 `${HOME}` 且与 `steps`/`transaction` 互斥；`output: secret` 必须写 `secret` 且禁止 `terminal: true`；`config` 与 `danger` 必须写 `undo`。
  - `service.repair_action` 非空时必须有 `service.candidates`，且指向本 profile 中非交互、非机密的 `manage` 动作（尚未核实的动作不得填入关联；命名实例去掉服务时一并去掉入口）。
- **子 profile 约束**：
  - 子 profile 只能包含元信息字段（`schema`、`id`、`display_name`、`profile_version`、`status`、`harness_versions_tested`、`docs`、`extends`、`when`）以及 `paths`、`consistency`、`checkup`，不得删除父 profile 的 `secrets` 规则；其父 profile 必须出现在同一次 lint 的文件集合中。

### 3.2 软警告规则（Warnings，支持 `--strict`）与命令审计

软警告默认打印但不阻断现有兼容加载；传入 `--strict` 时任何软警告均视为失败（返回非 0），新编写与提交 PR 时应尽量清零无意警告：

| 警告代码 | 触发条件 | 处理建议 |
| :--- | :--- | :--- |
| `deprecated_field` | 声明了已弃用的 `checkup`、`handoff` 或 `capabilities.checkup` | 新编写的 profile 不要配置这些字段；历史 profile 仅作向后兼容保留 |
| `redundant_body_path` | `paths` 中声明了 `category: body` 且同一 `(root, glob)` 已出现在 `body.paths` | 删除 `paths` 中的重复项，统一保留在 `body.paths` |
| `shadowed_path` | 同一 `root` 下前序更宽的非 `secrets` 规则遮蔽了后续非 `secrets` 规则 | 将更具体的规则移到通配规则之前（`secrets` 规则因优先级最高不触发此警告） |
| `exclusive_not_in_secrets` | `exclusive_secrets` 中的条目未被同 `root` 下任意 `category: secrets` 规则覆盖 | 在 `paths` 中为对应路径补充 `category: secrets` |
| `missing_capabilities` | 顶层 profile（非子 profile）未填写 `capabilities` 块 | 按 1.2 节如实填写各能力的 `tested` / `partial` / `untested` 状态 |
| `missing_secrets` | 顶层 profile 的 `paths` 中没有任何 `category: secrets` 规则 | 核对官方文档或源码补充凭据白名单，确实无凭据文件时再说明原因 |

**命令审计（`--commands`）**：`profilelint --commands` 与本地导入预览统一调用核心提取器，完整覆盖全部 15 类会在服务器端执行或触发安装的入口（共 18 个字段）：
1. 版本探测：`detect.version_argv`
2. 根目录发现：`roots[].discover_argv`
3. 身体重建：`body.bootstrap_argv`
4. 服务安装：`service.install_argv`
5. 管理页认证与生命周期：`entries[].auth.url_argv` / `token_argv`、`entries[].prepare.check_argv`（`daily`）、`entries[].prepare.steps[]`（`admin`）、`entries[].start.argv` / `stop_argv`（`owner`）
6. 入口改端口：`entries[].port_change.steps[]`
7. 健康检查：`health.checks[].argv`
8. 登录授权：`login[].argv`
9. 原生导出：`native.export_argv`
10. 命令行与向导：`native_cli`、`native_session_cli`
11. 管理动作与 B03 配置事务：`actions[].argv`、`actions[].steps[].argv`
12. 审批状态检查：`approval_check.argv`
13. 读取 Agent 名字：`identity.argv`
14. 官方安装与依赖：`install.requires[].install[].argv`、`install.steps[]`（`argv`、`npm_global`、`script`、`path_add`）
15. Agent 保护技能 MCP 注册与撤除：`agent_kit.mcp_add_argv`、`agent_kit.mcp_remove_argv`（`admin` 层级，用户确认后经独立管理连接执行）

审计结果同时标注权限层级（`daily` / `owner` / `admin`）与基础疑点检查结论（写「未发现基础疑点」或具体疑点，绝不写「安全」）。

### 3.3 B03 配置事务（2026-11-05 用户确认）

动作的 `argv` 与 `steps` 互斥。`steps` 为 1–16 个 `{id, argv}` 固定步骤，必须同时声明 `transaction: {root: state, env: {变量名: "${STAGE_DIR}[/相对路径]"}, writes: [相对文件或目录], scratch: [可选，命令每次运行都会重写的相对目录]}`（scratch 规则见 INSTALL.md 5.2.1）；只允许非交互、`output: none`、`risk: config` 且有 `undo` 的动作。`verification: {versions: [精确版本], env, date}` 记录完整事务证据；未核实时写 `verify: pending`，不提供执行入口。重定向与 writes 必须覆盖所有副作用，不把声明当成系统沙箱。

`approval_check` 可声明 `repair_action` 与 `repair_expect`（字段路径到目标值的 map），两者同时存在；动作必须是已核实事务，审批读法不可 pending，当前 harness 精确版本必须命中证据。每个非空 scope 必须满足 repair_expect 的全部值，空 scope、缺字段、unknown 不通过。暂存与发布后的复核都使用该目标，不能仅凭 ask 分类确认成功。步骤只能在暂存环境运行，首错停止，不自动重跑结果不明的步骤。

服务启停、目录交换、失败恢复与中断记录由固定引擎执行，profile 不提供条件、循环或任意 cleanup 命令。原本 inactive 的服务不启动；命名实例仍按 ForAgent 处理。完整执行、兼容、恢复与验收语义以用户确认的 [B03 设计](history/B03_DESIGN_PROPOSAL_2026-11-05.md) 第 3–7 节为本次实现依据。

## 4. 子 profile

用于覆盖同一 harness 下的不同插件形态，例如 DSH 的各种记忆插件。子 profile 通过 `extends` 继承父 profile，通过 `when` 设置生效条件，并只能**追加** `paths`、`consistency`（`sqlite`）与兼容字段 `checkup` 规则，不得删除父 profile 的 `secrets` 规则。子 profile 只用于正确地保存用户已经安装的东西，**不代表推荐**。

示例（仅演示机制，不代表推荐）：

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
```

## 5. 贡献要求

提交 PR 时必须附上以下内容：harness 的版本；`valise enroll --report-json` 的输出（凭据路径可以打码）；在哪台靶机上验证过；以及每条 `verify: pending` 被移除的依据（官方文档链接，或真实安装的观察结果）。详细流程与清单见 [PROFILE_AUTHORING.md](PROFILE_AUTHORING.md)。

---

## 6. 官方 Profile 核实依据与验收记录

官方 profile 以仓库 `profiles/*.yaml`（`openclaw.yaml`、`hermes.yaml`、`dsh.yaml`）为唯一现行正文：每条规则的出处均以注释形式（文档章节、源码位置或实机观察 `OBS`）就地记录，按能力核实状态以各文件内的 `capabilities` 块为准。

各阶段靶机核实与端到端验收流水记录统一归档于 `docs/acceptance/`，本规范不再堆积阶段性施工日志。

## 附录 A：为 harness 写一份资料（入门入口）

桌面版“这台服务器上的 Agent”在资料包不认识你的 harness 时链到这里。完整的人类编写与实机验证教程已独立成文，请直接阅读：

- **人类编写与实机教程**：[PROFILE_AUTHORING.md](PROFILE_AUTHORING.md)（含心智模型、5 大高频踩坑、离线校验 `./scripts/verify-profile.sh` / `valise profiles lint`、本机导入路径与 PR 贡献清单）。
- **AI 辅助编写技能**：`skills/valise-profile-author/`（可直接交给 Claude Code / Codex / Antigravity / OpenClaw，支持纯官方文档/源码交付与只读现场核验）。
