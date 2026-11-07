# CLAUDE.md — Valise 开发主控指令

Valise 是一个开源的「自托管 Agent 安装与管理入口」，帮助缺少系统管理经验的用户搭好并照看自己的 Agent，附带可靠的备份、恢复与迁移。品牌表达是「带着你的 Agent 去旅行」。2026-11-05 已确认产品重整、GUI / TUI 草图与后端计划，正式规范和 `docs/DEV_PLAN.md` 的 N1–N5 排期为当前依据；`docs/NEW_INSTRUCTURE.md` 保留讨论背景。`docs/N1_SERVICE_CONTRACTS_2026-11-05.md` 已于 2026-11-05 经用户确认定稿，N1 仅设计完成；N2–N5 按契约实现。规范中的目标能力不代表已实现，线级协议映射与后续自定义等专题仍须按开工条件定稿。它从零开始开发，不继承任何旧版代码或旧版文档。旧版名称 Fathom、旧版的 `fathomd`、`AEV1`、`agent-manifest.json`、AB 槽位软链、五级备份、`.fathom-soul/` 等概念**全部作废**，不得以任何形式复活。

## 1. 启动必读顺序

每次开始新会话或推进新里程碑前，按顺序阅读：

1. `AGENT.md`：靶机矩阵、操控铁律、安全与文案红线、Git 规范。
2. `docs/PRODUCT.md`：定位、目标用户、非目标、术语表。**任何功能取舍以此为准。**
3. `docs/ARCHITECTURE.md`：传输、日常钥匙与管理登录、接入服务器、登记、封存、复活、交换日志。
4. `docs/VAULT_FORMAT.md`：金库磁盘格式（公开格式，兼容性承诺）。
5. `docs/PROFILES.md`：Harness Profile 规范与 OpenClaw / Hermes / DSH 初稿。
6. `docs/LOGIN_RELAY.md`：登录中继、管理页与本地页面转发。
7. `docs/INSTALL.md`：Harness 安装、原生界面、管理动作、第三方工具与安装命令的安全边界。
8. `docs/AGENT_KIT.md`：受管方 MCP 工具与四个技能；管理方技能（规划）。
9. `docs/UI.md`：UI 规范（终端 UI 用第 8 节，桌面 UI 用 mygo；第 9 节信息架构两者共用；第 1 节的分层原则全程有效）。
10. `docs/DEV_PLAN.md`：确认当前里程碑。不跨阶段抢跑，不伪造未实现的状态。
11. `docs/REFERENCES.md`：已核实的外部参考。

进入 N2–N5 实施前，还须阅读已定稿的 `docs/N1_SERVICE_CONTRACTS_2026-11-05.md`；身份与旅程采用金库外世代/关系记录，源失联且独占身份无法核实隔离时不凭用户声明完整启动，管理方跨进程请求由人类 GUI/TUI 进程确认并执行，不共享解锁身份或通用票据。

## 2. 三条总原则

1. **能用代码解决的绝不用 prompt。**LLM 只参与三件事：登记时补充信息、交接时撰写交接内容、体检时用自然语言解读结果。封存、校验、复活、回滚的执行路径上不允许出现任何 LLM 调用。
2. **备份不能依赖 Valise 自己活着。**金库格式公开，加密只用标准 age。即使 Valise 项目消失，用户仍能用 `age`、`zstd` 和仓库内的 `tools/standalone-restore/` 恢复全部数据。
3. **只看见、只兜底，不越俎代庖。**Valise 不擅自修改 agent 的记忆；只在用户明确发起并确认后才安装 harness、依赖或工具，绝不悄悄往 harness 里放代码；不代管模型凭据；不为任何第三方软件背书。

## 3. 架构铁律（严禁违背）

