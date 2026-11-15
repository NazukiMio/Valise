# 开发环境与靶机手册

2026-11-15 受管来源与后续准备：已有隔离目录 `/home/parallels/.cache/valise-old-cleanup.SbPDHmH5/` 的独立 repeat-final.test 两端 SHA-256 均为 d720568f38a234f51bc4cc2549b624a74b20c81287188918d9204b6220d0dc77。`/tmp/valise-managed-repeat-linux-final.log` 为 MANAGED_REPEAT_FINAL_LINUX_PASS，回归父事务八项、原生九项、持久材料六项、取消/清理七项、可信准备十二项及来源/清理元数据边界。新场景从已验收 B1 取消 A2/A3，清理本次材料后仍保留当前 PID/程序/数据/验收，再实际切换/验收/独立清理 A4、B2；只使用已清理非活动槽，普通保留旧槽复用仍待接入。查询不改私有状态，测试最后随机服务文件/加载项清单前后均为零，TMPDIR 为空。第一轮夹具误用 HOME 指纹帮助函数，之后不适用的未验收断言也已修正；实际修复了完成的历史准备阻塞下一次准备、历史取消记录使当前读取失败两处问题。整仓与相关 race 通过，界面大小/滚动图在 `/tmp/valise-managed-repeat-shots/`。无实际 Agent、模型消息、部署、资料或用户应用更新，详见 acceptance/N4.md 6.38。

2026-11-15 设置中的已验收旧环境清理：在已有隔离目录 `/home/parallels/.cache/valise-old-cleanup.SbPDHmH5/` 上传独立 `settings-final.test`，SHA-256 两端均为 6efa9f35de4c97b641b07432bc61388c592ff74d44709c28a31810ef9eb54090。`/tmp/valise-settings-cleanup-linux-final.log` 为 SETTINGS_CLEANUP_FINAL_LINUX_PASS，覆盖元数据保护、父事务八项、原生九项、持久材料六项、取消/清理七项与可信准备十一项。新场景实际保存直通，核实当前 PID/内核标识/数据和人工验收不变，再另行清理；切回保留模式会拒绝旧审阅。清理后仍直通、当前受管环境及备份/固定原生入口保留；随机单元文件/加载项前后清单一致，TMPDIR 为空。只使用合成资料/自有随机服务，无实际 Agent、模型消息、部署或资料/用户应用更新。设置卡片与旧范围页渲染在 `/tmp/valise-settings-cleanup-shots/`；完整验收状态见 acceptance/N4.md 6.37，首次范围仍只覆盖已验收的旧环境，退回/取消后的非活动候选继续接入。

2026-11-15 旧环境独立清理：独立目录 `/home/parallels/.cache/valise-old-cleanup.SbPDHmH5/` 的最终 ARM64 `cleanup-final.test` 两端 SHA-256 均为 fc43e226e1bcaed73f9769f3a2fed7c1ae17c90934a3a0c205f4e82d1a0bb937。`/tmp/valise-old-cleanup-linux-final.log` 为 OLD_CLEANUP_LINUX_PASS，回归父事务八项、原生九项、持久材料六项、取消/清理七项及可信准备十项。新增普通/嵌套清理，实际终止已部分删除的工作进程，再只读重连与显式继续；当前数据、既有加密备份字节及清理后的备份/固定原生入口保持。替换旧目录、旧范围变更及嵌套备份拒绝，重复已完成清理只查询。嵌套夹具首轮遗漏它自己的网关所需 workspace 文件，补齐合成文件后重跑，不改变生产检查。随机原生/受管单元文件及已加载项前后清单一致、TMPDIR 已空；初次失败留下的自有随机失败单元已按确切名字 reset-failed，没有操作实际 Agent。界面大小与滚动渲染在 `/tmp/valise-old-cleanup-shots/`。整仓顺序检查及相关 race 通过；未部署 porter、更新资料包或用户应用，详见 acceptance/N4.md 6.36。

