# CLAUDE.md — Valise 开发主控指令

Valise 是一个开源的「活体 Agent 逃生舱」。它从零开始开发，不继承任何旧版代码或旧版文档。旧版名称 Fathom、旧版的 `fathomd`、`AEV1`、`agent-manifest.json`、AB 槽位软链、五级备份、`.fathom-soul/` 等概念**全部作废**，不得以任何形式复活。

## 1. 启动必读顺序

每次开始新会话或推进新里程碑前，按顺序阅读：

1. `AGENT.md`：靶机矩阵、操控铁律、安全与文案红线、Git 规范。
2. `docs/PRODUCT.md`：定位、目标用户、非目标、术语表。**任何功能取舍以此为准。**
3. `docs/ARCHITECTURE.md`：传输、密钥、登记、封存、复活、交换日志。
4. `docs/VAULT_FORMAT.md`：金库磁盘格式（公开格式，兼容性承诺）。
5. `docs/PROFILES.md`：Harness Profile 规范与 OpenClaw / Hermes / DSH 初稿。
6. `docs/LOGIN_RELAY.md`：登录中继与 Web 入口。
7. `docs/AGENT_KIT.md`：MCP 工具与四个技能。
8. `docs/UI.md`：mygo 桌面 UI 规范（M7 使用；第 1 节的分层原则全程有效）。
9. `docs/DEV_PLAN.md`：确认当前里程碑。不跨阶段抢跑，不伪造未实现的状态。
10. `docs/REFERENCES.md`：已核实的外部参考。

## 2. 三条总原则

1. **能用代码解决的绝不用 prompt。**LLM 只参与三件事：登记时补充信息、交接时撰写交接内容、体检时用自然语言解读结果。封存、校验、复活、回滚的执行路径上不允许出现任何 LLM 调用。
2. **逃生舱不能依赖自己活着。**金库格式公开，加密只用标准 age。即使 Valise 项目消失，用户仍能用 `age`、`zstd` 和仓库内的 `tools/standalone-restore/` 恢复全部数据。
3. **只看见、只兜底，不越俎代庖。**Valise 不擅自修改 agent 的记忆，不往 harness 里安装第三方代码，不为任何第三方插件背书。

## 3. 架构铁律（严禁违背）

- **不常驻**：`valise-porter` 由系统 OpenSSH 按需唤起。禁止监听任何端口，禁止 TLS 守护进程，禁止自签证书。
- **不内嵌**：客户端安装包不得 embed porter 二进制。部署方式只有两种：从 GitHub Release 下载并校验签名与 SHA-256，或由用户显式选择本地构建产物上传。
- **Harness 知识只存在于 `profiles/*.yaml`**：Go 代码中不得出现任何具体 harness 的名称、路径、端口或命令（测试 fixture 除外）。需要新能力时扩展 profile schema，而不是写 `if harness == "xxx"`。
- **不编造路径**：profile 中未在靶机上核实的条目必须标注 `verify: pending`。`status: draft` 的 profile 可以用于登记报告和封存；用于复活时，必须由用户显式传入 `--allow-draft-profile`，UI 上也必须显示"未核实"。
- **凭据只按声明分类**：凭据由 profile 白名单声明。禁止用 glob 黑名单猜测凭据，禁止扫描或解析文件内容来寻找 API Key。
- **绝不写入活动状态目录**：所有写入都发生在暂存目录。对活动目录唯一允许的修改，是带交换日志（journal）的目录交换。
- **不执行远程脚本**：禁止 `curl | bash`，禁止执行 LLM 或网页提供的安装命令。身体重建只依据登记时记录的来源，来源发生变化就立即停止并交给用户决定。
- **不自研密码学**：只使用 `filippo.io/age` 和 Go 标准库。禁止自定义加密容器格式。
- **隧道只绑定本机回环地址**（`127.0.0.1` / `[::1]`），禁止绑定 `0.0.0.0`。
- **MCP 工具的能力边界**：不得提供复活、删除、读取凭据、写入任意文件的能力（详见 `docs/AGENT_KIT.md`）。
- **金库只追加**：v0.1 不提供任何删除快照的能力。之后的 prune 只能由用户显式命令触发。
- **禁止实现**：插件市场、自定义插件解释器、临时有头容器桌面或 VNC、整机备份。

## 4. 前端与工具链

- `client/ui/src/` 按视图和状态职责拆分（如 `state.ts`、`views/instances.ts`、`views/vault.ts`、`views/settings.ts`、`components/`），单个 `.ts` 文件不得超过 400 行。
- macOS 下先执行：`export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3`
- UI 框架为 mygo。M7 开始时锁定当时最新的 tag，并记录在 `docs/UI.md` 中；锁定之后，升级必须经过用户同意。M7 之前不得引入 mygo 依赖。mygo CLI 以 Go tool 依赖的方式加入 `client/ui/go.mod`，统一通过 `go tool mygo …` 调用。具体约定见 `docs/UI.md`。
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

# 4. 服务层、CLI 与 UI（generate、typecheck、build:web 三步从 M7 起执行）
cd ui
go tool mygo generate
go test ./... && go vet ./... && test -z "$(gofmt -l .)"
bun run typecheck
bun run build:web
```
