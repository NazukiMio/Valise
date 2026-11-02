# Valise 架构规范

## 1. 拓扑

```text
[ valise CLI / TUI / mygo 桌面 UI ]
              │
[ 客户端服务层 (Go) ]
  ├─ Keystore：age X25519 身份，口令加密（age 原生 scrypt）
  ├─ 服务器访问：Valise 自己的 ssh_config、每台服务器的日常钥匙、可选的管理钥匙、Valise 自己的 ssh-agent（第 2.1 节）
  ├─ Profile 仓库：签名的资料包（单独发布，PROFILES.md 1.1）+ 用户本地 profile
  ├─ 金库读取与解密（只在客户端内存中解密）
  ├─ 隧道：管理页、本地页面转发、登录中继
  ├─ 安装与原生界面（INSTALL.md）
  └─ SSH 传输：系统 OpenSSH；日常钥匙可用 ControlMaster 复用，管理操作每次新开连接
              │
              │  SSH stdio：JSONL JSON-RPC 2.0（控制面）/ 原始字节流（数据面）
              ▼
[ 服务器：valise-porter（按需唤起，不监听任何端口）]
  ├─ ~/.ssh/authorized_keys                          日常钥匙（带强制命令）一行、可选的管理钥匙一行，注释 valise-daily@<本机名> / valise-admin@<本机名>
  ├─ ~/.config/valise/instances/<instance_id>.json   实例记录
  ├─ ~/.config/valise/instances/<instance_id>.profile.json  登记时生效的 profile（离线封存用）
  ├─ ~/.config/valise/recipients.txt                 age 收件人公钥
  ├─ ~/.config/valise/dedupe/<vault_id>.key          去重密钥（0600）
  ├─ ~/.config/valise/instances/<instance_id>.vault.json    金库周期设置（VAULT_FORMAT.md 10.2）
  ├─ ~/.config/valise/server.json                    服务器名（空间站目录名，添加时确定）
  ├─ ~/.config/valise/targets.json                   空间站（同步目标）与自动同步设置
  ├─ ~/.local/share/valise/vaults/                   服务器本地金库树（按实例 / 周期分档）
  ├─ ~/.local/share/valise/vault/                    分档之前的旧金库（只读，可整体删除）
  ├─ <状态目录的同级>/.valise-stage-<op_id>/           复活暂存目录
  ├─ <状态目录的同级>/.valise-prev-<op_id>/            交换后保留的旧状态
  └─ ~/.local/state/valise/{audit.jsonl, journal/, tmp/}
```

## 2. 传输

- **控制面**：`ssh -T <host> "<porter_path> rpc"` 启动一个会话级子进程，通过 stdin/stdout 交换单行 JSON-RPC 2.0 消息。首个请求必须是 `hello`，返回 porter 版本、协议版本、架构、HOME 路径、可用工具（`systemctl --user`、`git`、`npm`、`uv`、`rsync`、`rclone`；按登录环境的 PATH 查找，与登记、复活一致），以及**未完成的交换日志**。协议主版本不一致时，客户端拒绝继续。交换日志的内容格式在 M4 定义；在此之前，`hello` 只按 `journal/` 目录中的 `<op_id>.json` 文件名列出 op_id。
- **数据面**：大数据流使用独立的一次性 SSH 调用，例如 `porter blob-read`（加密 blob 从服务器流向客户端）和 `porter stage-write --op <id>`（复活时，明文 tar 流从客户端流向服务器）。明文只会出现在客户端内存中，以及目标服务器的暂存目录里。
- **连接复用**：优先复用用户已有的 ControlMaster；没有时，使用 `-o ControlMaster=auto -o ControlPath=<客户端缓存目录>/cm-%C -o ControlPersist=60`。Unix 套接字路径有长度上限（macOS 为 104 字节，OpenSSH 建立 master 时还会追加约 17 字节的临时后缀），缓存目录过长时改用 `/tmp/valise-<uid>`（必须是本人所有、权限 0700 的真实目录）；仍不满足时不使用连接复用。
- **主连接按访问级别分开命名**：OpenSSH 只按主机、端口、用户区分主连接，复用时不看用的是哪把钥匙。日常钥匙的主连接套接字用 `cmd-%C`，你自己 SSH 设置的用 `cm-%C`，管理操作用一次性的 `op-<随机>`，三者绝不互相复用（M10 真机发现：两个配置目录都退回 `/tmp/valise-<uid>` 时，完整登录复用了日常钥匙的主连接）。
- **只有日常钥匙使用持续的连接复用**：管理操作每次新开自己的连接（`-o IdentitiesOnly=yes`，不连到日常钥匙的 ControlMaster），需要时只为这一次操作开临时 ControlMaster，结束即关（第 2.1 节）。
- **协议版本**：格式为 `<主>.<次>`，当前为 `1.2`（1.1：M10 的访问级别；1.2：M11 的 `install_check`、`install_audit`、`entry_port_set`）。主版本号不一致时客户端拒绝继续；次版本号只表示向后兼容的新增。
- **porter 部署**：客户端从 GitHub Release 下载目标架构的二进制，用随客户端发布的公钥校验 `SHA256SUMS` 的 minisign 签名，再核对哈希；或者由用户显式选择本地构建产物。上传时先执行 `cat > ~/.local/bin/valise-porter.tmp`，再 `chmod 0755`，最后 `mv` 原子替换。
  - Release 资产命名：`valise-porter_linux_amd64`、`valise-porter_linux_arm64`、`SHA256SUMS`（`sha256sum` 输出格式）、`SHA256SUMS.minisig`。远端架构由 `uname -s -m` 判定，只支持 Linux 的 `x86_64` / `aarch64`。
  - minisign 校验：同时接受预哈希（`ED`，BLAKE2b-512）与旧版（`Ed`）签名，并且必须校验 trusted comment 的全局签名。签名公钥随客户端发布；客户端尚未配置发布公钥时，拒绝从 Release 部署，只允许上传本地构建产物。
  - 在 `chmod` 与 `mv` 之间，执行 `valise-porter.tmp version --json`：确认它能在目标架构上运行，并且它自报的 SHA-256 与本地文件一致，才执行 `mv`。任何一步失败都不替换已有的 porter。
  - 部署目标固定为 `~/.local/bin/valise-porter`，全程不使用 sudo。首次部署在添加服务器的引导登录中完成（第 3 节），之后的更新是管理操作。
  - **检查更新**（`valise porter check`）：客户端经 GitHub API 列出 Release，取 tag 形如 `v<主>.<次>.<修订>` 的正式版（与资料包的 `profiles-*` 区分，草稿与预发布不算），按数字比较，发现比服务器上更新的 porter 时提示，用户确认后按上述流程更新；不自动更新。发布签名公钥尚未配置的开发构建，界面不显示"一键部署"，只提供"上传本地构建"，不把尚未建立的发布链描述为可用。

### 2.1 日常钥匙、管理登录与操作分级（2026-11-01 用户决定）

目的：打开 Valise 不等于拿到服务器的完整权限。敏感操作必须另外出示一个秘密（管理口令，或服务器密码），并且这一限制由 OpenSSH 与 porter 的代码执行，而不是只靠界面。

