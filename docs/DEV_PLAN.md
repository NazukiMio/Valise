# Valise 开发计划

> 严格按里程碑顺序开发。每个里程碑结束时，必须跑通 CLAUDE.md 的自检清单和本里程碑的验收标准，再把状态从 `Ready` 改为 `Done`。
> **M4 完成后发布 v0.1（仅 CLI）。**那是第一个对外可用的版本，此前不对外宣传。
> **2026-11-01 改版**（`docs/REDESIGN_PROPOSAL.md`）：Valise 扩展为"自托管 Agent 的安装与管理入口"。M1–M7a.1 的验收记录保留为历史；M7a.2 收窄范围后收尾；M7b 暂停；新增 M9–M15（本文末尾）。顺序：M9 → M10 → M11 → M12 → M13 → M14 → M15，M7b 在 M12 之后恢复。

---

## M1：基础骨架、信任根与传输

- **状态**：`Done`（2026-10-30）
- **交付**：
  1. 仓库骨架：`core`、`porter`、`client`、`client/ui` 四个模块，统一 Go 版本，自检脚本可以运行。
  2. `core`：profile 数据模型与 YAML 解析（`go.yaml.in/yaml/v3`）、`profilelint`（实现 PROFILES.md 第 3 节的全部规则）、RPC 类型、金库格式的数据结构（暂不实现读写）。
  3. `client/keystore`：age X25519 身份的生成与导入；`keystore.age` 口令加密（12 位最低长度）；离线恢复套件导出与指纹核对；`UnlockedKey.Lock()`。
  4. `client/ssh`：解析 `~/.ssh/config` 中的 `Host`、`HostName`、`User`、`Port`、`ProxyJump`、`IdentityFile`；复用 ControlMaster；远端单次命令；stdio JSON-RPC 客户端。
  5. `porter`：`version`、`rpc`（实现 `hello`）、探测链。
  6. porter 部署：下载 Release 资产，校验 minisign 签名与 SHA-256；或上传本地构建产物；原子替换。
  7. `valise doctor <host>`：展示架构、porter 版本、可用工具。
  8. `testbed/fakessh/`：按 AGENT.md 中的规范实现，包括故障注入。
- **验收**：
  - 单元测试覆盖 keystore 的往返加解密、错误口令、篡改检测，以及用标准 `age` CLI 解开 `keystore.age`。
  - fake-ssh 测试覆盖 RPC 往返。
  - 在三台靶机上部署 porter，`valise doctor` 输出正确。在 `ubuntu-vm-x86` 上确认 porter 安装到 `~/.local/bin`，全程不需要 sudo。
- **验收记录**（2026-10-30，在开发 Mac 上执行）：
  - `testbed/selfcheck.sh` 在 macOS（Go 1.27.3）上全部通过，包括用标准 `age` CLI 解开 `keystore.age` 的测试。
  - `testbed/drill.sh` 输出 `ALL_PASS (failures=0)`：三台靶机均以 `--local` 部署 porter 到 `~/.local/bin`，`valise doctor` 的架构、porter 版本、可用工具输出正确；porter 文件属于登录用户，全程未使用 sudo（`ubuntu-vm-x86` 与 `nas-test` 本身没有 sudo）。`nas-test` 的 HOME 为 `/mnt/test/nas`，porter 位于 `/mnt/test/nas/.local/bin/`。
  - 额外核实：向 `ubuntu-vm`（arm64）部署 amd64 porter 被拒绝，原 porter 的 SHA-256 不变；ControlMaster 复用生效；主机不可达时给出"原因 + 建议"，`-v` 才显示 OpenSSH 原始报错。
  - 遗留：Release 的 minisign 签名公钥（`client/porter.ReleasePublicKey`）尚未配置，配置之前只能用 `--local` 部署。

## M2：Profile 引擎与登记

- **状态**：`Done`（2026-10-30）
- **交付**：
  1. porter 的 detect、resolve、classify 实现（包括凭据优先、未分类统计、roots 的 `discover_argv`）。
  2. 来源记录：npm、git（包括 dirty 状态与补丁）、uv/pipx、二进制哈希。
  3. 登记报告（终端表格和 `--report-json` 两种格式），以及实例记录的写入。
  4. 在靶机上安装真实的 harness：`ubuntu-vm` 上安装 OpenClaw 和 DSH，`ubuntu-vm-x86` 上安装 Hermes。逐条核实 PROFILES.md 初稿中的 `verify: pending`，把核实结果写回 `profiles/*.yaml` 和文档；能够全部核实的 profile，状态改为 `verified`。
- **验收**：
  - 三种 harness 上的 `valise enroll` 都能输出完整报告，凭据全部正确识别，身体被正确排除。
  - 每一条无法核实的条目都在文档中写明原因，并请用户决定如何处理。
- **进度**（2026-10-30）：
  - 已完成：交付 1–3 的代码与测试；在靶机上安装 OpenClaw 2026.10.9 与 DSH 0.2.1-rc.5（`ubuntu-vm`，Node 24.22.2 官方二进制包与 npm 都装在用户目录），Hermes 0.22.5 沿用 `ubuntu-vm-x86` 上已有的官方安装；三份 profile 已按官方文档和真实安装核实并写回（见 PROFILES.md 第 6 节）。
  - `testbed/drill.sh` M1 + M2 输出 `ALL_PASS (failures=0)`：三种 harness 的登记报告完整，凭据全部识别，身体被排除，npm 来源带 integrity（已与 registry 交叉核对），git 来源带 commit；`--accept` 写入 0600 的实例记录，再次登记能读到上次记录。
  - 新增设计（已写入文档）：SQLite 表级凭据剥离 `secret_tables`（M3 实现）；登记细节与默认值（ARCHITECTURE.md 第 4 节）。
  - 用户决定：PROFILES.md 6.2 中仍未核实的条目留到对应里程碑再核实，三份 profile 保持 `draft`；Hermes 的 14 个未分类文件保持未分类；6.4 示例 id 改为 `dsh-memory-json-store`。

## M3：封存与金库

- **状态**：`Done`（2026-10-30）
- **交付**：
  1. `core` 中金库格式的完整读写：blob、pack、index、快照、元数据、epoch。
  2. porter 的封存流水线：SQLite `VACUUM INTO`、`unstable` 检测、三个档位、凭据分层。
  3. 无私钥校验与有私钥校验；`porter sync` 同步到 `nas-test:/mnt/test/nas/valise-vault/`；`valise pull`。
  4. `tools/standalone-restore/restore.py`。
  5. 可选：定时封存（`systemd --user` timer，遵守 AGENT.md 中的 systemd 铁律）。
- **验收**：
  - 对未变化的实例做第二次封存时，新增字节数少于第一次的 1%。
  - 篡改任意一个 pack 后，校验能检测出来。
  - 只用 `restore.py`、`age` 和 `zstd`，就能把任意一个快照还原成逐字节一致的文件，全程不使用任何 Valise 二进制。
  - `soul` 档快照中不包含任何 `secrets` 类文件。