2026-11-15 人工验收/确认后切回：独立目录 `/home/parallels/.cache/valise-acceptance.v2l8JDpO/` 的最终 ARM64 `acceptance-final.test` 已核对两端 SHA-256 一致。`/tmp/valise-acceptance-linux-final.log` 为 HUMAN_ACCEPTANCE_LINUX_PASS，父事务八项、原生九项、持久材料六项、取消/清理七项及可信准备八项通过。首次场景增加确认前后相同 PID/启动标识、旧数据不清理、对话写入保留、重复已接受请求不改私有文件，以及 HTTP 失败/原独立程序损坏不阻断候选确认；恢复原独立程序后可明确切回，保留候选新写入和历史人工动作。测试只是固定人工动作适配，不发送实际模型消息、不作真实对话验收。首轮模拟损坏误写到 Workspace.HomeDir 数据副本，已改为 ProgramDir 程序树并重跑，未放宽生产校验。TMPDIR、随机原生/受管单元、启动链接及已加载项均已只读核实清理（`/tmp/valise-acceptance-linux-cleanup.log`）。GUI 渲染在 `/tmp/valise-acceptance-shots/`，含小窗口滚动操作；没有部署、更新资料包或用户应用，详见 acceptance/N4.md 6.35。

2026-11-15 切换审阅补测：独立目录 `/home/parallels/.cache/valise-switch-final.FA0KsNgw/` 上传最终 ARM64 恢复测试 `review.test`，仅创建合成资料与随机用户单元。`/tmp/valise-switch-linux-final.log` 为 SWITCH_REVIEW_LINUX_PASS，包含父事务八项、原生九项、持久材料六项、取消/清理七项及可信准备八项。增加审阅私有指纹不变、活动 A 持续写入摘要不变、错误摘要/缺少具体版本允许拒绝，候选暂存被篡改或改回后旧审阅仍拒绝且原 PID 保持；新审阅后实际切换及重新创建控制器后明确退回通过。TMPDIR、随机原生和受管 `valise-agent-*` 单元文件/链接/已加载项均已只读核实清理（`/tmp/valise-switch-linux-cleanup.log`）。客户端真实 RPC 断流替身只证明协议处理；界面渲染在 `/tmp/valise-switch-shots/`。没有操作实际 Agent、部署 porter、更新资料包或用户应用，正式切换能力尚不广告，见 acceptance/N4.md 6.34。

2026-11-15 首次准备传输/前端：新私有目录 `/home/parallels/.cache/valise-preparation-transport.SyP8xCzx/` 使用当前 ARM64 恢复测试，仅运行可信准备八项（增加未允许未核实能力时不写意图）和取消/清理七项。`/tmp/valise-preparation-transport-linux.log` 为 PREPARATION_TRANSPORT_LINUX_PASS，TMPDIR 与随机原生单元文件已核实清理。另有客户端 fake-ssh 的实际备份解密 tar/父子寻址/完成响应丢失后只查询/单独取消和清理测试；该传输替身不证明 Linux 网关、来源适配或真实备份的前端端到端。GUI 渲染在 `/tmp/valise-preparation-shots/`；没有操作实际 Agent、部署、更新资料包或用户应用，见 acceptance/N4.md 6.33。

2026-11-14 当前审批读取补测：新私有目录 `/home/parallels/.cache/valise-active-approval.0EFkOqd4/` 的 `/tmp/valise-active-approval-linux-final.log` 为 ACTIVE_APPROVAL_LINUX_PASS。真实 bwrap 在原生 A、正式 B、退回 A 核对当前数据、只读数据/程序、临时 HOME 和脱敏结果；同时回归只读挂载点缺失/别名拒绝、可信准备七项、持久绑定六项、取消/清理七项。TMPDIR 及随机原生单元文件已核实清理。仅合成资料与自己创建的单元，没有操作实际 Agent、部署 porter 或更新用户包，详见 acceptance/N4.md 6.32。

2026-11-14 旧档案只读/转换补测：新私有目录 `/home/parallels/.cache/valise-legacy-read.B8DD8LMf/` 的元数据与恢复测试只操作 t.TempDir 和合成记录。`/tmp/valise-legacy-read-linux.log` 覆盖普通旧格式读取、默认/命名投影、已接受记录优先、归档不覆盖、受管共享资料和整个转换范围预检，以及旧直接恢复模式；最终元数据产物的 `/tmp/valise-legacy-read-linux-final.log` 再核对归档中断重试、既有归档保留及坏档案不能回填，结果为 LEGACY_READ_LINUX_FINAL_PASS。TMPDIR 已空。没有操作真实 Agent/systemd、部署 porter 或更新资料包；二进制留在本轮私有目录，见 acceptance/N4.md 6.31。

