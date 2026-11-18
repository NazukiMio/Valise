# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-18。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. Phase B2（2026-11-18）

基线为 `07a2d33`，当前分支为 `design/gui-polish-phase2`。统一 Valise 口令、30 分钟默认免输（1–240 分钟）、管理钥匙联动解锁和改密、本机 6 位 PIN，以及本机副本 Gate 的免重复试解已实现。旧独立管理口令兼容，桌面添加、迁移和重办默认采用完整 Valise 口令。

验收包路径为 `/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261118-b2/`。自动检查覆盖范围见 [N4 验收记录第 7 节](acceptance/N4.md#7-phase-b2-客户端解锁与本机副本2026-11-18)，真机操作按包内 `TESTING.md` 复核。旅程虚拟化与备份退役仍见 0b 节。

## 0a. 界面整理第二阶段（2026-11-17，分支 `design/gui-polish-phase2`，已完成）

依据第一阶段只读审计（[acceptance/VALISE_DESIGN_AUDIT.html](acceptance/VALISE_DESIGN_AUDIT.html)）与用户授权（“你自己决定”），先改规范再改代码。本轮只动 `client/ui/gui` 与文档，不改 `client/ui/service`。

- **规范**：PRODUCT.md 8.1 改名“原地恢复”“创建分身”“搬运工”，统一“时间点”“同步”；UI.md 7.4.2 新增颜色名 `danger` `danger_bg` `danger_border`，新增第 15 节；新增 [DESIGN.md](DESIGN.md)（审美、交互与文案工程指南）；AGENTS.md 按需阅读表加一行。
- **已做（`client/ui` 的 `go test ./...`、`gofmt`、`go vet`、`go tool mygo vet` 通过；首页、Agent 页、服务器页用 `ui.Tester.Image()` 无窗口渲染看过）**：
  - `gui/scale.go` 尺度令牌，`gui/widgets.go` 公共组件（`pressable`、`pressableCard`、`over`、`notice`、`statusRow`、`toneDot`、`dangerButton`、`quietButton`、`backLink`、`chevron`）。
  - 诚实状态：恢复计划的警告改用 `notice` 并提到计划之前；健康页“严重”用 `danger`，一屏只有第一个修复是主按钮；删除周期、注销通行证、关闭备用锁、删除并清理用 `dangerButton`；错误框标题用 `danger`；“明确不在运行”画成空心点。
  - 对比度：浅色主题次要文字 `#6f685f`（纸面 5.09:1）、品牌色 `#86683c`；最小字号 12。
  - 悬停改为只重绘（`over`），构建期读取只剩 `textLink` 一处（有注释）。
  - 首页结论句正常时退后、数字滚动；提醒卡片进出有过渡；Agent 概览先状态牌，最近时间点整行可点。
  - 词表约 130 条改写，测试断言同步更新。
- **第二批（同一分支，用户授权全权裁量，2026-11-17）**：
  - 外壳：五种顶部横幅（找回模式、命令行连接、恢复进行中或待核实、备份测试、主题与词表警告）统一为 `gui/banners.go` 的 `banner`，有 `busy`/`warn` 语气和进出过渡，按钮靠右不再撑满；浅色侧栏的选中项改为卡片底色（原来几乎看不见）。
  - 健康页（`gui/healthlist.go`）：需要处理的排前，修复的进度与结果放在对应那条下面；两条以上正常项收成“N 项正常”一行，可展开。
  - 恢复流程（`gui/restoresteps.go`）：五步步骤条；时间点摘要改为一条横条；结果改为逐项带标记的行（文件、目录替换、服务、健康、旅程关系各自一行），回退问题与待核实提示用 `notice`。
  - 命令行与向导：“日常使用”改为两张选择卡（对话 / 服务器命令行），主按钮只留给首次设置向导；返回改为 `backLink`。
  - 口令框：顶部标签条写明是哪种密码、交给谁核对（`askKind`）。
  - 材质与重量级主题（UI.md 7.4.4）：`gui/material.go` 提供 `flat`/`journal`/`night` 三种材质，经 `card`、`pageHead`、窗口背景生效；新增内置主题“手账”（纸纤维与点阵，GPU 着色器，静止）与“夜航”（星空，闪烁每秒 12 帧，减少动态效果时静止），经典主题保持原样。主题文件暂不能选材质。
  - 词表：恢复、备份测试、命令行页约 26 条去掉“尚未 / 进行 / 此 / 该”；新增 `health.fine`、`restore.stage.*`、`ask.dest.*`、`theme.journal`、`theme.night`。
  - 验证：新增 `designshots_test.go`（`VALISE_SHOTS=<目录> go test ./gui -run TestDesignShots`，经典深色、手账、夜航各渲染首页、健康页、旅程页，外加三种口令框）；渲染图逐张看过。
- **第三批（用户实机体验后交互精修，2026-11-17）**：
  - 沉浸式标题栏：`cmd/valise-desktop/main.go` 启用 `mygo.TitleBarHidden`，`gui/shell.go`、`gui/layout.go` 随主题背景延伸到标题栏、为控制按钮留出安全区并支持 `.DragWindow()` 拖拽；全局横幅移入页面区顶部以免遮挡红绿灯与全高侧栏。
  - 导航点击回分类主页：`gui/shell.go` 中身处子页面点击所属一级导航项（`我的 Agent`/`服务器`/`备份位置`/`设置`）时直接回到分类根页面并清理子页面状态。
  - 旅程曲线横向滚动：`gui/curve.go` 取消 6 点截断，改为 `ui.ScrollHorizontal` 横向展示全部时间点（打开时默认定位在最右侧最新时间点，增量扫入时保持贴右并保留用户向左回翻位置）；波形与发丝按定长周期自然衔接。
  - 列表卡片圆角悬停修复：`gui/servers.go`、`gui/homeagents.go`、`gui/healthlist.go` 改用内边距 + `radPanel` 传给 `pressable`（`fineItems` 增 `.Clip()`），消除圆角外框内悬浮出直角矩形的问题。
  - 异步防跳动：`gui/home.go`、`gui/health.go`、`gui/journeylocations.go` 刷新按钮保持原地占位并禁用加小转圈；`gui/entry.go` 隧道步骤改为水平换行展示；`gui/journey.go` 将失败位置折叠移至曲线之后，避免滞后超时把页面按钮向下推走。
  - 验证：`client/ui` 全包 `go test -count=1 ./...`（`cmd/valise` 6.778s、`gui` 113.685s、`service` 340.211s、`tui` 183.585s）、`go vet ./...`、`gofmt` 与 `go tool mygo vet` 全过，并重打 `stage-test-20261117-ui` 待实机复核。
- **没做，留给下一轮**：存量视图的魔法数字只迁了改到的地方；其余页面（安装、备份位置、设置）没有重排；主题文件的 `material` 字段与 `valise-theme/2`；TUI 与 CLI 的同名术语（用户 2026-11-17 决定本轮不做）。
- **已定，后续独立阶段处理**：管理员通行证界面仍要求另设“管理口令”，与 AGENTS.md 第 3 节“默认用 Valise 口令”不一致；用户 2026-11-17 确认本分支不改，留给后续安全阶段。
- **实机前端验收（2026-11-18）**：用户已在真实桌面窗口使用 `stage-test-20261117-ui`（`23bdcc8`）完成前两轮界面整理及第 3 批体验优化验收，前端功能通过；实测暴露“备份数量很多时旅程页动效全量渲染卡顿”及对应的“备份退役机制”设计缺口，已纳入下方 `0b` 节交由新会话推进。

### 0b. 2026-11-18 用户拍板的下一阶段任务（新会话优先接续）

1. **旅程时间线「可视区域虚拟化（Viewport Virtualization）+ 滑入淡入」**（仅涉及 `client/ui/gui/`）：
   - **O(1) 视口裁剪绘制**（`gui/curve.go`）：依据 `a.curveScroll.X` 与外框宽度计算当前可见像素区间 `[viewLeft, viewRight]`，`paintHairs` 与正弦主脊仅对可见区间采样并绘制固定数量发丝，避免大量快照时 CPU 路径每 33ms 全宽重绘卡顿。
   - **卡片节点虚拟化 + 滑入淡入**：仅实例化落在可见区间 `[iMin, iMax]`（含左右各 1 个缓冲位）内的 `curveCard`，两侧由定宽占位块撑开滚动总宽；新进入视口的卡片配合 `ui.ElementTransition{Duration: motionBase, Enter: &ui.Motion{}}` 淡入，外框左右边缘可加约 24px `MaskLinearGradient` 羽化遮罩；下方超长月份列表改用 `ui.List` 虚拟列表。
2. **备份退役与双轨制架构：严格区分「自动快照（Auto Snapshot）」与「手动存档点（Savepoint）」**（需先修订 `VAULT_FORMAT.md`、`ARCHITECTURE.md`、`PRODUCT.md`、`UI.md`、`SERVICE_CONTRACTS.md` 后跨 `core/` / `porter/` / `client/ui/` 实现）：
   - **自动快照（Auto Snapshot）**：定时任务生成，定位为“近期快速回溯后悔药”。物理写入滚动短周期叶子（如 `auto/<start>_<end>/`），支持服务器上按最近 N 天 / M 个周期滚动退役清理，防止密集全量备份占满磁盘。
   - **手动存档点（Savepoint / 里程碑存档）**：由用户显式发起（可带备注）或在关键变更前留存，物理写入独立叶子（如 `savepoints/<id>/` 或 `pinned/`），**永不自动退役**；仍保持 100% 标准 `valise-vault/1` 格式，随时可脱离 Valise 用 `tools/standalone-restore/restore.py` 恢复。
   - **一键晋升（钉为存档点）**：支持在旅程中将任意一次有价值的「自动快照」转为永久「手动存档点」（免于滚动退役）。
   - **旅程区分展示**：存档点加醒目标记与备注，自动快照作次要/可折叠点并支持“仅看存档点”过滤。
   - 注：落地前需与 `VAULT_FORMAT.md` 10.4“只追加周期与不提供自动保留策略”旧决议做一次正式文档修订。
3. **后端已知问题**：等用户提供此前实测发现的问题清单后逐项复现和修复。

## 0c. 架构收敛、前端整固与 Phase B1 实施记录

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

1. **真机首轮与二轮实测修复（Phase B1、B1.1、B1.2，2026-11-16/17）**：
   - 用户在真实 `debian-vm` 靶机（`openclaw`）使用 `stage-test-20261116-b1`（`cc13a31`）与 `stage-test-20261116-b1.1`（`f328c4a`）验收后，相关规范已更新至 `AGENTS.md`（3、7.1、7.7）、`ARCHITECTURE.md`（2.1、2.2、3、3.1、3.3）、`PROFILES.md`（第 2 节 `native_session_cli`）、`INSTALL.md`（2、3、7）、`UI.md`（6、7.5、7.9、8、9.5、9.6、9.7、13、14）与 `DEV_PLAN.md`（第 6 项）：
     - **Phase B1（已完成，2026-11-16，`cc13a31`；实测包 `stage-test-20261116-b1`；“按原设置重新生效”真机通过）**：
       1. **按原设置重新生效**：`gui/protectdesired.go` 非 nil 空切片 + `sayCopies` 兜底。真机验收通过。
       2. **移除远程备份目标**：`core/vaultrpc` `target_remove` + `porter` `WithTargetRemove`/`RemoveTarget` + 客户端移除入口（B1.1 已补充全量阻碍汇总与一键清理计划）。
       3. **原生界面**：`gate.go` 本地库优先 + `native --instance <id> --shell` + `gui/native.go` 断联重连（B1.1 拆分为“原生会话 / 实例交互终端 / 原生引导”与引导前可选停服；B1.2 正名为「命令行与向导」并解决进退卡顿）。
       4. **已有 Agent 时保留安装入口**：`gui/onserver.go` `installTiles` 在已有 Agent 下方展示“安装新的 Agent”（B1.1 已解决上游脚本更新 SHA-256 变更死锁与自定义粘贴指令）。
       5. **真机记录说明**：`debian-vm` 地址变更为 `10.211.55.7`（DHCP），未改动 `LAB.md` 表格基线；`linger` 在真机 `debian-vm` 实测暴露无 sudo 退 1 且 GUI 无高级模式入口，已纳入 B1.1/B1.2 修复。
     - **Phase B1.1（已完成，2026-11-16，`f328c4a`；测试包 `stage-test-20261116-b1.1`）**：
       1. **移除备份位置一次性列出全部阻碍并一键解除**：`porter/internal/protection/reconcile.go` (`WithTargetRemove`) 收集全部生效的阻碍实例 ID 并返回 `target_in_use`；`client/ui/service/target_remove.go` (`ScanTargetRemoval` / `UnblockTarget`) 跨全部可读服务器汇总阻碍并依次移除 `ServerCopies`；`client/ui/gui/locremove.go` 提供「从上述 Agent 的保护计划中移除此位置并继续删除」，任一步失败立即停下如实报错、不删本地记录。
       2. **原生界面正名拆分与引导停服选项**：`core/profile/` 新增可选 `native_session_cli`；实测 `debian-vm`（`10.211.55.7`）`openclaw 2026.11.4` 确认 `openclaw tui --help` 后将 `profiles/openclaw.yaml` 升至 v24（实时交互会话仍 `verify: pending`）；`porter/internal/install/native.go`、`cmd/valise`、`service/native_instance.go`、`gui/native*.go` 落地 `--session`、引导前暂停服务与退出后一键恢复启动后台服务。
       3. **安装脚本上游更新容错与粘贴官方安装地址/指令**：`core/install/`（`check.go`、`script.go`、`custom.go`）与 `service/install*.go`、`gui/install*.go` 在 SHA-256 变更时静态检查并展示脚本全文与新旧哈希供确认放行（高危/混淆直接拦截），落地客户端本地下载并预览的自定义官方安装入口。
       4. **`linger` 修复失败后 GUI 支持「使用 sudo 重试」**：`service/system_fixes_run.go` 与 `gui/healthsysfix.go` 支持 `FixLinger` 带 `SystemFixOptions{Sudo: true}` 执行 `sudo -S loginctl enable-linger <user>` 并立即复核刷新。
       5. **补齐 `~/.ssh/config` 导入服务器 `userTransport` 私钥口令/密码 GUI 弹窗**：`service/access.go` `userTransport` 接入 `canPrompt()` 与 `relayEnv()`，真实 `/usr/bin/ssh` + `ssh-keygen -N` + `127.0.0.1` 服务端测试通过。
     - **Phase B1.2（已完成，2026-11-17；测试包 `stage-test-20261116-b1.2` 待用户真机验收）**：
       1. **新增 Debug 诊断日志模式（开发版默认开启，正式版默认关闭）**：`client/config/config.go` 新增 `DebugLog *bool`（`config.SetDebugLog`）；`client/ui/service/debuglog.go` 写入 `<Dirs.Data>/logs/valise-debug.log`（超过 5 MiB 自动轮转 `.1`，优先级 `VALISE_DEBUG=1/0` > `Config.DebugLog` > `Version` 含 `dev`/`test` 默认开、正式版默认关），记录时间戳、GUI 路由切换（`gui/shell.go`）、关键按钮（`gui/settingsdebug.go`、`native.go`、`locations.go`、`locsync.go`、`healthsysfix.go`）、Service RPC（`access.go`、`owner.go`，仅记方法/主机/耗时/安全错误码）、SSH 子进程起止（`client/ssh/transport.go` & `rpcclient.go` 的 `Mode` 与 `ProcessHook`）、脱敏 `SSH_ASKPASS` 提问分类/提示模版（`askpass_relay.go` `ClassifyAskpassPrompt`，绝不记录口令与私钥路径）以及备份/恢复/恢复测试/系统修复阶段流转（`backup.go`、`backup_facade.go`、`restore_task.go`、`restoretest.go`、`system_fixes_run.go`）。设置页 `gui/settingsdebug.go` 提供开关、「打开日志目录」与「复制最近诊断日志」；并修复 `askpass_relay_test.go` 原有 `-race` 竞态及 `Service.Close()` 关闭日志写入（`service/agent.go`）。单测 `debuglog_test.go` 与 `settingsdebug_test.go` 校验敏感密码/私钥路径/Token 零泄漏与 5 MiB 轮转。
       2. **修复进入/退出原生界面卡顿，并正名为「命令行与向导」**：`docs/UI.md`、`service/native_instance.go`、`gui/native.go` 与 `zh-CN.json` 正名为「命令行与向导」（入口「打开命令行与向导…」，三项子功能「Agent 终端会话（TUI）」「系统命令行（Shell）」「初始化配置向导（Onboard）」）。`service/gate.go` (`targetPlaces`) 跳过未同步目标（`LastSynced` 为空），对有同步记录的目标并发执行 `TargetList` 并施以 3 秒独立超时（超时立即跳过而不阻塞后续弹窗/启动）；同步加固 `client/ssh/rpcclient.go`（`Setpgid` 与已取消会话快速回收/进程组强杀）和 `service/station.go`（`localRunner` 进程组强杀）。`service/native_instance.go` 为交互终端配置 `Setpgid` + `Cancel` 强杀进程组与异步 `closeMasterAsync`；`gui/native.go` 与 `gui/native_service.go` 点击「结束连接」立即切回已断开态，退出后刷新移至 `5s` 超时后台 `refreshServices` 不阻塞页面。单测/集成测试覆盖 `TestGateSkipsHangingRemoteTargetWithinTimeout`、`TestNativeCommandSkipsHangingRemoteTargetGateAndCancelKillsProcessGroup`、`TestNativeDisconnectDoesNotStallUI`。
       3. **修复备份卡在离线 `nas` 时点「停止」无效**：`porter/internal/enroll/env.go` (`Env.Run`) 配置独立进程组 `Setpgid: true`、`Cancel` 强杀 `-pid` 与 `WaitDelay`；`porter/internal/backuprun/execute.go` 在 `StageCopying` 执行 `seal.Sync` 时派生 `copyCtx` 并启动每 200ms 轮询 `runID` 取消标记的协程（零数据竞态、结束即回收），检测到停止立即 `cancelCopy()` 中断 `rsync`/`sftp`/`ssh` 进程组，将该组件标为 `cancelled`、整体标为 `cancelled`（保留服务器本地快照与绑定）并落盘返回。`porter/internal/backuprun/cancel_test.go` 覆盖挂起目标 `<1s` 中断、真实卡死子进程组强杀与无 goroutine 泄漏。
       4. **修复 `linger` 免密 sudo 弹窗混淆与空输入拦截**：`gui/adminpass.go` 与 `zh-CN.json` 清晰区分「建立 SSH 管理连接（需要输入该服务器的登录密码或私钥口令）」与「sudo 提权密码」，并仅对 `askServer` 放行回车/确定提交空输入（保留 `askAdmin`/`askOwner` 非空校验）；`service/system_fixes_run.go` 确认 `sudo -n true` 通过时不弹 sudo 框，若失败发生在 `adminTransport` / `porter.Probe`（非 `porter_not_found`/`porter_unusable`）或 SSH 传输阶段则返回明确前缀 `SSH 管理登录未通过（非 sudo 失败）`。新增 `TestLingerFixSudoRetryNopasswdAndAdminLoginError`、`TestPassDialogEmptyAllowedOnlyForAskServer`、`TestHealthLingerNopasswdSudoRetryInWindow`。
       5. **备份位置卡片整卡可点击**：`gui/locations.go` 的 `locCard` 整卡可点击进入 `/locations/<name>`（`CursorPointer` + 悬停高亮边框 `palette.Brand`），同时保持右上角「同步」/「现在补齐」等子按钮独立点击不误跳，并对 `readLocations` 并发写入 `a.update` 加锁消除了 headless 测试竞态。新增 `TestLocationCardClickAndSyncButton`。
       6. **验证与真机边界**：本轮修改全部完成后，全仓跑通 `./testbed/selfcheck.sh`（`SELFCHECK_PASS`：5 步全过，含 `profilelint` 3 份资料 OK、`porter` 22 包及 `linux/arm64`/`linux/amd64` 静态交叉编译、`client`、`testbed`、`client/ui`、`go tool mygo vet`、`gofmt` 全净）、`porter ./internal/backuprun/... -race`、`client/ui/service` 定向 `-race`（`TestScanAndUnblock|TestTarget|TestNative|TestGate|TestUserTransport|TestLinger|TestDebug`）与 `client/ui/gui/...` 全包 `-race`；`gui/*.go` 最大 370 行、`tui/*.go` 最大 395 行（全 `<= 400`）。**未做**：`stage-test-20261116-b1.2` 的 macOS 真机窗口交互（含设置页 Debug 日志切换/打开/复制、命令行与向导断开离线目标、离线 `nas` 备份点击停止、免密 `debian-vm` `linger` 与位置卡片点击）未在真机实操，待用户使用本测试包现场验收。
2. **用户真机端到端验收（`65d2eb6` 阶段 A 测试包与此前隔离窗口/fake-ssh 验收基线，2026-11-16）**：测试包在 `/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261116/`，此前验证记录：
   - **已核对（实际运行）**：四个二进制均带有 `vcs.revision=65d2eb630567a9a2aaf4095076c7c4457cd78480`、`vcs.modified=false`（`d89a953` 时跑过的 Linux 靶机 porter 临时运行与 CLI linger 验证、A/B 清理核查与 `65d2eb6` 重打包的 4 个二进制版本/静态 ELF 产物保持一致，无任何 A/B 入口）；`codesign --verify --deep --strict` 通过；`SHA256SUMS` 复核全部 OK。
   - **真实运行补证（2026-11-16）**：
     - `d89a953` 构建期已跑：两个 porter 在真实 Linux 上从临时目录运行（`ubuntu-vm` ARM64、InstaBox x86_64，用完即删，未部署）：`hello` 握手正常，`environment_*` 均返回 `method not found`（`ubuntu-vm-x86` 当时连不上）；app 指向隔离 fake-ssh 启动运行 5 分钟以上无报错；askpass 转交助手与 CLI linger 修复在 fake-ssh 上跑通。
     - `9e46fd1` / `65d2eb6` 修复后测试与 `b63c764` 收尾：新增 `service` `TestCloneOnTheSameServer` 与 `gui` `TestSameServerCloneFlowCompletesWithoutStoppingService`（覆盖已占 ID/目录冲突拦截无执行按钮、`demo-copy2` 计划不含“会先停止服务”且含“不启动、不停止任何服务”、二次确认后恢复到 `.demo-copy2`、原实例 untouched 且均不标 `superseded`）；`AGENTS.md` 第 6 节全部 5 项检查、`go tool mygo vet` 与 `-race` 集成测试均已通过。
   - **窗口内验收（2026-11-16，打包的 `Valise Test.app` 真实窗口；隔离 fake-ssh 测试世界）**：健康页 linger + 服务器密码弹窗通过、安装页 64 KiB 文本截断通过、同机创建副本（`demo-copy` 走到“文件已恢复，未启动程序”与 `demo-copy2` 冲突拦截/计划生成）通过。
   - **此前顺带看到的待微调 UI 文案（非阻塞）**：取消密码后服务层错误文字仍含 CLI `valise server rekey` / 终端界面提示；本地未审核资料卡片写“官方安装方案”；服务器页标签折行；后台窗口切换不重绘。
   - **没做**：真实 `loginctl enable-linger`（靶机已预先配置 linger）；上一级目录 `valise-porter-test/` 里的同名 porter 仍是 11-14 的版本，没有覆盖。
3. **合并与后续演进**：Phase B1/B2 落地并重打测试包完成真机全链路闭环后，将分支 `refactor/revoke-ab-slots-and-doc-audit` 整理合并，并按 `docs/DEV_PLAN.md` 推进 MVP `v0.1` 收口与 5 项用户痛点规划。

### 必须保留的环境与基线事实

- **Phase B1.2 历史测试包**：`/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261116-b1.2/Valise Test.app`（`0.0.0-dev+stage-test-20261116-b1.2`，构建自 Phase B1.2 提交，具体 commit 与 SHA-256 见同目录 `BUILD.json` 与 `SHA256SUMS`）。验收步骤在同目录 `TESTING.md`：先在服务器设置里把靶机 porter 更新为同目录的 `valise-porter_linux_arm64`（`debian-vm`；x86 用 `_amd64`），再依次验收 B1.2 五项与既有 B1.1 回归项。先前测试包 `stage-test-20261116`（`65d2eb6`）、`stage-test-20261116-b1`（`cc13a31`）与 `stage-test-20261116-b1.1`（`f328c4a`）均原样保留。
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
