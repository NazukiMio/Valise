# N2 实施与验证记录（2026-11-05）

状态：实施中，尚未验收。依据已定稿的 [N1 服务契约](N1_SERVICE_CONTRACTS_2026-11-05.md)，不修改公开金库格式，不提高协议 1.3 版本，不引入 GUI 依赖。

## 线级映射

通过 hello.capabilities 分项协商；缺能力拒绝新方法，不用旧方法模拟新语义。

| 能力 | 方法 | 权限与含义 |
| --- | --- | --- |
| `identity/1` | `identity_get` | daily，只读返回指定 instance_id 的身份绑定；未接入世代显示 legacy_unbound，不初始化身份 |
| `identity/1` | 既有 `instance_write` | 保持 owner；明确接受接入时协调档案与身份写入，恢复内部普通档案写入不建立成功迁移关系 |
| `protection-plan/1` | `protection_get`、`protection_prepare`、`protection_status` | daily，只读权威计划、准备冻结变更、查询事务 |
| `protection-plan/1` | `protection_apply`、`protection_reconcile` | owner，精确世代与 revision 比较；核实/回退采用固定选择，不自动重跑未知 systemctl |
| `backup-runs/1` | `protection_backup`、`backup_run_status` | daily，按已接受计划执行和查询备份；固定运行记录绑定 AgentRef、计划版本及 SnapshotRef，未知结果不重发 |

新方法中的 revision 使用十进制字符串。保护变更计划首次执行期限为十分钟，摘要用于检测事实变化，不表示用户授权。人类确认留在客户端受信回调，RPC 修改仍需主人证明。

新 `protection-timer --instance ID` 由用户接受的计划产生，在运行时读取已接受版本；旧 `seal-timer` 的 soul 语义保留。接管后旧无版本 timer、period、目标设置写入拒绝。新计划备份不同时触发旧 targets.auto，复制只执行已接受计划的声明。备份成功与复制失败分别保留，不能冒称 N3 严格副本验证通过。

## 增补决定（2026-11-07 用户确认）

用户在 N2 实测后决定以下事项。后端部分在 N2 实现，使前端改动最少；弹窗、按钮等界面在 N4 落地，TUI 沿用已有健康页修复模式（`f 修复 · 动作名`）。与 N1 契约冲突处以本节为准。

### A. 服务器任务交给一次性 systemd 单元执行（方案 C）

- **范围**：按计划备份及计划内服务器复制。RPC 先按现有规则持久接受运行记录，再用 `systemd-run --user --collect --unit=valise-task-<run_id>` 启动 `valise-porter task-run --id <run_id>`；该单元只读运行记录里冻结的请求，不接受其他参数，跑完即结束，不是常驻服务。客户端关闭或断网不影响执行。
- **退化**：用户 systemd 不可用或启动单元失败时，在当前连接内执行，记录 `execution: attached` 并告诉用户“关闭 Valise 会中断这次备份”。
- **进度与查询**：执行器把阶段写进运行记录；客户端按 run_id 查询或订阅。保护计划提交只需数秒，仍在连接内执行，客户端以后台任务等待。
- **取消**：`backup_run_cancel`（daily）在记录里写入取消请求；执行器只在安全节点检查（封存提交前、每个复制目标之间），确认停下才记 cancelled，封存一旦提交就保留该快照。
- **本机任务**：拉回本机仓库、恢复上传暂存等必须经过本机的任务，在客户端进程内后台执行，任务索引落盘；关闭与强退按 N1 第 10 节。
- **服务接口**：Start 立即返回任务，另提供 List、Watch、Cancel；能力 `backup-runs/2`，旧 porter 退回连接内同步执行。

### B. 错过的定时备份

- porter 读取保护计划时，按计划日程、接受时间与 timer 运行记录，计算最近 30 天内应触发而没有运行的时刻，记为“错过”；其后已有定时备份成功的，记为“已过期”。结果随 `protection_get` 返回，时间按服务器时钟，时钟异常时一并提示。
- 客户端在打开时提示新的错过记录（GUI 弹窗在 N4）；“已读”标记只存客户端。
- 计划新增 `schedule.catch_up`（默认关闭，用户在设置里打开）：开启后 timer 使用 `Persistent=true`，服务器开机或恢复运行后，若错过了上一次就补跑一次。新字段要求能力 `protection-plan/2`；客户端不向旧 porter 发送，旧 porter 读到也会拒绝，不会当作没有开关。

### C. 被计划引用的复制目标的版本化修改