- **进度**（2026-10-30）：
  - 已完成：交付 1–4（格式、封存流水线、两种校验、rsync 同步、`valise pull`、`restore.py`）；交付 5（定时封存）按用户决定推迟。
  - `testbed/drill.sh` M1 + M2 + M3 输出 `ALL_PASS (failures=0)`：
    - 三个实例分别封存 soul 与 soul-secrets 档，同步到 `nas-test:/mnt/test/nas/valise-vault/<vault_id>/`，拉回本机后用私钥逐个文件校验通过。
    - 只用 `restore.py` + `age` + `zstd` 从 NAS 副本恢复：Hermes 341 个文件与服务器上的活文件逐字节一致（SQLite 为 `VACUUM INTO` 副本，按设计不比对；封存后被运行中的 harness 改写的文件单独列出）。
    - soul 档中没有任何凭据文件；OpenClaw `state/openclaw.sqlite` 的凭据表 `mcp_oauth_stores` 在 soul 副本中被清空。
    - 篡改本机副本中的一个 pack，免私钥与带私钥校验都能检出。
    - 未变化的 Hermes（首次封存 1.3 MiB）再次封存只新增 1352 字节，约 0.1%。
  - 测试中发现并修复：ControlMaster 套接字路径超长导致 SSH 全部失败；身体目录中的符号链接被记录进快照；`restore.py` 在大小写不敏感的文件系统上报错不清楚。
  - 用户决定："第二次封存新增少于首次的 1%"针对有实际数据的实例（首次封存 ≥ 1 MiB）验收。几乎为空的实例（刚初始化的 OpenClaw 39.6 KB、DSH 4.4 KB）因约 1.3 KiB 的固定开销（加密快照头、index、元数据）比例更高，数据本身完全去重。
  - 用户决定（M3 之后、M4 之前完成）："未知即敏感"——未分类文件进入 soul-secrets / full / raw 档；新增 `raw` 全量档位（见 ARCHITECTURE.md 第 5 节）；定时封存、raw 周期与保留策略移到 M8。靶机验证：Hermes 的 soul-secrets 档包含 14 个未分类文件；raw 档 95145 个文件、明文 2.8 GiB，首次写入金库 905 MiB（87 秒），未变化时再次封存只新增约 1 MB；从 NAS 副本在 `ubuntu-vm` 上用 `restore.py` 恢复 raw，身体部分 87524 个文件与 `ubuntu-vm-x86` 上的活文件逐字节一致，1124 个符号链接一致。
  - 两台 VM 的 `recipients.txt` 中有演练用的测试公钥（指纹 `0d5ba36bdcb0a906`），已用注释标明；身份文件在开发 Mac 的 `~/.cache/valise-drill/`。`valise key unregister` 实现后可以移除。

## M4：复活

- **状态**：`Done`（2026-10-30）
- **交付**：`valise key unregister`（语义见 ARCHITECTURE.md 第 7 节，执行前向用户说明旧快照无法撤销）、封存后的变动统计（ARCHITECTURE.md 第 6 节）、计划与 dry-run、按来源记录重建身体、暂存、交换日志、健康检查、自动回退、崩溃恢复、回滚、迁移、克隆三种模式、独占凭据处理、`valise prev drop`。
- **验收**（全部在靶机上完成）：
  - 回滚：同一个实例退回到更早的快照，服务恢复健康。
  - 跨架构迁移：OpenClaw 从 `ubuntu-vm`（ARM64）迁移到 `ubuntu-vm-x86`（x86_64，无 sudo），身体按来源记录重建后，服务健康。
  - 故障注入：让健康检查失败，结果为 `rolled_back`，原活动目录内容逐字节不变。
  - 崩溃注入：在 `LIVE_MOVED` 状态杀掉 porter，重新连接后，客户端提示未完成的操作，选择"退回"后状态恢复。
  - 克隆：新实例默认不带独占凭据，并且两个实例可以同时运行。
- **风险提示**（2026-10-30 模拟得出）：`raw` 档恢复会带回过期的 pid、锁和心跳文件；`soul-secrets` / `full` 档会带回未分类的锁文件（在 Hermes 上观察到的都是 0 字节）。复活流程必须先停服务、交换后再启动并做健康检查，计划中向用户说明这一点。
- **进度**（2026-10-30）：
  - 已完成全部交付。`testbed/drill.sh` M1–M4（`VALISE_DRILL_REVIVE=1`）输出 `ALL_PASS (failures=0)`，共 105 项。
  - 回滚：`ubuntu-vm-x86` 上运行中的 Hermes 退回到更早的 soul 快照，35.6 秒健康；凭据保留，3.5 GB 身体目录只改名搬动。演练中 OpenClaw（`ubuntu-vm`）每次回滚都健康。
  - 故障注入（health）：结果为 `rolled_back`，原活动目录（同一 inode）回到原位：Hermes 95143 个文件逐字节不变；OpenClaw 在演练中逐文件比对通过（基线在服务停止后采集，因为 harness 停止时会删掉自己的锁和临时文件）。
  - 崩溃注入：porter 在 `LIVE_MOVED` 退出后，`doctor` 报告未完成的操作；`revive resume --back` 后原目录与文件不变，服务恢复。
  - 克隆：OpenClaw 从 `ubuntu-vm`（ARM64）克隆到 `ubuntu-vm-x86`（x86_64，无 sudo），按来源记录安装 `openclaw@2026.10.9`，并执行 profile 的 `service.install_argv` 安装服务，用时 81 秒；两边同时运行。除两个运行中网关各自写入的 SQLite、锁与 tmp 文件外，其余文件逐一一致。OpenClaw profile 没有声明独占凭据（靶机未接入渠道，仍为 `verify: pending`），因此克隆带上了全部凭据。
  - 跨架构迁移：在克隆之上迁移，目标已有同一版本的身体，因此保持不变；目标启动前停止原机服务（原文件不动），用时 90 秒，服务健康；原数据库的全部行都在，目标上多出的是新网关启动后写入的行；克隆的旧目录保留为 prev，之后用 `valise prev drop` 删除。
  - 靶机上发现并修复：服务单元之外的进程（Hermes dashboard）在交换期间重新创建活动目录，导致退回时跳过改名却报告成功（现在列为阻断项，交换和退回时挪开闯入者并核实）；`VACUUM INTO` 副本变成 delete 日志模式（现在保留 WAL）；`hello` 的工具检测没有使用登录 PATH（`doctor` 看不到用户级 npm）；迁移、克隆写入的实例记录照抄了原服务器的 npm 前缀、服务状态为空（现在在目标上重新探测）。
  - 用户决定（2026-10-30）：迁移或克隆覆盖了目标上另一个已登记实例时，不删除它的记录，而是标记为"已被取代"；新增 `valise old list` / `valise old cleanup`，由用户再次确认后删除旧实例的记录和保留的旧目录（ARCHITECTURE.md 8.2.1）。v0.1 暂不单独发布。
  - 用户决定（2026-10-30）：迁移和克隆都沿用原实例的 `local_overrides`；规则随快照头保存（VAULT_FORMAT.md），原服务器不在也能恢复。
- **完成后发布 v0.1（CLI）。**

## M5：登录中继与 Web 入口

