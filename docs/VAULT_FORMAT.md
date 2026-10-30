# Valise 金库格式 `valise-vault/1`

> 这是一份公开格式。兼容性承诺：**读取端必须永久支持所有已发布的格式版本**。格式的任何变更都必须提升版本号，并同步更新 `tools/standalone-restore/`。

## 1. 设计目标

1. **只追加**：已经写入的文件永不修改。任何能够同步文件的工具（rsync、rclone、网盘）都可以直接作为异地存储。
2. **内容寻址去重**：未发生变化的数据（例如已经冷存档的旧会话）只需存储一次。
3. **服务器只持有公钥也能写入**：加密使用 age 收件人公钥完成。
4. **可脱离 Valise 恢复**：只需要 `age`、`zstd` 和几十行脚本，就能恢复全部数据。

## 2. 目录布局

```text
<vault>/
├── VAULT                         # 明文 JSON，金库描述（写入后永不修改）
├── epochs/<n>.json               # 明文 JSON，每个 epoch 一个文件
├── keys/
│   └── dedupe.<n>.age            # 去重参数，用 age 加密给该 epoch 的收件人
├── packs/<前2位>/<pack_id>.pack  # 不可变
├── index/<snapshot_id>.json      # 明文：本次封存新增的 blob 位置
└── snapshots/
    ├── <snapshot_id>.age         # age 加密的快照清单
    └── <snapshot_id>.meta.json   # 明文元数据，不含任何文件名
```

金库中的**每个文件写入后都永不修改**，因此同步时一律跳过已存在的文件即可断点续传。

`VAULT` 文件的内容示例：

```json
{
  "format": "valise-vault/1",
  "vault_id": "v_01JBX7Q2W8M3ZK5N6P9R4T0Y2A",
  "created_at": "2026-11-01T00:00:00Z",
  "chunker": { "algo": "restic-chunker", "min": 524288, "avg": 1048576, "max": 8388608, "small_file_max": 1048576 }
}
```

`epochs/<n>.json` 的内容示例（`n` 从 1 开始，按十进制书写，不补零）：

```json
{ "epoch": 1, "since": "2026-11-01T00:00:00Z", "recipient_fingerprints": ["3f2a9c0d1e4b5a67"] }
```

- 收件人指纹的定义见 ARCHITECTURE.md 第 3 节。
- id 格式：`vault_id` 为 `v_` 加 ULID，`snapshot_id` 为 `s_` 加 ULID（26 位 Crockford Base32，按时间排序）。
- 服务器的收件人集合（`recipients.txt`）与最新 epoch 的指纹集合不一致时，下一次封存前新建一个 epoch，并生成新的去重参数。

### 2.1 去重参数

`keys/dedupe.<n>.age` 解密后，以及服务器上的 `~/.config/valise/dedupe/<vault_id>.key`（明文，0600，只保存当前 epoch），内容都是：

```json
{ "format": "valise-dedupe/1", "vault_id": "v_…", "epoch": 1, "hmac_key": "<64 位十六进制>", "chunker_pol": "<十六进制>" }
```

- `hmac_key`：32 字节随机数，用于计算 blob_id。
- `chunker_pol`：内容定义分块使用的不可约多项式（restic/chunker 的 `Pol`），每个 epoch 随机生成。

两者都只用于继续去重；**恢复数据不需要它们**。

## 3. Blob

- **切分**：不超过 `small_file_max`（1 MiB）的文件整体作为一个 blob；更大的文件使用 `github.com/restic/chunker`，以当前 epoch 的 `chunker_pol` 和 VAULT 中的 `min`、`max` 做内容定义分块（平均约 1 MiB）。
- **blob_id**：`hex(HMAC-SHA256(hmac_key[epoch], 明文分块))`。
- **存储形式**：明文 → zstd 压缩（标准 zstd 帧）→ age 加密给当前 epoch 的全部收件人，得到一段独立、完整的 age 密文。
- **去重边界**：只在同一个 epoch 内去重。收件人集合一旦变化，epoch 就加 1，并生成新的去重参数。这样可以保证每个新快照都能被当前的密钥解开，不会因为引用了旧 epoch 的 blob 而依赖一把可能已经丢失的旧密钥。

## 4. Pack 与 Index

- 一个 pack 由多段独立的 age 密文依次拼接而成，目标大小约 16 MiB。写入过程：先写 `packs/tmp-<随机>`，fsync，然后用整个文件的 SHA-256 作为 `pack_id`，重命名为最终文件名。
- 每次封存新增一个 index 文件，内容如下：

```json
{
  "format": "valise-index/1",
  "snapshot_id": "s_…",
  "epoch": 1,
  "blobs": [ { "id": "…", "pack": "…", "offset": 0, "length": 123456, "plain_size": 1048576 } ]
}
```

- 完整的索引等于所有 index 文件的并集，porter 可以在本地缓存这份并集。

## 5. 快照清单（加密）

快照分两层，以便未变化的实例再次封存时，文件列表也能被去重：

