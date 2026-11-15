# 参考资料（已核实）

> 本文件只收录**已经核实**的条目。核实日期为 2026-10-30。旧版文档中无法核实的条目已全部删除，其中包括若干 DSH 插件名称，以及一条把"大肥鱼"（社区对 DeepSeek 的昵称）误当成桌宠插件的条目。新增条目时必须附上来源链接；引用的项目只作为设计参考，不代表推荐。

> 排期说明（2026-11-05）：本文件保留历史核实日期、外部事实和旧 M 编号，不代表本轮重新联网核实。当前工作按 DEV_PLAN.md N1–N5；旧 M13 的副本/恢复工作映射 N3/N4，新增存储类型及 M14–M17 扩展按 N5c 各自依赖推进。引用的旧界面术语按 PRODUCT.md 8.1 解释。

## 一、目标 Harness

> 2026-11-01 改版后，官方方案与"可安装"结论必须按具体版本与安装方式重新核实（官方安装方式、认证流程、管理页），结果记入 profile 的 `capabilities` 与本表；下表为 2026-10-30 的核实结果。

| 项目 | 已核实的事实 | 对 Valise 的意义 |
| :--- | :--- | :--- |
| DeepSeek Harness（dsh）<br>https://github.com/deepseek-ai/deepseek-harness | 2026-08-13 开源，MIT 许可证，基于 Node.js 和 Cordis 插件架构；通过 `npx @deepseek-ai/dsh web` 启动，Web UI 位于 `127.0.0.1:3080`；会话日志采用只追加设计；处于开发者预览阶段，可能出现破坏性变更 | 首批 profile 之一；需要锁定精确版本 |
| DSH 社区记忆插件（多个同名或近名项目） | 存储形态各不相同：每个项目一个 JSONL 文件、单个 `memories.json`、向量加图谱等 | 说明需要子 profile 机制；**不推荐任何一个** |
| Hermes Agent<br>https://hermes-agent.nousresearch.com | 状态集中在 `~/.hermes/`（`config.yaml`、`.env`、`skills/`、`memories/`、`state.db`）；身体位于 `~/.hermes/hermes-agent/`，包括 venv；官方提供 `hermes backup` / `hermes import`，以及配置档级别的 `hermes profile export` / `import` | 首批 profile 之一；身体在状态目录内部，必须重建 |
| OpenClaw<br>https://docs.openclaw.ai | `openclaw backup create/verify`：归档带有 manifest.json，校验时拒绝路径穿越，并确认清单中声明的载荷都存在；auth profile 和运行时 SQLite 位于 `agents/<agentId>/agent/` 下 | 首批 profile 之一；校验思路可以参考 |
| OpenClaw 迁移 Hermes<br>https://documentation.openclaw.ai/install/migrating-hermes | 先 dry-run 输出计划，执行前先创建并校验备份；凭据导入需要显式开启 | 复活流程"计划 → 确认 → 先备份"的同类实践 |
| hermes-portable（社区）<br>https://github.com/zpage/hermes-portable | 导出清单中带 SHA-256；凭据需要显式选择才导出；导入前自动备份 | 验证了"凭据可选"和"导入前备份"确实是用户需要的 |

## 二、背景："大肥鱼事件"

- DeepSeek-V4.1-Flash-1017 于 2026-10-17 发布，上下文窗口约 100 万 token，缓存输入价格极低，并针对 DeepSeek Harness 等做了调优。
  - https://huggingface.co/deepseek-ai/DeepSeek-V4.1-Flash-1017
  - https://www.marktechpost.com/2026/10/17/deepseek-just-released-v4-1-flash-1017-scores-77-0-on-deepswe-opens-search-api/
- 窗口足够大、续聊几乎没有成本，使得新手倾向于让一个会话一直跑下去。这正是健康区中 `long_session`、`cost_growth` 和交接功能的设计依据（事件经过见 PRODUCT.md 第 2 节）。

## 三、技术选型