**日常钥匙**（每台 Valise 管理的服务器都有）：
- 生成：`ssh-keygen -t ed25519 -N ''`，存于 `<客户端数据目录>/keys/<服务器>/daily`（0600）。
- 服务器端：`authorized_keys` 中一行 `restrict,pty,port-forwarding,permitlisten="127.0.0.1:1",permitopen="127.0.0.1:*",permitopen="[::1]:*",command="$HOME/.local/bin/valise-porter serve --tier daily" <公钥> valise-daily@<本机名>`（注释里的本机名用来分辨是哪台电脑的钥匙；安装时按公钥本身去重，只替换同一把钥匙，别的电脑或别名装的 Valise 钥匙不受影响）。`port-forwarding` 会同时放开远程转发（`-R`），而 `authorized_keys` 的 `permitlisten` 不接受 `none`（写了整行钥匙失效），所以只允许监听特权端口 1：普通用户的 sshd 子进程绑定不了它，远程转发因此全部失败（M10 在 OpenSSH 10.2 / 10.5 / 10.0 上实测）。`pty`（2026-11-02 用户同意）让登录授权的 `login-run` 能在终端里运行，强制命令照样决定运行什么，拿不到 shell；此前装的钥匙没有 `pty`，客户端在 `servers.json` 中以 `daily_tty` 区分，旧格式的服务器登录授权仍走开发者模式，服务器设置里提示"按 K 重新装一次"。TrueNAS 默认 `AllowTcpForwarding no`，这时任何钥匙都不能转发，管理页与本地页面转发不可用（NAS 上通常也不跑 harness）。
- 能做什么：第 2.2 节的日常操作，以及转发到服务器本机回环端口；拿不到 shell。可以使用 ControlMaster 复用连接。
- **限制必须真正生效才用**（M10 在 InstaBox 上发现：它的 SSH 网关用 `none` 认证放行任何人，根本不看机器上的 `authorized_keys`，日常钥匙在那里等于完整登录，而且该用户有免密 sudo）。添加或迁移时：先试一次不带任何凭据的登录，能登进去就拒绝使用日常钥匙；装好日常钥匙后再用它执行一条普通 shell 命令，若被执行（强制命令没生效），立即从服务器撤掉刚装的钥匙、恢复原来的记录并拒绝。两种情况都提示用户继续用自己的 SSH 设置接入，并说明这台服务器本身的登录风险。
- porter 的日常模式：`serve --tier daily` 读取 `SSH_ORIGINAL_COMMAND`，只允许 `rpc`、`blob-read`、`vault-cat`、`version`，以及带一次性许可的 `stage-write`、`login-run`（2.2 节）；`rpc` 中的主人操作要先完成主人证明，开发者模式的方法一律返回"需要管理权限"。即使日常钥匙被盗，没有 Valise 口令也不能着陆、删除、改收件人或改同步目标。

**管理登录**（执行管理操作时出示，两种方式任选）：
- **管理钥匙（可选，默认不创建）**：入门用法用不到管理功能，所以添加服务器时默认不创建；用户可以在添加服务器时勾选，或之后在服务器设置里创建（需要用服务器密码或自己的私钥登录一次来安装公钥）。
  - 生成：`ssh-keygen -t ed25519 -N <管理口令>`，存于 `keys/<服务器>/admin`（0600）；服务器端是普通一行 `<公钥> valise-admin@<本机名>`。私钥加密由 OpenSSH 完成，Valise 不自研。
  - **管理口令**：单独的口令，**与保护加密钥匙的 Valise 口令不同**（至少 12 个字符，界面检查两者不相同）。所有服务器的管理钥匙共用这一个管理口令；修改时全部重新加密。
  - 时间窗口：输入管理口令后由 `ssh-add -t <窗口>` 载入 **Valise 自己启动的 ssh-agent**（默认 15 分钟，设置中可改为 1–120 分钟）；到期由 ssh-agent 清除；退出 Valise 时结束这个 ssh-agent（它是 Valise 自己的子进程）。
- **临时登录**（没有管理钥匙时）：每次管理操作都用服务器密码（或用户自己的私钥）新登录一次，密码只在 OpenSSH 自己的提示中输入（GUI 经 Valise 的 askpass 转交，只在内存中经过），不保存，没有时间窗口。
- **连接隔离**：每次管理操作新开自己的连接，绝不复用日常钥匙的连接或任何已有连接；一次操作需要多次调用（例如着陆）时，为这一次操作开一个临时 ControlMaster（套接字名 `op-<随机>`），操作结束立即关闭，万一没有关闭，闲置 120 秒后由 OpenSSH 自动结束。原生界面、harness 安装与工具安装都是一次操作一条独立连接。
- **客户端的实现细节**（M10 确定）：
  - 管理口令经 askpass 交给 OpenSSH 工具：`SSH_ASKPASS` 指向 Valise 自己的可执行文件，口令放在只传给这一个子进程的环境变量里（同一用户的进程才能读到，与钥匙文件的保护相同）；口令错误时 askpass 回答空，让 `ssh-add` 放弃而不是反复询问。
  - Valise 的 ssh-agent 属于一个 Valise 进程：TUI / GUI 在整个运行期间共享时间窗口；CLI 每条命令是一个进程，窗口只在这条命令内有效。
  - TUI 运行时 OpenSSH 无法在终端里询问服务器密码：需要临时登录的管理操作在 TUI 中直接提示"创建管理钥匙或在命令行执行"；管理口令由 TUI 的掩码输入框询问。CLI 中由 OpenSSH 自己询问密码。
  - "自带钥匙"服务器的软限制只在界面（TUI / GUI）中实施，留到界面改版；CLI 用户本来就能直接 `ssh`，CLI 不加软限制。

**其他规则**：
- **Valise 口令**只保护加密钥匙（`keystore.age`），与服务器管理无关；Valise 不另设登录密码。
- **自带钥匙的服务器**：经用户自己的 `~/.ssh/config` 别名接入、未迁移到 Valise 日常钥匙的服务器，Valise 无法在服务器端设限。界面（不含 CLI）标明"软限制"：管理操作前要求输入管理口令（已创建时）或 Valise 口令（已设置时）确认，两者都没有时只做文字确认；并提供"迁移到 Valise 日常钥匙"。
- **如实说明的局限**：管理钥匙解锁期间，本机若已有恶意程序，它可以使用 ssh-agent 中的钥匙；这套分级防的是"别人打开你开着的 Valise"和"agent 借客户端越权"。Windows 上 ssh-agent 是系统服务，Valise 自启 agent 的方式 `verify: pending`，未核实前 Windows 版每次管理操作都直接要求管理口令、不提供时间窗口。

### 2.2 操作分级（2026-11-02 用户决定改为三级）

管理钥匙在概念上是**开发者模式钥匙**：只拦有安全风险的操作，不能成为基本功能的门槛。日常钥匙在本机没有口令保护，所以会写入 agent 目录或改变备份去向的基本操作，需要证明"你是主人"，这一级用用户本来就有的 **Valise 口令**，不需要管理钥匙。