- **状态**：`Done`（2026-10-31）
- **用户决定**（2026-10-31）：隧道由客户端自己在回环地址上监听、每个连接经 ControlMaster 用 `ssh -W` 转发（LOGIN_RELAY.md 第 5 节）；快照头保存封存时的完整 profile（`profile_doc`），客户端没有同版本 profile 时可显式改用它复活，避免 profile 升级后旧快照无法复活；验收用 `ubuntu-vm` 上 OpenClaw 的 OpenAI Codex 回环登录，由用户在本机浏览器授权，验收后登出；Web 入口空闲超时默认 30 分钟。
- **交付**：LOGIN_RELAY.md 中的 `loopback_oauth`、`device_code`、`paste_code` 三种方式，以及 Web 入口；在 profile 中补充 `login` 声明。
- **验收**：从 macOS 客户端，对靶机上至少一个真实的回环 OAuth 登录完成授权；本机端口被占用时给出明确的报错；Web 入口的三个状态独立展示。
- **进度**（2026-10-31）：
  - 已完成全部交付：`loopback_oauth`、`device_code`、`paste_code` 共用一套流程（PTY 转发、browser-shim 与输出扫描两条渠道截获链接、打开前展示域名）；`remote_browser` 按计划未实现。Web 入口 `valise open`。OpenClaw profile v5 声明了三种登录方式。快照头保存完整 profile（`profile_doc`）。
  - 真实回环 OAuth：从 macOS 对 `ubuntu-vm` 上 OpenClaw 的 OpenAI 登录（回调 `localhost:1455/auth/callback`），由用户在本机浏览器授权，经登录中继完成，登录命令退出码 0，凭据写入 `openclaw-agent.sqlite` 的 `auth_profile_store`（该表早已列为凭据表，现已核实）。验收后用 `openclaw models auth logout` 删除凭据，期间没有封存。
  - 端口占用：本机 1455 被占用时报错并指出占用进程（如"Python（pid 70210）"），不换端口，服务器上的登录命令被结束、会话文件清理；Web 入口在 18789 被占用时改用 18790 并说明。
  - Web 入口：隧道、远端监听、健康检查三个状态分别展示；127.0.0.1 与 [::1] 都可访问；空闲超时后关闭并释放端口。
  - `testbed/drill.sh` M1–M5（`VALISE_DRILL_REVIVE=1`）输出 `ALL_PASS (failures=0)`，共 112 项。
  - 验收中发现并修复：登录命令退出后客户端仍在等待终端输入（需按键才收尾）。顺带发现 `state/openclaw.sqlite` 中另有 7 张按列名判断为凭据的表，已补进 `secret_tables`（`verify: pending`）。

## M6：agent-kit

- **状态**：`Done`（2026-10-31）
- **用户决定**（2026-10-31）：三种 harness 都做 agent 验收，模型凭据由用户自己登录或填写（Hermes 用现有 Nous 凭据；OpenClaw 验收期间保留登录，结束后登出；DSH 的 DeepSeek Key 由用户填写），验收期间不做带凭据档的封存；MCP 服务端自己写最小实现；技能正文用英文、要求 agent 用用户的语言交流；MCP 接入走用户路径（`valise kit install` 打印 harness 自己的命令，由人照抄执行）。实现约定见 AGENT_KIT.md 第 8 节。
- **用户决定**（2026-10-31，Hermes 首轮实测后）：模型不可靠地主动封存，增加代码兜底——同一 MCP 连接中第一次调用 Valise 工具时，距上次封存超过 10 分钟就先自动封存（AGENT_KIT.md 8.1）；验收数据结束后用回滚恢复到验收前，并拒绝测试中产生的提议。
- **交付**：`valise-porter mcp` 的六个工具、四个技能、体检的计算、"安装逃生舱技能"操作；基于变动统计的 agent 归类提议与"降级"单独确认（AGENT_KIT.md 第 3.1 节）。
- **验收**：
  - 在每种 harness 上，agent 都能完成"学习逃生舱 → 风险操作前自动封存 → 交接后新会话能读到交接内容"。
  - 对人工构造的超长会话和膨胀的记忆文件，能产生正确的 finding。
  - MCP 无法读取凭据，也无法写入交接目标以外的文件。
- **进度**（2026-10-31）：
  - 已完成全部交付。三种 harness 都用 `valise kit install` 安装技能，并按打印出的方式接入 MCP（Hermes、OpenClaw 用各自的 `mcp add` 命令，DSH 在 profile 补丁层插入 `dsh-mcp-client` 条目）。
  - Hermes（`ubuntu-vm-x86`，用户的 Nous 账号与 DeepSeek V4.1 flash）：学习逃生舱时先封存再往 `SOUL.md` 加一行提醒，提交归类提议（代码拒绝了把 `shared/nous_auth.json` 改归类）；交接写入 `memories/MEMORY.md`，新会话读出已定与未定事项；用 DeepSeek 模型时 agent 先查看 `valise-upkeep`、主动 `valise_seal` 再写记忆。免费模型（stepfun、beam）不会主动封存，由此增加代码兜底。
  - OpenClaw（`ubuntu-vm`，用户的 OpenAI 账号，经登录中继登录，验收后登出）：学习时在 `AGENTS.md` 加提醒；交接写入工作区 `MEMORY.md`，新会话读出；写偏好前主动封存。
  - DSH（`ubuntu-vm`，用户提供的接口与 Key，`headless` profile）：学习时在 `~/.dsh/AGENTS.md` 加提醒；交接追加到 `~/.dsh/AGENTS.md`（DSH 每个会话都加载），新会话读出；写偏好前主动封存。
  - 实测中发现并修复：Hermes 失败请求转储带 `Authorization` 字段（改归凭据）；Hermes token 在 `session_model_usage`、OpenClaw token 在事件 JSON 中（新增 `usage` 表与 `tokens_json`）；DSH 会话为 zstd 压缩 JSONL；OpenClaw 记忆机制标记漏了 `MEMORY.md`；DSH 补丁层新增条目须用 `insert`；未分类文件经提议进入 soul 档时客户端提示；被频率限制挡回的封存也写审计；DSH agent 因没有记忆文件而写进了同机 OpenClaw 的 `USER.md`（`valise_status` 现在返回实例自己的记忆位置，upkeep 技能禁止写其他 agent 的文件；复测时 agent 改为询问用户）。
  - 收尾：两把付费 Key 由用户同意后删除；三个实例回滚到验收前的 soul 快照并拒绝全部提议。回滚按设计保留档位外内容（Hermes `config.yaml`、OpenClaw `openclaw.json` 归为凭据，旧版 DSH profile 中技能目录未分类），因此技能与 MCP 配置另用 harness 自己的命令和精确路径撤除。
  - `testbed/drill.sh` M1–M6（`VALISE_DRILL_REVIVE=1`）输出 `ALL_PASS (failures=0)`，共 117 项。
  - 第 2、3 项验收以合成实例自动化，写进 `testbed/drill.sh` M6：超长会话与膨胀记忆文件得到 `long_session`、`memory_bloat`；MCP 只有六个工具，任何输出中都没有 `.env` 的内容，把 `.env` 改归类与 root 外路径的提议被拒绝，改动交接区段以上内容的交接被拒绝。

## M7a：终端 UI（TUI）

- **状态**：`In progress`（2026-10-31 开始）。第一版完成后用户试用，认为信息架构不面向用户，要求重做；拆成 M7a.1 与 M7a.2 两步。
- **用户反馈与决定**（2026-10-31，已写入各文档）：
  - 首页只放服务器卡片，服务器页只放实例卡片（字符画图标取自 profile 的 `display`），实例页才有操作；设置页按"加密钥匙 / 服务器 / 云备份 / 更新 / 高级 / 关于"分栏（UI.md 第 9 节）。
  - 界面用语：每份快照是一个"逃生舱"，封存叫"建立逃生舱"，复活叫"逃生舱着陆"，入口叫"管理页"（PRODUCT.md 8.1）。逃生舱列表人性化展示并可搜索。
  - 金库按"服务器 / 实例 / 周期"分档，每个周期是独立金库，周期可设（默认自然月）；服务器与云端都可以由用户显式删除整个已结束的周期，各自确认，不做自动保留策略（VAULT_FORMAT.md 第 10 节）。
  - 自动云备份：默认随建立逃生舱顺带同步，可选 `systemd --user` 定时器（ARCHITECTURE.md 第 7 节）。
  - 管理页令牌：profile 声明取令牌的官方命令时由 Valise 代取并直接交给浏览器；否则提供"打开服务器终端"（LOGIN_RELAY.md 4.1）。
  - 加密：添加服务器时自动登记公钥，收件人管理放进"高级"；第一次启动走引导（ARCHITECTURE.md 第 3 节）。
  - 体检给处理建议，记忆整理技能 `valise-memory` 先占位；归类提议改称"归档建议"并入体检页（AGENT_KIT.md 8.6）。
  - 更新：客户端自身、porter、harness 资料包（单独下载的签名包，用户随后提议并决定）、逃生舱技能。
  - 推进方式：先改文档并做可点击的界面草图，用户确认后再写代码。
