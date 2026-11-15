# AGENTS.md — Valise 开发主控指令

Valise 是一个开源的「自托管 Agent 安装与管理入口」，帮助缺少系统管理经验的用户搭好并照看自己的 Agent，附带可靠的备份、恢复与迁移。品牌表达是「带着你的 Agent 去旅行」。本文件是主控指令的唯一正文（`CLAUDE.md` 只导入本文件）。正式规范在 `docs/` 根目录；排期以 `docs/DEV_PLAN.md` 的 N1–N5 为准；现行服务契约为 `docs/SERVICE_CONTRACTS.md`。规范中的目标能力不代表已实现。它从零开始开发，不继承任何旧版代码或旧版文档。旧版名称 Fathom、旧版的 `fathomd`、`AEV1`、`agent-manifest.json`、AB 槽位软链、五级备份、`.fathom-soul/` 等概念**全部作废**，不得以任何形式复活。

**2026-11-15 架构收敛决定（撤销 A/B 双槽位方案）**：经全面评估与用户确认，正式**撤销** 2026-11-12 至 2026-11-15 试探引入的 A/B 双槽位并行运行环境、`linux-bwrap` 容器接管、三档恢复保护模式（保留旧环境并行 / 小占用并行 / 直通）及 `environment-restore-*` 管理链路（历史设计已归档至 `docs/history/RESTORE_ENVIRONMENTS_DEPRECATED_2026-11-15.md` 与 `docs/history/AB_ENVIRONMENTS_CONTRACTS_AND_LAB_DEPRECATED_2026-11-15.md`）。Valise 恢复与迁移统一收敛为单一架构：
- **恢复与迁移**：暂存目录完整解压与逐项校验（`.valise-stage-<op>`）→ 带交换日志（journal）的原子目录交换 → 保留交换前目录 `.valise-prev-<op>` 供失败回退与人工一键退回。
- **恢复测试（健康页）**：只执行非破坏性的“文件解密校验 + 服务器暂存目录校验（`test_only` 用后即删）”，不拉起隔离网关或接管宿主 systemd 单元。
- **备份格式兼容**：所有读取端（`core/vault` 与 `tools/standalone-restore/restore.py`）永久兼容 `valise-snapshot/1` 与 `valise-snapshot/2`（`VAULT_FORMAT.md`），新封存不再强制采集运行期程序树。

## 1. 启动必读与按需阅读（Prompt Audit 后分级）

### 核心必读（每次新会话或交接前必读，共 3 份）

1. `docs/STATUS.md`：当前分支、进度、未完成事项、待用户决定的问题与交接。
2. `docs/PRODUCT.md`：定位、目标用户、非目标、未发掘痛点、术语与界面用语。**任何功能取舍以此为准。**
3. `docs/DEV_PLAN.md`：确认当前阶段、MVP（v0.1）目标边界与工程减负计划。不跨阶段抢跑，不伪造未实现的状态。

### 按任务领域按需阅读（On-Demand，只读与当前任务相关的规范）

| 任务领域 | 对应必读规范 |
| --- | --- |
| 传输、钥匙、接入、备份、直接恢复与交换日志 | `docs/ARCHITECTURE.md` |
| 共享 Go 服务契约、任务状态机与人类确认边界 | `docs/SERVICE_CONTRACTS.md`（按目录跳读相关章节） |
| 备份库格式、快照头版本与脱离 Valise 恢复 | `docs/VAULT_FORMAT.md` |
| Harness Profile schema 与能力核实状态 | `docs/PROFILES.md` |
| OAuth / 云盘表单登录中继、管理页与本地页面转发 | `docs/LOGIN_RELAY.md` |
| Harness 安装、原生界面、管理动作与高级模式安全边界 | `docs/INSTALL.md` |
| 受管方 MCP 工具与技能；管理方接口（N5） | `docs/AGENT_KIT.md` |
| TUI / mygo 桌面 UI 规范、400 行限制与 i18n 规则 | `docs/UI.md` |
| 外部官方参考来源（无需全量通读） | `docs/REFERENCES.md` |
| 操作真实靶机、部署测试二进制或写 fake-ssh 替身前 | `docs/LAB.md` |