- **日常操作**（只要日常钥匙）：`hello`/doctor、状态与实例列表、登记报告（只读）、建立逃生舱、同步到空间站、空间站列表、体检、`seal_log`、拉取与校验、技能状态、定时器状态、管理页隧道（不含令牌）、本地页面转发、只读且不输出机密的管理动作、追加审计记录（`install_audit`：管理操作结束后补记，不能因为管理窗口到期而丢失；它只追加，不改变任何受保护的东西）、**首次给服务器起名**（起好后 porter 拒绝修改），以及**服务器上还没有任何收件人时登记第一把加密公钥**（此前没有任何逃生舱可被泄露）。
- **主人操作**（日常钥匙 + Valise 口令证明）：逃生舱着陆（含 resume）、安装或卸载技能、接受登记与归档建议、重新接入、设置周期与定时器、添加或修改同步目标与自动同步、增加收件人、登录授权、代取管理页令牌、`manage` 风险的管理动作与输出机密的只读动作、服务启停、**不敏感的删除**（见下）。
- **开发者模式**（管理登录：管理钥匙或服务器密码）：服务器终端、原生界面、harness 安装、工具安装、`config` / `danger` 风险的管理动作、更新 porter、两边一起改端口、移除收件人、**敏感的删除**、创建管理钥匙、迁移到日常钥匙。
- **主人证明**（由 porter 执行，不只是界面）：在日常连接中，客户端调用 `owner_challenge`，porter 生成 32 字节随机数，用标准 age 加密给 `recipients.txt` 中的全部收件人后返回；客户端用 `keystore.age` 中的身份解密（需要 Valise 口令），以 `owner_prove` 回传；porter 以常数时间比较，正确后**本次会话**开放主人操作。服务器还没有收件人时无法证明，提示先登记加密钥匙。只用 age，不涉及自研密码学。客户端解开的身份只留在内存中，有效期与管理钥匙的时间窗口设置相同（默认 15 分钟），退出 Valise 即清除；CLI 每条命令单独询问。
- **主人操作需要的独立子命令**（`stage-write`、`login-run`）：日常强制命令只在出示一次性许可时放行。许可由已证明的会话调用 `owner_grant` 签发：porter 生成随机令牌，在私有目录记录"子命令 + 参数（如 op_id）+ 5 分钟有效期"，子命令带 `--grant <令牌>` 运行时核对并立即作废。
- **删除分级**（判断依据：删掉以后还能不能找回）：
  - 不敏感（主人操作，加一次确认）：已结束、并且其中每个逃生舱在至少一个空间站上都有副本的服务器周期；已结束、服务器上仍有同一周期的空间站周期；7 天前的"着陆前的样子"（`prev drop`）；7 天前遗留的暂存目录。
  - 敏感（开发者模式，加输入名称确认）：别处没有副本的周期、当前周期、7 天内的"着陆前的样子"、旧实例清理、移除收件人。
  - 是否敏感由 porter 按服务器上的记录判断（同步记录、周期是否结束、文件时间），客户端不能自称"别处有副本"；本机仓库只有客户端知道，不计入服务器的判断。
- 访问级别记入审计（`daily` / `owner` / `admin`）。
- **高级模式**（INSTALL.md 4.1）：在管理操作之上，允许 Valise 代为执行运行时与依赖的安装（含 sudo）。前提是已创建管理钥匙；开启需要两个口令的挑战（Valise 口令解开 `keystore.age`，管理口令解开该服务器的管理钥匙，两个都对）并确认免责；管理钥匙窗口到期后暂停，重新输入管理口令才继续。明显有害的命令在任何模式下都不执行。
- 新增 RPC 方法时必须在 `core/rpc` 中显式归入一级；未归类的方法按开发者模式处理。

## 3. Keystore

- **身份**：使用 age 原生的 X25519 身份（`AGE-SECRET-KEY-1…`），以 age 口令加密（scrypt）的形式保存为 `keystore.age`。用户随时可以用标准命令 `age -d keystore.age` 解出身份，**不依赖 Valise**。
- **口令**：至少 12 位。scrypt 工作因子使用 age 的默认值，不得调低。
- **收件人**：对应的 `age1…` 公钥登记到服务器的 `recipients.txt`。支持多个收件人，例如再加一把离线纸质备份钥匙。
- **指纹**：收件人公钥字符串（`age1…`）的 SHA-256 十六进制值的前 16 位。`VAULT` 中的 `recipient_fingerprints` 使用同一定义。
- **口令长度**：按 Unicode 字符计数，至少 12 个字符。
- **客户端数据目录**：`os.UserConfigDir()/valise/`（macOS 为 `~/Library/Application Support/valise/`，Linux 为 `~/.config/valise/`），其中包括 `keystore.age`、`config.json`、`servers.json`、`ssh_config`、`known_hosts` 与 `keys/`。ControlMaster 套接字与 Valise 的 ssh-agent 套接字放在 `os.UserCacheDir()/valise/` 下。
- **已记录服务器**：`servers.json` 保存服务器列表，每项为 `{"name": "<别名>", "source": "valise" | "ssh_config"}`。
  - `valise`：手动添加的服务器。`servers.json` 中记录地址、端口、用户名与临时登录方式（`login: password | key`，`key` 时只记私钥文件的路径）；Valise 由它生成自己的 `ssh_config`（`Host <别名>`、`HostName`、`Port`、`User`、`UserKnownHostsFile <客户端数据目录>/known_hosts`、`StrictHostKeyChecking yes`），每次调用 OpenSSH 时用 `-F` 传入；用户的 `~/.ssh/config` 不被修改。钥匙不写进 `ssh_config`，而是按访问级别在命令行上传入（日常：`-i daily -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes`；管理钥匙：`-i admin` 加 Valise 的 ssh-agent；临时登录：`-o PubkeyAuthentication=no` 或用户的私钥文件），这样临时登录不会退回到日常钥匙。从 `~/.ssh/config` 迁移时，如果用户的 `IdentityFile` 是公钥（私钥在 1Password 等密码管理器的 agent 里），同时记下该配置的 `IdentityAgent`（`login_agent`，未配置时为环境中的 `SSH_AUTH_SOCK`），只在这类临时登录中使用；日常钥匙与空间站钥匙一律 `IdentityAgent=none`。不保存任何密码。
  - `ssh_config`：从用户 `~/.ssh/config` 导入的别名（只列具体的 `Host`，跳过通配符），连接参数与密钥交给用户自己的配置。
  - CLI 仍然接受任意别名，不要求先记录；界面只列已记录的服务器。旧版只含别名字符串的 `servers.json` 读作 `ssh_config` 来源。服务器上的实例由 RPC `instance_list` 列出。
- **添加服务器**（2026-11-01 改版）：每一步失败都停在该步并给出原因与建议。
  1. **连接方式**：手动填写（地址、端口，默认 22、用户名、登录方式：密码或已有私钥文件、用户自选的别名），或从 SSH 配置导入。
  2. **确认服务器身份**：首次连接时展示主机密钥指纹（SHA256），说明"第一次连接无法自动验证，请与服务商控制台显示的指纹比对，或确认网络可信"，用户确认后写入 Valise 的 `known_hosts`。之后指纹变化一律停止，说明可能的原因（重装系统、中间人），由用户决定。
  3. **引导登录**（`valise` 来源）：用密码或用户私钥登录**一次**，为这次引导开一个临时 ControlMaster，以下 3–6 步共用，引导结束立即关闭。密码只在 OpenSSH 自己的提示中输入（TUI 把终端交给 ssh；GUI 用 Valise 的 askpass 程序转交，只在内存中经过），不保存。
     - 经标准输入发送 Valise 自带的固定脚本，把日常钥匙的公钥追加到 `~/.ssh/authorized_keys`（目录与文件权限按 OpenSSH 要求设为 0700 / 0600，已存在同注释的旧行先替换）；用户勾选了"创建管理钥匙"时（先设管理口令），一并安装管理钥匙的公钥。
     - `ssh_config` 来源的服务器可选"迁移到 Valise 日常钥匙"，同样步骤。
  4. **体检服务器**（`doctor`）。
  5. **部署 Valise 服务端**：没有 porter 时提供"一键部署"（第 2 节），在引导登录中完成。日常钥匙的强制命令固定指向部署目标 `$HOME/.local/bin/valise-porter`，所以在部署之前就能写好。
  6. **登记加密公钥**（`key register`，注释写本机名与日期）：加密钥匙已存在时在引导登录中完成；用户在第一次启动时跳过了钥匙时，第一次建立逃生舱前补上（此时服务器上还没有收件人，日常钥匙即可登记第一把，见 2.2 节）。
  引导结束后日常操作一律用日常钥匙；管理操作用管理钥匙或临时登录（2.1 节）。
  7. **确定服务器名**（写入 `server.json`，默认等于别名，用作空间站目录名，之后不可修改）。
  8. **发现 harness**：已有的 agent 可以直接接入 Valise（登记）；没有时引导安装第一个 harness（INSTALL.md）。两条路都可达，不强迫已有用户重装。
  多把钥匙、移除公钥等收件人管理放在设置的"高级"中，并用大白话说明后果。
