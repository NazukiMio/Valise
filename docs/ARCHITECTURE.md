# Valise 架构规范

## 1. 拓扑

```text
[ valise CLI / mygo 桌面 UI ]
              │
[ 客户端服务层 (Go) ]
  ├─ Keystore：age X25519 身份，口令加密（age 原生 scrypt）
  ├─ Profile 仓库：随版本发布的 profiles/*.yaml + 用户本地 profile
  ├─ 金库读取与解密（只在客户端内存中解密）
  ├─ 隧道与登录中继
  └─ SSH 传输：系统 OpenSSH + ControlMaster 复用
              │
              │  SSH stdio：JSONL JSON-RPC 2.0（控制面）/ 原始字节流（数据面）
              ▼
[ 服务器：valise-porter（按需唤起，不监听任何端口）]
  ├─ ~/.config/valise/instances/<instance_id>.json   实例记录
  ├─ ~/.config/valise/instances/<instance_id>.profile.json  登记时生效的 profile（离线封存用）
  ├─ ~/.config/valise/recipients.txt                 age 收件人公钥
  ├─ ~/.config/valise/dedupe/<vault_id>.key          去重密钥（0600）
  ├─ ~/.config/valise/targets.json                   异地同步目标
  ├─ ~/.local/share/valise/vault/                    服务器本地金库
  ├─ <状态目录的同级>/.valise-stage-<op_id>/           复活暂存目录
  ├─ <状态目录的同级>/.valise-prev-<op_id>/            交换后保留的旧状态
  └─ ~/.local/state/valise/{audit.jsonl, journal/, tmp/}
```

## 2. 传输

- **控制面**：`ssh -T <host> "<porter_path> rpc"` 启动一个会话级子进程，通过 stdin/stdout 交换单行 JSON-RPC 2.0 消息。首个请求必须是 `hello`，返回 porter 版本、协议版本、架构、HOME 路径、可用工具（`systemctl --user`、`git`、`npm`、`uv`、`rsync`、`rclone`），以及**未完成的交换日志**。协议主版本不一致时，客户端拒绝继续。交换日志的内容格式在 M4 定义；在此之前，`hello` 只按 `journal/` 目录中的 `<op_id>.json` 文件名列出 op_id。
- **数据面**：大数据流使用独立的一次性 SSH 调用，例如 `porter blob-read`（加密 blob 从服务器流向客户端）和 `porter stage-write --op <id>`（复活时，明文 tar 流从客户端流向服务器）。明文只会出现在客户端内存中，以及目标服务器的暂存目录里。
- **连接复用**：优先复用用户已有的 ControlMaster；没有时，使用 `-o ControlMaster=auto -o ControlPath=<客户端缓存目录>/cm-%C -o ControlPersist=60`。Unix 套接字路径有长度上限（macOS 为 104 字节，OpenSSH 建立 master 时还会追加约 17 字节的临时后缀），缓存目录过长时改用 `/tmp/valise-<uid>`（必须是本人所有、权限 0700 的真实目录）；仍不满足时不使用连接复用。
- **协议版本**：格式为 `<主>.<次>`，当前为 `1.0`。主版本号不一致时客户端拒绝继续；次版本号只表示向后兼容的新增。
- **porter 部署**：客户端从 GitHub Release 下载目标架构的二进制，用随客户端发布的公钥校验 `SHA256SUMS` 的 minisign 签名，再核对哈希；或者由用户显式选择本地构建产物。上传时先执行 `cat > ~/.local/bin/valise-porter.tmp`，再 `chmod 0755`，最后 `mv` 原子替换。
  - Release 资产命名：`valise-porter_linux_amd64`、`valise-porter_linux_arm64`、`SHA256SUMS`（`sha256sum` 输出格式）、`SHA256SUMS.minisig`。远端架构由 `uname -s -m` 判定，只支持 Linux 的 `x86_64` / `aarch64`。
  - minisign 校验：同时接受预哈希（`ED`，BLAKE2b-512）与旧版（`Ed`）签名，并且必须校验 trusted comment 的全局签名。签名公钥随客户端发布；客户端尚未配置发布公钥时，拒绝从 Release 部署，只允许上传本地构建产物。
  - 在 `chmod` 与 `mv` 之间，执行 `valise-porter.tmp version --json`：确认它能在目标架构上运行，并且它自报的 SHA-256 与本地文件一致，才执行 `mv`。任何一步失败都不替换已有的 porter。
  - 部署目标固定为 `~/.local/bin/valise-porter`，全程不使用 sudo。

