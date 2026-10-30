# 参考资料（已核实）

> 本文件只收录**已经核实**的条目。核实日期为 2026-10-30。旧版文档中无法核实的条目已全部删除，其中包括若干 DSH 插件名称，以及一条把"大肥鱼"（社区对 DeepSeek 的昵称）误当成桌宠插件的条目。新增条目时必须附上来源链接；引用的项目只作为设计参考，不代表推荐。

## 一、目标 Harness

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
| mygo | https://github.com/egoist/mygo | 桌面 UI；系统 WebView，纯 Go 无 cgo，从 Go 代码生成类型化的 TypeScript 客户端（见 `docs/UI.md`） |
| Model Context Protocol | https://modelcontextprotocol.io | `valise-porter mcp` 遵循的协议 |

## 四、认知层面的参考（只用于体检解读与选择标准的措辞，不引入任何依赖）

- Letta（原 MemGPT）：分层记忆，以及让 agent 显式编辑自己的记忆。https://github.com/letta-ai/letta
- Mem0：长期记忆的提取与去重。https://github.com/mem0ai/mem0
- Graphiti：事实随时间失效、被新结论替代的思路。https://github.com/getzep/graphiti
