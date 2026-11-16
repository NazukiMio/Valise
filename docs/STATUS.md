# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-16。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前工作与交接（2026-11-16 Phase B1 已完成并重打测试包 `stage-test-20261116-b1`，待用户真机验收与审批；之后 Phase B2）

**架构收敛与三阶段剥离完成状态**：
经 2026-11-15 代码审计与产品复盘，用户已确认**撤销** A/B 双槽位并行运行环境（`linux-bwrap` 容器接管、三档恢复保护模式、独立程序树采集与父子切换状态机）。分支 `refactor/revoke-ab-slots-and-doc-audit` 上三个阶段均已完成、验证并提交：
- **Stage 1（已完成，`bc3d917`）**：创建分支 `refactor/revoke-ab-slots-and-doc-audit`，完成 Prompt Audit 与规范文档重整；归档废止的 A/B 契约、提案与合成实验记录至 `docs/history/`；补充 MVP（`v0.1`）边界、工程减负计划与 5 项高阶用户痛点规划。
- **Stage 2（已完成，`c9de8b9`：`core` / `profiles` / `porter`）**：
  - 已剥离 `core/environment/`、`core/profile/{environments*,readiness*,runtime_executable_test}.go`、`core/revive/{environment_plan,environment_preparation,managed}.go`、`core/rpc/tier.go` 中 `environment_*` 方法、`profiles/openclaw.yaml` 的 `environments:` 段；
  - 已剥离 `porter/internal/{activationrecord,activeexec,activeview,environments,isolated,managedruntime,runtimeguard}/`、`revive/` 策略与 A/B 槽位代码、`server/` 环境探测/计划接口、`seal/` 运行期程序树叠加采集（新封存写回 `valise-snapshot/1`，`core/vault` 与 `tools/standalone-restore` 永久保持 `/1` 与 `/2` 读取兼容）；
  - 保留 `restore-review/1`、`backuprun`、`seal/journey`、`legacy` 只读兼容及 `porter/internal/install/native.go` 的非 A/B `native --instance` 入口。
- **Stage 3（已完成，本提交：`client/ui`）**：
  - 已移除 `client/ui/{service,tui,gui}` 中的 50 个 A/B 专用文件（`environment_*`、`active_approval_test.go`、`direct_policy_test.go`、`health_runtime*`、`restoretest_{baseline,capability_test,composition_test,environment*,support*}`、`tui/runtimecards_test.go`、`gui/{environment*,healthruntime*,healthtestenvironment*,healthtestmaterials*,restoreoldcleanup*,restoreparallel*,restorepolicy}.go`）；
  - 已清理 `client/ui/{service,cmd,tui,gui}` 保留文件中对 `core/environment`、`RuntimeState`/`Runtime`、`EnvironmentTestSupported`、`RestoreKindEnvironment`、`TestMaterialReview` 及 `isolated` 隔离深度的依赖；已从 `client/ui/gui/i18n/zh-CN.json` 移除 184 个失效 A/B 词条，`gui/` 与 `tui/` 全部 `.go` 文件均为 `<= 400` 行（最高 395 行）；
  - 已通过 `AGENTS.md` 第 6 节全部 5 步检查：`core`（含 `profilelint`）、`porter`（含 `linux/arm64` 与 `linux/amd64` 交叉编译）、`client`、`testbed`、`client/ui`（含 `go vet`、`gofmt`、`go tool mygo vet` 及 `go test ./...` 全部通过；其中本机 `127.0.0.1` 回环、`ssh-add`、`~/.ssh/config` 与 askpass 相关测试需在放开沙箱后运行）。

### 已保留的非 A/B 有效成果

1. **旅程范围化与增量加载**（`journey_stream.go`、`journey_scope.go`、`journeyread.go`、`journeylocations.go` 等）：按当前连接与明确迁移关系查询，支持分位置超时与重试。
2. **一次性备份进度与视图**（`backupfollow.go`、`backuponce.go`、`backupview.go`、`backupclientcopies.go` 及 `porter/internal/server/backuprun.go`）。
3. **直通恢复 / 失联恢复 / 导出 / 恢复前可选备份 / 恢复历史**（`restore.go`、`restoresetup.go`、`restoreprogress.go`、`restorehistory.go`、`restorelocations.go`、`lostsource.go`、`migrate.go` 及 `porter/internal/server/restore_review.go`）。
4. **健康页按时间点选择的文件与暂存恢复测试**（`healthrestore.go`、`healthtestselect.go`、`restoretest*.go`）：保留本地与服务器暂存两步文件核对（`test_only` 写入暂存后 `discard`），移除 `isolated` 隔离网关重建深度与来源弹窗。
5. **按实例原生终端**（`native.go`、`native_instance.go` 及 `porter` `native --instance <id>`）：保留 `--instance` 直接按 dossier/profile 解析 `native_cli`，去掉 A/B 运行环境守卫。
6. **快照与档案兼容**：`core/vault/` 与 `tools/standalone-restore/restore.py` 永久保持 `valise-snapshot/1` 与 `/2` 读取兼容；保留旧档案只读兼容逻辑（非 A/B 部分）。