身份与旅程采用备份库外世代/关系记录，源失联且独占身份无法核实隔离时不凭用户声明完整启动，管理方跨进程请求由人类 GUI/TUI 进程确认并执行，不共享解锁身份或通用票据（`docs/SERVICE_CONTRACTS.md`）。

## 2. 四条总原则

1. **能用代码解决的绝不用 prompt。**LLM 只参与三件事：登记时补充信息、交接时撰写交接内容、体检时用自然语言解读结果。封存、校验、复活、回滚的执行路径上不允许出现任何 LLM 调用。
2. **备份不能依赖 Valise 自己活着。**备份库格式公开，加密只用标准 age。即使 Valise 项目消失，用户仍能用 `age`、`zstd` 和仓库内的 `tools/standalone-restore/` 恢复全部数据。
3. **只看见、只兜底，不越俎代庖。**Valise 不擅自修改 agent 的记忆；只在用户明确发起并确认后才安装 harness、依赖或工具，绝不悄悄往 harness 里放代码；不代管模型凭据；不为任何第三方软件背书。
4. **克制复杂度，不接管原生服务，先跑通真实端到端闭环（2026-11-15 新增）。**Valise 是照看与兜底工具，不是容器编排器或 PaaS。禁止用私有运行时包装器（如 `bwrap` 槽位）替换用户原有的 systemd 服务或启动链路；禁止在端到端用户主路径尚未跑通前堆叠多层状态机。正式规范只保留现行终态规则，阶段性验证记录一律写进 `docs/acceptance/`，禁止把施工日志追加进正式规范正文。

## 3. 架构铁律（严禁违背）