## 3. Keystore

- **身份**：使用 age 原生的 X25519 身份（`AGE-SECRET-KEY-1…`），以 age 口令加密（scrypt）的形式保存为 `keystore.age`。用户随时可以用标准命令 `age -d keystore.age` 解出身份，**不依赖 Valise**。
- **口令**：至少 12 位。scrypt 工作因子使用 age 的默认值，不得调低。
- **收件人**：对应的 `age1…` 公钥登记到服务器的 `recipients.txt`。支持多个收件人，例如再加一把离线纸质备份钥匙。
- **指纹**：收件人公钥字符串（`age1…`）的 SHA-256 十六进制值的前 16 位。`VAULT` 中的 `recipient_fingerprints` 使用同一定义。
- **口令长度**：按 Unicode 字符计数，至少 12 个字符。
- **客户端数据目录**：`os.UserConfigDir()/valise/`（macOS 为 `~/Library/Application Support/valise/`，Linux 为 `~/.config/valise/`），其中包括 `keystore.age` 与 `config.json`。ControlMaster 套接字放在 `os.UserCacheDir()/valise/` 下。
- **离线恢复套件**：导出内容包括 `keystore.age`、收件人公钥、指纹，以及一页恢复说明（指向 `tools/standalone-restore/`）。导出为一个目录，内含 `keystore.age`、`recipient.txt`、`FINGERPRINT`、`RECOVERY.md`。导入时核对指纹：先确认 `FINGERPRINT` 与 `recipient.txt` 一致，解锁后再确认身份对应的公钥与 `recipient.txt` 一致，并把指纹展示给用户，与纸质记录比对。
- **内存处理**：解锁后的身份封装在 `UnlockedKey` 中，`Lock()` 时尽力清零。文档和 UI 中要如实说明：Go 的垃圾回收可能产生副本，无法保证彻底清除。UI 空闲 15 分钟后自动锁定（时长可配置）。

## 4. 登记（全部由代码完成）

1. **探测**：客户端把 profile 集合发给 porter，porter 按每个 profile 的 `detect` 规则（可执行文件、状态目录候选、标记文件）找出候选实例。
2. **解析**：把 profile 的 `paths` 规则展开到真实文件，为每个文件确定类别，并记录未分类的文件。
3. **来源记录**：按 `provenance` 规则读出身体的安装来源与版本，包括 npm 包名、精确版本、registry 和 integrity，git 的 remote、commit 和是否 dirty，uv/pipx 工具的包名与版本，以及二进制的哈希。如果 git 工作树是 dirty 状态，把差异记录为 `body_patch`，随灵魂一起封存。
4. **服务与入口**：确认 service 候选单元是否存在，入口端口是否在监听。
5. **登记报告**：客户端展示各类别的文件数量与大小、未分类文件、凭据文件清单（只列路径）、身体与来源、服务、入口、哪些 profile 条目未核实。用户确认，并对未分类文件做出处理决定后，才写入实例记录。
6. **agent 补充**（可选）：agent 通过 MCP 提议额外的路径和类别，这些提议以"待确认"状态保存。用户在客户端确认后，才合入实例记录的 `local_overrides`。

实例记录包含：`instance_id`、`profile_id`、`profile_version`、`roots`（解析后的绝对路径）、`provenance`、`service`、`entries`、`local_overrides`、`enrolled_at`。harness 升级后再次登记时，展示与上次的差异。

