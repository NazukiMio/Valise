# Valise 备份库格式 `valise-vault/1`

> 这是一份公开格式。兼容性承诺：**读取端必须永久支持所有已发布的格式版本**。格式的任何变更都必须提升版本号，并同步更新 `tools/standalone-restore/`。

> 2026-11-05：旅程与默认保护重整不修改本格式、现有版本号或旧快照含义。第 11 节补充新读取服务与未来范围扩展的约束；当前实现进度见 DEV_PLAN.md。

> 2026-11-12 / 2026-11-15：曾短暂写入 `valise-snapshot/2`（含加密 `runtime` 来源字段）。按 2026-11-15 架构收敛决定，当前新封存恢复写入标准 `valise-snapshot/1`（不再采集运行期程序树）；根据本格式的永久兼容承诺，所有读取端（`core/vault` 与 `tools/standalone-restore/restore.py`）永久同时支持读取 `valise-snapshot/1` 与 `valise-snapshot/2`。容器 `valise-vault/1`、age/zstd、pack/index/files/meta 保持读取兼容。第 10 节定义双轨布局。

## 1. 设计目标

1. **只追加**：已经写入的文件永不修改。任何能够同步文件的工具（rsync、rclone、网盘）都可以直接作为异地存储。整组周期删除及服务器定时退役的范围见第 10 节。
2. **内容寻址去重**：未发生变化的数据（例如已经冷存档的旧会话）只需存储一次。
3. **服务器只持有公钥也能写入**：加密使用 age 收件人公钥完成。
4. **可脱离 Valise 恢复**：只需要 `age`、`zstd` 和几十行脚本，就能恢复全部数据。

## 2. 目录布局

```text
<vault>/
├── VAULT                         # 明文 JSON，备份库描述（写入后永不修改）
├── epochs/<n>.json               # 明文 JSON，每个 epoch 一个文件
├── keys/
│   └── dedupe.<n>.age            # 去重参数，用 age 加密给该 epoch 的收件人
├── packs/<前2位>/<pack_id>.pack  # 不可变
├── index/<snapshot_id>.json      # 明文：本次封存新增的 blob 位置
└── snapshots/
    ├── <snapshot_id>.age         # age 加密的快照清单
    └── <snapshot_id>.meta.json   # 明文元数据，不含任何文件名
```

备份库中的**每个文件写入后都永不修改**，因此同步时一律跳过已存在的文件即可断点续传。

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

- 一个 pack 由多段独立的 age 密文依次拼接而成，目标大小约 16 MiB。写入过程：先写 `packs/tmp-<随机>`，fsync，然后用整个文件的 SHA-256 作为 `pack_id`，重命名为最终文件名。中断的写入或下载会留下未改名的临时文件（`tmp-`、`.tmp-`、`.part-` 开头），没有任何文件引用它们；读取与校验都忽略这些名字，不算格式错误（2026-11-08）。
- 每次封存新增一个 index 文件，内容如下：

```json
{
  "format": "valise-index/1",
  "snapshot_id": "s_…",
  "epoch": 1,
  "blobs": [ { "id": "…", "pack": "…", "offset": 0, "length": 123456, "plain_size": 1048576 } ]
}
```

- 完整的索引等于所有 index 文件的并集，porter 可以在本地缓存这份并集。新自动快照的 index 包含该时间点引用的全部 blob 位置（包括文件清单和同叶子内复用的 blob）；允许重复记录同一位置，数据包仍然去重。旧 index 仍可只记录新增项。

## 5. 快照清单（加密）

快照分两层，以便未变化的实例再次封存时，文件列表也能被去重：

1. `snapshots/<snapshot_id>.age`：age 加密的快照头（JSON）。
2. 文件列表：一份按确定顺序序列化的 JSON 文档，和普通数据一样分块、去重、存进 pack。快照头的 `files_blobs` 按顺序列出它的 blob，拼接后的 SHA-256 等于 `files_sha256`。