- **第一版试用中发现的 bug**（在 M7a.2 中修复并加测试）：归类提议页按 Esc 退出时 `index out of range [-1]` 崩溃（`tui/kit.go`）；列表超过窗口高度时光标移出屏幕、渲染停止。

### M7a.1：金库分档与云备份

- **状态**：`Done`（2026-10-31）
- **交付**：
  - 金库树（VAULT_FORMAT.md 第 10 节）：`TREE`、`PERIOD`、周期设置、按周期选择或新建叶子；分档之前的旧金库可读；`tools/standalone-restore/` 的说明更新为"指向一个叶子目录"。
  - 服务器名（`server.json`），云备份目录多一层服务器名；同步按叶子进行并跳过 `DELETING`。
  - 云端列表（rsync `--list-only`）与"服务器有 / 云端有 / 两处都有"对照。
  - 删除整个周期（服务器、云端分别执行），`DELETING` 标记与续删；不能删当前周期；没有其他副本时二次确认。
  - 自动云备份：随建立逃生舱顺带同步（`valise-porter autosync`）；可选定时器 `valise-seal@<instance>.timer`。
  - 添加服务器流程（doctor → 部署 porter → 自动登记公钥 → 服务器名），CLI 同步提供。
  - profile schema：`display`、`entries[].auth`、`checkup.remedies`；三份 profile 补上图标（`verify: pending` 直到对照官方 TUI 核实）与 OpenClaw 的管理页令牌命令（在靶机上核实）。
  - 管理页令牌的代取与"打开服务器终端"。
  - harness 资料包（PROFILES.md 1.1，用户 2026-10-31 提议并决定并入本步）：客户端不再内置 profile；`profilepack` 打包工具、验签安装（在线 / 本地文件）、未签名包作为本地 profile 安装、`valise profiles status|install`；演练与测试改用仓库 `profiles/` 作为本地 profile 或测试钥匙签名的包。
- **验收**：fake-ssh 单元测试覆盖周期切换（含修改周期设置）、删除中断后续删、同步跳过 `DELETING`；在 `ubuntu-vm`、`ubuntu-vm-x86` 与 `nas-test` 上实测：跨月份的两个叶子、同步到 NAS 后的目录结构、NAS 上删除一个周期、用 `restore.py` 脱离 Valise 恢复一个叶子、随逃生舱顺带同步、定时器开关；OpenClaw 管理页经代取令牌直接进入；drill 追加对应步骤并 `ALL_PASS`。
- **进度**（2026-10-31）：
  - 已完成交付（提交 `6cc7a29`、`d4e1f11`、`a905617`）：资料包（`profilepack`、验签安装、未签名作为本地 profile、`valise profiles`）；schema 的 `display`、`entries[].auth`（另加 `url_field`，见下）、`checkup.remedies`；金库树与周期、服务器名、按周期同步、云端列表、服务器与云端按周期删除、随封存自动同步、定时器、管理页令牌与 `valise shell`、`valise server setup|name`、`period`、`timer`、`target list|delete|auto`、`vault delete`、`pull --leaf`；`restore.py` 与恢复套件说明指向周期文件夹；drill 新增 M7a.1 段。
  - 单元测试：周期切换（改周期设置、长时间未封存）、旧金库可读、删除中断后续删、当前周期拒绝删除、定时器单元内容（无 capability 设置）、令牌命令输出不进入错误信息、资料包验签与篡改。依赖 GNU rsync（`--mkpath`）的云备份测试在 macOS（openrsync）上跳过，交叉编译后在 `ubuntu-vm` 上运行通过（同步、云端列表、云端删除、自动同步）。
  - 真机（`ubuntu-vm`、`ubuntu-vm-x86`）：部署新 porter；`server setup` 两台；旧金库显示为周期 `-`（107 个逃生舱）；封存写入新周期并生成 `TREE`、`PERIOD`；封存后自动同步子进程按间隔启动（间隔未到时不启动）；OpenClaw 管理页令牌经 `openclaw dashboard --no-open --json` 的一次性配对链接取得，不显示；定时器写入、手动触发一次成功封存（发起者 timer）、关闭后单元删除；服务器上删除过去周期（复制出的测试周期）与拒绝删除当前周期；拉取一个周期后用 `restore.py` 脱离 Valise 恢复。未签名资料包作为本地 profile 安装。
  - 核实 profile 时发现并修正：OpenClaw `dashboard --no-open` 的纯文本输出不带令牌，`--json` 同时给出长期令牌 `url` 与一次性 `browserUrl`，`gateway auth-token --show` 拒绝在非终端运行，因此 schema 增加 `url_field` 并选用 `browserUrl`；官方字符画比原定上限大（OpenClaw 10×20、Hermes 15×30），上限放宽为 16×32、原样保存，缩放由界面完成；DSH 安装包中没有字符画，使用占位图标。
  - NAS 实测（用户同意后，两台 VM 的 `nas-test` 改指向 192.168.101.109，主机密钥与用户 Mac 上已信任的 ED25519 指纹一致后才写入 `known_hosts`；原配置备份为 `~/.ssh/config.bak-valise-20261031`）：同步到 `nas-test:/mnt/test/nas/valise-vault/<服务器>/<实例>/<周期>/`、从 NAS 的周期文件夹用 `restore.py` 恢复、云端列表与服务器对照、NAS 上删除过去周期、随封存自动同步成功。
  - 演练中发现并修复：drill 仍按旧文案找"入口"；等待自动同步时读到上一轮的审计行、`pgrep` 经 ssh 匹配到自身；用户操作撞上后台自动同步时只提示"另一次封存或同步"，现在锁文件记录持有者，提示"正在后台自动同步到云备份"。
  - `testbed/drill.sh` M1–M7a.1（`VALISE_DRILL_REVIVE=1`）输出 `ALL_PASS (failures=0)`，共 133 项。

### M7a.2：TUI 重做

- **状态**：`Frozen`（2026-11-01）
- **2026-11-01 范围调整**（用户决定）：不再在旧信息架构上继续打磨，M7a.2 到此冻结，现有 TUI 保持可用。UI.md 8.1 的统一卡片与新图标标准移到 M12（TUI 改版）；在那之前 harness 图标一律用机器人占位，OpenClaw、Hermes 的新图标由用户另找方法重绘，到 M12 时加入 profile。
- **原交付**：按 UI.md 第 8、9 节重做 `valise tui`：第一次启动引导、首页服务器卡片、服务器页实例卡片（字符画图标）与云备份 / 空间清理 / 服务器设置子页、实例页五个分栏（概览、逃生舱、体检、管理页与登录、设置）、全局设置页六个分栏（含更新）；所有列表可滚动；CLI 的人类可读输出改用界面用语。
- **验收**：单元测试覆盖滚动、各页面状态迁移与上述两个 bug 的回归；在两台 VM 上由用户亲自试用通过。
- **进度**（2026-10-31，提交 `9ada4eb` 及之后）：
  - 按 UI.md 第 8、9 节重写 `client/ui/tui/`（每个文件不超过 400 行）：第一次启动引导（口令两次、钥匙位置与备份、资料包、第一台服务器）；首页只有服务器卡片；服务器页实例卡片（profile 字符画，盲文点阵按点缩放、其他字符画最近邻采样，没有图标用机器人占位）及云备份、空间清理、服务器设置、登记新 agent；实例页五个分栏（概览、逃生舱、体检、管理页与登录、设置）；全局设置六个分栏。所有列表与卡片网格随光标滚动，正文按窗口高度裁剪。
  - 新增只读 RPC `seal_log`：逃生舱元数据里没有"谁建立的、原因"，从 porter 的审计记录按实例读出，供列表显示（你手动建立 / agent 自己建立 / 安全网自动 / 交接前 / 定时）。
  - CLI 的人类可读输出改用界面用语（逃生舱、建立逃生舱、逃生舱着陆、轻装/标准/完整/原样、管理页、登录授权），命令名与参数不变；drill 相应更新。
  - 测试：首页 → 服务器 → 实例 → 建立逃生舱 → 列表搜索 → 详情（id 折叠）；原地着陆全流程（含交换期间拒绝退出）；两个 bug 的回归（归档建议为空时按 Esc；81 个逃生舱在 24 行窗口里滚动、光标可见、视图不超过窗口）；云备份 / 空间清理 / 服务器设置；全局设置六个分栏；第一次启动引导；图标缩放。
  - 真机（以真实服务层与 SSH 驱动 TUI 模型、打印 `View()`）：两台 VM 的首页卡片、服务器页（OpenClaw 原样吉祥物、Hermes 双蛇杖按点缩放、DSH 占位）、OpenClaw 96 个逃生舱的列表与详情、体检的条形图与处理建议、管理页与登录、实例设置。修正：卡片放宽到 46 列、图标框 20×10，使 OpenClaw 字符画原样显示；实例页标题处非盲文字符画不再强行缩小（改用主色色块）；处理建议使用 harness 的显示名。