- **第一次启动**：建议设置 Valise 口令与加密钥匙并备份，但允许暂时跳过，直接去添加服务器。添加服务器不需要任何口令；管理口令只在用户选择创建管理钥匙时设置。
  - 第一次建立逃生舱前，必须走完"备份钥匙"引导：导出恢复套件，或明确选择"我知道风险，稍后备份"（界面写明：口令或钥匙丢失后，所有逃生舱都无法打开）。
- **离线恢复套件**：导出内容包括 `keystore.age`、收件人公钥、指纹，以及一页恢复说明（指向 `tools/standalone-restore/`）。导出为一个目录，内含 `keystore.age`、`recipient.txt`、`FINGERPRINT`、`RECOVERY.md`。导入时核对指纹：先确认 `FINGERPRINT` 与 `recipient.txt` 一致，解锁后再确认身份对应的公钥与 `recipient.txt` 一致，并把指纹展示给用户，与纸质记录比对。
- **内存处理**：解锁后的身份封装在 `UnlockedKey` 中，`Lock()` 时尽力清零。文档和 UI 中要如实说明：Go 的垃圾回收可能产生副本，无法保证彻底清除。UI 空闲 15 分钟后自动锁定（时长可配置）。


### 3.1 导出与导入配置（2026-11-01 用户决定）

换电脑时带走"记录"，不带走任何钥匙。

- **内容**：已记录服务器（`servers.json` 的全部字段：别名、来源、地址、端口、用户名、登录方式、私钥文件路径）、这些手动服务器在 Valise `known_hosts` 中的主机密钥（公开信息，导入后仍能识别冒充者）、设置（目前只有管理窗口时长；`ssh_binary` 等与机器有关的设置不导出）。不包含 `keystore.age`、日常钥匙、管理钥匙、密码或任何秘密；加密钥匙只通过恢复套件（第 3 节）迁移。
- **格式**：一个明文 JSON 文件（默认名 `valise-config.json`，权限 0600），`{"format": "valise-config", "version": 1, "exported_at", "servers": [...], "known_hosts": ["<field> <type> <base64>", ...], "settings": {"admin_window_minutes"}}`。不加密：里面没有秘钥，但有服务器地址和用户名，导出时提醒不要公开分享。导出不覆盖已有文件。
- **导入**：只添加这台电脑上没有的服务器；同名服务器无论内容是否相同都不覆盖，内容不同的跳过并在结束时列出。手动服务器的主机密钥只在这台电脑还没有该地址的密钥时写入。管理窗口时长只在这台电脑未设置时采用。导入的手动服务器没有这台电脑的日常钥匙，需要逐台执行 `valise server rekey <服务器>`（用服务器密码或私钥登录一次，等同于重新走一遍添加服务器的钥匙安装与检查）；来自 `~/.ssh/config` 的服务器要求新电脑的 SSH 配置里有同名 `Host`。
- **CLI**：`valise config export <文件>`、`valise config import <文件>`、`valise server rekey <服务器> [--admin-key]`。

## 4. 登记（全部由代码完成）

1. **探测**：客户端把 profile 集合发给 porter，porter 按每个 profile 的 `detect` 规则（可执行文件、状态目录候选、标记文件）找出候选实例。
2. **解析**：把 profile 的 `paths` 规则展开到真实文件，为每个文件确定类别，并记录未分类的文件。
3. **来源记录**：按 `provenance` 规则读出身体的安装来源与版本，包括 npm 包名、精确版本、registry 和 integrity，git 的 remote、commit 和是否 dirty，uv/pipx 工具的包名与版本，以及二进制的哈希。如果 git 工作树是 dirty 状态，把差异记录为 `body_patch`，随灵魂一起封存。
4. **服务与入口**：确认 service 候选单元是否存在，入口端口是否在监听。
5. **登记报告**：客户端展示各类别的文件数量与大小、未分类文件、凭据文件清单（只列路径）、身体与来源、服务、入口、哪些 profile 条目未核实。用户确认，并对未分类文件做出处理决定后，才写入实例记录。
6. **agent 补充**（可选）：agent 通过 MCP 提议额外的路径和类别，这些提议以"待确认"状态保存。用户在客户端确认后，才合入实例记录的 `local_overrides`。

实例记录包含：`instance_id`、`profile_id`、`profile_version`、`roots`（解析后的绝对路径）、`provenance`、`service`、`entries`、`local_overrides`、`enrolled_at`，以及被取代时的 `superseded`（见 8.2.1）。harness 升级后再次登记时，展示与上次的差异。

实现细节（M2 确定）：

- **profile 来源**：已安装的签名资料包（PROFILES.md 1.1），另外读取客户端本机的 `os.UserConfigDir()/valise/profiles/*.yaml`（标记为"本地，未经审核"）。porter 自身不携带任何 profile。
- **PATH**：非交互 SSH 的 PATH 通常不包含用户级安装前缀。porter 执行 `detect.binaries`、`version_argv`、`discover_argv` 和来源查询时，使用用户登录 shell 的 PATH（`$SHELL -lc` 读取一次），并补上 `~/.local/bin`。这些命令都设有超时。
- **实例范围**：M2 每种 harness 在每台服务器上只探测默认状态目录（`state_dir_candidates` 中第一个存在且有标记文件的）。harness 自身的多 profile（例如 `~/.hermes/profiles/<name>`、`~/.openclaw-<name>`）推迟处理。
- **instance_id**：默认等于 profile id，可用 `--id` 指定，命名规则与 profile id 相同。
- **嵌套 root**：一个文件只属于包含它的最深的 root。例如工作区位于状态目录内部时，工作区中的文件只按 `workspace` root 的规则分类。
- **分类优先级**：凭据规则（profile 与 `local_overrides` 中的 `secrets`）> `local_overrides` > `body.paths` > `paths` 中第一条命中的规则。
- **SQLite 附属文件**：`consistency.sqlite` 声明的数据库，其 `-wal`、`-shm`、`-journal` 附属文件归入该数据库，不单独分类、不单独计数（封存时由 `VACUUM INTO` 覆盖）。
- **符号链接**：root 内部的符号链接只记录，不跟随。root 自身的路径是（或经过）符号链接时（例如把状态目录软链到另一块磁盘），按解析后的真实目录扫描，报告中同时展示两者。**M4 待定**：这种情况下复活的目录交换应该作用于链接还是目标。
- **确认**：`valise enroll` 默认只展示报告；加 `--accept` 才写入实例记录。未分类文件可用 `--assign <root>:<glob>=<类别>` 归类，写进 `local_overrides`；其余保持未分类并计数。
- **npm 来源**：全局安装没有锁文件。包名与版本取自已安装包的 `package.json`；registry 取自 `npm config get registry`；integrity 用 `npm view <包>@<版本> dist.integrity` 向该 registry 查询。查不到时如实记录缺失，并给出警告。
- **git 来源**：记录 remote、commit、是否 dirty；dirty 时差异（`git diff HEAD --binary`）作为 `body_patch`，未跟踪文件只计数并警告（不进入补丁）。

