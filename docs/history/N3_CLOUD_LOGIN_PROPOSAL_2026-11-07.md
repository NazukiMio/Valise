# N3 云盘备份位置：登录与同步（提案，2026-11-07）

> DEPRECATED（2026-11-07）：已由 ARCHITECTURE.md 7.3、LOGIN_RELAY.md 第 7 节、AGENT_KIT.md 8.4 取代。用户决定：服务器与本机都用 Valise 自己的 rclone 配置文件；退回时由 Valise 用本机 rclone 代为运行 `rclone authorize`；不加 rclone crypt；整盘权限的云盘允许服务器登录但加重说明；N3 先严格副本再云盘；实机验收用 Google Drive 测试账户。

原状态：待用户确认。依据 STATUS.md 3a 第 6 条与 DEV_PLAN.md N3“云盘”交付；确认后写入 ARCHITECTURE.md 第 7 节、LOGIN_RELAY.md、AGENT_KIT.md 8.4 与 UI.md 9.x，本提案移入 `history/`。

## 1. 已定（用户决定，不再讨论）

- 备份位置新增“云盘”，全部经 rclone；首批 Google Drive、OneDrive、Dropbox，坚果云（WebDAV）与自定义云下一步。Valise 不实现任何云盘协议。
- 两种登录，各自代表不同的同步级别：
  - **服务器登录**：rclone 与配置在服务器上，备份后服务器直传，定时与自动复制依赖它，不需要 Valise 打开。
  - **这台电脑登录**：rclone 与配置在用户电脑上，经本机仓库或临时目录中转，只在 Valise 打开时更新。
- 两种都登录时先服务器直传，失败再经这台电脑中转，结果写明路径；只有服务器登录的云盘能被保护计划自动复制使用。
- 授权内容只在 rclone 自己的配置文件里，Valise 不读取、不解析、不保存、不记录。
- 服务器登录：在服务器上运行 rclone 的配置命令，复用登录中继 `loopback_oauth` 转发 53682 端口，在本机浏览器授权；端口被占用或该云盘不支持时，退回 rclone 自己的无浏览器授权并粘贴。
- 这台电脑登录：优先用 PATH 里已有的 rclone（显示版本）；没有时经用户确认，按 `kit/tools.yaml` 锁定版本下载、核对 SHA-256，放进 Valise 自己的目录（需补 macOS 条目）。

## 2. 云盘清单放在哪里

Go 代码不写任何云盘名称或参数（与 harness 知识只在 profile 同理）。签名资料包里的 `kit/tools.yaml` 在 rclone 条目下增加 `clouds`：

```yaml
clouds:
  - id: gdrive
    name: Google Drive
    rclone_type: drive
    params: { scope: drive.file }      # 固定参数，用户不能改成更大的权限
    access: folder                     # folder：只能看到 rclone 自己建的文件；account：能访问整个网盘
    access_note: "只能读写 Valise 上传的文件，看不到你网盘里的其他东西"
    verify: pending
  - id: onedrive
    name: OneDrive
    rclone_type: onedrive
    params: {}
    access: account
    access_note: "能读写整个 OneDrive"
    verify: pending
  - id: dropbox
    name: Dropbox
    rclone_type: dropbox
    params: {}
    access: account
    access_note: "能读写整个 Dropbox"
    verify: pending
```

`verify: pending` 的条目只展示、不能添加，与 Kit 工具相同。每家的授权范围以 rclone 文档为准：Google Drive 可用 `drive.file`（只能访问 rclone 自己创建的文件）；rclone 内置的 Dropbox 与 OneDrive 应用拿到的是整个网盘的权限，Dropbox 的“App 文件夹”需要用户自己注册应用。

## 3. 服务器登录

