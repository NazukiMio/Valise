# Valise · 开发环境与靶机手册

## 一句话产品定义

Valise 是开源的自托管 Agent 安装与管理入口。它通过系统 OpenSSH 接入服务器（新手手动填地址即可），部署按需唤起的服务端 porter，依据经过审核的 Harness Profile 安装或接入 harness、打开管理页、完成登录授权与日常管理。保障方面，它把 agent 的灵魂（人格、记忆、技能、会话、状态）与凭据分层加密，封存进内容寻址金库，由代码在同机、异机、跨架构的环境中完成回滚、迁移、克隆和自动回退。此外还提供记忆健康体检。

## 仓库目录结构

```text
valise/
├── CLAUDE.md
├── AGENT.md
├── docs/
│   ├── PRODUCT.md            # 定位、用户、非目标、术语
│   ├── ARCHITECTURE.md       # 传输、密钥、登记、封存、复活
│   ├── VAULT_FORMAT.md       # 金库公开格式
│   ├── PROFILES.md           # Harness Profile 规范与初稿
│   ├── LOGIN_RELAY.md        # 登录中继、管理页与本地页面转发
│   ├── INSTALL.md            # Harness 安装、原生界面、管理动作、第三方工具
│   ├── AGENT_KIT.md          # MCP 工具与技能
│   ├── UI.md                 # TUI 与 mygo 桌面 UI 规范
│   ├── DEV_PLAN.md           # 里程碑
│   ├── REFERENCES.md         # 已核实的参考
│   └── REDESIGN_PROPOSAL.md  # 2026-11-01 改版意见稿（历史输入，已并入各规范）
├── profiles/                 # Harness Profile 数据：openclaw.yaml、hermes.yaml、dsh.yaml …
├── core/                     # 共享纯逻辑 (module valise/core)：profile 模型与校验、金库格式、RPC 类型、profilelint
├── porter/                   # 远端搬运工 valise-porter (module valise/porter)：静态二进制，按需唤起
├── client/                   # 客户端核心 (module valise/client)：keystore、ssh、tunnel、relay、金库读取
├── client/ui/                # 服务层 + CLI(valise) + TUI(valise tui, client/ui/tui) + mygo 桌面 UI (module valise/ui)
├── agent-kit/skills/         # valise-enroll / valise-upkeep / valise-handoff / valise-checkup
├── tools/standalone-restore/ # 不依赖 Valise 的恢复脚本（Python 3 标准库 + age + zstd 命令行）
└── testbed/                  # module valise/testbed
    ├── fakessh/              # fake-ssh 测试替身（见下文规范）
    ├── fakesshenv/           # 测试辅助：构建 fake-ssh 与 porter、准备假主机
    ├── selfcheck.sh          # CLAUDE.md 自检清单
    └── drill.sh              # 靶机全链路演练脚本
```

关于 Go 版本：`core`、`porter`、`client` 三个模块的 `go` 指令统一写为 `go 1.26`，这样靶机上自带的 Go 1.26.8 可以直接运行测试。`client/ui` 在 M7b 引入 mygo 后，按所锁定版本的要求设定。

## 靶机矩阵

fake-ssh 只能验证本地协议连通性。凡是涉及真实 Linux 用户态权限、`systemd --user`、跨架构二进制、无 sudo 环境、真实 harness 安装与重建、异地金库同步、SSH 隧道的功能，都必须在以下靶机上实测：

| 别名 | 架构 | 系统 | 用户 / sudo | 地址 | 用途 | 部署的 Harness |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `ubuntu-vm` | ARM64 | Ubuntu 26.04.1 LTS / Linux 7.0.0-38 | `parallels` / 免密 sudo | `10.211.55.5`（Parallels 网桥） | 主力开发与基准靶机，预装 Go 1.26.8、Python 3.14.4、age、zstd、git | OpenClaw、DSH（npm 用户级前缀安装，不使用 sudo） |
| `ubuntu-vm-x86` | x86_64 | Ubuntu 26.10 / Linux 7.3.0-9 | `nazukimio` / 无 sudo | `192.168.101.108`（DHCP，可能变化） | 跨架构迁移目标；验证纯用户态部署 | Hermes（官方方式，用户级安装） |
| `nas-test` | x86_64 | TrueNAS 27.0.0-RC.1 / Linux 6.18.52（ZFS 2.4.4） | `nas` / 无 sudo | `192.168.101.109`（DHCP，可能变化） | 异地金库同步目标，预装 rsync、rclone、restic | 不安装 harness |