### 前端整固 Phase F1（已完成并经用户审批，2026-11-16，`d43f228`）

依据 [UI.md](UI.md) 第 4、7.10/7.11、9.5、11 节与 [DEV_PLAN.md](DEV_PLAN.md) 第 5 项，只改 `client/ui/gui` 与 `client/ui/service` 的一处判断：

1. **全局状态**：`gui/i18n.go` 的当前词表 `words` 改为 `atomic.Pointer[catalog]`（后台 goroutine 调 `T()` 与主线程 `chooseLocale()` 不再竞态）；包级 `confirmations` 收进 `App.confirms`（带锁的实例字段），“现在备份 / 备份一次 / 保存保护计划”仍是一键确认。测试：`gui/concurrency_test.go`（`-race` 下并发 `T()` + `chooseLocale()`；两个 `App` 的确认互不可见、各只生效一次）。
2. **创建副本**：`gui/restoresetup.go` 只在“迁移”时把原服务器排除出目标列表；切到“创建副本”默认选中原服务器，同机时直接展开目录映射输入区并给一句提示（新词条 `restore.same_server_hint`）。服务层随后已在 `9e46fd1`（`service/revive_plan.go` 放开同机副本：须不同实例标识与显式目录、禁止与已接入实例目录重合或嵌套、须声明 `relocation`、不取代原实例且不启停任何服务）与 `65d2eb6`（无受控服务时不显示“复活前会先停止服务”）打通。测试：`restore_test.go` `TestCloneOnTheOnlyServer`、`service/revive_samehost_test.go` `TestCloneOnTheSameServer` 与 `gui/restore_integration_test.go` `TestSameServerCloneFlowCompletesWithoutStoppingService`。
3. **健康页修复的服务器密码**：根因在服务层——`adminTransport` 早已支持 askpass 转交，但 `service/system_fixes_run.go` 的 `adminLoginAvailable` 只看 `NoTerminalLogin`、没看 `canPrompt()`，桌面上没有管理钥匙的密码登录服务器因此拿不到 linger 一键修复（被判成只能手动）。已补 `|| s.canPrompt()`（本阶段唯一的 `service` 改动）。GUI 删除 `fixError` 里“去终端执行 `valise action …`”的退回与 3 个词条 `health.need_password*`；`health.admin_login` 改为同时说明管理口令与服务器密码；UI.md 7.3 一处旧表述同步。
4. **linger 一键开启**：新文件 `gui/healthsysfix.go`：点击后先 `PlanSystemFix`，确认框直接列出命令、改变的系统设置、撤销办法与需要的管理登录；确认后 `RunSystemFix`（摘要绑定计划），结果分别显示每条命令的退出码与复核结论，未通过时可重试；服务层判为不能代为执行时只说明原因并列出命令。时钟修复与“忽略”不在 GUI 里（v0.2）。
5. **验证（均为实际运行）**：`gui/healthfix_test.go` 经 fake-ssh（ssh 替身按 OpenSSH 的方式通过 `SSH_ASKPASS` 询问密码）跑通“无管理钥匙 → 窗口弹出服务器密码 → linger 修复 / 需管理登录的 profile 动作执行”，含取消密码后不执行、可重试；撤掉服务层修复后该测试与 `TestAdminLoginAvailableNeedsAPrompt` 均失败。`go test -race ./gui/...`、`go tool mygo vet`、AGENTS.md 第 6 节 5 步自检全部通过；`gui/*.go` 最大 381 行。**未做**：真实 OpenSSH + sshd 靶机上的 askpass 与 `loginctl`（polkit）行为，fake-ssh 不能代替，留给下面的真机闭环。

### 前端整固 Phase F2（已完成并经用户审批，2026-11-16，`2177be0`）

依据 [UI.md](UI.md) 7.4.3，只改 `client/ui/gui`：