2026-11-14 当前原生命令补测：`/tmp/valise-active-native-linux-fixed.log` 在原隔离目录 `/home/parallels/.cache/valise-cancel-preparation.awaJ777I/` 核对原生 A（父记录未建立）、正式 B 与独立退回 A 的固定私有原生命令和管理页取令牌；程序/实际数据/HOME/PID 隔离、伪造命令拒绝及私有文件指纹不变通过，同时回归准备、取消/清理与解密备份。随机原生文件/启动链接清理见 `/tmp/valise-active-native-linux-cleanup.log`。另用正常 porter 的 `native --instance` 新开真实 `ssh -tt -o ControlPath=none`，合成 t.TempDir 实例验证 PTY 回车/行模式/尺寸/双向字节和退出，`/tmp/valise-active-native-pty-final.log` 为 NATIVE_REAL_SSH_PTY_PASS；协调器结束后清理自身实例。未操作实际 Agent、部署或更新资料，GUI 原生终端尚未打进现有阶段包（acceptance/N4.md 6.29）。

2026-11-14 旧直接恢复模式约束：Linux ARM64 合成回归在新私有目录 `/home/parallels/.cache/valise-direct-policy.F0Hsxmsn/` 运行 `policy.test -test.run='^(TestDirectPolicy|TestRollbackKeepsGenerationAndLegacy)'`。核对默认并行、请求 mode/ID/路径别名/服务不能绕过、旧计划失效仍可退回、已交换日志继续/退回和独立清理、旧元数据不导入、文件测试无需直通，以及旧恢复不伪造身份；日志 `/tmp/valise-direct-policy-linux.log` 全部通过，TMPDIR 已空。测试服务命令均为夹具脚本，不触及真实 systemd 单元、Agent 或资料；测试二进制留在本轮目录，不部署为 porter。

2026-11-14 活动备份补测：`/tmp/valise-active-backup-linux-verified.log` 在原隔离目录 `/home/parallels/.cache/valise-cancel-preparation.awaJ777I/` 使用本轮 `valise-active-backup-arm64.test`。解密实际备份核对活动 A/B 数据、私有程序与链接、原样排除旧隐藏文件及程序破损后仍可保存数据；另回归持久材料六项、可信准备七项及取消/清理七项，覆盖没有正式父记录时的初始数据绑定及切换/退回后的只读数据绑定。随机原生单元文件和启动链接清理已只读核实（`/tmp/valise-active-backup-linux-cleanup-verified.log`）。未操作实际 Agent 或部署 porter；最新用户包为 `stage-test-20261114`，只包含已接入设置/预览/健康事实及原有测试入口，没有正式 A/B 操作链。生产规模全程序校验性能仍未验收，见 acceptance/N4.md 6.28。

2026-11-14 当前环境读取补测：`/tmp/valise-active-view-linux-verified.log` 在原隔离目录 `/home/parallels/.cache/valise-cancel-preparation.awaJ777I/` 用最终 `valise-active-view-arm64.test -activation-lab` 验证持久材料六项、可信准备七项及取消/清理七项。读取覆盖原生 A、受管 B、独立退回 A、嵌套目录和来源更新；反复核对 Valise 私有文件内容/模式/mtime 指纹不变。新增合法原服务重启后只读识别新进程，同时拒绝旧冻结切换计划。没有操作实际 Agent、部署 porter 或安装工具；随机原生单元/启动链接清理已只读核实，见 `/tmp/valise-active-view-linux-cleanup.log`。生产规模的全程序读取性能仍须在真实备份端到端验收时测量，不能用小型合成程序作性能结论。