## 5. 类别与封存档位

| 类别 | 含义 | soul | soul-secrets | full | raw |
| :--- | :--- | :---: | :---: | :---: | :---: |
| `identity` | 人格、用户画像、最高准则 | ✓ | ✓ | ✓ | ✓ |
| `memory` | 长期与短期记忆文件 | ✓ | ✓ | ✓ | ✓ |
| `skills` | 技能与程序性知识 | ✓ | ✓ | ✓ | ✓ |
| `config` | 不含凭据的配置 | ✓ | ✓ | ✓ | ✓ |
| `sessions` | 会话记录 | ✓ | ✓ | ✓ | ✓ |
| `state` | 状态数据库与索引 | ✓ | ✓ | ✓ | ✓ |
| `body_patch` | 身体的本地修改补丁 | ✓ | ✓ | ✓ | ✓ |
| `secrets` | 凭据（含可能内联令牌的配置文件） |  | ✓ | ✓ | ✓ |
| `unclassified` | 没有任何规则命中的文件（不能在 profile 中声明） |  | ✓ | ✓ | ✓ |
| `workspace` | 工作区与资产 |  |  | ✓ | ✓ |
| `body` | 身体本身，依据来源记录重建 |  |  |  | ✓ |
| `cache` | 可再生缓存 |  |  |  | ✓ |
| `ignore` | 日志、harness 自带备份等 |  |  |  | ✓ |

匹配规则：

- 一个文件匹配多条规则时，取第一条命中的规则。**但只要有任何一条规则把它判定为 `secrets`，它就是 `secrets`**（凭据优先）。
- **未知即敏感**：没有任何规则命中的文件归为 `unclassified`。它们不进入 `soul` 档，而是和凭据走同一层，进入 `soul-secrets`、`full`、`raw` 档，并在快照中单独标记。这样 profile 写漏不会导致完整备份丢数据，而 `soul` 档始终只含已声明的灵魂。未分类文件的数量会出现在登记报告和每次封存报告中，用户可以随时为它们指定类别。
- **`raw` 档**（全量）：把实例所有 root 下的全部文件原样封存，包括身体、缓存、忽略项和未分类文件，用于在 profile 不完整、或身体无法按来源重建时兜底。规则：
  - 只封存本实例的 root，**不是整机备份**。因此身体位于 root 之外时（例如 npm 全局前缀中的 OpenClaw、DSH），raw 不包含身体，只能按来源重建；身体位于状态目录内部时（例如 Hermes），raw 才能为身体兜底。
  - 整个快照按凭据层对待：恢复时必须显式同意包含凭据（`restore.py --include-secrets`）；声明了 `secret_tables` 的数据库不剥离。
  - SQLite 仍然使用 `VACUUM INTO` 一致性副本；符号链接全部记录、不跟随。
  - 复活时优先按来源重建身体；只有重建失败、并且目标服务器与封存时是同一架构、同一路径时，才允许用 raw 中的身体兜底，并且必须由用户确认。界面必须如实说明：身体中的运行时常含绝对路径，换路径或换架构后不可用。
  - raw 首次封存体积大（身体常达数 GB），之后依靠去重增量很小。默认不启用；低频周期执行见 DEV_PLAN 的 M8。

## 6. 封存流水线

1. **预检**：确认实例记录与 profile 版本一致，并确认 `~/.local/state/valise/tmp/` 的剩余空间足够容纳 SQLite 副本。
2. **一致性处理**：
   - profile 声明的 SQLite 文件：用 `modernc.org/sqlite`（纯 Go 实现，兼容 `CGO_ENABLED=0`）以只读方式打开，执行 `VACUUM INTO` 生成临时副本，再对副本执行 `PRAGMA integrity_check`，结果必须为 `ok`。封存的是副本的内容，记录的是原文件的路径。
   - **表级凭据剥离**：profile 在 `consistency.sqlite` 条目上用 `secret_tables` 声明哪些表存放凭据。`soul` 档封存时，只在临时副本中清空这些表（`DELETE FROM`）后再 `VACUUM`，绝不修改原文件；`soul-secrets` 与 `full` 档封存未剥离的副本。这仍是"只按声明分类"，代码不扫描表内容。（M3 实现；M2 只在登记报告中展示。）
   - profile 声明 `quiesce: stop_service` 的实例：停止服务，封存，再启动服务并做健康检查。停机时长需要在计划中提前告知用户。默认值为 `none`。
   - 其他文件：读取前后各比较一次 size 和 mtime。发生变化时重读一次；仍然变化就标记为 `unstable` 并写入报告，不视为失败。
3. **分块与去重**：按 `VAULT_FORMAT.md` 的规则执行。
4. **写入**：依次写入新的 pack、index、加密的快照清单、明文元数据，每一步都要 fsync。最后写入审计记录。
5. **报告**：文件数、各类别大小、新增字节数、未分类文件数、`unstable` 列表、耗时。

实现细节（M3 确定）：

- **profile 来源**：`enroll --accept` 时，客户端把当时生效的 profile（已合并子 profile）连同实例记录一起写到服务器的 `instances/<id>.profile.json`。封存一律使用这份 profile，因此定时封存和 MCP 封存不需要客户端在线。profile 升级后需要重新登记。
- **档位**：见第 5 节的表格。`local_overrides` 参与分类。
- **body_patch**：git 来源且工作树 dirty 时，封存当时的 `git diff HEAD --binary` 作为虚拟文件 `valise:body.patch`。
- **互斥**：同一台服务器同一时刻只允许一个封存或同步，用 `~/.local/state/valise/vault.lock`（flock）保证。
- **临时文件**：SQLite 副本写在 `~/.local/state/valise/tmp/`，封存结束后由 Go 代码删除。
- **变动统计**（M4 实现）：每次封存后，porter 更新 `~/.local/state/valise/seen/<instance_id>.json`，记录 identity、config、skills 类文件的路径、SHA-256、连续发生变化的封存次数。它只包含路径和哈希，不含文件内容；服务器本来就持有这些明文文件，因此不新增暴露面。这份统计是给 agent 和用户的归类线索（见 AGENT_KIT.md 第 3.1 节），不会自动修改任何分类。

触发封存的方式有四种：

- CLI 或 UI（包括首次备份教程）；
- agent 通过 MCP 触发，但只能封存 `soul` 档，并且有频率限制；
- 用户开启的定时任务（`systemd --user` timer，见第 7 节"自动同步到空间站"）；
- 复活之前自动封存。

封存由 porter 在服务器上使用收件人公钥加密完成，**不需要客户端在线**。

**教学逃生舱**（2026-11-01 用户决定）：首次备份教程中建立的逃生舱带教学标记。标记不写入金库格式（VAULT_FORMAT.md 不变），而是由客户端在封存请求中带 `purpose: "tutorial"`，porter 把它写入该次封存的审计记录，经 `seal_log` 返回给界面。界面在逃生舱列表与详情中标"教学"，并说明它只证明当时的流程与内容，不证明之后新增的配置、渠道凭据或工作区已被覆盖；用户完成重要配置后，界面适时建议建立一次实际的逃生舱。金库只追加，教学逃生舱与其他逃生舱一样保留，随所在周期一起删除。服务器丢失、只剩空间站副本时教学标记随审计记录丢失，界面按普通逃生舱显示。

## 7. 金库与同步