1. **宽度令牌**：`Sizes` 增加 `label_width`（80）、`time_width`（120）、`dialog_width`（460）与 `panel_radius`（10）；旧 `theme.json` 不写时沿用经典值，宽度限 40–960（新词条 `theme.err.widths`）。`fact` 的标签、旅程行的时间改读令牌并可换行；全部 19 个弹窗改用 `dialog(列, 经典宽度)`，实际宽度随 `dialog_width` 按比例缩放。其余页面里仍有写死的列宽（如 `locdetail.go`、`serversettings.go`、`wizardprep.go` 的行标签），本阶段没动。
2. **语义样式函数**：`theme.go` 新增 `subPanel`、`reportPanel`、`codeBox`、`cardTitle`、`sectionTitle`、`dialogTitle`、`dialog`；已替换 `agent.go`、`health.go`、`healthsysfix.go`、`entry.go`、`protect.go`、`installpage.go` 及各弹窗标题里的内联 Padding/Radius/FontSize。其他文件里同样写法的内联面板（约 30 处）留待按页推进。
3. **区块渲染**：`layout.go` 的 `renderSections` + `pageSection`；Agent 页拆成 `header`、`status`、`tabs`、`content`。区块页的布局树只在代码里（`Theme.Pages`），`theme.json` 还不能写。
4. **安装页文本降级**：`tailtext.go` 的 `tailText` 只保留最近 64 KiB（按行、按字符边界截断），截断后界面写明“更早的已省略”（新词条 `install.text_cut`）。云盘登录的文本降级（`cloudlogin.go`）没有改。
5. **`App` 分组**：`app.go` 只留共享部分，其余按业务域内嵌为 `hooks`、`agentData`、`lookView`、`homeView`、`agentView`、`journeyView`、`healthView`、`protectView`、`locationsView`、`serversView`、`keysView`、`restoreView`（`appstate.go`）；清理辅助为 `closePoint`、`closeAdding`、`closeInstall`、`forgetServer`。字段 `journeyView`（0 时间点 / 1 路线）改名 `journeyMode`，给同名分组让路。
6. **验证（均为实际运行）**：新测试 `sections_test.go`、`tailtext_test.go`、`theme_test.go` `TestWidthTokens`。重构前后各生成 50 张界面截图逐字节比较：45 张相同；`journey`、`journey-dark`、`key-settings`、`loc-list` 4 张在同一份代码连跑两次时本身就不相同（动画与时间），不能用来判断；`srv-installed` 起初差 2 像素内边距，已改回，复跑后前后相同。`go test -race ./gui/...`、`go tool mygo vet`、AGENTS.md 第 6 节 5 步自检通过；`gui/*.go` 最大 381 行。**未做**：真机上的观感检查。

### 下一步待办