- **不常驻**：`valise-porter` 由系统 OpenSSH 按需唤起。禁止监听任何端口，禁止 TLS 守护进程，禁止自签证书。
- **日常钥匙与管理登录**（`docs/ARCHITECTURE.md` 第 2.1 节）：Valise 管理的服务器都有日常钥匙，它在服务器 `authorized_keys` 中被强制命令限制为 porter 日常模式，porter 用代码拒绝日常模式下的管理方法。管理操作必须出示管理登录：可选的管理钥匙（默认不创建；由单独的管理口令加密，与 Valise 口令不同；解锁后放入 Valise 自己的 ssh-agent 并带时间窗口），或每次用服务器密码临时登录。每次管理操作新开自己的连接，原生界面、harness 安装与工具安装绝不复用日常通道或已有连接。Valise 不保存任何 SSH 密码。
- **不内嵌**：客户端安装包不得 embed porter 二进制。部署方式只有两种：从 GitHub Release 下载并校验签名与 SHA-256，或由用户显式选择本地构建产物上传。
- **Harness 知识只存在于 `profiles/*.yaml`**：Go 代码中不得出现任何具体 harness 的名称、路径、端口或命令（测试 fixture 除外）。需要新能力时扩展 profile schema，而不是写 `if harness == "xxx"`。
- **不编造路径**：profile 中未在靶机上核实的条目必须标注 `verify: pending`；能力按项记录核实状态（`docs/PROFILES.md` 第 1.2 节）。着陆能力未核实的 profile 用于复活时，必须由用户显式传入 `--allow-draft-profile`，UI 上写明“恢复尚未在此版本测试”及具体影响（旧参数名保持兼容）。
- **凭据只按声明分类**：凭据由 profile 白名单声明。禁止用 glob 黑名单猜测凭据，禁止扫描或解析文件内容来寻找 API Key。
- **绝不写入活动状态目录**：所有写入都发生在暂存目录。对活动目录唯一允许的修改，是带交换日志（journal）的目录交换。
- **不盲目执行远程内容**：禁止 `curl | bash` 等把网络内容直接交给解释器的做法。安装只执行经签名资料包审核的官方方案：来源锁定版本，脚本由客户端下载、核对 profile 中的 SHA-256、展示给用户、确认后上传到暂存目录再执行（`docs/INSTALL.md` 第 3 节）。用户或 LLM 提供的安装命令在 INSTALL.md 第 7 节的规则落地前不实现；落地后也必须展示全部内容、经基础疑点检查与用户确认，编码或混淆而无法展示实际内容的一律不执行。**高级模式**（INSTALL.md 4.1）只能由用户在界面里亲自开启，需通过两个口令（Valise 口令与管理口令）的挑战并确认免责，开启后可以代为安装运行时与依赖（含 sudo）；明显有害的命令在任何模式下都不执行，也不提供绕过开关，用户坚持时只能自己去服务器执行。身体重建只依据登记时记录的来源，来源发生变化就立即停止并交给用户决定。
- **不自研密码学**：只使用 `filippo.io/age` 和 Go 标准库。唯一例外：校验 minisign 签名时，预哈希使用 `golang.org/x/crypto/blake2b`（minisign 格式解析自行实现，验签使用标准库 `crypto/ed25519`）。禁止自定义加密容器格式。
- **隧道只绑定本机回环地址**（`127.0.0.1` / `[::1]`），禁止绑定 `0.0.0.0`。
- **MCP 工具的能力边界**：受管方不得提供恢复、删除、读取凭据、写入任意文件的能力。管理方是待实现的用户侧独立接口，首批限定查询、计划和允许且已授权的任务；当前同样不开放恢复/删除执行接口。未来新增此类请求须单独评审工具清单与人工确认绑定，不因产品计划自动获得权限；任何 MCP 都不能读取秘密、替用户确认或开启高级模式（详见 `docs/AGENT_KIT.md`）。
- **金库只追加**：金库按"实例 / 周期"分档（`docs/VAULT_FORMAT.md` 第 10 节），周期内部只追加。唯一的删除方式是用户显式删除一整个已结束的周期（服务器与每个备份位置分别确认）；不提供自动保留策略，不删除单个快照。
- **禁止实现**：插件市场、自定义插件解释器、临时有头容器桌面或 VNC、整机备份。profile 登记的管理动作只是固定的 argv 加风险分类与确认（`docs/INSTALL.md` 第 5 节），不是解释器。
- **不代管模型凭据**：不提供 API Key 输入框，不解析、不保存、不记录用户在原生界面里输入的内容。

## 4. 前端与工具链

- `client/ui/src/` 按视图和状态职责拆分（如 `state.ts`、`views/instances.ts`、`views/vault.ts`、`views/settings.ts`、`components/`），单个 `.ts` 文件不得超过 400 行。
- macOS 下先执行：`export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3`
- 终端 UI 用 Bubble Tea，版本锁定见 `docs/UI.md` 第 8 节；`client/ui/tui/` 下单个 `.go` 文件不得超过 400 行。
- 桌面 UI 框架为 mygo，版本锁定为用户指定的 v0.7.3，记录在 `docs/UI.md` 中；锁定之后，升级必须经过用户同意。M7b 的后续实施并入 N4，按 `docs/DEV_PLAN.md` 的新阶段依赖开工，不再等待所有 TUI 边缘功能；正式进入 N4 之前不得引入 mygo 依赖。mygo CLI 以 Go tool 依赖的方式加入 `client/ui/go.mod`，统一通过 `go tool mygo …` 调用。具体约定见 `docs/UI.md`。
- 单元测试和集成测试中，"远端"一律通过 fake-ssh 测试替身模拟（规范见 AGENT.md）。fake-ssh 测试通过，不等于靶机验收通过。
- **禁止执行 `rm -f`**（会触发宿主审批拦截）。清空文件用 `: > file`；Go 测试中一律使用 `t.TempDir()`。porter 删除目录时只能在 Go 代码内调用 `os.RemoveAll`，并且只能由用户显式命令触发。

## 5. 规范没有覆盖时

遇到文档未覆盖或互相矛盾的情况，**停下来向用户提问**，不要自行发明。用户做出决定后，先修改对应文档，再写代码。文档与代码冲突时以文档为准；如果你认为文档有误，先提出修改建议。

## 6. 自检清单

每完成一个子模块或里程碑，依次执行以下检查，任何一项失败都不得进入下一阶段：

```bash
# 1. 共享核心
cd core
go test ./... && go vet ./... && test -z "$(gofmt -l .)"
go run ./cmd/profilelint ../profiles/*.yaml

# 2. 远端搬运工与交叉编译
cd ../porter
go test ./... && go vet ./... && test -z "$(gofmt -l .)"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null .
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null .

# 3. 客户端核心
cd ../client
go test ./... && go vet ./... && test -z "$(gofmt -l .)"

# 4. 测试替身
cd ../testbed
go test ./... && go vet ./... && test -z "$(gofmt -l .)"

# 5. 服务层、CLI 与 UI（generate、typecheck、build:web 三步从 M7b 起执行）
cd ../client/ui
go tool mygo generate
go test ./... && go vet ./... && test -z "$(gofmt -l .)"
bun run typecheck
bun run build:web
```

`testbed/selfcheck.sh` 按上述顺序执行全部检查；M7b 之前自动跳过 `go tool mygo generate`、`bun run typecheck`、`bun run build:web` 三步。
