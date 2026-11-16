# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-16。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前工作与交接（2026-11-16 Phase B1 `cc13a31` 已完成真机反馈收拢，规范已同步为 Phase B1.1 与修订后的 Phase B2）

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

1. **真机首轮实测修复与口令体验简化（Phase B1 已完成，接下来 Phase B1.1 → Phase B2，2026-11-16 规范已定稿）**：
   - 用户在真实 `debian-vm` 靶机（`openclaw`）使用 `stage-test-20261116-b1`（`cc13a31`）验收后，确认“按原设置重新生效”通过，同时发现 4 项阻断/体验问题及 1 项 SSH 口令通道遗漏，相关规范已更新至 `AGENTS.md`（3、7.1）、`ARCHITECTURE.md`（2.1、2.2、3、3.1、3.3）、`PROFILES.md`（第 2 节 `native_session_cli`）、`INSTALL.md`（2、3、7）、`UI.md`（7.5、7.9、8、9.5、9.6、9.7）与 `DEV_PLAN.md`（第 6 项）：
     - **Phase B1（已完成，2026-11-16，`cc13a31`；实测包 `stage-test-20261116-b1`；“按原设置重新生效”真机通过）**：
       1. **按原设置重新生效**：`gui/protectdesired.go` 非 nil 空切片 + `sayCopies` 兜底。真机验收通过。
       2. **移除远程备份目标**：`core/vaultrpc` `target_remove` + `porter` `WithTargetRemove`/`RemoveTarget` + 客户端移除入口（待 B1.1 补充全量阻碍汇总与一键清理计划）。
       3. **原生界面**：`gate.go` 本地库优先 + `native --instance <id> --shell` + `gui/native.go` 断联重连（待 B1.1 拆分为“原生会话 / 实例交互终端 / 原生引导”与引导前可选停服）。
       4. **已有 Agent 时保留安装入口**：`gui/onserver.go` `installTiles` 在已有 Agent 下方展示“安装新的 Agent”（待 B1.1 解决上游脚本更新 SHA-256 变更死锁与自定义粘贴指令）。
       5. **真机记录说明**：`debian-vm` 地址变更为 `10.211.55.7`（DHCP），未改动 `LAB.md` 表格基线；`linger` 在真机 `debian-vm` 实测暴露无 sudo 退 1 且 GUI 无高级模式入口，纳入 B1.1 修复。
     - **Phase B1.1（下一开发 Goal：B1 真机测试反馈 5 项收口，优先级高于 B2）**：
       1. **移除备份位置一次性列出全部阻碍并一键解除**：`WithTargetRemove` 返回全部引用该位置的活动实例 ID；客户端跨所有已读服务器汇总全部阻碍的 `服务器 / Agent`，并在移除弹窗提供“一键从受影响保护计划中移除此位置并继续删除”。
       2. **原生界面正名拆分与引导停服选项**：`PROFILES.md` 新增可选 `native_session_cli`（`openclaw.yaml` 补配 `[openclaw, tui]` 并实机验证）；GUI 将 `native_cli`（`openclaw onboard`）改名为“原生引导”，并列“原生会话”“实例交互终端”作为主入口；若受管后台服务正在运行，进原生引导前提示独占状态冲突并提供“暂停后台服务进入引导（退出后可一键恢复启动服务）”。
       3. **安装脚本上游更新与粘贴官方指令**：profile 的 `script` 实际 SHA-256 与记录不符时不再死锁，改为 `install.CheckCommand` 静态疑点检查 + 展示新旧哈希与完整脚本预览，未命中禁令规则时允许用户确认继续；同时提前落地 `INSTALL.md` 第 7 节“粘贴官方安装地址或指令”（客户端解析 URL 并本地下载、计算哈希、静态检查、展示全文确认后再上传暂存目录执行，绝不在服务器跑 `curl | bash`）。
       4. **`linger` 修复失败后支持 GUI 内使用 `sudo` 重试**：保持默认先无 `sudo` 执行 `loginctl enable-linger <user>`；仅当无 sudo 失败（退出码非 0 且问题仍在）时，结果卡片直接提供“使用 sudo 重试”按钮，经 askpass 或内嵌终端完成提权修复。
       5. **补齐 `~/.ssh/config` 导入服务器 `userTransport` 私钥口令/密码 GUI 弹窗**：修复 `service/access.go` 中 `userTransport` 在 GUI 可弹窗（`canPrompt`）时关闭 `BatchMode` 并挂载 `relayEnv()`（`SSH_ASKPASS`），并用 `ssh-keygen -N` 真实加密私钥测试验证弹窗可正常唤起。
     - **Phase B2（延后与修订：单一 Valise 主口令统管 + 30 分钟免输/本机 6 位 PIN 码快捷解锁）**：
       1. 未启用快捷解锁时，默认解锁时间窗口设为 **30 分钟**（设置 1–240 分钟可调）；
       2. `service/gate.go` 对 `repo-trial.json` 中已有试解验证记录的本机仓库，后续新快照只需通过免私钥 `vault.VerifyNoKey` 即可放行，不再反复弹 Valise 口令；
       3. 取消“管理口令必须与 Valise 口令不同”约束：实现单一 Valise 口令同时保护加密钥匙与默认管理钥匙、向导登录页在已设 Valise 口令时默认勾选一并创建管理钥匙、输入一次 Valise 口令同时解锁 `ownerIdentity` 与 `ssh-agent` 中的管理钥匙（向后兼容历史独立管理口令钥匙，并在设置页提供一键同步/重办为 Valise 口令）；
       4. 新增可选**本机 6 位 PIN 码**快捷解锁（默认关闭）：客户端用随机盐 + `age/scrypt` 保存 `pin-unlock.age`，启用后**每次触发主人/管理操作直接弹出 6 位 PIN 验证**，不再保留长免输窗口，连续输错 5 次立即销毁包裹退回完整 Valise 口令；系统钥匙串/Touch ID/Windows Hello 与 1Password CLI (`op`) 留待后续迭代。
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