1. 前提：服务器上有 Kit 安装的 rclone（`~/.local/bin/rclone`，版本与 SHA-256 已核实）。没有时先走 Kit 安装（主人操作）。
2. 配置文件：`~/.config/valise/rclone/rclone.conf`（0600），所有 rclone 调用都显式带 `--config`。remote 名为 `valise-<位置名>`。见问卷第 1 题。
3. 登录（主人操作，新开管理连接，`ssh -tt`）：porter 在 PTY 里运行固定 argv
   `rclone config create valise-<名> <rclone_type> <params…> config_is_local=true --config <文件>`。
   rclone 在服务器 `127.0.0.1:53682` 起回调，打印 `http://127.0.0.1:53682/auth?state=…`；登录中继按 LOGIN_RELAY.md 5.2 截获链接、绑定本机同一端口、显示域名后打开浏览器，回调经隧道到达服务器。PTY 输出只显示给用户，不写日志与审计。
4. 退回：本机 53682 被占用，或 `clouds` 条目声明不支持回环授权时，改为 `config_is_local=false`：rclone 打印一行 `rclone authorize "<类型>" "<参数>"`，请用户在这台电脑上运行（见问卷第 3 题），把得到的结果粘贴回服务器终端。Valise 只原样转发终端字节，不解析。
5. 授权完成后 porter 用 `rclone lsjson --max-depth 1 valise-<名>:` 做一次只读连通测试，再按 N2 G 节 `target_check` 保存位置：`targets.json` 记 `{"name", "kind": "rclone", "dest": "valise-<名>:valise-vault/"}`，不含任何授权内容。
6. 移除位置：`rclone config delete valise-<名>`，界面写明“服务器上的授权已删除；网盘账户里的授权需要你在账户设置里撤销”。

## 4. 这台电脑登录

1. rclone 来源：PATH 中的 rclone（显示 `rclone version` 第一行），或 Valise 下载到 `<配置目录>/tools/rclone`（`kit/tools.yaml` 增加 `darwin/amd64`、`darwin/arm64` 条目；下载前展示地址、大小与 SHA-256，用户确认）。
2. 配置文件：`<配置目录>/rclone/rclone.conf`（0600），同样显式 `--config`。见问卷第 2 题。
3. 登录：在本机前台运行 `rclone config create valise-<名> <类型> <参数…> --config <文件>`，rclone 自己打开本机浏览器。
4. 中转：沿用 ARCHITECTURE.md 第 7 节“经本机仓库中转 / 临时中转”，最后一步由本机 rclone 上传。

## 5. 同步规则（两种登录相同）

- 只追加：先 `rclone lsf -R --files-only` 列出目标已有文件，只上传缺少的；按既有叶子顺序（pack → index → 快照 → 元数据 → epochs/keys → PERIOD → VAULT）分批 `rclone copy --files-from … --ignore-existing --immutable`。不使用 `sync`，不删除。
- 中断：rclone 对这三家都是整文件上传后才可见，不留半个文件；下次续传。
- 删除整个周期：与 sftp 相同，先写 `DELETING`，逐个删除文件，最后删除标记（VAULT_FORMAT.md 10.4）。
- 副本校验与其他位置相同（ARCHITECTURE.md 7.1 的五类事实），云端列表只读名字与大小。
- 云端可见的明文：目录名（服务器名、实例名、周期）与文件大小；文件内容是 age 加密的。见问卷第 4 题。

## 6. 访问级别与诚实状态

- 添加、登录、移除云盘是主人操作；同步、列表沿用远程备份位置现有级别。
- 界面分开显示：已登录（服务器 / 这台电脑）、连通测试通过、最近一次同步结果与所用路径。“已登录”不等于“已同步”。
- 服务器登录的说明：“授权保存在服务器上。服务器被攻破时，这个授权会一起暴露（备份内容本身是加密的）。” `access: account` 的云盘另加一句“这个授权能访问整个网盘”。

## 7. 验收

- fake-ssh：rclone 用测试替身模拟 `config create`（打印回环链接）、`lsf`、`copy`；覆盖端口占用退回、只追加续传、DELETING 删除、授权内容不出现在日志与审计。
- 实机：`ubuntu-vm` 经 Kit 安装 rclone，用 Google Drive 测试账户完成服务器登录、直传、续传与删除周期；本机 rclone 完成这台电脑登录与中转。真实账户由用户提供并亲自在浏览器里授权。