- `target_change_prepare`（daily）列出所有引用该目标的计划及其版本，冻结新目标定义；`target_change_apply`（owner）在保护计划锁内逐一核对这些版本，全部一致才原子写入 `targets.json`，并把每个引用计划的版本加一，让其他客户端看到变化；任何一个不一致则整次不写。
- 删除被引用的目标仍拒绝，须先修改计划不再复制；名称即标识，不支持改名。旧 `target_set` 对被引用目标继续拒绝。能力 `protection-plan/2`。

### D. 来源不明的旧快照用于恢复

恢复计划在源身份未知时列出快照自带信息（记录的实例名、profile 与版本、来源服务器与周期、时间、范围），并明确说明来源无法核实。执行前用户必须二选一：“我清楚，继续”（受信的人类确认，不接受 JSON 自称）或“先备份当前状态再恢复”（对目标现有实例先做一次 full 备份）。交换前的旧状态仍按既有规则保留。

### E. linger 与服务器时钟的健康提示及一键修复

- **linger**：计划开启了定时、linger 未开启时，健康页提示“退出登录或服务器重启后，定时备份不会运行”。一键修复执行 `loginctl enable-linger <用户>`；系统要求管理员权限时，经管理登录与高级模式用 sudo 执行；都不可用时显示命令，由用户自己执行。
- **时钟**：服务器与本机时间差超过 5 分钟，或 NTP 未同步时提示。一键修复执行 `timedatectl set-ntp true`；已开启 NTP 仍有偏差的，执行 `systemctl restart systemd-timesyncd` 后复核；均需管理员权限，规则同上。不从客户端时间手动改写服务器时钟。复核仍有偏差就如实说明，给出下一步。
- 两项都是 Valise 内置的固定系统动作，不是 harness 知识，不进 profile。确认页按 UI.md 健康一节列出实际命令与将改变的系统设置，用户再次同意后才执行；执行后复核并写审计。

## 恢复收尾身份与关系映射

`identity/1` 在既有主人权限的恢复协议上增加可选来源上下文：确认计划绑定 mode、SourceSnapshot（VaultID / SnapshotID）及可核实的 SourceAgentRef；没有快照与世代绑定证据时，源 Agent 身份保持未知，不将当前同名实例当作快照来源。旧 porter 缺此能力时不发送新增语义，也不声称建立关系。

目标仅在交换完成且服务健康（`HEALTHY`）后写入 `migration_completed` / `clone_from`；`RESTORED_NOT_STARTED` 只恢复了文件，不生成成功迁移边。失败、退回与暂存测试均不写成功边。恢复当前实例保留目标世代并记录 `restored_from`；迁移与副本建立目标新世代。记录位于配置目录的独立 `identity/`，不改写公开快照或 dossier schema，以远端 op_id 关联幂等收尾；身份/关系提交未完成时不发布成功绑定，显式继续收尾不重做目录交换。

源隔离状态必须来自现有执行路径的实际核实：客户端停止源服务并查询实际状态后才能汇报 `stopped_verified`；副本排除 profile 声明的独占项并完成暂存校验后才能汇报 `exclusive_omitted`。不提供用户设置该状态的选项，不以用户 JSON 声明代替确认或执行证据。无法取得证据为 `unresolved`，不得生成完整迁移成功关系。目标端按已确认 mode、操作与实际健康状态限制关系收尾；源状态的客户端传递不代表目标自行验证过源 SSH 主机信任。

## 当前验证边界

实现与测试结果在完成后逐项补录。fake-ssh 不代表真实 systemd 验收。成功迁移关系、统一任务全量适配等须按实际结果记录，不因底座类型存在而声称完成。

### 自检（2026-11-06）

`testbed/selfcheck.sh` 按 CLAUDE.md 第 6 节全部通过（SELFCHECK_PASS），含 Linux arm64/amd64 交叉编译；M7b 前的 mygo / bun 三步按规则跳过。`client/ui/service` 全包 `-race` 曾报两处数据竞争：管理钥匙 ssh-agent 的存活判断读取了另一 goroutine 写入的 `ProcessState`（`agent.go`，改为由 Wait 关闭的通道判断），以及 `TestActions` 的假剪贴板变量未加锁（仅测试）。2026-11-07 修正后全包 `-race` 通过。

### 实机记录（2026-11-06，`ubuntu-vm` ARM64，systemd --user，linger=yes）

使用当前工作树临时构建的 porter，放在 `~/.cache/valise-n2-*/` 的隔离夹具实例上（夹具文件只有一个 config、一个 memory 和一个声明为 secrets 的测试文件，不含真实凭据，不读写已有 Agent）。逐项通过：