### M7a 第一版记录（2026-10-31）


- **用户决定**（2026-10-31）：UI 分两步，先 TUI（M7a）后 GUI（M7b）；TUI 用 Bubble Tea，作为 `valise tui` 子命令；功能范围与 GUI 计划一致，并加上 agent-kit。规范见 `docs/UI.md` 第 8 节。顺带加上 `valise kit status` 与 `valise kit uninstall`（被修改过的技能保留，`--force` 才删，AGENT_KIT.md 8.5）。已记录服务器显式维护：`servers.json` 只存别名，`valise server add|remove|list`，TUI 可从 `~/.ssh/config` 导入（ARCHITECTURE.md 第 3 节）。
- **交付**：
  - 已记录服务器（`servers.json`、`valise server`、导入 SSH 配置）与实例列表 RPC；`valise kit status`、`valise kit uninstall`。
  - 实例总览：每台已记录服务器上的实例，展示 profile 的核实状态、服务状态、最近一次封存与同步状态、finding 和入口。
  - 金库：快照列表、封存（档位选择）、同步、复活向导（计划 → 暂存 → 确认 → 交换进度 → 结果）、prev 与旧实例清理。
  - 登录中继与入口：选择登录方式后暂停 TUI、把终端交给登录中继，结束后回到 TUI；入口的三个状态分开展示。
  - agent-kit：体检、归类提议的确认（降级单独确认）、安装逃生舱技能。
  - 设置与信任根：密钥（生成、导入、显示、导出恢复套件）、登记与移除公钥、同步目标。
- **验收**：服务层零改动或只做通用扩展（不出现 TUI 专用逻辑）；TUI 的状态与渲染有单元测试（不依赖真实终端）；连接 `ubuntu-vm` 与 `ubuntu-vm-x86` 完成真实的交互验收（总览、一次封存、一次回滚、一次登录中继、一次入口）；CLI 全部功能不受影响。
- **进度**（2026-10-31）：
  - 已完成交付：`valise tui`（Bubble Tea v2.1.1）——服务器总览、实例详情（封存档位选择、体检、归类提议逐个确认降级、逃生舱技能状态与安装卸载、登录中继、入口）、金库（快照、同步、同步目标、交换前状态、被取代的旧实例）、复活向导（选项 → 掩码口令只用一次 → 计划 → 暂存确认 → 交换确认 → 结果）、设置与信任根；`valise server`、`valise kit status|uninstall`；`instance_list` 返回实例的实时服务与入口状态。服务层只有通用扩展（服务器列表、实例列表、kit 状态与卸载），没有 TUI 专用逻辑。
  - 单元测试直接驱动 `Update`/`View`（含 fake-ssh 上的完整回滚向导与"复活期间拒绝退出"），`-race` 通过。
  - 真机（经伪终端驱动真实的 `valise tui`）：两台 VM 的总览（5 个实例的实时服务、入口、封存与同步、体检）、一次封存、一次 OpenClaw 回滚（计划正确拦下 v7 实例需要快照内 profile，同意后暂存、交换，服务健康）、一次入口（三个状态、Ctrl-C 关闭后回到 TUI）、一次登录中继（端口被占用的路径：报错停留在屏幕上，回车回到 TUI，服务器无残留）。`kit status/uninstall` 在 Hermes 上实测：被改过的技能保留，`--force` 才删。
  - 冒烟测试中发现并修复：服务层在确认交换后仍跟随调用方的取消（Ctrl-C 可能打断交换）——改为 `context.WithoutCancel`；TUI 交换期间按 Ctrl-C 会退出；关闭入口时 SIGINT 连带结束 TUI；CLI 步骤的输出被 TUI 立即覆盖；封存后详情不刷新；计划的建议写的是 CLI 参数。

## M7b：桌面 UI（mygo）

- **状态**：`Paused`（2026-11-01 用户决定：避免按旧信息架构做一遍再推倒重来；在 M12 完成、新信息架构在 TUI 上稳定后恢复。原生界面在 GUI 中的内嵌终端随 M7b 设计，见 INSTALL.md 第 2 节）
- **交付**（遵守 `docs/UI.md`，信息架构与 TUI 相同，见第 9 节）：
  - 开工第一步：锁定 mygo 版本（用户已指定 v0.7.3，提交 `6f28d7b`），写入 `docs/UI.md` 第 3 节，并对照该 tag 的文档复核 UI.md 中的全部约定。
  - 实例总览：实例卡片，展示 profile 的核实状态、服务的三个状态、最近一次封存与同步状态、finding 和入口。
  - 金库：快照列表、封存档位选择、复活向导（计划 → 暂存 → 确认 → 交换进度 → 结果）。
  - 设置与信任根：导入 SSH 配置、管理密钥、登记公钥、导出恢复套件、管理同步目标。
  - 登录中继的交互界面。
- **验收**：`go tool mygo generate && bun run typecheck && bun run build:web` 零警告通过；检查确认 main 函数中没有任何副作用，入口只通过系统浏览器打开；连接 `ubuntu-vm` 完成真实的交互验收。

## M8：raw 周期

- **状态**：`Blocked (depends on M13)`（定时建立逃生舱已并入 M7a.1；保留策略由"用户显式删除整个周期"代替，用户 2026-10-31 决定不做自动保留策略）
- **交付**：低频 raw 周期（例如每周一次），只在服务器与空间站空间充足时建议开启，默认关闭；空间紧张时界面主动提醒。
- **验收**：待 M8 开工时与用户确认。

## M9：规范修订

- **状态**：`Done`（2026-11-01，用户审阅确认；之后又按用户决定补充：完全访问模式、两边一起改端口、管理钥匙可选且用单独的管理口令）
- **交付**：按 REDESIGN_PROPOSAL.md 第 11 节逐份修订：PRODUCT、CLAUDE、AGENT、ARCHITECTURE（日常钥匙、管理登录与操作分级、手动添加服务器、教学逃生舱、取回本地）、PROFILES（按能力展示、图标新标准、`install`/`native_cli`/`actions`）、LOGIN_RELAY（同端口规则、本地页面转发）、新建 INSTALL.md、AGENT_KIT（受管方 / 管理方）、UI（卡片标准、信息架构）、DEV_PLAN、VAULT_FORMAT（教学标记不改格式）、REFERENCES。
- **验收**：用户审阅修订后的规范；文档之间、文档与 CLAUDE.md 铁律之间没有冲突。