- **收件人登记**：`valise key register <服务器>` 把本机密钥的公钥追加到服务器的 `recipients.txt`（每行一个 `age1…`，`#` 开头为注释）。收件人集合变化后，下一次封存新建 epoch。服务器上没有任何收件人时拒绝封存。`--label <说明>` 会在公钥上方写一行注释 `# <说明> (added <日期>)`，用来标明这把钥匙属于谁、用途是什么（例如测试钥匙）；注释不影响收件人集合和指纹。
- **移除收件人**（`valise key unregister`，M4 实现）：从 `recipients.txt` 删除一把公钥，下一次封存会新建 epoch，此后的快照不再加密给它。**必须向用户如实说明**：已经写入金库的旧快照仍然可以被这把钥匙解开，加密无法追溯撤销；如果钥匙已经泄露，应当把旧快照视为已暴露。拒绝移除最后一把公钥。
- **本机仓库**（2026-11-02 用户决定；"仓库"专指本机文件夹，区别于各种空间站）：用户在客户端选择一个或多个本机文件夹（普通目录、外接硬盘或网盘桌面客户端的同步文件夹），记在客户端 `config.json` 的 `repos`（`[{"name", "path"}]`），所有服务器共用。同步由客户端完成：对服务器上每个未在删除中的周期执行与"拉取"相同的只追加复制，写到 `<仓库>/<服务器名>/<实例>/<周期>/`，`<仓库>/<服务器名>/TREE` 不存在时按 VAULT_FORMAT.md 10 节写入一次；每个周期复制后做不需要私钥的校验。服务器名未设置时先请用户起名。客户端不常驻，因此只在用户手动上云、打开 Valise、在界面里建立逃生舱之后同步，界面写明"这一份只在 Valise 打开时更新"。同步记录（每台服务器每个仓库的最近成功时间与错误）保存在客户端数据目录的 `repo-state.json`。仓库里的目录结构与空间站相同，`tools/standalone-restore/` 可以直接使用。
- **金库树**：每台服务器的本地金库按"实例 / 周期"分档，每个周期是一个独立的 `valise-vault/1` 金库（VAULT_FORMAT.md 第 10 节）。同步到空间站时目标上多一层服务器名：`<dest>/<server_name>/<instance_id>/<周期>/`。
- **空间站**（界面原称云备份）由 `porter sync --target <name>` 完成，支持 rsync、rclone 和 sftp。目标配置写在 `targets.json` 中；rclone 使用用户自己的 remote 配置，Valise 不保存其中的凭据。
- 同步按叶子逐个进行，叶子内的顺序固定为：pack → index → 快照 → 元数据 → epochs/keys → PERIOD → VAULT；带 `DELETING` 的叶子跳过。由于叶子内只追加，任何中断都可以安全续传。
- **targets.json**：`{"targets": [{"name": "nas", "kind": "rsync", "dest": "nas-test:/mnt/test/nas/valise-vault/", "auto": {"every": "24h"}}]}`。`kind` 取值 `rsync`、`rclone`、`sftp`；已实现 `rsync`（经服务器自己的 SSH 配置，加 `--ignore-existing`）与 `sftp`。
- **协议回落**（2026-11-02 用户决定）：`kind: rsync` 的目标在服务器或目标上没有 rsync 时（本机找不到 rsync，或远端报 `command not found` 等），这一次操作自动改用 sftp，结果写明所用方式与原因（"rsync 不可用，改用 sftp，速度会慢一些"）；`kind: sftp` 直接用 sftp。sftp 只需要目标上 OpenSSH 自带的 sftp-server：经 `sftp -b -`（服务器自己的 SSH 配置、`BatchMode=yes`）先逐层列出目标上已有的文件，只上传缺少的，每个文件先传为同目录下的 `.part-<名字>` 再 `rename`，因此中断不会留下半个文件、云端只追加；列表跳过 `.part-*`。删除整个周期时先写 `DELETING` 标记，再按列表逐个删除文件、由深到浅删除目录，最后删除标记。界面上未实现的种类不渲染。同步结果写入审计记录，`porter status` 据此报告每个目标的最后同步时间。早期的目标由用户在服务器上自己配置 SSH（"服务器自己配置的空间站"），仍然可用；新的空间站由 Valise 管理（下一条）。
- **Valise 管理的空间站**（2026-11-02 用户决定）：服务器在公网、NAS 在家里内网是常态，空间站的连接信息因此归 Valise 管，不要求用户去改服务器的 SSH 配置。
  - **记录**：客户端数据目录的 `stations.json`（`[{"name", "address", "port", "user", "path"}]`），主机指纹写入 Valise 的 `known_hosts`（与服务器相同的确认流程）。建议用户在 NAS 上为 Valise 开一个**专用用户**并只给它一个目录，界面在添加时说明原因。
  - **钥匙与证书**（2026-11-02 用户决定）：客户端一把 `keys/_stations/<名字>/client`（用于中转、列表与删除），在空间站上以 `restrict` 授权。服务器的钥匙由 porter 用 `ssh-keygen` 在服务器上生成（`~/.config/valise/station-keys/<名字>`，私钥不离开服务器），**本身不被空间站授权**：空间站上只加一行 `cert-authority,restrict,principals="<用户>" <Valise 签发公钥>`，服务器凭客户端签发的 OpenSSH 证书登录。签发私钥（`keys/_stations/_ca.age`）用用户的 age 收件人加密，签发时由已解锁的身份临时解开，因此签证书需要 Valise 口令。证书内容由 Valise 决定，不依赖 NAS 的附加能力：`-O clear`（不能开终端、不能转发）；有效期默认 7 天（`config.json` 的 `station_cert_hours`，1–720），每次打开 Valise 且口令已解锁时自动续期，快到期而未解锁时提示"上云一次即可续期"；`source-address` 为服务器的地址（先用 5 分钟的探测证书执行 `echo $SSH_CLIENT` 得到空间站看到的地址；地址会变的服务器可以选择不限定）；`force-command`：空间站有 rrsync 时为 `rrsync -wo -no-del -no-overwrite <路径>/<服务器名>`（只写、不删、不覆盖，限定在这台服务器的目录，服务器端以该目录为根），否则为 sftp-server 并禁止删除、改名、改权限等请求（仍可能覆盖已有文件，界面建议开启 NAS 快照），两者都没有时不给服务器凭据、一律中转。服务器钥匙不能读也不能删，因此空间站的列表与删除由客户端完成（删除仍按 2.2 节分级：服务器上还有这个周期时为主人操作，否则为开发者模式）。添加空间站时，若它不需要任何凭据就能登录，明确警告；并建议为 Valise 开专用用户、为其目录开启定时快照。
  - **三条路径，依次回落**：① **直传**：服务器 → 空间站（rsync，缺 rsync 时 sftp）。② **经本机仓库中转**：已有本机仓库时，先把服务器上的逃生舱补到仓库，再由客户端从仓库传到空间站。③ **临时中转**：没有仓库时，取回到客户端缓存目录下的临时目录，传到空间站，完成或失败都删除临时目录；开始前检查本机空间。直传因网络或登录原因失败（不认识主机名、连不上、拒绝登录、指纹未确认），或服务器尚未配置这个空间站时，自动改走中转；结果写明用了哪条路径。中转期间 Valise 要保持打开，界面写明。
  - **同步记录**：中转成功后，客户端调用 porter 的 `sync_record`（主人操作）把这些逃生舱记进服务器上该空间站的同步记录，删除分级与"已上云"显示因此一致。
  - 云端列表与删除整个周期同样先直连、失败时由客户端连接。服务器自己配置的空间站在客户端的 SSH 配置里也有同名主机时，同样可以中转。