1. **真机首轮实测修复与口令体验简化（Phase B1 → Phase B2，2026-11-16 用户已在规范层面确认并定稿）**：
   - 用户在真实 `debian-vm` 靶机（`openclaw`）使用 `stage-test-20261116` 包实测发现 5 个阻塞/体验问题，相关规范已同步至 `AGENTS.md`（3、7.1）、`ARCHITECTURE.md`（2.1、2.2、3、3.3、7）、`INSTALL.md`（2、4.1、4.2）、`UI.md`（7.5、7.8、7.9、8、9.3、9.7、13）与 `DEV_PLAN.md`（第 6 项）：
     - **Phase B1（已完成，2026-11-16，单个提交，待用户审批；porter 一侧已在 `debian-vm` 实测；测试包已重打为 `stage-test-20261116-b1`，界面的真机验收由用户进行，尚无结果）**：
       1. **按原设置重新生效**：`gui/protectdesired.go`（从 `protect.go` 拆出 `desired()`）里过滤旧世代计划的副本改用非 nil 空切片，新增 `sayCopies` 在 `desired()` 的生效计划分支与 `applyDesired()` 提交前兜底，请求里始终是 `"server_copies":[]`。测试：`gui/protectdesired_test.go`（副本全属其他服务器 / 为空 / 缺失三种情况下，原样与改过时间的计划都通过 `Validate()`）。
       2. **移除远程备份目标**：
          - `core/vaultrpc` 新增 `target_remove`（`TargetRemoveParams`、能力 `target-remove/1`），`core/rpc/tier.go` 归入主人操作。
          - porter：`protection.Store.WithTargetRemove` 持同一把锁；有未完成的计划事务时不动；目标被**生效中**的计划引用时以 `target_in_use` 拒绝并提示先在保护计划里取消；只被旧世代计划引用（身份核实返回 `identity_conflict`）或无引用时放行；世代判断不了时保守拒绝。`seal.RemoveTarget` 只删 `targets.json` 里的记录与同步状态并写审计，不碰远端文件，也不删云盘登录（那是 `cloud_forget`）；目标已不存在视为完成。
          - 客户端：`service.TargetRemove`；`gui/locverify.go` 的 `removeLocation` 对 `station` 与 `ssh` 先逐台服务器 `target_remove`、再删本机记录（服务器拒绝则整体停下，本机记录不动）；`gui/locdetail.go` 对所有位置开放“移除…”（新词条 `loc.remove_what.ssh`，`loc.remove_what.station` 补一句）。
          - 测试：`porter/internal/protection/targetremove_test.go`、`service/target_remove_test.go`（fake-ssh）、`gui/locremove_test.go`（弹窗 + fake-ssh 全链路：移除后服务器状态里不再出现，被计划引用的保留并显示服务器给的原因）。
          - CLI 与 TUI：`valise target remove <服务器> <名称>`（`station remove` 的提示改为指向它）；TUI 备份位置页按 `x` 移除当前位置（`tui/cloud_remove.go`：确认页写明删什么、留什么；云盘走 `cloud_forget`，其余走 `target_remove`，Valise 管理的位置再删本机记录）。测试：`cmd/valise` `TestCLITargetRemoveAndNativeShellUsage`、`tui/cloud_remove_test.go`（fake-ssh）。
          - **限制**：GUI 与 TUI 都只对本次已读到状态、且状态里有这个位置的服务器调用 `target_remove`（TUI 先当前服务器、再其余已读到的）；这次没读到的服务器上的记录会留着，确认页写明了这一点。旧 porter 没有 `target-remove/1` 时客户端报“尚不支持这项能力”。
       3. **原生界面**：
          - `service/gate.go`：`AdvancedGate` 先只看本机仓库（不建连接）；每个受检实例在本机仓库里都有可恢复的备份时直接放行，不再让服务器列出远程目标（备份偏旧也一样放行，只是照常给出“偏旧”提醒，提醒的措辞改为只说被核对的那个位置，不再说“放在别处的最新备份”）。只有本机仓库不满足时才去列远程目标。测试：`TestGateDoesNotAskCopyLocationsWhenARepositoryHasThePod`（不可达目标 + 本机有备份 → 没有去列目标，备份偏旧时同样）。
          - `native --instance <id> --shell`：`porter/internal/install/native.go` 的 `NativeCommand` 增加 shell 方式，仍要求实例已接入、未被取代且资料声明了原生界面；按 `ForInstance` 的环境把用户自己的 `$SHELL` 作为**登录交互 shell** 启动：POSIX shell（sh/bash/zsh/dash/ksh/ash）先跑登录 profile，再把实例的命令目录放回 PATH 最前（`/etc/profile` 会重置 PATH），然后进入交互；fish 用 `-l -i`；其他 shell 用 `-i`。命令是固定的，不接受请求传入的命令。`NativeInfo` 新增 `shell` 字段由新 porter 置真；`service.InstanceNativeCommand` / `RunInstanceNative` 增加 `NativeMode`，命令行由 `nativeLine` 固定拼出。测试：`porter/internal/install/native_shell_test.go`（各 shell 的固定命令）、`TestInstanceShellCarriesTheInstanceEnvironment`（命令行构建、实例名加引号；fake-ssh 下用真实 `/bin/sh` 跑：登录 profile 之后资料的命令目录仍在 PATH 最前，且是交互模式）。
          - `gui/native.go`：准备页并列“连接原生界面（引导 / 默认命令）”与“打开实例交互终端（Shell）”（后者只在 porter 声明支持时画）；会话结束或断开后页面显示该会话，顶部状态栏给“重新连接”、另一种入口（原生界面结束后是“改用交互终端”，交互终端结束后是“改用原生界面”）与“返回准备页”，两种入口在准备页和结束后的视图里都够得着（需要先备份的情况仍走原来的确认）。测试：`TestNativeEntriesAndReconnectAfterExit`。
          - CLI：`valise native <服务器> --instance <id> --shell`（只能与 `--instance` 同用）。**未验证**：真实 PTY 与真实 shell 的行为。
       4. **已有 Agent 时保留安装入口**：`gui/onserver.go` 的 `installTiles` 在找到 Agent 时改以“安装新的 Agent”区块排在列表下方（新词条 `onserver.more_title` / `onserver.more_sub`；没有可装的 harness 时不画空区块）。测试：`TestAgentsOnServer` 更新。
       5. **验证（均为实际运行）**：AGENTS.md 第 6 节 5 步自检、`go tool mygo vet`、`go test -race ./gui/...` 全部通过；`gui/*.go` 与 `tui/*.go` 没有超过 400 行的文件。
       6. **`debian-vm` 真机实测（2026-11-16，只测 porter 一侧）**：把当前代码编出的 `linux/arm64` porter 放进靶机 `/tmp` 临时目录运行，用完即删，没有部署、没有改动 `~/.local/bin` 与用户的 `targets.json`。
          - 靶机地址已从 LAB.md 记的 `10.211.55.6` 变为 `10.211.55.7`（DHCP）；连接时用原已信任的主机密钥核对身份（`HostKeyAlias`），没有接受新密钥。LAB.md 与 `~/.ssh/config` 没有改，等用户决定。
          - `version --json` 声明了 `target-remove/1`。
          - `target_remove`：在一次性的空 HOME 里 `target_set` 两个位置后移除其一，`targets.json` 只剩另一个；再移除一次返回成功；非法名字返回 `target_name_invalid`；审计写了 1 条。“被生效计划引用时拒绝”没有在真机上测（只在单元与 fake-ssh 测试里）。
          - `native --instance openclaw-test --shell`（真实接入的命名实例，登录 shell 为 `/bin/bash`，用标准输入代替键盘）：`OPENCLAW_STATE_DIR=/home/parallels/.openclaw-test`，`openclaw` 解析到 `~/.openclaw/bin/openclaw`，PATH 最前是 `~/.openclaw/bin` 与 `~/.openclaw/tools/node/bin`，是交互模式，交接用的环境变量没有漏进 shell，退出码 0；多给一个参数时按用法拒绝（退出码 2）。
          - **如实说明**：这个 shell 是“先以登录方式载入 profile，再进入交互 shell”，所以最终的 bash 进程自身不带登录标志（`shopt login_shell` 为 off），`~/.bash_logout` 不会执行；这样做是为了登录 profile 重置 PATH 之后仍能把实例的命令目录放回最前。
          - **没在真机上测的**：GUI/TUI 的任何界面（重新生效、位置移除弹窗、原生界面两个入口与重连、安装入口）、真实 PTY、门槛提速、保护计划对移除的拦截。测试包已重打（见“当前用户构建”），界面由用户在真机上验收。
       7. 注：实测机已有 linger 时如需验证“开启 linger”，可在服务器执行 `sudo loginctl disable-linger $USER` 后在健康页点重新检查复现。
     - **Phase B2（单一 Valise 主口令统管与联动解锁）**：
       1. 默认解锁窗口从 15 分钟延长至 **60 分钟**（设置 1–240 分钟可调）；
       2. `service/gate.go` 对 `repo-trial.json` 中已有试解验证记录的本机仓库，后续新快照只需通过免私钥 `vault.VerifyNoKey` 即可放行，不再反复弹 Valise 口令；
       3. 取消“管理口令必须与 Valise 口令不同”约束：实现单一 Valise 口令同时保护加密钥匙与默认管理钥匙、向导登录页在已设 Valise 口令时默认勾选一并创建管理钥匙、输入一次 Valise 口令同时解锁 `ownerIdentity` 与 `ssh-agent` 中的管理钥匙（向后兼容历史独立管理口令钥匙，并在设置页提供一键同步/重办为 Valise 口令）。
