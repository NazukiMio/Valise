# 当前状态与交接

> 每次交接时覆盖更新本文件，不另建交接文件。最后更新：2026-11-07。新会话读完 `AGENTS.md` 后先读本文件，再按 `AGENTS.md` 第 1 节读规范。

## 1. 代码与分支

- 工作分支 `n2-agent-protection-journey`，**未推送**。N2 实现从 `305e14e` 开始，之后的提交都在这个分支上；`references/`（用户的参考 HTML）有意不纳入版本控制。
- 全仓自检 `testbed/selfcheck.sh` 在 `021121c` 上通过（SELFCHECK_PASS）。之后只有文档整理与代码注释改动。
- **提交署名缺口**：`c04b266`、`cfcb916`、`de37ced`、`e30a84e`、`021121c` 少了 `AGENTS.md` 第 8 节要求的两行 Co-authored-by（只有 Claude 的署名）。分支未推送，可以用非交互的 `git rebase --exec` 补上；是否改写等用户决定。更早的 `b990c75`、`6364c15`、`6c7e3c5`、`b45a883` 也没有这两行，不是本轮提交。
- 三台靶机（`ubuntu-vm`、`ubuntu-vm-x86`、`debian-vm`）已部署 `021121c` 构建的正式 porter（声明 identity/1–2、protection-plan/1–2、backup-runs/1–2、system-fix/1、copy-check/1）。`nazukiki-cloud` 未部署本轮 porter。

## 2. N2 进度（实现完成，尚未验收为 Done）

已实现并在真实 systemd 上验证（详见 [acceptance/N2.md](acceptance/N2.md)）：

- 身份与旅程：实例世代、迁移关系（ubuntu-vm → x86 真实迁移）、恢复与创建副本的关系、跨服务器 Agent 列表标注“已迁出”。
- 保护计划：版本化 CAS、意图 → 配置 → 发布 → 核实事务、失败回退、崩溃后的待核实（故障注入构建实测三种崩溃）、`valise protection resolve`。
- 按计划备份：默认 full、服务器复制、分离执行（一次性 systemd 单元，由“分离模式”设置开关，默认关闭）、取消。
- 错过的定时备份与补跑（`Persistent=true`）、补跑标记“服务器从停机状态恢复”；重启后未登录时定时照常运行（debian-vm，linger）。
- 被计划引用的复制目标的版本化修改；来源不明快照恢复前的明确选择。
- 健康：linger 与时钟一键修复（列命令与改动，再次同意）、接管模式（TUI 内 `sudo -n`）、破坏性步骤才检查别处备份、高级模式持续到按 `L` 关闭/切换服务器/退出。
- 复制位置：失败原因分类留在记录里、计划视图给出各位置最近结果、健康页解释、保存前由服务器 `target_check`。
- Debian 13（systemd 257）时间戳兼容修复。

尚未在实机覆盖：DST 实际切换、客户端退出登录、创建副本关系（fake 测试已覆盖）、需要 sudo 的时钟修复与接管模式（靶机都是“你自己的 SSH 设置”，开不了高级模式）、复制失败原因的真实失败记录。

界面层：弹窗、错过记录“标记已读”、分离模式开关的界面入口、发起按计划备份的界面都属于 N4；当前提示只在服务层结果中。

## 3. 待用户决定

1. **补提交署名**：是否用 rebase 给上述 5 个提交补 Co-authored-by。
2. **界面与 CLI 的旧说法**（约 30 个代码文件里的“空间站”“逃生舱”等，含 TUI 的“仓库与空间站”页、porter 返回的原因文字）：现在单独统一，还是并入 N4 的 TUI 对齐。
3. **把一台靶机改为 Valise 接入**（会装日常钥匙、创建管理钥匙，需要服务器密码与管理口令），用于实测高级模式、接管模式与 sudo 时钟修复；用哪台。
4. **N2 能否标 Done**：剩余未实机覆盖的项哪些记为已知限制。
5. **B03 链接适配**：[proposals/B03_SYMLINK.md](proposals/B03_SYMLINK.md) 仍待确认。
6. **N3 云盘服务器登录流程**：开工前定稿（DEV_PLAN N3 已记录两步登录与首批云盘）。

## 4. 文档整理（2026-11-07）后的遗留

- **规范正文用语统一**（用户已决定要做）：PRODUCT.md 8.1 已改为“正式规范叙述改用新说法”，但各规范正文尚未逐处改写（整理时约有 117 处“逃生舱”、79 处“空间站”、45 处“着陆”）。逐份人工改写，代码标识保持原样。
- **服务契约的增补并入正文**：N2 的增补决定 A–G 已移入 [SERVICE_CONTRACTS.md](SERVICE_CONTRACTS.md) 第 16 节，并在第 4、5、6、7.3、9、10 节开头加了指引；冲突条款还没有在原章节逐条改写。
- 各规范开头的“2026-11-05 同步”说明段较多且重复，可在用语统一时一并精简。

## 5. 演练环境

- 演练客户端：`valise --config-dir "$HOME/.cache/valise-drill/config"`，口令文件 `$HOME/.cache/valise-drill/passphrase.txt`（不要打印内容）。
- 靶机现状：`ubuntu-vm` 的 OpenClaw 计划 revision 约 10，日程关闭，复制到 `nas`（经 Tailscale）；`debian-vm` 的 OpenClaw 计划日程开启，每天 04:40 UTC，linger 已开；`ubuntu-vm-x86` 保留一个恢复前目录 `~/.valise-prev-op_01M6TJ0J2JHBKHV2SFXHXHB56Y-state`。靶机详情见 [LAB.md](LAB.md)。
- 本轮的验收脚本放在会话临时目录，不在仓库里；需要时按 [acceptance/N2.md](acceptance/N2.md) 的步骤重写，故障注入用 `go build -tags faultinject` 构建 porter，测完换回正式构建。

## 6. 建议的下一步

1. 用户处理第 3 节的决定。
2. 规范正文用语统一与服务契约第 16 节并入正文（第 4 节）。
3. 视决定把一台靶机改为 Valise 接入，补测高级模式相关项，然后评估 N2 Done。
4. 进入 N3（严格副本、自动同步收敛到服务层、源失联恢复、云盘）。