| 组件 | 地址 | 用途 |
| :--- | :--- | :--- |
| age | https://github.com/FiloSottile/age | 加密；口令保护的身份文件；收件人公钥 |
| restic | https://github.com/restic/restic | 内容寻址、pack 与 index 设计的参考（只参考设计，不作为依赖） |
| restic/chunker | https://github.com/restic/chunker | 内容定义分块 |
| modernc.org/sqlite | https://modernc.org/sqlite | 纯 Go 实现的 SQLite，用于 `VACUUM INTO` 和 `integrity_check`，兼容 `CGO_ENABLED=0` |
| zstd（Go） | https://github.com/klauspost/compress | blob 压缩 |
| minisign | https://jedisct1.github.io/minisign/ | Release 校验和文件的签名 |
| golang.org/x/crypto/blake2b | https://pkg.go.dev/golang.org/x/crypto/blake2b | minisign 预哈希签名（`ED`）的 BLAKE2b-512；age 本身已依赖 `golang.org/x/crypto` |
| golang.org/x/term | https://pkg.go.dev/golang.org/x/term | CLI 中无回显读取口令 |
| go.yaml.in/yaml/v3 | https://github.com/yaml/go-yaml | profile 解析（含注释节点，用于统计 `verify: pending`） |
| mygo | https://github.com/egoist/mygo | 桌面 UI，锁定 v0.7.3（tag 提交 `6f28d7b`）；Valise 用它的原生 UI（`ui` 包，Go 写，GPU 绘制，纯 Go 无 cgo，2026-11-08 用户决定），不用它的 WebView 前端（见 `docs/UI.md` 第 2–7 节） |
| mygo 终端插件 | https://github.com/egoist/mygo/tree/v0.7.3/plugins/terminal | 2026-11-08 用户决定用于桌面版安装（UI.md 7.5）。与 mygo 同版本（MIT）；终端模拟用 Ghostty 的 libghostty-vt（MIT，源码提交 `befcdfd2c3a1`），运行时加载、无 cgo。原生库随 `mygo build` 放进应用资源并一起签名；不经打包运行（`go run`、`go test`、手工包）时从 mygo 的 GitHub Release 下载到用户缓存，核对插件 `mygo-plugin.json` 里锁定的 SHA-256。视图测试不加载它 |
| Model Context Protocol | https://modelcontextprotocol.io | `valise-porter mcp` 遵循的协议 |
| ascii-motion / ascii.rest | https://github.com/bas3line/ascii | 火箭字符画动画的运行时与 `hash()`（MIT）；场景由用户创作，在 `assets/TUI/rocket`（文件内保留 ascii.rest 的许可声明），移植到 `client/ui/tui/rocket.go` |

## 四、设计参考：magpie（2026-11-02 用户决定）

https://github.com/yetone/magpie （MIT，阅读时的提交 `4d55a0a`，2026-11-02）。多 agent 的本地模型切换工具，在用户电脑上识别各种 harness。代码量大，**只读下表"可以学"的部分，按需读、不通读**；MIT 允许复制，但优先按 Valise 自己的结构重写小块逻辑，确需复制时保留其许可声明并记入此处。它对 harness 路径的说法是线索，写进 profile 前仍按 PROFILES.md 在靶机上核实。

| 状态 | 部分 | 学什么 | 用在 |
| :--- | :--- | :--- | :--- |
| 可以学 | `internal/agent/*`（openclaw.go、hermes.go、dsh.go 等） | 路径按环境变量的优先级解析（如 OpenClaw：`OPENCLAW_CONFIG_PATH` → `OPENCLAW_STATE_DIR` → `OPENCLAW_PROFILE` 的 `~/.openclaw-<名字>` → `~/.openclaw`，`OPENCLAW_HOME` 改变 `~`；Hermes：`HERMES_HOME`），注释中给出的 harness 源码出处 | profile 发现规则（M12 健康页之后的"多实例"一步） |
| 可以学 | 同上：Hermes 的多 bot profile、OpenClaw 的命名 profile | 一个安装里有多个 agent | 接入时识别为多个实例 |
| 可以学 | `internal/agent/agent.go`（Detected、agentDir、Taken）与版本管理器路径解析（cliupdate_shim.go） | 防误判：技能安装工具留下的只有 `skills/` 的目录、同名的其他程序、mise / asdf / volta 的垫片 | 接入扫描与版本读取 |
| 可以学 | `internal/proc`（进程列表、端口由哪个进程监听） | 判断 agent 是否在运行 | 健康页与首页的"运行中 · 最近活动" |
| 可以学 | `internal/sessions` | 只读解析会话记录得到真实 token 数；大文件按路径缓存解析结果 | 健康页（替换按字节估算） |
| 可以学 | `internal/library` | 按各 harness 自己的格式写入技能与 MCP，卸载时只拿回自己写的部分 | Valise Kit 的安装与卸载 |
| 可以学 | `LESSONS.md`、`docs/code-standards.md` | 核实、对待用户文件、测试与发布的经验 | 开发习惯 |
| 以后再看 | `internal/davsync`（WebDAV、S3 兼容、ETag 条件写入） | 不靠 rclone 的远程备份位置种类 | M13 远程备份位置，需要时再读 |
| 以后再看 | `lastgood`、`steady`、`filelock` | 状态文件抗损坏、多进程互斥写入 | 客户端与 porter 的状态文件 |
| 以后再看 | `internal/edit`、`claudelock`、`internal/update`（packaged.go）、`internal/uninstall`、`docs/session-relocation.md`、`agentenv`、`dirent`、`netguard` | 保留格式改配置、遵守 harness 的锁、识别包管理器安装的程序、完整撤销、绑定文件状态的确认令牌、测试沙箱、符号链接、下载地址限制 | 遇到对应问题时再读 |
| 不要碰 | `gateway`、`provider`、路由相关 | 代管模型凭据，违反"不代管模型凭据" | — |
| 不要碰 | `redact`、`pii`、`masking` | 扫描内容找密钥，违反"禁止扫描文件内容找 API Key" | — |
| 不要碰 | `plugin`、`pluginrt`、`agentplug` | 插件运行时，属于禁止实现的插件解释器 | — |
| 不要碰 | `mcpauth`、`oidc`、`claudebridge`、`stats` | 替人保管令牌、订阅桥接、使用统计 | — |