旧版快照头（`/1`，永久可读）：

```json
{
  "format": "valise-snapshot/1",
  "snapshot_id": "s_…",
  "instance_id": "openclaw",
  "tier": "soul | soul-secrets | full | raw",
  "profile": { "id": "openclaw", "version": 2, "status": "draft | verified" },
  "provenance": { "...": "登记时的来源记录" },
  "profile_doc": { "...": "封存时使用的完整 profile" },
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

`profile_doc` 是封存时使用的完整 profile（已合并子 profile，JSON 形式，与实例的 `<id>.profile.json` 相同），可选。客户端没有同一 id 与版本的 profile 时，复活可以改用它（见 ARCHITECTURE.md 8.2.1）。

`local_overrides` 是封存时这个实例生效的补充归类规则（与实例记录中的同名字段相同），可选；没有这个字段的快照按空列表处理。

`name` 与 `env`（可选，2026-11-02 起）：命名实例的名字，以及让 harness 的命令针对这个实例的环境变量（PROFILES.md 第 2 节"多实例"）。没有这两个字段的快照是默认实例。

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

`<snapshot_id>.meta.json` 的作用是让用户在没有私钥的情况下也能列出快照。元数据的结构化字段不包含备份文件路径或文件名。

教学标记、建立者与原因由审计记录提供。允许三个可选字段：`kind` 为 `auto` 或 `savepoint`；传统周期中缺少该字段的时间点按历史备份展示，`savepoints` 叶子内缺少时按存档点展示。`note` 为用户明确输入的存档点备注，首尾去空白，最多 140 个字符，拒绝控制字符；它以明文同步，禁止程序自动填入文件路径、命令或凭据。`promoted_from` 为原 `<vault_id>/<snapshot_id>`，仅晋升时写入。

```json
{ "format": "valise-meta/1", "snapshot_id": "s_…", "instance_id": "openclaw", "tier": "soul", "created_at": "…",
  "file_count": 42, "plain_bytes": 1234567, "added_bytes": 34567, "epoch": 1, "profile_id": "openclaw" }
```

`added_bytes` 是本次封存新写入备份库的字节数（新 pack、index、快照清单与元数据之和）。

## 7. 校验

- **不需要私钥**：每个 pack 的 SHA-256 等于它的 `pack_id`；index 中的每个 offset 和 length 都在 pack 的范围内；每个 meta 都有对应的 `.age` 快照文件；每个 meta 的 `epoch` 都有对应的 `epochs/<n>.json` 和 `keys/dedupe.<n>.age`。
- **需要私钥**：解开快照清单，按文件拼接 blob，校验 sha256；抽样或全量都可以。

## 8. 安全说明（需要在 UI 中如实告知用户）

- 备份库中的明文信息包括：实例 id、档位、时间、大小、blob 数量与用户明确填写的存档点备注。文件名和路径全部加密。
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

`tools/standalone-restore/restore.py` 只使用 Python 3 标准库，通过子进程调用 `age` 和 `zstd`。参数为 `--vault --identity --snapshot --out [--include-secrets]`，恢复完成后逐个文件校验 sha256。文件写到 `<out>/<root>/<path>`（`body_patch` 写到 `<out>/valise/body.patch`），并恢复权限位与 mtime。自动周期和永久存档点均可直接用叶子路径恢复；符号链接按记录重建。未加 `--include-secrets` 时跳过 `secrets` 和 `unclassified` 类文件；`raw` 档快照整体按凭据层对待，必须加 `--include-secrets` 才能恢复。

## 10. 备份库树 `valise-vault-tree/1`（按实例与周期分档）

> 用户于 2026-10-31 决定：服务器与异地都按"服务器 / 实例 / 周期"分档，每个周期是一个独立的 `valise-vault/1` 备份库。第 1–9 节的格式不变，本节只规定备份库之上的目录层级。

### 10.1 目录布局

服务器本地（`~/.local/share/valise/vaults/`）：

```text
vaults/
├── TREE                                   # 明文 JSON，写入后永不修改
└── <instance_id>/
    ├── 2026-10-01_2026-10-31/             # 一个完整的 valise-vault/1 备份库（叶子）
    │   ├── PERIOD                         # 明文 JSON，写入后永不修改
    │   ├── VAULT  epochs/  keys/  packs/  index/  snapshots/
    │   └── DELETING                       # 只在删除进行中存在（10.4）
    ├── 2026-11-01_2026-11-30/
    └── savepoints/                      # 永久存档点叶子，无 PERIOD