> 改版后的 OpenClaw 端到端验收（空服务器 → 部署 → 安装 → 管理页）需要一台没有装过 harness 的干净靶机；开工前与用户确认用哪台、是否允许重置。

### 操控铁律

- **复杂脚本一律通过标准输入传递**：`ssh <host> 'bash -s' < script.sh`。严禁把多行脚本塞进参数引号里。
- **ARM 靶机断连时的应急穿透**：单条命令用 `/usr/local/bin/prlctl exec "Ubuntu 26.04 ARM64" <命令>`。复杂脚本先写到共享目录 `/media/psf/Home/`，在 VM 内执行，并把输出重定向到 `/tmp/out.log`。注意 `prlctl exec` 默认以 root 身份运行，切勿改动 `parallels` 用户目录下文件的属主。
- **x86 靶机容量很紧**：根分区可用空间约 3.8G。安装 Hermes 前先执行 `df -h ~`。Hermes 的虚拟环境可能占用数百 MB；空间不足时**询问用户**，不要自行清理任何文件。测试产物用 `: > file` 截断或 `t.TempDir()`。复活流程需要的暂存空间约为灵魂数据大小的 2 倍，演练前要先算清楚。
- **NAS 路径约定**：所有数据只能写到 `/mnt/test/nas/` 下，金库同步目标固定为 `/mnt/test/nas/valise-vault/`。

### systemd 真机铁律（防止 218/CAPABILITIES 崩溃）

- 三台靶机上的用户级服务（`systemctl --user`）都由非特权 systemd 实例管理，没有 `CAP_SETPCAP`。
- **严禁在 `--user` 单元中写 `CapabilityBoundingSet` 或 `AmbientCapabilities`**。`--user` 作用域只保留 `NoNewPrivileges=yes`。
- porter 不是服务。只有用户显式开启"定时建立逃生舱"时，才会生成 `valise-seal@<instance>.timer`（`--user`）；关闭时删除。

### porter 二进制探测链

客户端按以下顺序探测远端的 `valise-porter`。探测结果只作为当前会话的缓存，断连重连后重新探测，不写入长期配置：

1. `~/.local/bin/valise-porter`（首选）
2. `~/.local/lib/valise/valise-porter`
3. `/usr/local/bin/valise-porter`
4. 裸命令 `valise-porter`（兼容 `authorized_keys` 中 `command="..."` 的强制命令模式）

使用 Valise 双钥匙的服务器，porter 固定在 `~/.local/bin/valise-porter`（日常钥匙的强制命令指向它，ARCHITECTURE.md 第 2.1 节）；探测链只用于自带钥匙的服务器。

## fake-ssh 测试替身规范

### 它是什么

fake-ssh 是一个**冒充 `ssh` 命令的本地小程序**。客户端平时调用系统的 OpenSSH；在测试中，客户端改为调用这个小程序。它解析 ssh 风格的参数，然后不连接任何网络，直接在本机的临时目录里执行"远端命令"。它的作用是：在没有服务器的情况下，快速、可重复地测试 RPC、数据流、错误处理和断线等逻辑。

### 实现要求（M1 交付）