2026-11-14 旧入口保护补测：最终 `/tmp/valise-runtime-guard-linux-final.log` 在已有隔离目录 `/home/parallels/.cache/valise-cancel-preparation.awaJ777I/` 运行归属读取、旧执行入口、可信准备六项及取消/清理七项，全部通过。未部署 porter，仅上传 `valise-runtime-guard-{arm64,reader}.test`；所有 systemd 操作都是测试创建的随机原生单元，结束后文件和开机链接已只读核实清理。`testing.T.TempDir` 子目录按 0777 与进程 umask 创建，Linux 此处为 0775；持有私有环境的测试 HOME 必须显式 chmod 0700，不得放松生产目录验证。无环境记录的普通 HOME 不继承此私有环境条件，另有回归。详见 acceptance/N4.md 6.26。

2026-11-14 取消/清理补测：`TestPreparationCancellationAndExplicitCleanupLab -activation-lab` 在独立目录 `/home/parallels/.cache/valise-cancel-preparation.awaJ777I/` 验证七项取消/清理/重连，包含终止自己的复制、实际隔离探针及实际清理工作进程。测试按私有生命周期锁核实 namespace 已结束，不因工作进程退出猜测停止；原生 PID/启动策略与活动数据保持不变。清理保留模式、可再次准备、旧清理不删新 A，替换目录和过期审阅拒绝。最终日志 `/tmp/valise-cancel-preparation-linux-complete.log`；同轮原生/父事务/完整材料/旧隔离回归在 `/tmp/valise-cancel-preparation-linux-final.log`。仅自己创建的合成资料、随机用户单元及私有目录，无实际 Agent、部署或工具安装。测试 worker 必须用测试文件的显式标志和固定元数据路径，不能将其当作 porter 执行接口。

2026-11-14 可信准备控制器补测：`TestTrustedNativePreparationUsesSelectedDataAndNeverStopsSourceLab -activation-lab` 直接调用生产准备控制器，以接受的合成 npm 来源和精确备份输入保存意图、独立 A、真实登记/预留 B、正式数据子日志及独立探针；不是在测试中手工拼装准备结果。六项最终通过（8.03 秒），含实际激活及重新创建控制器后的独立退回、缺少数据不消耗构建意图、预览变化、准备前/切换前来源变化和探针失败。测试特意让探针写数据，核实正式 B/交换暂存仍为原快照；原生 PID 和数据在准备期间不变。日志 `/tmp/valise-preparation-linux-final.log`，独立目录 `/home/parallels/.cache/valise-preparation.F4k3xnfR/`；原生九项、受管父事务八项、持久材料六项、预览五项和旧隔离 full/组合回归一起通过。初次回退检查因合成程序仍按候选检查原数据而失败，修正测试程序的原数据分支后完整重跑通过；正式代码未因此放宽检查。随机原生服务/文件/启动链接已只读核实清理，未部署 porter 或操作实际 Agent。

2026-11-14 只读恢复条件预览：`TestNativeEnvironmentPlanUsesRealSourceWithoutMutationLab -activation-lab` 在 ubuntu-vm 使用合成 npm 来源和随机原生用户服务，验证匹配来源，以及来源/设置/布局/世代变化拒绝。调用真实只读预览控制器后核对原 PID、内核启动标识、开机策略和活动数据均未变，没有创建准备/程序/数据子日志等状态。日志 `/tmp/valise-environment-plan-linux-final.log`，测试目录 `/home/parallels/.cache/valise-environment-plan.9X8t13NA/`；五项通过，随机文件、开机链接和已加载单元已核实清理。此结果不代表准备、正式切换或实际 Agent 的恢复通过。

2026-11-13 嵌套内容根补测：`TestBoundActivationReopensMaterialsAndUsesConcreteVerifierLab` 现有六项，新增 `nested-native-success` / `nested-native-failure`。同一 Agent 的 workspace 位于状态根内部，测试网关实际读写 workspace，正式运行只挂载最外层状态根，失败后回原目录和独立程序。随 `TestNestedManaged*`、原生九项和既有父事务八项一并在 ubuntu-vm 通过，日志 `/tmp/valise-nested-roots-linux.log`，目录 `/home/parallels/.cache/valise-nested-activation.v9vd9G44/`。仅合成数据/随机用户单元，不读取或启停实际 Agent；子目录身份、空根、别名和改映射拒绝属于同一回归。