## M10：接入服务器、日常钥匙与管理登录

- **状态**：`Done`（2026-11-01）
- **范围调整**（2026-11-01 用户决定"TUI 和 GUI 将在新的阶段开始优化"）：TUI 中手动添加服务器的完整流程、第一次启动可跳过钥匙的引导、软限制的界面移到 M12；M10 只给 TUI 接上管理口令输入框，并在 TUI 中遇到需要服务器密码的操作时给出明确提示。
- **交付**：Valise 自己的 `ssh_config` 与 `known_hosts`；手动添加服务器（主机身份确认、引导登录、安装日常钥匙、可选创建管理钥匙）；`servers.json` 新格式与旧格式兼容；porter `serve --tier daily` 与 RPC 方法分级（含"没有收件人时日常钥匙可登记第一把"）；管理登录（管理钥匙与单独的管理口令、Valise 自己的 ssh-agent 与时间窗口；没有管理钥匙时用服务器密码临时登录；每次操作独立连接）；自带钥匙服务器的软限制与"迁移到日常钥匙"；第一次启动允许跳过钥匙；porter 检查更新；TUI 的添加服务器流程与解锁提示（UI.md 9.1、9.3、8 节）。
- **验收**：fake-ssh 测试覆盖分级拒绝与参数构造；靶机实测：不经 `~/.ssh/config` 手动添加一台服务器、日常钥匙无法拿到 shell 也无法调用管理方法、`permitopen` 只放行回环、没有管理钥匙时管理操作要求服务器密码、管理钥匙窗口到期后要求管理口令、管理操作不复用日常连接、指纹变化时停止；NAS（TrueNAS）上强制命令的支持情况如实记录。
- **进度**（2026-11-01，提交 `df6e332` 及之后）：
  - 已完成交付：porter `serve --tier daily`（自己解析 `SSH_ORIGINAL_COMMAND`，只允许 `rpc`、`version`、`vault-cat`）与 RPC 分级（`core/rpc/tier.go`，测试强制每个方法都要归类）；"没有收件人时日常钥匙可登记第一把"；`servers.json` 新格式与 Valise 自己的 `ssh_config`；日常钥匙；管理钥匙（`ssh-keygen` 以管理口令加密、Valise 自己的 ssh-agent 与时间窗口）与临时登录；每次管理操作独立连接；按方法分级自动路由的 porter 会话；手动添加、创建管理钥匙、迁移、修改管理口令、`porter check`；TUI 的管理口令输入框。
  - 真机（用户同意后，在三台的 `~/.ssh/authorized_keys` 中临时加钥匙，先备份为 `authorized_keys.bak-valise-m10`，验收后删除）：`vm-arm`（ubuntu-vm，OpenSSH 10.2）、`vm-x86`（ubuntu-vm-x86，10.5）、`nas`（TrueNAS，10.0）都不经 `~/.ssh/config` 手动添加成功（指纹与 Mac 已信任的记录一致，临时登录用已有私钥文件）。三台上日常钥匙：拿不到 shell（126）、不能分配终端、scp 与 sftp 失败、RPC 管理方法返回 `admin_required`、`-W` 只放行回环、`-R` 失败。vm-arm 上日常钥匙登记报告、建立逃生舱、体检；管理口令错误时不解锁，正确时设置周期；着陆计划（dry-run）经管理钥匙走通，结束后没有残留的临时主连接与 agent。vm-x86 上时间窗口设为 1 分钟：窗口内第二次管理操作不再询问，65 秒后被清除，再次操作重新询问。本机副本中伪造指纹，连接被拦下并提示核对。`ubuntu-vm` 别名从 `~/.ssh/config` 迁移成功，与 `vm-arm` 的日常钥匙并存。
  - 真机发现并修正：`restrict,port-forwarding` 同时放开了远程转发（`-R` 能在服务器回环上监听），`permitlisten="none"` 会让整行钥匙失效，改为 `permitlisten="127.0.0.1:1"`（普通用户绑定不了特权端口）；安装钥匙按注释删除旧行会删掉别的电脑或别名装的钥匙，改为按公钥去重、注释带本机名；没有终端也没有口令时报错不清楚。TrueNAS 默认 `AllowTcpForwarding no`，任何钥匙都不能转发（已写入 ARCHITECTURE.md）。
  - 测试中发现并修正：askpass 用"含 old"判断旧口令，被路径里的 `folders` 误判，生成钥匙时卡住；口令错误时 `ssh-add` 无限重问。
  - 附加靶机 InstaBox `nazukiki-insta-01`（`m_agqvodzudt6na6ts26xpdu3hgi`，Debian 13 x86_64，有免密 sudo；用户 2026-11-01 提供并授权清空历史）：先拍快照 `valise-pre-wipe-20261101`（`sn_agqzkux7dyi6p2rpmnwzl4xdsu`），再清除旧 Fathom 文件与程序、Hermes 及其用户服务、后装的 apt 包（age、zstd、sqlite3、golang 1.24），家目录回到模板状态。从零用用户自己的 SSH 设置接入并部署 porter 成功（没有加密钥匙时按设计推迟登记）。发现（严重）：InstaBox 的 SSH 网关以 `none` 认证放行任何人、不看 `authorized_keys`，日常钥匙在那里拿到完整 shell。修正：添加与迁移前先探测无凭据登录，装好后再验证限制确实生效，否则撤掉钥匙并拒绝（fake-ssh 新增对应模拟，单元测试覆盖；在 InstaBox 上实测两条路径都被拒绝、服务器上没有留下钥匙）。
  - drill 回归中发现并修正（严重）：测试配置与 drill 配置的缓存目录都过长、都退回 `/tmp/valise-<uid>`，而主连接套接字只按主机、端口、用户命名，drill 对 ubuntu-vm 的完整登录复用了测试配置里日常钥匙的主连接，管理操作被拒绝，崩溃注入后的 `resume --back` 没有执行，OpenClaw 停在 `LIVE_MOVED`。处理：关闭共享主连接后 `resume --back` 退回成功（数据完整）；日常钥匙的主连接改用 `cmd-%C`，与完整登录的 `cm-%C` 分开（单元测试覆盖）。顺带修正：服务在目录被移走期间崩溃重启触发 systemd 启动频率限制，退回后启动失败，porter 现在启动前先 `reset-failed`。
  - 修正后 `testbed/drill.sh` M1–M7a.1（`VALISE_DRILL_REVIVE=1`）输出 `ALL_PASS (failures=0)`，共 133 项（drill 的 op id 匹配同时接受 M7a.2 改名后的"着陆计划"）。验收结束后按用户要求删除三台靶机上的测试钥匙（`authorized_keys` 与备份逐字一致后删除备份），并删除测试在 NAS 上生成的 `~/.config/valise/`；测试钥匙确认已被拒绝。

## M11：OpenClaw 端到端安装