- 位置：`testbed/fakessh/`，是一个独立的 Go 程序。测试时由 `go build` 构建到 `t.TempDir()` 中。
- 注入方式：客户端配置中有一项 `ssh_binary`（默认值 `ssh`），测试时把它指向 fake-ssh。**不修改 PATH**，也不影响开发机上的真实 ssh。
- 参数：解析 `-T`、`-tt`、`-o key=value`、`-p`、`-l`、`-F`、主机别名，以及剩下的远端命令。不认识的选项忽略，但要在调试日志中记录下来。
- 根目录：由环境变量 `FAKESSH_ROOT` 指定 `<临时目录>`。假 HOME 不存在的别名，按"无法解析主机名"处理（退出码 255）。调试日志写入 `FAKESSH_LOG` 指定的文件（未设置时不记录）。`FAKESSH_*` 变量不会传给远端命令。
- 执行：每个主机别名对应一个独立的假 HOME（`<临时目录>/hosts/<别名>/home`）。远端命令以 `sh -c "<命令>"` 的方式执行，并设置 `HOME` 为对应的假 HOME，工作目录也设为假 HOME。stdin、stdout、stderr 原样透传，退出码原样返回。
- porter：测试准备阶段，为宿主机系统构建 porter，放到假 HOME 的 `~/.local/bin/valise-porter`，这样探测链也能被测到。
- 故障注入（通过环境变量控制）：
  - `FAKESSH_FAIL_CONNECT=<别名>`：以退出码 255 退出，并输出类似 OpenSSH 的连接失败信息。
  - `FAKESSH_DELAY_MS=<毫秒>`：启动前延迟。
  - `FAKESSH_DROP_AFTER_BYTES=<字节数>`：stdout 传输到这个字节数后强制断开，用来模拟数据流中途断线。

### 它测不了的东西（必须上靶机）

- 端口转发（`-L`、`-N`）、ControlMaster、ProxyJump、真实的认证流程；
- `authorized_keys` 的强制命令与 `permitopen` 限制、ssh-agent 的时间窗口、首次密码登录与主机密钥确认；
- `systemd --user`、真实的文件权限与属主、跨架构二进制；
- 真实 harness 的登记、封存与复活。

依赖这些能力的测试，在 fake-ssh 下必须显式 `t.Skip` 并说明原因。隧道相关的单元测试，只测试参数构造和本机监听逻辑。


## 安全与文案红线

1. **登录凭据**：Valise 绝不保存 SSH 密码；首次手动添加服务器时，密码只在 OpenSSH 自己的提示中输入一次（GUI 经 Valise 的 askpass 转交，只在内存中经过）。Valise 为每台服务器生成两把专用密钥：日常钥匙（无口令、0600，服务器端用强制命令限制为 porter 日常模式）与管理钥匙（`ssh-keygen` 以 Valise 口令加密，解锁后进入 Valise 自己的 ssh-agent，带时间窗口）。用户自己的 SSH 密钥仍交给系统 OpenSSH 和用户的 SSH Agent，Valise 不读取。详见 ARCHITECTURE.md 第 2.1 节。
2. **模型凭据**：Valise 不提供 API Key 输入辅助。用户在 harness 原生界面或管理页里自己填写；Valise 不解析、不读取、不保存、不记录原生界面中的输入与回显，也不声称这些字节"完全不经过 Valise"（GUI 的终端经 Valise 中转，INSTALL.md 第 2 节）。
3. **凭据分层**：凭据只按 profile 的声明分类。只有 `soul-secrets` 和 `full` 两个档位会把凭据封存进金库，并且做独立标记。凭据内容和任何文件内容都不得出现在日志、审计记录、错误信息或 MCP 返回值中。
4. **文案去黑话**：界面用语以 `docs/PRODUCT.md` 8.1 的对照表为准（逃生舱、建立逃生舱、逃生舱着陆、管理页、登录授权、空间站、原生界面、体检……）。错误信息必须包含"失败原因 + 下一步建议"，原始技术细节默认折叠。
5. **诚实状态**：以下每一组都是彼此独立的状态，严禁合并成一个笼统的绿色徽标：
   - 隧道已建立、端口在监听、健康检查通过；
   - 封存已写入服务器金库、已同步到异地；
   - profile 的每项能力已核实、未核实（PROFILES.md 1.2）；
   - 命令已退出（退出码）、服务或功能真正健康。

   未实现的能力不渲染按钮，也不用灰色禁用按钮画饼。
6. **不背书**：任何界面、技能、文档都不点名推荐第三方插件或工具。只给出选择标准，以及该 harness 官方文档中的原生选项。安装检查写"未发现基础疑点"，绝不写"安全"。

## Git 提交约定

每个 Commit 的范围必须清晰，末尾附带以下两个 trailer：

```text
Co-authored-by: NazukiMio <170129864+NazukiMio@users.noreply.github.com>
Co-authored-by: Nazukiki <347054528+Nazukiki@users.noreply.github.com>
```

`git config user` 保持为 `NazukiMio <nazukimio@gmail.com>`。除非用户明确要求，否则不要执行 `git push`。