- **不常驻**：`valise-porter` 由系统 OpenSSH 按需唤起。禁止监听任何端口，禁止 TLS 守护进程，禁止自签证书。
- **日常钥匙与管理登录**（`docs/ARCHITECTURE.md` 第 2.1 节）：Valise 管理的服务器都有日常钥匙，它在服务器 `authorized_keys` 中被强制命令限制为 porter 日常模式，porter 用代码拒绝日常模式下的管理方法。管理操作必须出示管理登录：可选的管理钥匙（默认不创建；由单独的管理口令加密，与 Valise 口令不同；解锁后放入 Valise 自己的 ssh-agent 并带时间窗口），或每次用服务器密码临时登录。每次管理操作新开自己的连接，原生界面、harness 安装与工具安装绝不复用日常通道或已有连接。Valise 不保存任何 SSH 密码。
- **不内嵌**：客户端安装包不得 embed porter 二进制。部署方式只有两种：从 GitHub Release 下载并校验签名与 SHA-256，或由用户显式选择本地构建产物上传。
- **Harness 知识只存在于 `profiles/*.yaml`**：Go 代码中不得出现任何具体 harness 的名称、路径、端口或命令（测试 fixture 除外）。需要新能力时扩展 profile schema，而不是写 `if harness == "xxx"`。
- **不编造路径**：profile 中未在靶机上核实的条目必须标注 `verify: pending`；能力按项记录核实状态（`docs/PROFILES.md` 第 1.2 节）。恢复能力未核实的 profile 用于复活时，必须由用户显式传入 `--allow-draft-profile`，UI 上写明“恢复尚未在此版本测试”及具体影响（旧参数名保持兼容）。
- **凭据只按声明分类**：凭据由 profile 白名单声明。禁止用 glob 黑名单猜测凭据，禁止扫描或解析文件内容来寻找 API Key。
- **绝不写入活动状态目录**：所有写入都发生在暂存目录。对活动目录唯一允许的修改，是带交换日志（journal）的目录交换。
- **不盲目执行远程内容**：禁止 `curl | bash` 等把网络内容直接交给解释器的做法。安装只执行经签名资料包审核的官方方案：来源锁定版本，脚本由客户端下载、核对 profile 中的 SHA-256、展示给用户、确认后上传到暂存目录再执行（`docs/INSTALL.md` 第 3 节）。用户或 LLM 提供的安装命令在 INSTALL.md 第 7 节的规则落地前不实现；落地后也必须展示全部内容、经基础疑点检查与用户确认，编码或混淆而无法展示实际内容的一律不执行。**高级模式**（INSTALL.md 4.1）只能由用户在界面里亲自开启，需通过两个口令（Valise 口令与管理口令）的挑战并确认免责，开启后可以代为安装运行时与依赖（含 sudo）；明显有害的命令在任何模式下都不执行，也不提供绕过开关，用户坚持时只能自己去服务器执行。身体重建只依据登记时记录的来源，来源发生变化就立即停止并交给用户决定。
- **不自研密码学**：只使用 `filippo.io/age` 和 Go 标准库。唯一例外：校验 minisign 签名时，预哈希使用 `golang.org/x/crypto/blake2b`（minisign 格式解析自行实现，验签使用标准库 `crypto/ed25519`）。禁止自定义加密容器格式。
- **隧道只绑定本机回环地址**（`127.0.0.1` / `[::1]`），禁止绑定 `0.0.0.0`。
- **MCP 工具的能力边界**：受管方不得提供恢复、删除、读取凭据、写入任意文件的能力。管理方是待实现的用户侧独立接口，首批限定查询、计划和允许且已授权的任务；当前同样不开放恢复/删除执行接口。未来新增此类请求须单独评审工具清单与人工确认绑定，不因产品计划自动获得权限；任何 MCP 都不能读取秘密、替用户确认或开启高级模式（详见 `docs/AGENT_KIT.md`）。
- **备份库只追加**：备份库按"实例 / 周期"分档（`docs/VAULT_FORMAT.md` 第 10 节），周期内部只追加。唯一的删除方式是用户显式删除一整个已结束的周期（服务器与每个备份位置分别确认）；不提供自动保留策略，不删除单个快照。
- **禁止实现**：插件市场、自定义插件解释器、临时有头容器桌面或 VNC、整机备份。桌面主题（`docs/UI.md` 7.4）只是数据：令牌、具名变体与声明式布局树，摆放 Valise 暴露的固定插槽，不得含条件、循环、表达式或脚本，不能隐藏 UI.md 7.4 列出的始终显示项。profile 登记的管理动作只是固定的 argv 加风险分类与确认（`docs/INSTALL.md` 第 5 节），不是解释器。
- **不代管模型凭据**：不提供 API Key 输入框，不解析、不保存、不记录用户在原生界面里输入的内容。

## 4. 前端与工具链

- 桌面界面的文字一律用英文标准键 `T("key")` 从词表取（`client/ui/gui/i18n/zh-CN.json`，规则见 `docs/UI.md` 7.6），非测试代码里不写界面文字字面量；主题只管样子，不改文字。
- 桌面界面用 **mygo 原生 UI**（`ui` 包，Go 写，GPU 绘制；2026-11-08 用户决定），代码在 `client/ui/gui/`，入口 `client/ui/cmd/valise-desktop/`，直接调用 service，没有 TS 前端与绑定生成。`client/ui/gui/` 下单个 `.go` 文件不得超过 400 行。约定见 `docs/UI.md` 第 2–7 节。
- macOS 下先执行（详见 `docs/LAB.md`）：`export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3`
- 终端 UI 用 Bubble Tea，版本锁定见 `docs/UI.md` 第 8 节；`client/ui/tui/` 下单个 `.go` 文件不得超过 400 行。
- mygo 版本锁定为用户指定的 v0.7.3，记录在 `docs/UI.md`；升级必须经过用户同意。库与 CLI 都写在 `client/ui/go.mod`（CLI 为 Go tool 依赖），统一通过 `go tool mygo …` 调用。
- 单元测试和集成测试中，"远端"一律通过 fake-ssh 测试替身模拟（规范见 `docs/LAB.md`）。fake-ssh 测试通过，不等于靶机验收通过。
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

