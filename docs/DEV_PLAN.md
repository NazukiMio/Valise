# Valise 开发计划

> 严格按里程碑顺序开发。每个里程碑结束时，必须跑通 CLAUDE.md 的自检清单和本里程碑的验收标准，再把状态从 `Ready` 改为 `Done`。
> **M4 完成后发布 v0.1（仅 CLI）。**那是第一个对外可用的版本，此前不对外宣传。

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

- **状态**：`Blocked (depends on M4)`
- **交付**：LOGIN_RELAY.md 中的 `loopback_oauth`、`device_code`、`paste_code` 三种方式，以及 Web 入口；在 profile 中补充 `login` 声明。
- **验收**：从 macOS 客户端，对靶机上至少一个真实的回环 OAuth 登录完成授权；本机端口被占用时给出明确的报错；Web 入口的三个状态独立展示。

## M6：agent-kit

- **状态**：`Blocked (depends on M5)`
- **交付**：`valise-porter mcp` 的六个工具、四个技能、体检的计算、"安装逃生舱技能"操作；基于变动统计的 agent 归类提议与"降级"单独确认（AGENT_KIT.md 第 3.1 节）。
- **验收**：
  - 在每种 harness 上，agent 都能完成"学习逃生舱 → 风险操作前自动封存 → 交接后新会话能读到交接内容"。
  - 对人工构造的超长会话和膨胀的记忆文件，能产生正确的 finding。
  - MCP 无法读取凭据，也无法写入交接目标以外的文件。

## M7：桌面 UI（mygo）

- **状态**：`Blocked (depends on M6)`
- **交付**（遵守 `docs/UI.md`）：
  - 开工第一步：锁定 mygo 当时最新的 tag，把版本号写入 `docs/UI.md` 第 3 节，并对照该 tag 的文档复核 UI.md 中的全部约定。
  - 实例总览：实例卡片，展示 profile 的核实状态、服务的三个状态、最近一次封存与同步状态、finding 和入口。
  - 金库：快照列表、封存档位选择、复活向导（计划 → 暂存 → 确认 → 交换进度 → 结果）。
  - 设置与信任根：导入 SSH 配置、管理密钥、登记公钥、导出恢复套件、管理同步目标。
  - 登录中继的交互界面。
- **验收**：`go tool mygo generate && bun run typecheck && bun run build:web` 零警告通过；检查确认 main 函数中没有任何副作用，入口只通过系统浏览器打开；连接 `ubuntu-vm` 完成真实的交互验收。

## M8：定时封存、raw 周期与保留策略

- **状态**：`Blocked (depends on M4)`（用户 2026-10-30 提议并确认排在 M4 之后；具体排期届时由用户决定）
- **交付**：
  - 定时封存（`systemd --user` timer，遵守 AGENT.md 的 systemd 铁律），由用户显式开启。
  - 低频 raw 周期（例如每周一次），只在服务器与同步目标空间充足时建议开启，默认关闭。
  - 保留策略：用户设定（例如 raw 保留最近 4 份、轻量封存保留 90 天）；空间紧张时界面主动提醒。清理只能由用户执行 `valise prune`，执行前列出将要删除的快照与可回收空间；需要重新打包 pack 并核对引用，任何被保留快照引用的 blob 都不得删除。异地副本保持只追加，清理异地副本需要单独确认。
- **验收**：待 M8 开工时与用户确认。

---

## 贯穿全程：`testbed/drill.sh`

每个里程碑都往演练脚本中追加对应的步骤。M4 完成时，脚本必须覆盖：部署 → 登记 → 三个档位的封存 → 同步到 NAS → 脱离 Valise 恢复 → 回滚 → 跨架构迁移 → 故障与崩溃注入 → 克隆。最终输出 `ALL_PASS (failures=0)`。