2. **用户真机端到端验收（`65d2eb6` 阶段 A 测试包与此前隔离窗口/fake-ssh 验收基线，2026-11-16）**：测试包在 `/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261116/`（待 Phase B1/B2 完成后重新构建），此前验证记录：
   - **已核对（实际运行）**：四个二进制均带有 `vcs.revision=65d2eb630567a9a2aaf4095076c7c4457cd78480`、`vcs.modified=false`（`d89a953` 时跑过的 Linux 靶机 porter 临时运行与 CLI linger 验证、A/B 清理核查与 `65d2eb6` 重打包的 4 个二进制版本/静态 ELF 产物保持一致，无任何 A/B 入口）；`codesign --verify --deep --strict` 通过；`SHA256SUMS` 复核全部 OK。
   - **真实运行补证（2026-11-16）**：
     - `d89a953` 构建期已跑：两个 porter 在真实 Linux 上从临时目录运行（`ubuntu-vm` ARM64、InstaBox x86_64，用完即删，未部署）：`hello` 握手正常，`environment_*` 均返回 `method not found`（`ubuntu-vm-x86` 当时连不上）；app 指向隔离 fake-ssh 启动运行 5 分钟以上无报错；askpass 转交助手与 CLI linger 修复在 fake-ssh 上跑通。
     - `9e46fd1` / `65d2eb6` 修复后测试与 `b63c764` 收尾：新增 `service` `TestCloneOnTheSameServer` 与 `gui` `TestSameServerCloneFlowCompletesWithoutStoppingService`（覆盖已占 ID/目录冲突拦截无执行按钮、`demo-copy2` 计划不含“会先停止服务”且含“不启动、不停止任何服务”、二次确认后恢复到 `.demo-copy2`、原实例 untouched 且均不标 `superseded`）；`AGENTS.md` 第 6 节全部 5 项检查、`go tool mygo vet` 与 `-race` 集成测试均已通过。
   - **窗口内验收（2026-11-16，打包的 `Valise Test.app` 真实窗口；隔离 fake-ssh 测试世界）**：健康页 linger + 服务器密码弹窗通过、安装页 64 KiB 文本截断通过、同机创建副本（`demo-copy` 走到“文件已恢复，未启动程序”与 `demo-copy2` 冲突拦截/计划生成）通过。
   - **此前顺带看到的待微调 UI 文案（非阻塞）**：取消密码后服务层错误文字仍含 CLI `valise server rekey` / 终端界面提示；本地未审核资料卡片写“官方安装方案”；服务器页标签折行；后台窗口切换不重绘。
   - **没做**：真实 `loginctl enable-linger`（靶机已预先配置 linger）；上一级目录 `valise-porter-test/` 里的同名 porter 仍是 11-14 的版本，没有覆盖。