2026-11-13 原生入口首次切换补测：同一父模块测试程序增加 `TestNativeFirstActivationTransactionLab`，仍用 `-activation-lab` 明确开启。测试仅新建 `fixture-native-<随机标识>.service`、复制独立合成程序及合成数据；先保持原生 A 运行，切换后关闭原生入口，以受管 B 运行，失败/明确切回使用独立受管 A。覆盖外部重启、启动策略变化、原安装失效、取消保留 PID、重连不重放和关闭开机启动。`TestBoundActivationReopensMaterialsAndUsesConcreteVerifierLab` 同时覆盖原生/既有受管入口与成功/失败四项，重新创建控制器后核对落盘材料。最终日志 `/tmp/valise-native-activation-linux-complete.log`；独立测试目录 `/home/parallels/.cache/valise-native-activation.pFc6F5lO/`。随机单元经停止/cgroup/启动策略核实后删除测试文件，最终确认没有文件、启动链接或已加载单元残留。没有操作实际 Agent 或部署 porter；合成通过不提升官方 profile 能力。

> 原根目录 `AGENT.md`（2026-11-07 改名移到这里）。本文只记录开发环境、仓库结构、靶机与测试替身；规则（三条总原则、架构铁律、安全与文案红线、Git 约定、自检）以根目录 [`AGENTS.md`](../AGENTS.md) 为准，产品定义见 [PRODUCT.md](PRODUCT.md)，当前进度见 [STATUS.md](STATUS.md)。

2026-11-15 收尾节点专项：`/tmp/valise-closeout-linux-verified.log` 在既有 `/home/parallels/.cache/valise-old-cleanup.SbPDHmH5/` 运行当前 ARM64 恢复测试程序，两项保留旧槽/取消后直接重用、切回后再次恢复均通过（13.64 / 15.11 秒）。历史查询指纹不变，过期切回请求不修改登记；测试创建的随机文件清单前后一致，TMPDIR 为空。只操作合成数据和测试随机单元，未部署 porter 或启停实际 Agent；用户已取消自动 Goal，后续按明确测试轮次推进。最终扩展 Linux `/tmp/valise-closeout-linux-final.log` 为 CLOSEOUT_LINUX_PASS，父事务八项、原生九项、持久材料六项、取消/清理七项及可信准备十四项通过，随机文件/加载项归零。完整顺序自检 `/tmp/valise-closeout-selfcheck.log` 为 SELFCHECK_PASS；相关 race 与双架构恢复测试编译通过，详见 acceptance/N4.md 6.39。

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
│   ├── VAULT_FORMAT.md       # 备份库公开格式
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
- **NAS 路径约定**：所有数据只能写到 `/mnt/test/nas/` 下，备份库同步目标固定为 `/mnt/test/nas/valise-vault/`。

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

## 开发版读取资料包（2026-11-10）

开发版没有发布签名公钥，装不上正式资料包，也就读不到 Kit 工具与云盘清单（本地资料不会增加工具）。真实窗口测试时，用 `client/ui/cmd/drillpack`（不入库）生成一份演练钥匙签名的资料包，再用同一把钥匙编译桌面版与命令行，装进本机数据目录：

```bash
cd client/ui && OUT=$HOME/.cache/valise-drill/devpack && go run ./cmd/drillpack $OUT && LD="-X valise/client/porter.ReleasePublicKey=$(cat $OUT/key.pub)" && go build -ldflags "$LD" -o /tmp/valise-desktop ./cmd/valise-desktop && go build -ldflags "$LD" -o /tmp/valise ./cmd/valise && /tmp/valise profiles install --file $OUT/valise-profiles.tar.gz --yes
```

钥匙每次随机生成：重新生成资料包后，桌面版和命令行都要用新钥匙重新编译。不带这个 `-ldflags` 编译的开发版会把已装的资料包报为“签名不对”，此时只能用本地资料。