# 5. 服务层、CLI、TUI 与桌面界面
cd ../client/ui
go test ./... && go vet ./... && test -z "$(gofmt -l .)"
go tool mygo vet
```

`testbed/selfcheck.sh` 按上述顺序执行全部检查；`client/ui/go.mod` 尚未引入 mygo 时自动跳过 `go tool mygo vet`。

## 7. 安全与文案红线

1. **登录凭据**：Valise 绝不保存 SSH 密码；首次手动添加服务器时，密码只在 OpenSSH 自己的提示中输入一次（GUI 经 Valise 的 askpass 转交，只在内存中经过）。Valise 为每台服务器生成日常钥匙（无口令、0600，服务器端用强制命令限制为 porter 日常模式）；管理钥匙可选、默认不创建（`ssh-keygen` 以单独的管理口令加密，解锁后进入 Valise 自己的 ssh-agent，带时间窗口），没有管理钥匙时管理操作每次用服务器密码临时登录。用户自己的 SSH 密钥仍交给系统 OpenSSH 和用户的 SSH Agent，Valise 不读取。详见 ARCHITECTURE.md 第 2.1 节。
2. **模型凭据**：Valise 不提供 API Key 输入辅助。用户在 harness 原生界面或管理页里自己填写；Valise 不解析、不读取、不保存、不记录原生界面中的输入与回显，也不声称这些字节"完全不经过 Valise"（GUI 的终端经 Valise 中转，INSTALL.md 第 2 节）。
3. **凭据分层**：凭据只按 profile 的声明分类。只有 `soul-secrets` 和 `full` 两个档位会把凭据封存进备份库，并且做独立标记。凭据内容和任何文件内容都不得出现在日志、审计记录、错误信息或 MCP 返回值中。
4. **文案去黑话**：界面用语以 `docs/PRODUCT.md` 8.1 为准：备份、恢复当前实例、迁移、创建副本、备份位置、管理页、登录授权、原生界面、健康。不再用逃生舱、着陆、空间站命名主界面操作；内部 ID、协议、命令名与历史报告保持兼容。错误信息必须包含“失败原因 + 下一步建议”，原始技术细节默认折叠。
5. **诚实状态**：以下每一组都是彼此独立的状态，严禁合并成一个笼统的绿色徽标：
   - 隧道已建立、端口在监听、健康检查通过；
   - 封存已写入服务器备份库、已同步到异地；
   - profile 的每项能力已核实、未核实（PROFILES.md 1.2）；
   - 命令已退出（退出码）、服务或功能真正健康。

   未实现的能力不渲染按钮，也不用灰色禁用按钮画饼。
6. **不背书**：任何界面、技能、文档都不点名推荐第三方插件或工具。只给出选择标准，以及该 harness 官方文档中的原生选项。安装检查写"未发现基础疑点"，绝不写"安全"。

## 8. Git 提交约定

每个 Commit 的范围必须清晰，末尾附带以下两个 trailer（Claude 等工具要求的署名行另加在其后）：

```text
Co-authored-by: NazukiMio <170129864+NazukiMio@users.noreply.github.com>
Co-authored-by: Nazukiki <347054528+Nazukiki@users.noreply.github.com>
```

`git config user` 保持为 `NazukiMio <nazukimio@gmail.com>`。除非用户明确要求，否则不要执行 `git push`。

## 9. 文档放置规则

- `docs/` 根目录只放现行规范、`STATUS.md` 与 `LAB.md`，文件名不带日期或阶段号；规则改动直接改对应规范。
- 阶段的实现映射与验收记录放 `docs/acceptance/`（如 `N2.md`）；尚待用户确认的设计放 `docs/proposals/`，确认后写进正式规范，提案移入 `docs/history/`。
- `docs/history/` 是 DEPRECATED 的历史文档，只读，不作为依据；文件开头写明被什么取代。
- 交接时覆盖更新 `docs/STATUS.md`，不新建交接文件。
- 文档一律不删除：弃用时标 DEPRECATED，并告诉用户哪些被改名、弃用或新增。
