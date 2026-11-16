# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-16。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前工作与交接（2026-11-16 A/B 剥离与前端整固 F1、F2 完成并经审批；v0.1 测试包已构建，待用户真机验收）

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
2. **创建副本**：`gui/restoresetup.go` 只在“迁移”时把原服务器排除出目标列表；切到“创建副本”默认选中原服务器，同机时直接展开目录映射输入区并给一句提示（新词条 `restore.same_server_hint`）。测试：`restore_test.go` `TestCloneOnTheOnlyServer`（单服务器可选中原服务器并发起副本计划）。
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

1. **用户真机端到端验收（阶段 A 测试包已构建，2026-11-16）**：测试包在 `/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261116/`，构建自 `d89a953`（`2177be0` 加一处 porter 清理；首次打包自 `2177be0`，清理后整包重打），内含 `Valise Test.app`、CLI `valise`、`valise-porter_linux_arm64` / `_amd64`、`standalone-restore/`、`SHA256SUMS`、`BUILD.json` 与验收步骤 `TESTING.md`。
   - **打包方式**（没有 `mygo.json`，沿用 11-14 包的做法）：`go build -trimpath -ldflags "-X main.version=… -X valise/client/porter.ReleasePublicKey=$(cat ~/.cache/valise-drill/devpack/key.pub)"` 编出桌面程序与 CLI，放进 `.app` 骨架（`Info.plist`、`PkgInfo`、mygo 终端插件声明的 `libghostty-vt.dylib`，SHA-256 与插件清单一致），整个 `.app` 做 ad hoc 签名；porter 用 `CGO_ENABLED=0` 交叉编译。没有重新生成资料包或钥匙。
   - **已核对（实际运行）**：两个 porter 为静态 ELF（aarch64 / x86-64，`CGO_ENABLED=0`）；四个二进制都带 `stage-test-20261116` 版本号且不含旧版本号；`valise version` 输出新版本；`valise profiles status` 报 2026.11.10 已验签；`codesign --verify --deep --strict` 通过；桌面程序 `-h` 可执行；`SHA256SUMS` 复核全部 OK。
   - **A/B 入口**：四个二进制里搜不到 `linux-bwrap`、`environment-restore`、`valise/core/environment`、`restore.policy` 等（11-14 包里有 18 处）；CLI 帮助里没有环境相关命令。首次打包时 `porter/internal/server/action_conflicts.go` 的加锁名单里还留着 `environment_test`、`environment_settings_set` 两个没有处理函数的名字；已在 `d89a953` 删除并重新打包，新二进制里搜不到。
   - **真实运行补证（2026-11-16）**：
     - 四个二进制的构建来源戳都是 `vcs.revision=d89a953…`、`vcs.modified=false`；以下各项均在重新打包后的产物上重做过（app 的 5 分钟运行与 askpass 助手是在首次打包的产物上做的，界面代码两次打包相同）。
     - 两个 porter 在真实 Linux 上从临时目录运行（`ubuntu-vm` ARM64、InstaBox x86_64；用完即删，未部署）：`version` 为本版本号、非动态链接、SHA-256 与清单一致、`hello` 握手正常；`environment_settings_set`、`environment_test`、`environment_status`、`environment_restore_prepare` 实测均返回 `method not found`。`ubuntu-vm-x86` 当时连不上。
     - 打包的 `Valise Test.app` 指向隔离的 fake-ssh 数据目录启动并运行 5 分钟以上，无报错输出，没有自行发起管理登录。
     - 打包 app 程序的 askpass 转交助手单独运行通过（取回答案；被拒绝时失败退出）。
     - 打包的 CLI 在同一 fake-ssh 目录上跑通 linger 修复（Valise 接入、无管理钥匙、密码登录）：密码错不执行，密码对则 `loginctl enable-linger` 退出码 0、复核通过。
     - 同一份界面代码（`2177be0` 之后只改过文档与 porter 的加锁名单）上重跑并通过：`TestCloneOnTheOnlyServer`、`TestHealthLingerFixAsksServerPasswordInWindow`、`TestHealthActionAsksServerPasswordInWindow`、`TestTailTextKeepsTheNewestOutput`、`TestInstallTextFallbackSaysWhenCut` 等。
   - **没做**：没有在真实窗口里点过界面（用户 2026-11-16 决定自己验收），所以“创建副本”页、健康页 linger 确认框与密码弹窗、安装页文本截断在这个包的窗口里没有操作过，依据是来源戳加同一提交上的界面测试；没有部署任何服务器；上一级目录 `valise-porter-test/` 里的同名 porter 仍是 11-14 的版本，没有覆盖（LAB.md 约定那里放最新 porter，是否覆盖等用户决定）。
   - 之后可按页继续把其余内联面板与写死列宽换成语义函数与令牌（非阻塞）。
2. **真机闭环范围**：在 macOS 桌面 + Linux 靶机上实测「添加服务器 → 接入 → 保护计划 → 手动备份 → 文件/暂存恢复测试 → 健康页 linger 一键开启（密码登录、无管理钥匙）→ 原生界面 → 恢复当前实例 / 迁移 / 同机创建副本」；真实 OpenSSH + sshd 的 askpass 与 `loginctl`（polkit）行为此前只在 fake-ssh 上验证过。
3. **合并与后续演进**：真机闭环通过后，将分支 `refactor/revoke-ab-slots-and-doc-audit` 整理合并，并按 `docs/DEV_PLAN.md` 推进 MVP `v0.1` 收口与 5 项用户痛点规划。

### 必须保留的环境与基线事实

- **当前用户构建**：`/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261116/Valise Test.app`（`0.0.0-dev+stage-test-20261116`，构建自 `d89a953`，A/B 已剥离，含 Phase F1/F2）。更新测试服务器的 porter 时选同目录的 `valise-porter_linux_arm64`（ubuntu-vm / debian-vm）或 `valise-porter_linux_amd64`（ubuntu-vm-x86）。上一个包 `stage-test-20261114` 构建于撤销 A/B 之前，不再使用。
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