- **状态**：`Done`（2026-11-01）
- **进度**：
  - 已完成：官方方案（install-cli.sh 本地前缀，脚本哈希核对、展示、上传后执行）与 `native_cli`；依赖检测（`install_check`）与审计（`install_audit`）；`detect.bin_dirs`；`capabilities` 与 lint；安装后检测并引导纳入管理、打开管理页；管理页同端口规则与"两边一起改端口"（`entry_ports`、`entry_port_set`，OpenClaw 的 `port_change` 在 InstaBox 上 18789 → 18790 → 18789 核实）；完全访问模式（只用于 profile 依赖安装，INSTALL.md 4.1 的 M11 范围）与第 7 节基础检查（`core/install` `CheckCommand`）；本地页面转发 `valise forward`（同端口、只接受回环地址、查询与片段不显示不记录，日常钥匙即可）。
  - drill：新增 M11 段（转发、非回环拒绝、端口占用提示、两边改端口并改回），M5 的端口冲突断言改为同端口规则；`testbed/drill.sh ubuntu-vm` ALL_PASS（74 项，2026-11-01）。
  - 原生引导收尾（2026-11-01 用户决定）：`valise shell <服务器> --profile <id>`（harness 终端，PATH 含 `bin_dirs`，不改服务器配置）；profile `native_hints`（打开原生界面前显示）；可选安装步骤（`optional`/`id`/`skip_effect`，默认不执行，`--with <id>`）与 `path_add` 步骤（porter `path-add`，带标记、只加一次）；OpenClaw v12 声明可选的 PATH 步骤与引导提示。debian-vm 核实：普通 ssh 找不到 openclaw，harness 终端里可以；勾选 `path` 后 `~/.profile` 追加两行，登录 shell 能直接运行 openclaw；重装没有改动用户已有的 `openclaw.json`。
  - 原生引导验收（2026-11-01，用户在 debian-vm 上操作）：`valise native` 走完 `openclaw onboard`；引导末段等待控制台登录时，另开终端 `valise open` 带一次性令牌自动登录，引导收到成功信号；harness 终端与 PATH 步骤可用。
- **靶机记录（2026-11-01）**：
  - InstaBox（Debian 13 amd64，清空后）：从零安装 OpenClaw 2026.10.9，登记（来源含 integrity），管理页健康 HTTP 200；本机端口被占用时报告占用进程并提示 `--change-port`；两边一起改端口并改回。
  - debian-vm（Debian 13 arm64，全新）：Valise 接入（日常钥匙 + 管理钥匙，限制检查通过）→ 部署 porter → `install --full-access`：两个口令与确认句挑战、sudo 依赖（git、curl）装好、重新检测通过 → 官方方案 5 步完成 → 登记 → 管理页 HTTP 200。管理窗口到期（清空 agent）后，下一步要求重新输入管理口令，错误口令时停止，后续命令未执行；含"网络内容经管道交给解释器"的批次在开启前整批拒绝；审计记录开启、每条命令与退出码、关闭（`install_audit` 改为日常级，窗口到期后仍能补记）；`valise forward` 只用日常钥匙打开 OpenClaw 管理页（HTTP 200），地址中的测试令牌未出现在输出、审计与本机文件中，非回环地址被拒绝。用临时本地 profile 在 debian-vm 上验证三种错误提示：脚本哈希不符（停止，提示等资料包更新或原生安装）、版本不符（列出实际与要求的版本，未开始安装）、步骤失败（报告第几步与退出码）。
- **交付**：OpenClaw 的官方方案（`install`）与 `native_cli`；原生界面（TUI，独立连接）；依赖检测；完全访问模式（口令挑战、免责确认、接管执行、窗口到期暂停、疑点检查与明显有害命令拒绝，INSTALL.md 4.1、第 7 节）；本地页面转发（同端口）；管理页改为同端口规则与"两边一起改端口"（LOGIN_RELAY.md 4.2，OpenClaw 的 `port_change` 在靶机上核实）；安装后检测并引导打开管理页；`capabilities` 字段与 lint。
- **验收**：在一台干净靶机上从零完成"手动连接 → 部署 porter → 安装 OpenClaw → 原生引导 → 打开管理页"，另测已安装实例的纳入管理；本机端口被占用（关闭占用程序 / 两边一起改端口）、脚本哈希不符、版本不符、安装失败的提示；完全访问模式下装好缺失的 Node 运行时，窗口到期后要求重新输入口令，明显有害的命令被拒绝；原生界面的输入不进入任何日志。

## M12：日常管理