- **云端列表**：客户端经 porter 列出目标上的金库树（rsync：`--list-only`，只读目录与文件名、大小），与服务器本地对照，展示每个周期"服务器有 / 云端有 / 两处都有"、逃生舱数量与大小。目标上的元数据文件（`*.meta.json`）可以按需拉取用于展示，不需要私钥。
- **删除周期**：见 VAULT_FORMAT.md 10.4。RPC 只接受"实例 + 周期 + 位置（服务器或某个目标）"，不接受任意路径。
- **自动同步到空间站**（2026-10-31 用户决定，两种方式并存）：
  - **随建立逃生舱顺带同步**（默认方式，不常驻）：每次成功建立逃生舱后（无论由用户、agent 经 MCP、安全网还是定时器触发），porter 检查每个开启了 `auto` 的目标，距上次成功同步超过 `auto.every` 时，启动一个脱离会话的 `valise-porter autosync` 子进程完成同步后退出。它与封存共用 `vault.lock`，拿不到锁就放弃本次，等下一次逃生舱再试。结果写入审计记录。
  - **定时器**（可选，由用户在实例设置中显式开启）：生成 `valise-seal@<instance>.timer` 与对应的 service（`--user`，遵守 AGENT.md 的 systemd 铁律），按用户设定的频率建立一个 `soul` 档逃生舱，随后按上一条规则顺带同步。界面写明它会在服务器上留下哪两个单元文件；关闭时删除它们。
- **取回本地**分两种，界面分别说明目的地、是否包含凭据、文件的保护状态：
  - **保存加密备份**：`valise pull`，把金库（或一个周期）拉取到本机目录。拉取只复制本机没有的文件（金库只追加），然后做不需要私钥的校验。文件仍是加密的，打开需要加密钥匙与口令。
  - **导出可查看的文件**（M13 实现）：解锁加密钥匙，在内存中解密一个逃生舱，写到用户选择的本机目录。默认不含凭据层，包含时必须单独确认；导出的目录与文件权限设为 0700 / 0600，界面写明"这些文件不再加密，请自行妥善保管或用完删除"。
- **带私钥的校验**只在客户端进行：`valise verify --vault <本机目录> --key` 解锁本机密钥，在内存中解密快照清单与 blob，按文件校验 sha256。
- UI 中"已写入服务器金库"、"已同步到 <空间站>"与"已取回本地"分开展示；建立成功时写明目前是否只存在于原服务器。

## 8. 复活

### 8.1 三种模式

| 模式 | 场景 | 目标 |
| :--- | :--- | :--- |
| 回滚 | 同一个实例退回到更早的封存 | 原服务器、原状态目录 |
| 迁移 | 把实例搬到另一台服务器，可以跨架构 | 新服务器。原实例在新实例健康、且用户确认之前保持不动 |
| 克隆 | 生成一个新实例 | 新的 `instance_id`，默认不带独占凭据 |

### 8.2 流水线

1. **计划**：由代码计算并展示以下内容，用户确认后才继续（`--dry-run` 只输出计划）：
   - 源快照；
   - 目标服务器与状态目录；
   - 身体的处理方式：保持不变、按来源记录安装同一版本，或版本不同需要用户确认；
   - 是否恢复凭据，以及独占凭据冲突的处理方案；
   - 预计停机时间；
   - 所需磁盘空间（暂存空间加上旧状态的保留空间）；
   - 暂存目录与状态目录是否在同一个文件系统（用 `stat` 比较设备号，不在同一文件系统就拒绝执行）。
2. **身体**：
   - 目标服务器上已有同一来源、同一版本的身体时，保持不变。
   - 否则按来源记录安装：npm 使用记录中的 registry 和精确版本，安装到用户级前缀，并比对 integrity；git 克隆记录中的 remote，检出记录中的 commit，再应用 `body_patch`；然后执行 profile 中的 `bootstrap_argv`；uv 和 pipx 同样使用精确版本。
   - **来源校验失败时立即停止**，例如 remote 或 registry 不一致、包已下架、哈希不符。此时向用户展示差异，由用户决定是否改用 profile 文档中的全新安装方式，手动安装后重试。
   - 全局安装的身体（例如 npm 用户级前缀）无法预先放进暂存区，安装动作推迟到交换窗口内执行，并在交换日志中记录原版本，以便回退时重装。
3. **暂存**：客户端在内存中解密 blob，组装成 tar 流，交给 `porter stage-write` 写入 `.valise-stage-<op>/`。之后逐个文件校验 sha256，对 SQLite 执行 `integrity_check`，并恢复文件权限；`secrets` 类文件一律设为 0600。
4. **确认**：展示暂存校验的结果，请用户做第二次确认。
5. **交换**：交换日志的状态依次为 `PLANNED → STOPPED → LIVE_MOVED → STAGE_MOVED → BODY_SWITCHED → STARTED → HEALTHY`；失败时进入 `ROLLING_BACK → ROLLED_BACK`。
   - 正常路径：写日志 → 停止服务并确认已停止 → 把活动目录重命名为 `.valise-prev-<op>` → 把暂存目录重命名为活动目录 → 按需切换身体 → 启动服务 → 健康检查。
   - 健康检查：先等待宽限期（profile 定义，默认 20 秒），然后每 5 秒检查一次，连续 3 次通过即为健康，总超时 120 秒。
   - 失败路径：停止服务 → 把活动目录重命名为 `.valise-failed-<op>` → 把 prev 目录改回活动目录 → 恢复身体 → 启动服务并做健康检查 → 向用户返回 `rolled_back` 和最近的服务日志。这些日志只展示给用户，不写入审计记录。
6. **收尾**：`.valise-prev-<op>` 默认保留 7 天，UI 中提供"立即退回到交换前"的操作。清理只能由用户执行 `valise prev drop`，由 porter 在 Go 代码中调用 `os.RemoveAll` 完成。

### 8.2.1 实现细节（M4 确定）

