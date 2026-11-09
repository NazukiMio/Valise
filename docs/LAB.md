# 开发环境与靶机手册

> 原根目录 `AGENT.md`（2026-11-07 改名移到这里）。本文只记录开发环境、仓库结构、靶机与测试替身；规则（三条总原则、架构铁律、安全与文案红线、Git 约定、自检）以根目录 [`AGENTS.md`](../AGENTS.md) 为准，产品定义见 [PRODUCT.md](PRODUCT.md)，当前进度见 [STATUS.md](STATUS.md)。

## 仓库目录结构

```text
valise/
├── AGENTS.md                 # 主控指令（唯一正文；CLAUDE.md 只导入它）
├── CLAUDE.md                 # @AGENTS.md
├── AGENT.md                  # DEPRECATED：指向本文
├── docs/
│   ├── STATUS.md             # 当前状态与交接（每次交接覆盖更新）
│   ├── PRODUCT.md            # 定位、用户、非目标、术语与界面用语
│   ├── ARCHITECTURE.md       # 传输、钥匙与登录、接入、备份、恢复、交换日志
│   ├── SERVICE_CONTRACTS.md  # 共享服务契约（N1 定稿，2026-11-07 增补已并入正文）
│   ├── VAULT_FORMAT.md       # 金库公开格式
│   ├── PROFILES.md           # Harness Profile 规范
│   ├── LOGIN_RELAY.md        # 登录中继、管理页与本地页面转发
│   ├── INSTALL.md            # 安装、原生界面、管理动作、第三方工具与高级模式
│   ├── AGENT_KIT.md          # MCP 工具与技能
│   ├── UI.md                 # TUI 与 mygo 桌面 UI 规范
│   ├── DEV_PLAN.md           # N1–N5 排期与未完成项
│   ├── REFERENCES.md         # 已核实的外部参考
│   ├── LAB.md                # 本文
│   ├── acceptance/           # 各阶段实现映射与验收记录（N2.md）
│   ├── proposals/            # 尚待用户确认的设计提案
│   ├── history/              # DEPRECATED 的历史文档（只读）
│   ├── sketches/             # 已批准的草图
│   └── icons/                # 图标素材
├── profiles/                 # Harness Profile 数据：openclaw.yaml、hermes.yaml、dsh.yaml …
├── kit/tools.yaml            # Kit 工具清单（随签名资料包分发）
├── core/                     # 共享纯逻辑 (module valise/core)
├── porter/                   # 远端 valise-porter (module valise/porter)：静态二进制，按需唤起
├── client/                   # 客户端核心 (module valise/client)
├── client/ui/                # 服务层 + CLI(valise) + TUI (module valise/ui)；N4 起加入 mygo 桌面 UI
├── agent-kit/skills/         # Agent 保护技能
├── examples/themes/          # 桌面主题样例（UI.md 7.4.2；测试核对每个样例都能导入）
├── tools/standalone-restore/ # 不依赖 Valise 的恢复脚本（Python 3 标准库 + age + zstd）
└── testbed/                  # module valise/testbed：fakessh、fakesshenv、selfcheck.sh、drill.sh
```

关于 Go 版本：`core`、`porter`、`client` 三个模块的 `go` 指令统一写为 `go 1.26`，靶机上自带的 Go 1.26.8 可以直接运行测试。`client/ui` 在 N4 引入 mygo 后按锁定版本的要求设定。开发机构建前先 `export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3`。

## 靶机矩阵

fake-ssh 只能验证本地协议连通性。凡是涉及真实 Linux 用户态权限、`systemd --user`、跨架构二进制、无 sudo 环境、真实 harness 安装与重建、异地同步、SSH 隧道的功能，都必须在靶机上实测。以下状态核对于 2026-11-07；所有靶机都是测试用，没有重要数据（用户 2026-11-07 确认）。

