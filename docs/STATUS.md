# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-16。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前工作与交接（2026-11-16 v0.1 测试包已从 `65d2eb6` 重打完成；同机创建副本已打通并补齐端到端集成测试，待真实 Linux 靶机闭环）

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

1. **用户真机端到端验收（阶段 A 测试包已从 `65d2eb6` 整包重打并完成窗口/fake-ssh 验收，2026-11-16）**：测试包在 `/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261116/`，最新包构建自 `65d2eb6`（包含 `2177be0`、`d89a953`、`9e46fd1` 同机创建副本支持与 `65d2eb6` 计划提示修正），内含 `Valise Test.app`、CLI `valise`、`valise-porter_linux_arm64` / `_amd64`、`standalone-restore/`、`SHA256SUMS`、`BUILD.json` 与验收步骤 `TESTING.md`。
   - **打包方式**（没有 `mygo.json`，沿用 11-14 包的做法）：`go build -trimpath -ldflags "-X main.version=… -X valise/client/porter.ReleasePublicKey=$(cat ~/.cache/valise-drill/devpack/key.pub)"` 编出桌面程序与 CLI，放进 `.app` 骨架（`Info.plist`、`PkgInfo`、mygo 终端插件声明的 `libghostty-vt.dylib`，SHA-256 与插件清单一致），整个 `.app` 做 ad hoc 签名；porter 用 `CGO_ENABLED=0` 交叉编译。没有重新生成资料包或钥匙。
   - **已核对（实际运行）**：四个二进制均带有 `vcs.revision=65d2eb630567a9a2aaf4095076c7c4457cd78480`、`vcs.modified=false`（`d89a953` 时跑过的 Linux 靶机 porter 临时运行与 CLI linger 验证、A/B 清理核查与 `65d2eb6` 重打包的 4 个二进制版本/静态 ELF 产物保持一致，无任何 A/B 入口）；`codesign --verify --deep --strict` 通过；`SHA256SUMS` 复核全部 OK。
   - **真实运行补证（2026-11-16）**：
     - `d89a953` 构建期已跑：两个 porter 在真实 Linux 上从临时目录运行（`ubuntu-vm` ARM64、InstaBox x86_64，用完即删，未部署）：`hello` 握手正常，`environment_*` 均返回 `method not found`（`ubuntu-vm-x86` 当时连不上）；app 指向隔离 fake-ssh 启动运行 5 分钟以上无报错；askpass 转交助手与 CLI linger 修复在 fake-ssh 上跑通。
     - `9e46fd1` / `65d2eb6` 修复后测试与本次收尾：新增 `service` `TestCloneOnTheSameServer` 与 `gui` `TestSameServerCloneFlowCompletesWithoutStoppingService`（覆盖已占 ID/目录冲突拦截无执行按钮、`demo-copy2` 计划不含“会先停止服务”且含“不启动、不停止任何服务”、二次确认后恢复到 `.demo-copy2`、原实例 untouched 且均不标 `superseded`）；`AGENTS.md` 第 6 节全部 5 项检查、`go tool mygo vet` 与 `-race` 集成测试均已通过。
   - **窗口内验收（2026-11-16，打包的 `Valise Test.app` 真实窗口；数据目录是隔离的 fake-ssh 测试世界：一台 Valise 接入、无管理钥匙、密码登录的服务器）**：
     - **健康页 linger + 服务器密码弹窗：通过。**“开启 linger”→ 确认框列出命令、改变的系统设置、撤销办法与“要输入服务器密码”，此时服务器上没有执行任何命令 → “执行”后窗口弹出“输入 me@203.0.113.9 的服务器密码”→ 取消：退出码 -1、复核“问题仍在”、未执行、给出“重试”→ 重试并输入密码：“退出码 0”“复核结果：本次检查通过：linger 已开启”，该项从列表消失。
     - **安装页文本截断：通过。**用无效的终端库路径启动（`MYGO_GHOSTTY_VT`）使终端加载失败，测试资料的安装方案打印 5000 行约 315 KiB；页面显示“只显示最近约 64 KiB 的输出，更早的已省略”，文字从第 3961 行开始。
     - **同机创建副本（`9e46fd1` 与 `65d2eb6` 窗口实测）：通过（含冲突拦截与计划生成）。**`9e46fd1` 包窗口内：同机副本 `demo-copy` 从填写目录一路走到“文件已恢复，未启动程序”，副本落在指定目录、原实例文件与记录未变且均未标“已被取代”。`65d2eb6` 重打新包窗口内：用已占用的标识加原实例目录被拦下且不显示执行按钮；换为 `demo-copy2` 与新目录后计划正常生成，矛盾的“会先停止服务”提示已消失（该轮窗口手动操作停在“计划生成”未点执行，完整的冲突拦截 + `demo-copy2` 二次确认执行全链路已由 `gui` fake-ssh 集成测试 `TestSameServerCloneFlowCompletesWithoutStoppingService` 补齐通过）。
     - **顺带看到的（未改）**：取消密码后的错误文字来自服务层，提到 `valise server rekey` 与“终端界面按 K”，还提示“开启高级模式”，对桌面用户不合适；本地未审核资料在安装入口卡片上写“官方安装方案”（方案页里有“未经审核”提示）；服务器页“用户通行证 · 能用”标签折成两行；窗口在后台时不重绘。
   - **没做**：真实 OpenSSH + sshd 与真实 `loginctl`（polkit）；没有部署任何服务器；上一级目录 `valise-porter-test/` 里的同名 porter 仍是 11-14 的版本，没有覆盖（LAB.md 约定那里放最新 porter，是否覆盖等用户决定）。
   - 之后可按页继续把其余内联面板与写死列宽换成语义函数与令牌（非阻塞）。
2. **真机闭环范围**：在 macOS 桌面 + Linux 靶机上实测「添加服务器 → 接入 → 保护计划 → 手动备份 → 文件/暂存恢复测试 → 健康页 linger 一键开启（密码登录、无管理钥匙）→ 原生界面 → 恢复当前实例 / 迁移 / 同机创建副本」；真实 OpenSSH + sshd 的 askpass 与 `loginctl`（polkit）行为此前只在 fake-ssh 上验证过。
3. **合并与后续演进**：真机闭环通过后，将分支 `refactor/revoke-ab-slots-and-doc-audit` 整理合并，并按 `docs/DEV_PLAN.md` 推进 MVP `v0.1` 收口与 5 项用户痛点规划。

### 必须保留的环境与基线事实

- **当前用户构建**：`/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261116/Valise Test.app`（`0.0.0-dev+stage-test-20261116`，构建自 `65d2eb6`，A/B 已剥离，含 Phase F1/F2 与同机创建副本支持）。更新测试服务器的 porter 时选同目录的 `valise-porter_linux_arm64`（ubuntu-vm / debian-vm）或 `valise-porter_linux_amd64`（ubuntu-vm-x86）。上一个包 `stage-test-20261114` 构建于撤销 A/B 之前，不再使用。
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
