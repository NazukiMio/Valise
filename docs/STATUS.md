# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-19。新会话读完 `AGENTS.md` 核心必读 3 份后先按第 0 节接续当前分支任务。

## 0. 当前阶段：Valise v0.1（Valise Recovery）发版收口（2026-11-19）

- **分支状态**：`feat/cloud-rclone-and-server-shell`（含 B3.1 与 B3.2 提交 `c098ce9`、`28b6696`，测试包 `stage-test-20261119-b3.2`）已通过全部真机验收并经 `--ff-only` 合入 `main`；当前工作分支 **`feat/v01-recovery-closure`** 已完成 v0.1 条目 1（`5152107`）与条目 2（`cef529f`）开发及 `./testbed/selfcheck.sh`（`SELFCHECK_PASS`），并打好真机验收测试包 `stage-test-20261119-v01r1`。
- **B3 / B3.1 / B3.2 真机验收结论（2026-11-19，用户实机确认「全部通过」）**：
  1. **备份双轨与寿命闭环（B3）**：定时日期周期、永久手动存档点 `savepoints/`、主人级快照晋升、默认关闭的服务器自动退役与长旅程曲线视口虚拟化；原版恢复脚本验证源周期退役后仍可恢复。
  2. **服务器 `rclone` 与多服务器云盘登录（B3.1）**：`porter` 优先使用 `~/.local/bin/rclone` 并回退服务器 `PATH`；GUI 接通无 `sudo` Kit 安装（展示版本/URL/SHA-256 并显式确认）；云盘添加/重登录支持多选服务器串行登录（共享单次操作级 PIN 租约），`client_id`/`client_secret` 仅在当前页面内存会话保留并在离开/完成/关闭窗口时清零。
  3. **服务器命令行、Agent 原生对话与资料包更新（B3.2）**：服务器命令行移入服务器详情页并接通本地 PTY + `TIOCSWINSZ` 实时缩放与终端配色继承；Agent 页保留「原生对话」与「首次向导」，探测接入资料版本升级时主动提示并支持跳转「设置 › 资料包」下载或导入本地资料包且实时重载生效；清理云盘/位置页 11 处重复或过度防御性文案。
- **已完成并待真机验收（`feat/v01-recovery-closure`，`5152107` + `cef529f`）**：
  1. **条目 1 · 桌面零 CLI 维护收口**：Agent 详情页「改名…」（`SetAgentLabel`）、服务器设置「空间清理」（`PrevList`/`PrevDrop` 恢复前旧目录与 `OldList`/`OldCleanup` 已被取代旧实例，含 7 天权限分级标签与逐项危险确认）及「设置 › 钥匙与口令 › 配置导入与导出」（`ExportConfig`/`ImportConfig`，不含私钥且支持缺通行证一键跳转重装）。
  2. **条目 2 · 低门槛云盘存储（WebDAV 与 S3 兼容）**：扩展 `kit/tools.yaml` 与 `core/kittools` 支持非浏览器凭证表单云盘（`browser: false`，不依赖回环端口 `53682`）与存储桶路径（`needs_bucket: true`，目标记为 `valise-<名>:<bucket>/valise-vault/`）；桌面与 CLI（`--bucket`）打通 WebDAV / S3 兼容云盘登录、长选项滚动与 `verify: pending` 显式确认试用，待配合真实 WebDAV / S3 服务端实机核实。
- **明确冻结（延后至 v0.2+）**：主题引擎扩展（`valise-theme/2`、主题文件 `material` 字段）与 TUI 1:1 对齐在 `v0.1` 冻结；通用页面转发与 OAuth 中继完善、macOS Keychain/Touch ID、`hermes.yaml` 真机消 pending、`pi.yaml` 与兄弟项目 [ValiseMemo](../../ValiseMemo/docs/README.md) 集成（仅桌面侧只读浏览与按需控制面）归入 `v0.2–v0.3`（`Valise Client`）。
- **测试包打包触发标准（2026-11-19 用户约定）**：满足以下任一条件即打新 `stage-test-*` 测试包交由用户验收，否则先继续合并至下一批更新：
  1. 更新可能引发性能倒退或致命错误；
  2. 更新可能出现 UI/交互 Bug；
  3. 属于新内容附加，且对后端也进行了较多更改；
  4. 新内容涉及安全或风险边界变更。