| SSH 别名 | 架构 | 系统 | 用户 / sudo | 地址 | 用途 | 已装 harness |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `ubuntu-vm` | ARM64 | Ubuntu 26.04.1 LTS / Linux 7.0.0-38 | `parallels` / 免密 sudo | `10.211.55.5`（Parallels 网桥，NAT 后） | 主力开发与基准靶机；**没有 rsync**（复制自动回落 sftp） | OpenClaw、Hermes、DSH |
| `ubuntu-vm-x86` | x86_64 | Ubuntu 26.10 / Linux 7.3.0-9 | `nazukimio` / sudo 需要密码 | `192.168.101.108`（DHCP，可能变化） | 跨架构迁移目标、纯用户态部署；时钟慢约 2.5 天且由 chrony 同步（不要改） | OpenClaw（迁移来的）、Hermes |
| `debian-vm` | ARM64 | Debian 13 / Linux 6.12 | `parallels` / 免密 sudo | `10.211.55.6` | systemd 257 兼容、重启验收、linger 与时钟一键修复、高级模式与接管模式；**由 Valise 接入**（日常钥匙 + 管理钥匙，2026-11-07）；装有 Parallels Tools，会把时钟同步回宿主时间（调钟测试要考虑）；NTP 于 2026-11-07 开启 | OpenClaw（含命名实例 `openclaw-test`）、合成实例 `n2-copy` |
| `nazukiki-cloud` | ARM64 | Oracle Linux 9.8 / Linux 6.12 UEK | `opc` / 免密 sudo | 公网（见本机 `~/.ssh/config`） | 公网服务器场景 | OpenClaw |
| `nas-test` | x86_64 | TrueNAS 27.0.0-RC.1（ZFS） | `nas` / 无 sudo | 局域网 `192.168.101.109`；`ubuntu-vm` 经 Tailscale `100.71.234.46` 访问 | 异地同步目标，有 rsync、rclone、restic | 不安装 harness |

`debian-vm` 以 Valise 日常钥匙 + 管理钥匙接入演练客户端（`$HOME/.cache/valise-drill/config`，管理口令文件 `$HOME/.cache/valise-drill/admin-passphrase.txt`，不要打印），可实测高级模式与接管模式；其余服务器仍是“你自己的 SSH 设置”，不能开启高级模式。演练客户端另有本机仓库 `local`（`$HOME/.cache/valise-drill/repo-local`）。早期另有 InstaBox `nazukiki-insta-01`（Debian 13 x86_64），见 [history/MILESTONES_M1-M17.md](history/MILESTONES_M1-M17.md) M10，是否仍可用未核实。

### 操控铁律

- **复杂脚本一律通过标准输入传递**：`ssh <host> 'bash -s' < script.sh`。严禁把多行脚本塞进参数引号里。
- **ARM 靶机断连时的应急穿透**：单条命令用 `/usr/local/bin/prlctl exec "Ubuntu 26.04 ARM64" <命令>`。复杂脚本先写到共享目录 `/media/psf/Home/`，在 VM 内执行，并把输出重定向到 `/tmp/out.log`。注意 `prlctl exec` 默认以 root 身份运行，切勿改动 `parallels` 用户目录下文件的属主。
- **NAS 访问**：在 NAT 后的靶机（如 `ubuntu-vm`）访问 NAS 要走 Tailscale；局域网地址不可直连。
- **x86 靶机容量很紧**：根分区可用空间约 3.8G。安装 Hermes 前先执行 `df -h ~`。Hermes 的虚拟环境可能占用数百 MB；空间不足时**询问用户**，不要自行清理任何文件。测试产物用 `: > file` 截断或 `t.TempDir()`。复活流程需要的暂存空间约为灵魂数据大小的 2 倍，演练前要先算清楚。
- **NAS 路径约定**：所有数据只能写到 `/mnt/test/nas/` 下，金库同步目标固定为 `/mnt/test/nas/valise-vault/`。

### systemd 真机铁律（防止 218/CAPABILITIES 崩溃）

- 所有靶机上的用户级服务（`systemctl --user`）都由非特权 systemd 实例管理，没有 `CAP_SETPCAP`。
- **严禁在 `--user` 单元中写 `CapabilityBoundingSet` 或 `AmbientCapabilities`**。`--user` 作用域只保留 `NoNewPrivileges=yes`。
- porter 不是服务。只有用户显式开启“自动备份”时，才会生成 `valise-seal@<instance>.timer`（`--user`）；关闭时删除。

### porter 二进制探测链

客户端按以下顺序探测远端的 `valise-porter`。探测结果只作为当前会话的缓存，断连重连后重新探测，不写入长期配置：

1. `~/.local/bin/valise-porter`（首选）
2. `~/.local/lib/valise/valise-porter`
3. `/usr/local/bin/valise-porter`
4. 裸命令 `valise-porter`（兼容 `authorized_keys` 中 `command="..."` 的强制命令模式）