- **快照来源**：客户端先从快照所在服务器 `valise pull` 到本机，在内存中解密快照与 blob；明文以 tar 流经 `porter stage-write --op <id>` 写入目标服务器的暂存目录。profile 优先取已安装资料包（或本地）的同 id、同 `profile_version` 的 profile；没有时，改用快照头中的 `profile_doc`（封存时使用的完整 profile），计划中标明"来自快照，未经当前客户端审核"，并且必须显式传入 `--allow-snapshot-profile`（其中的服务安装命令等仍逐字展示）。两者都没有时停止。着陆能力未核实（`capabilities.restore` 不是 `tested`，或 `status: draft`）的 profile 必须显式传入 `--allow-draft-profile`（PROFILES.md 1.2）。
- **目标路径**：目标的状态目录由目标服务器按 profile 的 `detect.state_dir_candidates` 解析（使用目标用户的 HOME）。其余 root：原路径在原状态目录下的，映射到新状态目录下的同一相对路径；在原 HOME 下的，映射到新 HOME 下；都不是时停止，由用户用 `--root <id>=<路径>` 指定。
- **符号链接**：目标的 root 本身是符号链接时，v0.1 拒绝复活并说明原因（链接常由其他工具管理；交换时被改名的会是链接而不是数据）；封存不受影响。root 的上级路径中有符号链接（例如 macOS 的 `/var`、把 `/home` 软链到数据盘）不影响原子交换，允许。
- **交换单位**：只交换没有嵌套在其他 root 内的 root（例如工作区在状态目录内时，只交换状态目录，工作区随之移动）。暂存目录为 `<root 的父目录>/.valise-stage-<op>-<root>`，必须与 root 在同一文件系统。
- **回滚的携带语义**：回滚只替换快照档位内的类别；当前活目录中档位以外的内容（例如用 `soul` 档回滚时的凭据、未分类文件、工作区、缓存）在服务停止后**复制**进暂存目录；位于 `body.paths` 的身体目录（形如 `<目录>/**`）在交换窗口内整体改名搬入新活目录，并记入交换日志。迁移与克隆不携带任何内容：目标目录整体替换（已有目录按流程保留为 prev）。
- **运行时前提**：按来源重建身体所需的工具（npm、git、uv、pipx）必须已在目标服务器的登录 PATH 中；计划阶段检查，缺少时停止，并给出 profile 中的 `fresh_install_doc`。着陆流程不安装运行时；需要时由用户经 INSTALL.md 的安装流程（官方方案或原生界面）先行准备。
- **来源校验**：npm 身体在计划阶段向记录中的 registry 查询该精确版本的 integrity，与登记时记录的值不一致就停止。git 身体检查 remote 一致、commit 可取得。v0.1 中 git 身体只支持"目标已有同一 commit 的身体，保持不变"；需要重建而 `bootstrap_argv` 尚未核实时，计划停止并说明（raw 档兜底见第 5 节）。
- **服务单元**：目标服务器上没有 profile 声明的服务单元时，执行 profile 的 `service.install_argv`（harness 自带的安装命令）。计划中逐字展示这条命令，用户确认后才执行。Valise 不自己写单元文件。
- **克隆范围**：v0.1 的克隆只支持到另一台服务器（或另一个用户），同一服务器内的克隆推迟（通常需要改端口、服务名等 harness 配置）。
- **独占凭据**：迁移默认在目标启动前停止原实例的服务（原实例的文件不动）；克隆默认不暂存匹配 `exclusive_secrets` 的文件。复活失败回退后，客户端重新启动原实例的服务。
- **其他进程**：服务单元之外仍在使用状态目录的进程（工作目录或打开的文件位于 root 内，Linux 上通过 `/proc` 判断），会在交换期间按路径重新创建活动目录。计划阶段发现就列为阻断项；服务停止后、移走活动目录之前再检查一次，仍有就停止并退回。Valise 不会替用户结束这些进程。（M4 在靶机上发现：Hermes 的 dashboard 进程独立于网关服务运行。）
- **路径被重新创建**：交换或退回时，如果目标路径已被其他进程重新创建，先把它改名挪到 `.valise-intruder-<op>-<root>` 保留（不删除），再完成改名，并在改名后核实；结果中提示用户检查。`valise prev drop` 一并清理。
- **健康检查**：按 profile 的 `health.checks` 执行（`systemd_active`、`tcp`、`http`、`argv`）。没有服务单元的实例（前台运行）不由 Valise 启动：计划中说明，交换完成后结果为"已恢复、未启动"，不做健康检查。
- **新实例的记录**：迁移与克隆成功后，在目标服务器写入实例记录和 profile 副本。`roots` 取目标路径；`provenance`、`service`、`entries` 在目标上重新探测（快照里的记录描述的是原服务器，例如 npm 前缀、二进制路径）；目标上的身体与快照的来源种类不同时，保留快照的来源记录。`local_overrides` 沿用快照头中记录的封存时规则（迁移和克隆都沿用；规则使用相对 root 的路径，跨服务器仍然成立；不依赖原服务器还活着）。
- **被取代的实例**：迁移或克隆的目标路径与目标服务器上另一个已登记实例的 root 重叠（相同或互相包含）时，计划中列出该实例并说明后果（警告，不阻断）。交换成功后不删除它的记录，而是标记为"已被取代"（`superseded`：取代它的实例、op_id、时间）；它原来的目录按流程保留为 prev。被取代的实例不能再封存，也不能作为回滚目标；用同一 id 重新登记会清除标记。实例 id 不改名，因为金库中的快照按 id 关联。
- **旧实例清理**：`valise old list <服务器>` 列出被取代的实例、取代它的操作，以及那次操作保留的旧目录和大小。`valise old cleanup <服务器> <实例>` 在用户再次确认后删除这个实例的记录、profile 副本，以及取代它的那次操作保留的旧目录（与 `prev drop` 相同）。只能由用户显式执行，porter 在 Go 代码中删除。金库中该实例的逃生舱不受影响（按周期删除见 VAULT_FORMAT.md 10.4）。
- **交换日志**：`~/.local/state/valise/journal/<op_id>.json`，每次状态变化原子写入并 fsync，内容包括模式、实例、各 root 的 live / stage / prev 路径、已搬动的身体目录、npm 身体的原版本、当前状态与时间。结束（`HEALTHY` 或 `ROLLED_BACK`）后移到 `journal/done/`，`hello` 只报告未完成的日志。
- **故障注入**（仅用于演练与测试）：客户端隐藏参数 `--inject-fault health|crash-at=<状态>` 通过环境变量传给本次 porter：`health` 让健康检查一律失败；`crash-at` 在写入该状态后立即退出进程。
- **prev**：`valise prev list <服务器>` 列出保留的旧状态及其天数；`valise prev drop <服务器> <op_id>` 由用户显式执行，porter 在 Go 代码中调用 `os.RemoveAll` 删除。

### 8.3 崩溃恢复

porter 每次被唤起时都会检查 `journal/` 目录，`hello` 会报告所有未完成的操作。客户端必须立即提示用户，让用户选择"继续完成"或"退回"（`valise revive resume <服务器> <op_id> --continue|--back`）。porter 绝不在没有客户端确认的情况下自动推进。

### 8.4 独占凭据与迁移

profile 可以把某些凭据声明为 `exclusive_secrets`，例如社交平台的 Bot Token，两个实例同时使用会互相冲突。迁移或克隆时，如果两个实例会同时运行，计划中必须二选一：

- **(a) 先停止原实例，再启动目标实例**（迁移的默认方案）；
- **(b) 目标实例不带独占凭据启动**（克隆的默认方案）。

### 8.5 跨架构

灵魂数据与架构无关，身体在目标服务器上按来源记录重建。工作区中如果有架构相关的二进制文件，只在报告中列出，不做修复。

## 9. 体检（代码部分）

`porter checkup` 按 profile 的 `checkup` 声明计算以下指标：

- 每个会话的大小与 token 量。有 usage 字段时直接求和；没有时按字节数除以 4 估算，并标注为"估算"。
- 会话持续的天数，以及每日 token 增长率。
- 记忆文件的行数与 profile 阈值的比较。
- 冷会话占比（90 天前的会话所占的体积比例）。
- 是否启用了任何记忆机制（依据 profile 中声明的标记文件判断）。

计算结果以固定的 finding id 输出：`long_session`、`cost_growth`、`memory_bloat`、`session_pileup`、`no_memory_mechanism`。解读文案由 agent-kit 和 UI 提供，体检的计算过程不涉及 LLM。

## 10. 审计

`audit.jsonl` 每行记录：时间、操作、实例、发起者（`client` / `agent-mcp` / `timer`）、结果、快照 id、字节数、可选的原因与用途（如 `tutorial`），以及访问级别（`daily` / `owner` / `admin`）。审计记录绝不包含文件内容或凭据内容。

## 11. 错误与文案

所有错误统一为 `{code, reason, suggestion, detail}` 结构。UI 默认展示 `reason` 和 `suggestion`，`detail` 默认折叠。