## 五、认知层面的参考（只用于体检解读与选择标准的措辞，不引入任何依赖）

- Letta（原 MemGPT）：分层记忆，以及让 agent 显式编辑自己的记忆。https://github.com/letta-ai/letta
- Mem0：长期记忆的提取与去重。https://github.com/mem0ai/mem0
- Graphiti：事实随时间失效、被新结论替代的思路。https://github.com/getzep/graphiti


## 2026-11-12 并行环境底座参考

- [bubblewrap 官方项目与安全边界](https://github.com/containers/bubblewrap)：它提供低层命名空间/挂载机制，隔离保障取决于调用方构造的完整策略，不以工具名字作背书。
- [bubblewrap 官方私有 HOME 示例](https://github.com/containers/bubblewrap/blob/main/demos/bubblewrap-shell.sh)：OS 目录只读、其余可见目录独立；Valise 的测试策略不启用 share-net。
- [systemd.exec 官方文档](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html)：文件系统只读与 Unix socket 可达性是不同边界；用户单元的命名空间选项受系统能力约束。不更改 LAB.md 禁止用户单元写 CapabilityBoundingSet/AmbientCapabilities 的规则。

ubuntu-vm 只读探针：已存在 bwrap 0.11.1；普通 unshare -Urnm 被拒，bwrap 的独立命名空间 true 探针可运行。未安装工具或改 sysctl/AppArmor，未改 Agent；此探针不代表私有运行环境或 harness 恢复已通过。

- [Linux PR_SET_DUMPABLE 手册](https://man7.org/linux/man-pages/man2/PR_SET_DUMPABLE.2const.html)：第二模块的隔离探针关闭自身 dumpable，避免受测进程读取/改写探针的 /proc fd 或内存；仅进程属性，不修改宿主策略。[PR_SET_NO_NEW_PRIVS](https://man7.org/linux/man-pages/man2/PR_SET_NO_NEW_PRIVS.2const.html) 说明该属性按线程继承；Go 多线程执行不能把单线程 prctl 误称为整个进程已统一设置。


2026-11-12 实机固定提交补核：[Hermes credential-free discovery](https://github.com/NousResearch/hermes-agent/blob/bf0203a794a07aac3ff764972ab9087baa4e4f4e/hermes_cli/gateway_runtime_discovery.py)、[runtime endpoint validation](https://github.com/NousResearch/hermes-agent/blob/bf0203a794a07aac3ff764972ab9087baa4e4f4e/hermes_cli/gateway_runtime.py)、[bounded readiness](https://github.com/NousResearch/hermes-agent/blob/bf0203a794a07aac3ff764972ab9087baa4e4f4e/gateway/readiness.py)。通过已安装 checkout 的 `git show HEAD:<path>` 阅读，并对当前默认 owner 作只读 identify；这是离线规则的来源证据，不是命名实例/隔离重建能力验收。


## 并行准备的隔离生命周期（2026-11-14 核实）

[bubblewrap v0.11.1 源码](https://github.com/containers/bubblewrap/blob/v0.11.1/bubblewrap.c) 的 `--sync-fd` 由 namespace init 持有，并在执行应用前关闭应用侧副本。已与 ubuntu-vm 安装版本的 `--help` 和真实进程中断验收交叉核实；该事实用于生命周期文件锁，不提供任意 FD/命令 RPC。见 SERVICE_CONTRACTS.md 19.19 与 LAB.md。