- **状态**：`In progress`（2026-11-01）
- **进度**：
  - 草图 `docs/sketches/M12-tui.md` 与设计点已由用户确认（动作规则、首页卡片样式、图标尺寸），结论已写回 INSTALL.md 第 5 节、PROFILES.md、UI.md 9.2。
  - 已完成（后台）：图标 lint 改为 12×24 / 6×12（含 `icon_compact`、只允许单宽字符），Hermes 原图案超标，移到 `docs/icons/` 待用户重绘，暂用内置机器人；管理动作 schema 与 lint；porter `action_run`（按服务器上保存的 profile 副本执行，日常钥匙只能跑非机密的只读动作，审计不含输出）与 `action-exec`（需要终端的动作）；服务层 `Actions` / `RunAction`（`config`/`danger` 默认先建逃生舱，`manage` 及以上动作后检查服务与管理页）与剪贴板（20 秒，仅当剪贴板仍是该机密时清除）；CLI `valise action`。
  - OpenClaw v13 声明 5 个动作（status、health、copy-token、restart、doctor），前四个在 debian-vm 上核实：只读动作走日常钥匙；复制令牌不显示、20 秒后清除、审计只记 id 与退出码；重启后服务运行、管理页端口在监听。`doctor --generate-gateway-token` 不是令牌轮换（已有令牌时不变），没有作为动作。
  - 用语改为「接入 Valise」（PRODUCT.md 8.1）与点缀语规则（UI.md 第 9 节），用户确认。
  - 终端 UI 改版（草图经 Artifact 确认）：首页整宽服务器卡片纵向排列；服务器页 52×14 标准卡片与 36×8 紧凑卡片（窄于 80 列或矮于 24 行时统一换用）、横向翻页与页码圆点；图标区固定 24×12 / 12×6，内置机器人图标，盲文按点比例缩放；实例页标题用紧凑图标，分栏改为 概览 / 管理 / 体检 / 逃生舱 / 设置；概览给出需要处理的事、快捷操作、支持程度与一行备份状态；管理分栏含管理页、本地页面、登录授权、按风险分组的管理动作（确认框、逃生舱勾选、高风险输入名称、结果分开展示退出码与实际状态、机密 20 秒倒计时）与原生界面 / harness 终端；顶栏显示管理权限窗口（L 立即锁定）与完全访问模式；底部按键换行；点缀语集中在 `flourish.go`。单元测试覆盖卡片尺寸、翻页、动作流程与 80×24 下光标可见。
  - 用户第一轮试用反馈（2026-11-01）与处理：设置页分栏改为 ↑↓ 切换、→ 进入列表；实例页分栏滚动跟随光标；归类规则做成子页；顶栏 `U` 主动解锁管理权限；逃生舱分栏与详情加"上云"；原生界面用 Ctrl+C 结束不再报"未预期的错误"（OpenClaw v14 加一条提示）；取回与着陆显示阶段、进度条、百分比与已用时间，配用户提供的火箭动画（着陆成功后火箭才出现，上云成功后升空）；终端界面里的 OpenSSH 一律 `BatchMode`，修复着陆时 ssh 向终端要密码导致输入无效、界面卡死的问题，管理窗口过期时由界面重新询问；新增导出 / 导入 Valise 配置（ARCHITECTURE.md 3.1，只含记录，不含钥匙）与 `valise server rekey`。
  - 第二轮反馈：服务器设置 `n` 起名、上云前缺名时先起名；着陆与上云改为全屏火箭场景加科幻遥测面板（草图经 Artifact 确认，UI.md 第 8 节）：航天说法的步骤与白话小字、底部大进度条与右上遥测、下令交换后火箭下降并在未完成时悬停、成功提示等落地或飞离后出现、完成后才可 enter 跳过、失败时火箭飞离；背景播放到离开页面。
  - 三级权限（2026-11-02 用户决定，ARCHITECTURE.md 2.2）：日常 / 主人（日常钥匙 + Valise 口令，porter 用 age 加密的随机数确认）/ 开发者模式（原管理登录）；着陆、技能、周期、定时器、同步目标、不敏感的删除改为主人操作；`stage-write` 在日常钥匙上凭一次性许可运行；删除按"别处是否还有副本"分级；着陆改为只看 `capabilities.restore`（PROFILES.md 1.2）。登录授权暂留开发者模式：`login-run` 需要终端，而日常钥匙的 `restrict` 禁止分配终端，放开需要改服务器上的 authorized_keys（待用户决定）。
  - 剩余步骤（2026-11-02 用户确认顺序）：③ 同步回落到 sftp（另一台机器缺 rsync 时）与**本机仓库**（客户端取回后写入本机文件夹，可与空间站并存；"仓库"专指本机文件夹）；④ Valise Kit 三级（AGENT_KIT.md 8.4.1，含"加强套装"清单与 `完全访问模式` → `高级模式` 改名）与高级功能前提（INSTALL.md 4.2）（2026-11-02 已完成：改名、高级功能前提；Kit 增强与专家两级的清单与 schema 待用户确认）；⑤ 健康页（2026-11-02 已完成：健康汇总与分栏、恢复测试（本机与服务器各解开一次，不切换，过后删除；在 Oracle Linux 上实测通过）、`valise_declare`、服务器时钟偏差、被拦下时一键备份到仓库；待做：首页卡片标记、`approval_check`）（UI.md 9.5，含着陆演练、Bypass 警告、MCP `valise_declare`，参考 magpie 的 `proc` 与 `sessions`，见 REFERENCES.md 第四节）；⑥ 多实例与发现规则（参考 magpie 的 `internal/agent`）。云盘（rclone）放进 M13。
  - ③ 已完成（2026-11-02）：porter 同步改为可替换的传输层，rsync 缺失时自动改用 sftp（逐层列出、`.part-` 上传后改名、删除先写标记），结果写明方式与原因；本机仓库（`valise repo add|list|remove|sync`，TUI"仓库与空间站"页、上云可选"全部"、打开 Valise 与建立逃生舱后在后台补齐、逃生舱详情逐个列出副本）。顺带修正 `pull --to` 把周期名中的 `-` 全部替换成 `legacy` 的错误。sftp 回落已在 ubuntu-vm（Ubuntu 26.04 arm64，删除 rsync 后）对 TrueNAS 实测：自动改用 sftp、上传新逃生舱无残留 `.part-`、云端列表与大小正确。同步失败时按 SSH 报错指出原因（不认识主机名、拒绝登录、未确认指纹、连不上）。
  - ③b Valise 管理的空间站（2026-11-02 用户决定）：空间站记录与钥匙归客户端（`stations.json`、`valise station add|list|remove`、TUI 添加时在终端核对指纹并输入一次密码）；传输层移到 `core/stationsync` 供 porter 与客户端共用；每台服务器由 porter 用 ssh-keygen 生成自己的空间站钥匙，客户端装到空间站（`restrict`），并把地址、端口、用户与指纹写进服务器 `targets.json`；上云先直传，服务器连不上时经本机仓库或临时目录中转，中转后 `sync_record` 记入服务器；本机 openrsync 不支持 `--mkpath` 时也改用 sftp。服务器凭客户端签发的短期证书访问空间站（7 天、自动续期、来源地址、rrsync 只写不删不覆盖）。2026-11-02 在 TrueNAS 上实测：debian 直传成功；服务器证书无法开 shell、读取或删除；经仓库中转与临时中转（临时目录已删除）成功，本机 openrsync 自动改用 sftp。Parallels 的虚拟机经 Mac 做地址转换，来源地址与 Mac 相同；之后在外网服务器上补测（见下）。顺带修正：确认主机指纹后复扫按顺序比较导致误报"身份在确认期间变了"。同日经 Tailscale 让外网服务器连上 NAS：直传成功，证书限定来源地址为服务器的 Tailscale 地址；同一套钥匙与证书拷到 Mac 后，经 Tailscale 与局域网都被 NAS 拒绝；在服务器上能登录，但只能运行 rsync；高级功能前提按空间站副本放行。同日在外网的 Oracle Linux 9.8（arm64，SELinux Enforcing）上：迁移、部署 porter 正常；服务器连不上家里的 NAS，试连证书（5 分钟，只能回显地址）失败后自动经本机仓库中转，服务器没有拿到可写入的证书。修正：迁移时私钥在密码管理器 agent 里的登录（`IdentityFile` 指向公钥）改用用户的 agent；迁移后尚未部署 porter 不再报"失败"，改为提示下一步。
  - 未完成：在靶机上用 TUI 走一遍日常管理（含 `doctor` 交互动作）；真实渠道下的日常管理验收。
- **交付**：首页与实例页按 UI.md 第 9 节改版（管理优先、备份安静）；UI.md 8.1 的统一卡片、横向翻页与新图标标准（含 profile 图标重绘与 lint 新尺寸）；管理动作 schema 定稿与实现（INSTALL.md 第 5 节）；状态与错误的诚实展示；按能力展示 profile 支持程度。
- **验收**：在不同安装方式与真实渠道下验证 OpenClaw 的日常管理（不记录秘密）；动作的确认与结果展示符合风险等级。

## M13：备份闭环

- **状态**：`Blocked (depends on M12)`
- **交付**：备份教程与教学逃生舱；恢复演练引导（优先隔离位置）；定时备份向导；空间站引导、首次同步与从空间站恢复；取回本地（保存加密备份、导出可查看的文件）。
- **验收**：靶机上完成教程、演练、同步、从空间站恢复、两种取回；教学与实际备份在界面上区分清楚。

## M16：聊天转接（2026-11-02 用户决定，排在 M13 之后）

- **状态**：`Blocked (depends on M13)`
- **交付**：经用户同意后把 agent 的聊天转接到 Valise 界面，只原样转发文字，不附加任何 prompt，不保存聊天记录；profile 新增 `chat` 能力声明收发方式（回环隧道上的接口或固定命令）；属于主人操作，并受 INSTALL.md 4.2 的前提约束；界面写明 agent 动手时用的是它自己的权限。

## M17：Profile 编辑与核实证据（2026-11-02 用户决定，排在 M16 之后）

- **状态**：`Blocked (depends on M16)`
- **交付**：profile 编辑页（高级功能）：以表单拾取具体功能，不直接显示 YAML，可勾选或去掉，重要项（凭据白名单、目录、会执行的命令）二次确认；改过的官方 profile 另存为"基于官方 vN 修改"的本地版本，可查看与官方版的差别；保存时运行 lint。核实证据按三种记录（PROFILES.md 1.2）。agent 编写、HarnessProfileCreator 表单、交给其他 agent 照官方文档生成的技能为后期方向。

## M14：Profile 方法沉淀

- **状态**：`Blocked (depends on M17)`
- **交付**：按版本与能力的测试矩阵；把 Hermes、DSH 按同一方法补齐能力核实；之后再做用户自建 profile 的表单与技能（INSTALL.md 第 8 节）。

## M15：第三方工具

- **状态**：`Blocked (depends on M14)`
- **交付**：tool-profile 与已安装工具的登记管理（INSTALL.md 第 6 节）；之后再实现用户提供的安装命令（第 7 节）。管理方技能（AGENT_KIT.md 第 9 节）、沙盒插件、服务器健康、推荐记忆工具在此之后单独立项。

---

## 贯穿全程：`testbed/drill.sh`

每个里程碑都往演练脚本中追加对应的步骤。M4 完成时，脚本必须覆盖：部署 → 登记 → 三个档位的封存 → 同步到 NAS → 脱离 Valise 恢复 → 回滚 → 跨架构迁移 → 故障与崩溃注入 → 克隆。最终输出 `ALL_PASS (failures=0)`。