- 明确接受接入后 `identity_get` 返回 bound 世代；
- 旧显式 soul 封存只含 2 个非凭据文件，旧语义保留；
- `protection_prepare` 由目标 `systemd-analyze calendar` 校验并返回 4 次触发预览，`protection_apply` 提交 revision 1，核实 timer active/enabled、next trigger 与 linger；
- 接管后旧 `period_set` 返回 revision_conflict；
- 手动按计划备份成功，同一 run_id 重试返回同一 SnapshotRef，不重复封存；
- RPC 连接关闭后，timer 在 05:21:23 UTC 触发 `protection-timer`，运行记录 reason=timer、state=succeeded，并绑定 SnapshotRef；
- 关闭日程提交 revision 2，核实 timer 已停用。

验收后保留该测试金库的快照，夹具的已接受计划文件移入同一测试目录，避免它的接管标记阻止同一服务器上其他实例的旧目标写入。

### 实机记录（2026-11-07，`ubuntu-vm-x86` x86_64，无 sudo，linger=yes）

同一隔离夹具流程 9 项全部通过，另加 full 快照实机核对：按计划 full 快照 3 个文件，含声明为凭据的测试文件；旧 soul 为 2 个。客户端断开后 timer 触发并成功备份。注意：该虚拟机系统时间比实际慢约 2.5 天（NTPSynchronized=yes 但显示 11-04），timer 按其本机时钟触发，结论不受影响；未修改靶机时钟。

### 真实迁移（2026-11-07，OpenClaw `ubuntu-vm` ARM64 → `ubuntu-vm-x86` x86_64）

用户授权使用测试靶机上的 harness。两台以 `porter deploy --local` 正式部署当前构建（原为协议 1.2 / 1.1，部署后 1.3 并声明 identity/1、protection-plan/1、backup-runs/1）。流程：源机 `enroll --accept` 建立世代 → 采用 full 计划（日程关闭）并按计划备份，`backup_snapshot_binding` 返回 bound → `revive --mode migrate`。结果：5550 个文件 97.3 MiB 暂存校验通过，服务健康；源服务 inactive，目标 active。目标得到新世代，并写入 `migration_completed` 关系，记录源 AgentRef、源 SnapshotRef、目标 AgentRef 与 op；两端身份提交记录均为 complete。目标保留交换前的旧目录（`.valise-prev-op_…`），未删除。

### 时间轴实机核对（2026-11-07）

把 `ubuntu-vm-x86` 金库用 `repo sync` 复制到本机隔离仓库（4 个周期、66 个快照，复制后校验通过），时间轴不连服务器读取，分 2 页（每页 50）读完 66 个点；快照 ID 与范围与服务器 `vault_status` 逐一一致，全部为 metadata_only（材料完整性留待 N3）。

### 提交失败回退与服务器复制（2026-11-07，`ubuntu-vm` 的 OpenClaw 计划）

- **真实失败回退**：先把计划改为日程关闭（revision 4，无 timer 单元），再在最高优先级的 `~/.config/systemd/user.control/` 用 `/dev/null` 链接屏蔽 `valise-seal@openclaw.service`，然后提交开启日程。真实 systemd 拒绝启动 timer，`protection_apply` 返回 `timer_failed`；事务记录为 failed、rollback_succeeded=true、committed=false。计划仍为 revision 4，timer 未启用、单元文件已清理，没有待核实警告，随后按计划备份正常。屏蔽链接已移除。（初次尝试用 `mask --runtime`，该位置优先级低于 `~/.config/systemd/user`，未能制造失败，提交正常成功；属于测试方法问题。）
- **服务器复制**：计划引用已有 `nas` 目标（`nas-test:/mnt/test/nas/valise-vault/`，最小间隔 1h），提交 revision 3。按计划备份：server_backup 成功，server_copy 成功（约 31 秒）；一小时内再次备份时 server_copy 为 skipped。旧入口 `target_set` 改 `nas` 返回 revision_conflict。

本轮同时收窄旧目标写入拦截：只拒绝被某个已接受计划 `server_copies` 引用的目标，以及存在未完成计划事务时的所有目标写入；未被引用的目标照旧可改（其 auto 只驱动 legacy 实例，计划备份不读 targets.auto）。此前实现在服务器有任一计划时拒绝全部目标写入，会导致采用计划后无法再新增复制目标；按 N1 第 4.3 节"影响计划时统一核对引用版本"改为现行规则。被引用目标的版本化修改入口尚未实现：要改 `nas` 的地址，需先把引用它的计划改为不复制。

`ubuntu-vm` 当前状态：OpenClaw 计划 revision 4，full，日程关闭，复制到 nas；该实例迁移后源服务已停止。同机其他 legacy 实例改 `nas` 目标会被拒绝。

尚未在实机覆盖：崩溃留下的 pending（fake 测试已覆盖）、DST 实际切换、客户端退出登录或重启、创建副本与恢复当前实例的关系（fake 测试已覆盖）。