3. **合并与后续演进**：Phase B1/B2 落地并重打测试包完成真机全链路闭环后，将分支 `refactor/revoke-ab-slots-and-doc-audit` 整理合并，并按 `docs/DEV_PLAN.md` 推进 MVP `v0.1` 收口与 5 项用户痛点规划。

### 必须保留的环境与基线事实

- **当前用户构建**：`/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261116-b1/Valise Test.app`（`0.0.0-dev+stage-test-20261116-b1`，构建自 Phase B1 提交，哈希见同目录 `BUILD.json`；2026-11-16 用户决定由自己验收）。验收步骤在同目录 `TESTING.md`：先在服务器设置里把靶机 porter 更新为同目录的 `valise-porter_linux_arm64`（`debian-vm`；x86 用 `_amd64`），再依次验收 B1 四项。上一个包 `stage-test-20261116`（`65d2eb6`）原样保留，不含 B1。
- **默认资料包与演练公钥**：默认资料包为 `2026.11.10`（OpenClaw 22、Hermes 11）；默认配置公钥 `~/.cache/valise-drill/devpack/key.pub`，演练配置公钥 `~/.cache/valise-drill/drillpack/key.pub`，不得混用或覆盖。
- **构建环境**：macOS 上始终执行 `export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3`；禁止执行 `rm -f`（用 `git rm` 或 `t.TempDir()`）；禁止自动 `git push`。
- **原基线**：分叉前分支 `n2-agent-protection-journey` 位于 `f243bcf`（含 `07d7952`、`4bdd0fb`、`3e9f124`、`4d890c2`、`f243bcf`）；工作树中的 `references/` 与 `.DS_Store` 保留不提交。

---

## 1. 历史阶段摘要（N1–N3 已验收，N4 进行中）

| 阶段 | 状态 | 说明与记录位置 |
| :--- | :--- | :--- |
| N1 规范与服务契约 | `Done`（2026-11-05） | [SERVICE_CONTRACTS.md](SERVICE_CONTRACTS.md) 第 1–16 节 |
| N2 汇总、保护计划、时间轴与 B03 | `Done`（2026-11-07） | [acceptance/N2.md](acceptance/N2.md)、[acceptance/B03.md](acceptance/B03.md) |
| N3 严格副本、云盘与失联恢复 | `Done`（2026-11-08） | [acceptance/N3.md](acceptance/N3.md) |
| N4 mygo GUI 完整流程与 TUI 对齐 | `In progress`（架构减负中） | [acceptance/N4.md](acceptance/N4.md) |
| N5 管理方自动化、自定义与扩展 | `Planned` | [DEV_PLAN.md](DEV_PLAN.md) |
