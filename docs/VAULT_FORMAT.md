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
├── VAULT                         # 明文 JSON，金库描述
├── keys/
│   └── dedupe.<epoch>.age        # 去重密钥，用 age 加密给当时的收件人
├── packs/<前2位>/<pack_id>.pack  # 不可变
├── index/<snapshot_id>.json      # 明文：本次封存新增的 blob 位置
└── snapshots/
    ├── <snapshot_id>.age         # age 加密的快照清单
    └── <snapshot_id>.meta.json   # 明文元数据，不含任何文件名
```

`VAULT` 文件的内容示例：

```json
{
  "format": "valise-vault/1",
  "vault_id": "v_01J…",
  "created_at": "2026-11-01T00:00:00Z",
  "chunker": { "algo": "restic-chunker", "min": 524288, "avg": 1048576, "max": 8388608, "small_file_max": 1048576 },
  "epochs": [ { "epoch": 1, "since": "2026-11-01T00:00:00Z", "recipient_fingerprints": ["…"] } ]
}
```

## 3. Blob

- **切分**：不超过 1 MiB 的文件整体作为一个 blob；更大的文件使用 `github.com/restic/chunker` 做内容定义分块，分块参数见 `VAULT`。
- **blob_id**：`hex(HMAC-SHA256(dedupe_key[epoch], 明文分块))`。
- **存储形式**：明文 → zstd 压缩 → age 加密给当前 epoch 的全部收件人，得到一段独立、完整的 age 密文。
- **去重边界**：只在同一个 epoch 内去重。收件人集合一旦变化，epoch 就加 1，并生成新的去重密钥。这样可以保证每个新快照都能被当前的密钥解开，不会因为引用了旧 epoch 的 blob 而依赖一把可能已经丢失的旧密钥。

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

```json
{
  "format": "valise-snapshot/1",
  "snapshot_id": "s_…",
  "instance_id": "openclaw-main",
  "tier": "soul | soul-secrets | full",
  "profile": { "id": "openclaw", "version": 1, "status": "draft | verified" },
  "provenance": { "...": "登记时的来源记录" },
  "created_at": "…",
  "roots": { "state": "/home/u/.openclaw", "workspace": "/home/u/.openclaw/workspace" },
  "files": [
    { "root": "state", "path": "agents/main/sessions/a.jsonl", "category": "sessions",
      "mode": 420, "mtime": "…", "size": 2345678, "sha256": "…", "blobs": ["…", "…"] }
  ],
  "symlinks": [ { "root": "state", "path": "…", "target": "…" } ],
  "notes": { "sqlite_vacuumed": ["…"], "unstable": ["…"], "unclassified_count": 3 }
}
```

## 6. 明文元数据

`<snapshot_id>.meta.json` 包含 `instance_id`、`tier`、`created_at`、文件数、明文总大小、新增字节数、`epoch` 和 `profile.id`。它的作用是让用户在没有私钥的情况下也能列出快照。**元数据中不包含任何路径或文件名。**

## 7. 校验

- **不需要私钥**：每个 pack 的 SHA-256 等于它的 `pack_id`；index 中的每个 offset 和 length 都在 pack 的范围内；每个 meta 都有对应的 `.age` 快照文件。
- **需要私钥**：解开快照清单，按文件拼接 blob，校验 sha256；抽样或全量都可以。

## 8. 安全说明（需要在 UI 中如实告知用户）

- 金库中的明文信息包括：实例 id、档位、时间、大小、blob 数量。文件名和路径全部加密。
- 去重密钥以明文形式（0600 权限）保存在服务器上，用于计算 blob_id。如果服务器被攻破，攻击者可以验证某段已知内容是否存在于历史 blob 中，**但无法解密任何内容**。

## 9. 脱离 Valise 恢复

```bash
age -d keystore.age > identity.txt                              # 输入恢复口令
age -d -i identity.txt snapshots/<id>.age > snapshot.json
# 对每个文件：按 blobs 顺序，从 index 查到 pack、offset、length，
#   dd 取出这段密文 | age -d -i identity.txt | zstd -d，依次拼接，最后校验 sha256
```

`tools/standalone-restore/restore.py` 只使用 Python 3 标准库，通过子进程调用 `age` 和 `zstd`。参数为 `--vault --identity --snapshot --out [--include-secrets]`，恢复完成后逐个文件校验 sha256。