使用 Valise 日常钥匙的服务器，porter 固定在 `~/.local/bin/valise-porter`（日常钥匙的强制命令指向它，ARCHITECTURE.md 第 2.1 节）；探测链只用于自带钥匙的服务器。

## fake-ssh 测试替身规范

### 它是什么

fake-ssh 是一个**冒充 `ssh` 命令的本地小程序**。客户端平时调用系统的 OpenSSH；在测试中，客户端改为调用这个小程序。它解析 ssh 风格的参数，然后不连接任何网络，直接在本机的临时目录里执行"远端命令"。它的作用是：在没有服务器的情况下，快速、可重复地测试 RPC、数据流、错误处理和断线等逻辑。

### 实现要求（M1 交付）

- 位置：`testbed/fakessh/`，是一个独立的 Go 程序。测试时由 `go build` 构建到 `t.TempDir()` 中。
- 注入方式：客户端配置中有一项 `ssh_binary`（默认值 `ssh`），测试时把它指向 fake-ssh。**不修改 PATH**，也不影响开发机上的真实 ssh。
- 参数：解析 `-T`、`-tt`、`-o key=value`、`-p`、`-l`、`-F`、`-i`、`-O`、主机别名，以及剩下的远端命令。不认识的选项忽略，但要在调试日志中记录下来。
- 公钥检查（M10）：给了 `-i <钥匙>` 且没有 `-o PubkeyAuthentication=no` 时，模拟 sshd：`<钥匙>.pub` 必须出现在假 HOME 的 `~/.ssh/authorized_keys` 中，否则以退出码 255 报 `Permission denied (publickey)`；该行带 `command="…"` 时，改为执行这条强制命令，并把原命令放进 `SSH_ORIGINAL_COMMAND`。没有 `-i` 或指定了 `PubkeyAuthentication=no`（模拟密码登录）时照常放行。`-O`（主连接控制）直接以 0 退出。`-o PreferredAuthentications=none`（不带任何凭据登录）默认被拒绝；`FAKESSH_AUTH_NONE=<别名>` 模拟"谁都能登录"的网关。`FAKESSH_IGNORE_KEYS=<别名>` 模拟不检查、也不执行 `authorized_keys` 的服务器（任何钥匙都放行，强制命令被忽略）。
- 根目录：由环境变量 `FAKESSH_ROOT` 指定 `<临时目录>`。假 HOME 不存在的别名，按"无法解析主机名"处理（退出码 255）。调试日志写入 `FAKESSH_LOG` 指定的文件（未设置时不记录）。`FAKESSH_*` 变量不会传给远端命令。
- 执行：每个主机别名对应一个独立的假 HOME（`<临时目录>/hosts/<别名>/home`）。远端命令以 `sh -c "<命令>"` 的方式执行，并设置 `HOME` 为对应的假 HOME，工作目录也设为假 HOME。stdin、stdout、stderr 原样透传，退出码原样返回。
- porter：测试准备阶段，为宿主机系统构建 porter，放到假 HOME 的 `~/.local/bin/valise-porter`，这样探测链也能被测到。
- 故障注入（通过环境变量控制）：
  - `FAKESSH_FAIL_CONNECT=<别名>`：以退出码 255 退出，并输出类似 OpenSSH 的连接失败信息。
  - `FAKESSH_DELAY_MS=<毫秒>`：启动前延迟。
  - `FAKESSH_HANG=<别名>[,<别名>…]`：这些主机一直不回应（模拟网络变化后卡住的连接），由调用方的超时结束。
  - `FAKESSH_DROP_AFTER_BYTES=<字节数>`：stdout 传输到这个字节数后强制断开，用来模拟数据流中途断线。

### 它测不了的东西（必须上靶机）

- 端口转发（`-L`、`-N`）、ControlMaster、ProxyJump、真实的认证流程；
- `authorized_keys` 的强制命令与 `permitopen` 限制、ssh-agent 的时间窗口、首次密码登录与主机密钥确认；
- `systemd --user`、真实的文件权限与属主、跨架构二进制；
- 真实 harness 的登记、封存与复活。

依赖这些能力的测试，在 fake-ssh 下必须显式 `t.Skip` 并说明原因。隧道相关的单元测试，只测试参数构造和本机监听逻辑。