- **后续专项待办**：全仓 UI 文案二次审计（平衡精简其余「过度防御 / 过度解释」说明）。

### 已完成阶段摘要（架构收敛 + F1/F2 + B1–B3.2）

- **架构收敛与 A/B 剥离（2026-11-15，`refactor/revoke-ab-slots-and-doc-audit`）**：撤销 A/B 双槽位并行运行环境与 `bwrap` 容器接管，统一回归「暂存校验 → 带交换日志原子交换 → `.valise-prev-<op>` 保留与回退」；永久保持 `valise-snapshot/1` 与 `/2` 读取兼容；保留范围化旅程加载、`restore-review/1`、`backuprun` 与非 A/B `native --instance`。
- **前端整理与整固（2026-11-16/17，Phase F1 `d43f228` / F2 `2177be0` / 界面整理三批）**：消除 `gui` 全局状态竞态、`confirmations` 收归 `App`、打通同机创建副本与无管理钥匙 `linger` 弹窗修复；落地三层样式/区块分离、沉浸式标题栏、旅程横向虚拟化与「经典 / 手账 / 夜航」材质主题，验收记录见 [acceptance/N4.md](acceptance/N4.md)。
- **真机闭环修复（2026-11-16–19，Phase B1–B3.2）**：落地保护计划「按原设置重新生效」、移除位置一键清理障碍、引导前可选暂停受管服务、官方脚本上游哈希变更容错与粘贴安装地址/指令、单次 Valise 主口令 + 30 分钟窗口 + 本机 6 位 PIN 快捷解锁、操作级 PIN 临时租约、结构化 Debug 日志、离线 `nas` 复制秒级取消、本地 PTY 与资料包热更新。

### 必须保留的环境与基线事实

- **当前待验收测试包**：`/Users/nazukimio/Downloads/valise-porter-test/stage-test-20261119-v01r1/Valise Test.app`（`0.0.0-dev+stage-test-20261119-v01r1`，构建自 `feat/v01-recovery-closure`，含 `darwin/arm64` 客户端、`valise` CLI 与 `valise-porter_linux_{arm64,amd64}`；具体提交见包内 `BUILD.json`）。已通过真机验收的历史包 `stage-test-20261119-b3.2` 等均保留未动。
- **默认资料包与演练公钥**：当前开发资料包位于 `~/.cache/valise-drill/devpack`（`2026.11.19`：`dsh v4`、`hermes v11`、`openclaw v24`，`kit-tools.yaml` 含 `webdav`/`s3`，公钥 `RWSFvljhQMWMsDJdlOcX+EhDZWostM2MqU1HJKwffgq1JQb5ImJiYZQS`）；演练配置 `~/.cache/valise-drill/drillpack` 独立保留，不得混用或覆盖。
- **构建与检查环境**：macOS 上始终执行 `export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.27.3`；构建发布/测试包时 `-ldflags` 必须同时注入 `main.version` 与 `valise/client/porter.ReleasePublicKey`；禁止执行 `rm -f`（清空用 `: > file`，测试用 `t.TempDir()`）；未经用户明确要求不执行 `git push`。

---

## 1. 历史阶段摘要（N1–N4 主体闭环，N5 延后）

| 阶段 | 状态 | 说明与记录位置 |
| :--- | :--- | :--- |
| N1 规范与服务契约 | `Done`（2026-11-05） | [SERVICE_CONTRACTS.md](SERVICE_CONTRACTS.md) 第 1–16 节 |
| N2 汇总、保护计划、时间轴与 B03 | `Done`（2026-11-07） | [acceptance/N2.md](acceptance/N2.md)、[acceptance/B03.md](acceptance/B03.md) |
| N3 严格副本、云盘与失联恢复 | `Done`（2026-11-08） | [acceptance/N3.md](acceptance/N3.md) |
| N4 mygo GUI 主流程与 v0.1 收口 | `In progress`（v0.1 收口；TUI 1:1 对齐冻结） | [acceptance/N4.md](acceptance/N4.md) |
| N5 管理方自动化、自定义与扩展 | `Planned`（v0.2+） | [DEV_PLAN.md](DEV_PLAN.md) |