2026-11-11 核对：默认配置的 2026.11.10 包对应 `~/.cache/valise-drill/devpack/key.pub`，而 `~/.cache/valise-drill/config` 里的包对应 `~/.cache/valise-drill/drillpack/key.pub`。使用哪个配置目录，就用与其包一致的公钥构建；不要把默认配置可验签的构建直接当作演练配置也可用。用对应构建的 `valise [--config-dir <目录>] profiles status` 只读核对签名，勿为消除错误而覆盖另一配置的资料包。

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

## porter 开发构建位置（2026-11-11 用户指定）

后续交付给用户更新的 Linux porter 统一放在 `/Users/nazukimio/Downloads/valise-porter-test/`，文件名区分 `valise-porter_linux_arm64` 与 `valise-porter_linux_amd64`。不自动部署；由桌面版选择文件更新并记录 SHA-256。


### 隔离环境底座的真实 Linux 验证（2026-11-12）

普通 `go test` 不把 fake-ssh 当作 Linux 命名空间证据。`porter/internal/isolated/lab_linux_test.go` 的真实内核测试需要显式 `-isolated-lab`；测试只运行合成网关，不读实际 Agent，不装工具或修改主机策略。测试命令需有明确超时，材料放私有临时目录。

在 porter 模块用 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/valise-isolation-lab.test ./internal/isolated` 构建；向 ubuntu-vm 的 `mktemp -d` 目录上传后，运行 `isolation.test -test.v -test.run=Test -isolated-lab`。测试自行用 `t.TempDir()` 管理材料，验证只读映射、私有 HOME、外连/宿主回环隔离、会话环境、嵌套 user namespace、私有网关、取消及后台子进程终止。合成测试内监听不代表 porter 可增加监听器。不能用 host-root 的只读映射替代正式挂载策略。

2026-11-12 ubuntu-vm 已有 bwrap 0.11.1；未安装工具或改 sysctl/AppArmor。最终底座记录见 acceptance/N4.md 6.8；这些结果不是 harness 或命名实例的恢复能力证明。


### 私有候选构建与网关试运行（2026-11-12）

在 porter 模块构建 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/valise-candidate-lab.test ./internal/environments`，上传至主力 ubuntu-vm 的新 `mktemp -d` 临时目录后运行 `valise-candidate-lab.test -test.v -test.run=Test -candidate-lab`。`-candidate-lab` 才启用真实内核闭环；其他情况下只跑本机文件/归档/状态规则。合成程序、数据和私有 HOME 均由 `t.TempDir()` 管理。

测试会占用宿主 `127.0.0.1:18391` 作为另一合成网关，证明不能把宿主结果误认为私有候选；若端口已被占用则失败，不停止或替换现有服务。随后同端口的候选网关只运行在独立网络里。测试不访问真实 harness，不需要 sudo，不安装依赖或修改主机策略。新内部 helper 不得直接在宿主启动或用于任意命令；只允许专用 bwrap 命名空间、固定只读挂载和配置文件。此检查不是官方 harness 的恢复验收。


### 已登记私有程序来源的只读验收（2026-11-12）

`porter/internal/environments/source_lab_linux_test.go` 使用独立开关 `-installed-source-lab -installed-instance <明确默认实例>`。只读 profile/dossier 和声明的程序文件，核对 npm 包元数据，采集完整程序/运行时摘要并复制到 t.TempDir 的独立标准 tar；不安装或启动 harness、不读 Agent 状态/凭据、不写备份库、不部署 porter。普通 candidate-lab 仍只运行合成程序。可显式加 `-runtime-profile <资料>`，在源码/根/服务声明与登记一致时额外构建私有候选，只用空白合成数据启动真实复制程序并验证网关与原服务 PID/启动标识保持；不是实际备份恢复验收。程序准备空间需先核实，材料摘要/复制后复核为流式读取，不把大程序装进内存。此验收不是实际快照恢复或正式切换证明。


### 选定暂存到隔离网关的闭环（2026-11-12）

`porter/internal/revive/environment_lab_linux_test.go` 的 `-environment-test-lab` 只使用合成 dossier、明确备份绑定、标准测试程序和数据，验证冻结测试计划、暂存校验、独立程序复制/数据构建、HTTP 就绪、停止、重复请求只读和原操作清理。内部 helper 同样在 bwrap 中执行。

