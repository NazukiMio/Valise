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

尚未在实机覆盖：full 快照文件数的实机核对（fake-ssh 已核对 4 个文件含凭据与未分类）、提交中途失败回退与崩溃 pending、DST 实际切换、客户端退出登录或重启、`ubuntu-vm-x86` 与无 sudo 环境、计划内服务器复制目标。成功迁移关系的实机验证尚未进行。