```

异地（同步目标的 `dest` 之下多一层服务器名）：

```text
<dest>/<server_name>/TREE
<dest>/<server_name>/<instance_id>/<周期或 savepoints>/…
```

`TREE` 的内容：`{"format": "valise-vault-tree/1", "server_name": "ubuntu-vm", "created_at": "…"}`。`server_name` 由用户在添加服务器时确定（默认是 SSH 别名，规则同实例 id），保存在服务器的 `~/.config/valise/server.json`。用户改名时（ARCHITECTURE.md 第 3 节），目录随之改名，`TREE` 里的 `server_name` 改写为新名字，并加 `"renamed_from": ["旧名"]`。用户删除服务器并选择清理时（ARCHITECTURE.md 2.3），`server.json` 加可选字段 `"removed_at": "<RFC 3339 时间>"`，表示这台服务器已从 Valise 删除、备份库保留；读不懂该字段的旧版本忽略它，重新添加时去掉。

`PERIOD` 的内容：

```json
{ "format": "valise-period/1", "instance_id": "openclaw", "unit": "month", "tz": "Asia/Shanghai",
  "start": "2026-10-01", "end": "2026-10-31" }
```

### 10.2 周期

- 定时任务（`timer`）与受管方 MCP 触发的滚动备份（`agent-mcp`：`valise_seal`、`valise_handoff_begin` 与首次调用兜底自动备份）产生 `kind: auto` 快照，写入按日期滚动的周期叶子（`<start>_<end>`），可按保护计划的周期保留策略参与自动退役，也可由用户在需要时晋升为永久存档点。人类用户发起的手动备份与操作前保护备份写入独立的 `savepoints/`（`kind: savepoint`），每个实例一个，历次直接封存的存档点在叶子内按 epoch 去重。
- `unit` 取 `week`（周一开始）、`month`（自然月，默认）、`quarter`（自然季度）。`tz` 是设置周期时客户端所在的 IANA 时区。设置保存在服务器的 `~/.config/valise/instances/<id>.vault.json`（`{"unit": "month", "tz": "…"}`），没有这个文件时按 `month` 与 UTC 处理。
- 叶子目录名为 `<start>_<end>`（`tz` 下的日期，首尾都包含）。封存时按封存时刻在 `tz` 下的日期选择叶子：落在现有叶子范围内就写入它，否则新建叶子。
- 新叶子的 `start` 取"上一个叶子 `end` 的次日"与"封存日期所在周期的第一天"中较晚的一个（没有上一个叶子时取后者），`end` 是 `start` 所在周期的最后一天。因此修改周期设置只影响下一个叶子，已有叶子永不改名；长时间没有封存也不会产生不包含封存日期的叶子。
- 每个叶子有自己的 `vault_id`、epoch 与去重参数，去重只在叶子内进行；新叶子的第一份备份是全量。

### 10.3 读取与兼容

- 叶子就是 `valise-vault/1` 备份库：第 9 节的脱离恢复方法与 `restore.py` 直接作用于某个叶子目录，不需要了解备份库树。
- 快照 id 用全局唯一的 ULID 生成。晋升保留原 id 和加密头，另写独立的存档点备份库身份。定位副本时使用实际叶子；按 id 查询的入口优先使用永久叶子。
- RPC 与数据面用"叶子名"指代一个叶子：`<instance_id>/<周期>`；分档之前的旧备份库的叶子名为 `-`。
- 晋升读取自动快照的完整 index，验证源 pack 哈希，按区间搬运所需的 age 密文到目标 pack。目标可复用已存在的 blob。目标 epoch 号与 index、meta 一致；源 epoch 与加密去重参数归档到目标，遇到号码冲突可为导入材料分配本地号码，加密快照头保留原 epoch（解密恢复依据 blob id）。独立存档点先通过无钥匙校验再发布 meta；同一晋升重试返回既有结果及原备注。
- **分档之前的备份库**（`~/.local/share/valise/vault/`，整台服务器一个备份库）保持可读，界面上显示为"分档之前的旧备份库"，可以像一个叶子那样整体删除（10.4）；新的备份一律写入备份库树。异地已有的旧备份库同样处理。

### 10.4 删除周期

备份库的叶子内部只追加。删除以整个周期叶子为单位。

- 禁止删除当前日期仍在范围内的周期、永久 `savepoints` 叶子或单个快照。
- 删除前，客户端列出这个叶子在另一处（服务器 / 每个同步目标）是否有完整副本：必须核对相同备份库/周期身份、快照 ID 集合及其必要的快照头、index、pack、epoch 和 key 材料，并执行第 7 节相应完整性校验；数量相等不代表内容相同。核对范围、时间和读取失败分别展示，未知不算已有完整副本（核对方法见 ARCHITECTURE.md 7.1.1）。没有任何其他副本时，必须再确认一次，文案写明“删除后这些备份将无法恢复”。服务器与每个同步目标上的删除分别确认，互不连带；现有权限分级不因展示层新增本机位置而自动放宽。
- 删除过程：先写入 `DELETING`（内容为删除开始时间与发起者），再删除 `snapshots/`、`index/`、`packs/`、`keys/`、`epochs/`，最后删除 `VAULT`、`PERIOD`、`DELETING` 和目录本身。中途中断时，带 `DELETING` 的叶子在列表中显示为"删除未完成"，只提供继续删除；同步跳过带 `DELETING` 的叶子。
- 服务器上的删除由 porter 在 Go 代码中执行（`os.RemoveAll`）；同步目标上的删除用同步工具自身完成（rsync：用空目录加只匹配该叶子的过滤规则执行 `--delete`），不在目标主机上执行任何命令。
- 用户确认的保护计划可设 `keep_auto_periods`。0 关闭退役，正数保留最近 N 个已结束的纯自动周期。每次该计划定时封存成功后，服务器用同一互斥锁及 `DELETING` 流程处理更早的周期，记入 `leaf_retire` 审计；手动、历史、混合与无法核对的叶子不自动删除。配置以保留数为准，即使没有异地副本，旧自动周期也可退役。退役不删除任何异地副本。

## 11. 旅程读取、恢复材料与后续扩展（2026-11-05）

### 11.1 时间点与独立恢复

- 一次成功保存对应一个快照，头中的 tier 与文件清单描述当时的内容。新默认 full 不改变任何旧快照的 soul / soul-secrets / full / raw 含义。
- 每个快照有独立完整的恢复清单，文件/blob 可以复用同叶子、同 epoch 的历史数据块；“独立”指无需逐日重放，不代表快照头单文件自包含全部数据。恢复必须具备它引用的索引与数据包等依赖。
- 日历周期、加密 epoch、实例生命周期是不同概念。现有 `instance_id`、服务器显示名及周期目录不能被重新解释为跨服务器长期身份；已确认的实例世代与迁移关系保存在备份库外，不覆盖旧字段，不新增必须随快照携带的永久 AgentID。
- 从完整备份库副本可重建保存时间点的列表并按第 9 节导出，不依赖源服务器、dossier 或客户端时间轴索引。审计原因、教学标记、备注及迁移关系缺失不阻止读取快照，但不得编造补全。
- 时间轴查询缓存、任务及验证记录不作为恢复文件的必要依赖。具体存储契约见已定稿 [N1 共享服务契约](SERVICE_CONTRACTS.md) 第 3、5、9 节；身份/迁移记录丢失仅使关联未知，不阻断重建保存点或导出。双轨定义仍使用原有备份库文件。

### 11.2 副本的证据与验证边界

“发现元数据”“某时间点材料齐全”“整个周期副本齐全”“无钥匙完整性校验”“解密文件校验”“暂存测试”“实际启动验证”分别记录。任何验证结论必须附对象、范围与核实时间；读取失败为未知，不能由计数、旧同步记录或单个 meta 存在推导更强结论。

按单个快照取回的缓存允许只包含它的必要 pack，必须保留 ARCHITECTURE.md 8.2.1 的 `.partial.json` 标记与中断规则；不得作为完整周期副本，不能用于承诺其他时间点可恢复。完整副本除快照 ID 和 pack 外还要具备必要头、index、epoch 和 key，按第 7 节校验。无钥匙校验不能保证明文可解或程序可运行。

### 11.3 自定义范围与格式演进的开工条件

后续自定义范围必须能无歧义表达备份时的生效范围，以及范围内缺失文件和范围外文件的恢复处理；不能仅以缺少某文件推断它在备份时不存在。权限/读取错误、不稳定内容与有意排除必须区分，不能据此错误删除目标数据。

具体 schema、匹配优先级、SQLite 单位、完整性与恢复拒绝条件、旧客户端兼容闸门须先定稿。若需要格式变更，按本文件兼容承诺提升对应版本并同步独立恢复工具；必须实际验证旧工具在交换前拒绝不能解释的新语义，不能仅新增可选字段然后假定安全。尚未定稿前不添加 custom tier、不改变旧 tier 的恢复携带语义、不自动扩展 roots。

## 12. 快照头 `/2` 与运行时来源（2026-11-12 引入；2026-11-15 起仅作读取兼容）

> **2026-11-15 说明**：撤销 A/B 双槽位并行环境方案后，新写入的快照恢复使用 `valise-snapshot/1`（不再采集运行期程序树与 `runtime` 字段）。按本格式兼容性承诺，所有读取端与 `tools/standalone-restore/` **永久保留对 `/2` 的完整读取支持**，2026-11-12 至 2026-11-15 期间写入的 `/2` 快照无需迁移或重封。

历史写入的 `/2` 头使用 `format: valise-snapshot/2`，其原有字段与文件恢复语义和 `/1` 相同，新增加密字段 `runtime`。缺少该字段表示运行时证据未知，仍可恢复文件，不能自动推断 HOME、运行时或命名实例的来源。`/1` 不解释 `runtime`；若它被混入 `/1` 头，新读取器拒绝，避免以旧标签携带新语义。未知头版本必须在返回头/文件、暂存或目录交换之前拒绝。原 `/1` 读取器本来就检查精确版本号，因此会拒绝 `/2`；需在兼容测试中实际验证旧工具拒绝且没有输出数据。

`runtime` 使用独立 `format: valise-runtime/1`，记录以下公开来源事实（整个字段仍只在 age 头内）：

- `captured_at`：本次备份采集时间；`os` / `arch` 为 porter 所在系统/架构。
- `home` 与 `roots`：`{path, resolved?}`，分别为来源用户 HOME 和所选 Agent 的根。path 是登记/系统给出的逻辑绝对路径；resolved 是当时只读解析得到的规范路径。解析失败不猜测，记入 `unresolved`。这是来源记录，不授予挂载或写入这些路径的权限。
- `installation`（可选）：`install_id`、`default_instance_id`、`accepted_at`、`profile_doc`、`roots`、`registered_provenance`。从本次备份所在 dossier 的默认实例和接入时 profile 取得，专门保留命名实例所属安装的来源；它是接入时的记录，不能冒充备份时重新核实的当前程序。默认实例不唯一、缺失或档案不匹配则缺省，并记 unresolved。不会因此让命名实例继承默认实例的 tested 环境能力。
- `dependencies`（可选）：逐项 `{role, path, resolved?, size?, sha256?, version?, provenance?}`。只由来源适配器从 profile 明确声明的程序/运行时信息采集；不读宿主模型凭据、不以登录 PATH 猜解释器。目前没有适配证据时为空。
- `unresolved`：有限原因码数组，列出缺失的运行时依赖、来源或布局证据。未核实依赖时必须标记 `runtime_dependencies_unverified`，仅有登记来源而未核实时标记 `program_source_unverified`；“有一份 runtime 记录”绝不等于可以完整重建。正式来源适配器还须核对登记来源、目标版本和材料摘要。

不增加永久 AgentID，不把 runtime 放入明文 meta，不改变 tier 携带的数据范围，不自动增加受管根。旧快照保持原样；新客户端及新版独立恢复工具永久支持旧头。独立恢复工具只恢复文件，不自动执行 runtime 中的任何路径、命令或安装。

### 12.1 已安装程序的精确材料证据

`dependencies` 的 `role: program-tree` 表示 profile 明确 `body.paths` 中一个无内部通配符的目录（`明确目录/**`）。`path`/`resolved` 是其逻辑/规范来源，`size` 为普通文件大小之和，`sha256` 是以下标准 JSON 对象的 SHA-256：`{"format":"valise-program-tree/1","files":[...]}`。files 按相对来源 HOME 的 path 字节序排序，每项字段顺序为 `path,size,sha256,mode,link_target`，使用 UTF-8、无缩进，末尾无换行；link_target 仅符号链接存在，普通文件省略。普通文件 sha256 校验原字节，mode 为将应用的私有权限位（原权限与 0700 按位与）；链接 size 为 0、mode 为 511，sha256 校验目标文字的 UTF-8。JSON 字符串转义采用 encoding/json.Marshal 默认规则（含 HTML 字符与 U+2028/U+2029 转义）。树摘要不含采集时间、宿主用户名前缀或原文件 mtime。

只在所有文件/链接均按声明属于程序、来源和范围可核实时记录完整证据，不为读取敏感文件而猜黑名单。拒绝硬链接、特殊节点、程序外的链接、源 HOME 外的规范目录及采集中变化；无法完整采集不记录部分树为完整依赖，不影响原数据备份，但保留 unresolved。首个来源适配针对登记的 npm_global：严格核对登记 package/prefix/version、HTTPS registry 与 integrity 记录，以及该私有安装的 package.json 中 name/version；不执行 npm/node、不读登录 PATH、模型配置或凭据。此检查不宣称给所有依赖重新验过官方签名。

完整测试/恢复若复用服务器当前程序，必须由解密头提供 program-tree 证据，重新读取并逐项匹配树摘要、来源、系统/架构和布局，再复制到本次独立材料并冻结完整 tar 摘要；复制后复核来源/清单，禁止硬链接或直接挂入活动程序。只读登记、同版本标签或没有该证据的旧头不足以复用当前程序，应转入记录版本的来源重建方案；该方案不可用时说明不支持。程序证据与程序字节分开，记录摘要不表示备份中已经包含程序。

`role: runtime-executable` 表示 profile 明确的用户服务进程所用可执行文件；path/resolved 为核实的 HOME 内路径，size/sha256 校验该普通文件的原字节，version 缺省时不得按目录名补版本。只读服务/PID/启动标识/可执行文件身份并哈希，不读进程参数或环境。仅程序树证据不代表运行时已经核实。准备独立材料时必须按这项路径/大小/摘要逐项匹配，不能借用共享可变解释器。程序来源摘要绑定登记来源，具体文件/树/归档摘要分别绑定实际材料，不把两者混为一项。