实现细节（M2 确定）：

- **profile 来源**：客户端内置仓库 `profiles/*.yaml`，另外读取客户端本机的 `os.UserConfigDir()/valise/profiles/*.yaml`（标记为"本地，未经审核"）。porter 自身不携带任何 profile。
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
  - raw 首次封存体积大（身体常达数 GB），之后依靠去重增量很小。默认不启用；低频周期执行与保留策略见 DEV_PLAN 的 M8。

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

触发封存的方式有四种：

- CLI 或 UI；
- agent 通过 MCP 触发，但只能封存 `soul` 档，并且有频率限制；
- 用户开启的定时任务（`systemd --user` timer；推迟到 M3 之后实现）；
- 复活之前自动封存。

封存由 porter 在服务器上使用收件人公钥加密完成，**不需要客户端在线**。

## 7. 金库与同步

- **收件人登记**：`valise key register <服务器>` 把本机密钥的公钥追加到服务器的 `recipients.txt`（每行一个 `age1…`，`#` 开头为注释）。收件人集合变化后，下一次封存新建 epoch。服务器上没有任何收件人时拒绝封存。`--label <说明>` 会在公钥上方写一行注释 `# <说明> (added <日期>)`，用来标明这把钥匙属于谁、用途是什么（例如测试钥匙）；注释不影响收件人集合和指纹。
- **移除收件人**（`valise key unregister`，M4 实现）：从 `recipients.txt` 删除一把公钥，下一次封存会新建 epoch，此后的快照不再加密给它。**必须向用户如实说明**：已经写入金库的旧快照仍然可以被这把钥匙解开，加密无法追溯撤销；如果钥匙已经泄露，应当把旧快照视为已暴露。拒绝移除最后一把公钥。
- 每台服务器有一个本地金库。异地同步由 `porter sync --target <name>` 完成，支持 rsync、rclone 和 sftp。目标配置写在 `targets.json` 中；rclone 使用用户自己的 remote 配置，Valise 不保存其中的凭据。
- 同步顺序固定为：pack → index → 快照 → 元数据。由于金库只追加，任何中断都可以安全续传。
- **targets.json**：`{"targets": [{"name": "nas", "kind": "rsync", "dest": "nas-test:/mnt/test/nas/valise-vault/"}]}`。`kind` 取值 `rsync`、`rclone`、`sftp`；M3 只实现 `rsync`（经服务器自己的 SSH 配置，加 `--ignore-existing`，按 pack → index → 快照 → 元数据 → epochs/keys → VAULT 的顺序逐个目录同步）。同步结果写入审计记录，`porter status` 据此报告每个目标的最后同步时间。服务器登录目标所需的凭据由用户自己配置，Valise 不保存。
- 客户端可以执行 `valise pull`，把金库拉取到本机保存。拉取只复制本机没有的文件（金库只追加），然后做不需要私钥的校验。
- **带私钥的校验**只在客户端进行：`valise verify --vault <本机目录> --key` 解锁本机密钥，在内存中解密快照清单与 blob，按文件校验 sha256。
- UI 中"已写入服务器金库"和"已同步到 <目标>"分开展示。
- v0.1 不提供删除功能。之后的 `prune` 必须由用户显式执行。

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

### 8.3 崩溃恢复

porter 每次被唤起时都会检查 `journal/` 目录，`hello` 会报告所有未完成的操作。客户端必须立即提示用户，让用户选择"继续完成"或"退回"。porter 绝不在没有客户端确认的情况下自动推进。

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

`audit.jsonl` 每行记录：时间、操作、实例、发起者（`client` / `agent-mcp` / `timer`）、结果、快照 id、字节数。审计记录绝不包含文件内容或凭据内容。

## 11. 错误与文案

所有错误统一为 `{code, reason, suggestion, detail}` 结构。UI 默认展示 `reason` 和 `suggestion`，`detail` 默认折叠。