在 porter 模块交叉构建 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/valise-environment-pipeline.test ./internal/revive`。靶机运行 `pipeline.test -test.v -test.run=TestEnvironmentTestStageThroughGatewayAndCleanupLab -environment-test-lab`。TMPDIR 必须位于该用户 HOME 下的新私有缓存临时目录；测试的合成来源 HOME 不能在隔离策略禁止的 `/tmp` 根下，不为测试放宽正式策略。测试材料由 t.TempDir 清理；测试程序仅上传临时目录，不替换已部署 porter。这不是实际 Agent 的完整恢复验收。


2026-11-13 同一 `TestEnvironmentTestStageThroughGatewayAndCleanupLab` 增加 composed 子用例：目标轻装备份加较早 full，验证已删除目标范围文件不出现、范围外数据来自明确底座，随后同样验证私有网关/停止/不重放/清理。此次临时程序与 TMPDIR 在 `/home/parallels/.cache/valise-environment-composition.3lB2ed8h/`；不读取真实 Agent 数据、不控制原生服务或替换 porter。

### 正式受管入口及数据子日志的合成验收（2026-11-13）

`porter/internal/managedruntime` 用 `-managed-runtime-lab` 验证真实 systemd 用户单元、独立程序、稳定数据根、拥有的回环 HTTP 入口、重连只读和分别启停开机启动。在 porter 模块构建 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/valise-managed-runtime.test ./internal/managedruntime`，上传新私有目录，执行 `-test.v -test.run=Test -managed-runtime-lab`。合成 AgentRef 生成随机单元名，测试仅停/删自己创建且已核实停止的单元和锁，不控制实际 Agent。普通 go test 不启用真实服务。

`porter/internal/revive/managed_data_lab_linux_test.go` 用独立的 `-managed-data-lab` 接上目录交换子日志。构建该包的 Linux 测试产物，上传私有目录，将 TMPDIR 指向同目录，再运行 `-test.v -test.run='^TestManagedData' -managed-data-lab`。A 活跃时准备 B；停止并核实 A 后交换；B 返回 HTTP 503，先停并核实 B，再按原日志退回数据、更新原定义并启动/核实 A。原目录和失败 B 的写入分别保留。单元由系统运行 bwrap，不启动 porter 守护进程。

2026-11-13 合成运行目录分别为 `/home/parallels/.cache/valise-managed-runtime.d2180wkN/`、`/home/parallels/.cache/valise-managed-data.aajRJWpC/`，内部材料用 t.TempDir 管理。只上传临时测试程序；未安装工具、改系统策略、读实际 Agent 数据、停止原生服务或部署 porter。这是内部组合验收，不是选定真实备份的正式恢复、对话或生产 profile tested。

### 持久父事务的真实合成验收（2026-11-13）

在 porter 模块构建 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/valise-activation.test ./internal/revive`，上传至新私有 HOME 缓存目录，TMPDIR 指向该目录后执行 `activation.test -test.v -test.run=TestManagedActivationTransactionLab -activation-lab`。测试调用正式内部 ActivationRunner，不用测试代码手动代替父事务；每边用独立的只读合成程序副本、同一稳定数据根和随机 AgentRef 用户单元。档案和环境登记在合成 HOME 中，不用实际 Agent 的登记。

该开关覆盖成功待验收、失败退回、准备取消、实际候选文件变化、未知意图重开只读、原开机策略保留、停止证据暂不可核实和原程序来源适配拒绝。停止未知用自己创建的精确单元文件权限变化注入，恢复权限后由明确 Return 重试；不碰其他单元。程序校验拒绝用内部可信校验器的失败注入，不把它冒充真实来源改变。材料由 t.TempDir 管理，测试结束只停止/删除已核实属于本次随机单元的服务及锁。

最终真实目录 `/home/parallels/.cache/valise-activation.jpEqSLGn/`，运行前确认 HOME 可用 11 GB，峰值仅两份合成程序与数据。最终八场景合计 9.59 秒。没有读取实际 Agent 数据、停原生服务、修改策略、安装依赖或部署 porter。生产来源/首次接入及前端仍待后续验收，不能以此标 profile tested。