1. `snapshots/<snapshot_id>.age`：age 加密的快照头（JSON）。
2. 文件列表：一份按确定顺序序列化的 JSON 文档，和普通数据一样分块、去重、存进 pack。快照头的 `files_blobs` 按顺序列出它的 blob，拼接后的 SHA-256 等于 `files_sha256`。

快照头：

```json
{
  "format": "valise-snapshot/1",
  "snapshot_id": "s_…",
  "instance_id": "openclaw",
  "tier": "soul | soul-secrets | full | raw",
  "profile": { "id": "openclaw", "version": 2, "status": "draft | verified" },
  "provenance": { "...": "登记时的来源记录" },
  "local_overrides": [ { "root": "state", "glob": "plugins/foo/**", "category": "memory" } ],
  "created_at": "…",
  "epoch": 1,
  "roots": { "state": "/home/u/.openclaw", "workspace": "/home/u/.openclaw/workspace" },
  "files_blobs": ["…"],
  "files_sha256": "…",
  "sqlite": [ { "root": "state", "path": "agents/main/agent/openclaw-agent.sqlite", "stripped_tables": ["auth_profile_store"] } ],
  "notes": { "unstable": ["…"], "unclassified_count": 3 }
}
```

`local_overrides` 是封存时这个实例生效的补充归类规则（与实例记录中的同名字段相同），可选；没有这个字段的快照按空列表处理。

文件列表（`format` 为 `valise-files/1`；`files` 与 `symlinks` 都按 `root`、`path` 的字节序排序）：

```json
{
  "format": "valise-files/1",
  "files": [
    { "root": "state", "path": "agents/main/sessions/a.jsonl", "category": "sessions",
      "mode": 420, "mtime": "…", "size": 2345678, "sha256": "…", "blobs": ["…", "…"] }
  ],
  "symlinks": [ { "root": "state", "path": "…", "target": "…" } ]
}
```

- `category` 取 ARCHITECTURE.md 第 5 节中的值；没有任何规则命中的文件记为 `unclassified`（只出现在 `soul-secrets`、`full`、`raw` 档）。
- `body_patch` 以虚拟文件的形式出现在 `files` 中：`root` 为 `valise`，`path` 为 `body.patch`，`category` 为 `body_patch`。恢复时它不属于任何状态目录。
- `sqlite` 列出以 `VACUUM INTO` 副本封存的数据库；`stripped_tables` 是本次快照中已清空的凭据表（只在 `soul` 档出现）。`files` 中这些数据库的 `size` 与 `sha256` 是副本的值。
- `mode` 为 Unix 权限位（十进制），`mtime` 为 RFC 3339。

## 6. 明文元数据

`<snapshot_id>.meta.json` 的作用是让用户在没有私钥的情况下也能列出快照。**元数据中不包含任何路径或文件名。**

```json
{ "format": "valise-meta/1", "snapshot_id": "s_…", "instance_id": "openclaw", "tier": "soul", "created_at": "…",
  "file_count": 42, "plain_bytes": 1234567, "added_bytes": 34567, "epoch": 1, "profile_id": "openclaw" }
```

`added_bytes` 是本次封存新写入金库的字节数（新 pack、index、快照清单与元数据之和）。

## 7. 校验

- **不需要私钥**：每个 pack 的 SHA-256 等于它的 `pack_id`；index 中的每个 offset 和 length 都在 pack 的范围内；每个 meta 都有对应的 `.age` 快照文件；每个 meta 的 `epoch` 都有对应的 `epochs/<n>.json` 和 `keys/dedupe.<n>.age`。
- **需要私钥**：解开快照清单，按文件拼接 blob，校验 sha256；抽样或全量都可以。

## 8. 安全说明（需要在 UI 中如实告知用户）

- 金库中的明文信息包括：实例 id、档位、时间、大小、blob 数量。文件名和路径全部加密。
- 去重密钥以明文形式（0600 权限）保存在服务器上，用于计算 blob_id。如果服务器被攻破，攻击者可以验证某段已知内容是否存在于历史 blob 中，**但无法解密任何内容**。

## 9. 脱离 Valise 恢复

```bash
age -d keystore.age > identity.txt                              # 输入恢复口令
age -d -i identity.txt snapshots/<id>.age > snapshot.json       # 快照头
# 定位一个 blob：在 index/*.json 中查到它的 pack、offset、length，
#   dd 取出这段密文 | age -d -i identity.txt | zstd -d 得到明文分块。
# 1. 按 snapshot.json 的 files_blobs 依次取出并拼接，得到文件列表 files.json，校验 files_sha256。
# 2. 对 files.json 中的每个文件，按 blobs 依次取出并拼接，最后校验 sha256。
```

`tools/standalone-restore/restore.py` 只使用 Python 3 标准库，通过子进程调用 `age` 和 `zstd`。参数为 `--vault --identity --snapshot --out [--include-secrets]`，恢复完成后逐个文件校验 sha256。文件写到 `<out>/<root>/<path>`（`body_patch` 写到 `<out>/valise/body.patch`），并恢复权限位与 mtime；符号链接按记录重建。未加 `--include-secrets` 时跳过 `secrets` 和 `unclassified` 类文件；`raw` 档快照整体按凭据层对待，必须加 `--include-secrets` 才能恢复。
