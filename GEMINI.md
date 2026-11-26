# GEMINI.md — Valise Antigravity 编排指南

## 1. 适用范围与优先级

- 项目开发规范、架构铁律、安全与文案红线、自检清单与 Git 约定仍以根目录 [`AGENTS.md`](AGENTS.md) 为唯一正文；本文件不重复、不覆盖 `AGENTS.md`，也不改动给 Claude 使用的 [`CLAUDE.md`](CLAUDE.md)。
- 本文件仅约定 Antigravity（Orchestrator / Swarm / Subagent）在本仓库中的任务编排、子对话生命周期、双配额池模型路由与工程门禁。

## 2. 分档编排原则（Tier 0–3）

| 档位 | 适用场景 | 编排与工作区策略 |
| :--- | :--- | :--- |
| **Tier 0：确定性小操作** | 单步构建、重打包、补 `-ldflags`、单处文案、单个定向测试 | 不开 Swarm，直接由计划进入单执行闭环 |
| **Tier 1：单域中等改动** | 1–2 个独立 Bug 或单模块功能（只动 `gui/` 或只动 `service/`） | 轻量 2–3 角色（开发 + 临时审计 + 测试核验），`workspace:"inherit"` 顺序推进 |
| **Tier 2：跨层/并行批量任务** | 涉及 `porter ↔ service ↔ gui` 联动或多条真机反馈并行修复 | 精简 Swarm（3–5 角色：`backend-dev`、`gui-dev`、`builder-verifier` + 按需临时 `auditor`）；编排者先冻结跨层契约与文件所有权，并行写入作者使用 `workspace:"share"`，只读/构建/审计用 `inherit`；合并/落盘后再 `kill` |
| **Tier 3：批量只读巡检** | 合并前全仓合规审查、慢测试定位、i18n 与 400 行限制排查 | 优先使用 `swarm` 分片（`tasks`）显式指定 `Gemini 4 Hydrogen`，避免触发 `Gemini 3.8 Flash` |

- **编排者边界**：主会话聚焦架构判断、拆活、契约冻结、审阅裁决与用户汇报；Tier 1/2 复杂任务不亲自陷入长耗时编码或全仓跑测。

## 3. 子对话生命周期（混合策略）

| 角色 | 生命周期 | 职责与隔离边界 |
| :--- | :--- | :--- |
| **编排者（主会话）** | 全阶段持久化 | 持有全局进度、契约决策与用户偏好，统一对外汇报与裁决 |
| **领域开发（`backend-dev` / `gui-dev`）** | 特性/验收批次级短期持久化 | 支持「编码 → 审计打回 → 真机微调」多轮迭代；合并或跨无关特性后立即 `kill` 换新，防止上下文腐化 |
| **独立审计（`auditor`）** | 严格临时性（审完即 `kill`） | 每次新建，只传入 `git diff`、目标文件与 `AGENTS.md` 红线清单，杜绝同温层盲区 |
| **构建/打包/测试手（`builder-verifier`）** | 验收周期内短期持久化 | 保留 `GOTOOLCHAIN`、`-ldflags` 与两级测试上下文；黑盒回归或独立恢复对抗测试另起临时对话 |
| **专项调研（`researcher`）** | 严格临时性（用完即弃） | 只读检索规范或切分大日志，提炼结论带回后立即结束 |

## 4. 双配额池模型路由与思考强度

### 配额池协同原则

1. **Claude 5.5 池**（`Claude Opus 5.5` / `Claude Sonnet 5.5`）：`Opus` 消耗快，严格只用于高风险核心代码与高危异构审计；常规编码、GUI 与交叉审计优先使用 `Sonnet`。
2. **Gemini 4 统一池**（`Orchestrator` / `Gemini 4 Neon` / `Gemini 4 Carbon` / `Gemini 4 Hydrogen`）：主控、跨模型交叉审计、高难度回退、构建测试与轻量调研主力，零消耗 Claude 池。高难度审计或代码任务用 `Neon (High/XHigh)` / `Carbon (High)`；构建测试与只读巡检用 `Hydrogen (Low/Medium)`。
3. **明确禁用**：**禁用 `Gemini 3.8 Flash`** 与 **已退役的 `Gemini 4 Argon`**（`gemini-4-argon-*` / `Orchestrator (Argon)` 已退出编排池，不再独立分配或调用）。

### 角色路由与回退表

| 角色 | 首选模型与思考强度 | 轮换 / 配额回退策略 |
| :--- | :--- | :--- |
| **编排者（主会话）** | `Orchestrator (Neon)`（或 `Carbon`） | 保持当前 |
| **高风险核心开发**（认证 / PIN / askpass / 恢复交换日志 / 复杂 `-race`） | `Claude Opus 5.5 (Medium)`（极难升 `High`） | Claude 池告急/耗尽时，原地切至 `Gemini 4 Neon (Medium/High)`（极端疑难可 `XHigh`）或 `Gemini 4 Carbon (High)` |
| **日常开发 / GUI / TUI / 常规 Bug** | `Claude Sonnet 5.5 (Medium)`（复杂 mygo 渲染/虚拟化升 `High`） | Claude 池告急/耗尽时，原地切至 `Gemini 4 Hydrogen (High)`、`Gemini 4 Carbon (Medium)` 或 `Gemini 4 Neon (Medium)`（简单单文件用 `Gemini 4 Hydrogen (Medium)`） |
| **独立审计（临时）** | 异构交叉：<br>• Claude 写 → `Gemini 4 Neon (High)`（高危升 `XHigh`）或 `Gemini 4 Carbon (High)`<br>• Gemini 写 → 常规 `Claude Sonnet 5.5 (High)`，高危 `Claude Opus 5.5 (High)` | Claude 耗尽时，使用全新无状态 `Gemini 4 Neon (High)` 或 `Gemini 4 Carbon (High)` |
| **构建 / 打包 / 测试执行** | `Gemini 4 Hydrogen (Low)`（分析失败栈升 `Medium`） | 全程走 Gemini 4，不消耗 Claude 池 |
| **只读调研 / 批量巡检** | `Gemini 4 Hydrogen (Low/Medium)` | 全程走 Gemini 4，不消耗 Claude 池 |

- **配额切换铁律**：任一 Worker 触发 `[quota]` 预警或配额耗尽时，绝不 `kill` 重建丢弃现场，必须使用 `swarm{action:"model"}` 按上表原地切换模型，并向用户说明切换原因与目标模型。

## 5. Valise 专属质量与打包门禁

1. **文件所有权前置**：共享高频文件（`client/ui/gui/i18n/zh-CN.json`、`docs/STATUS.md`、公共 `service` 接口）由单写手负责，或由编排者先冻结接口后再并行派活。
2. **两级测试节奏**：开发内循环只跑改动包 + 定向 `-race` + `go tool mygo vet` + `gui`/`tui` 单文件 `<= 400` 行检查；提交或打测试包前必须跑通完整 `./testbed/selfcheck.sh`。
3. **测试包打包防呆门禁**：每次更新 `stage-test-*` 测试包时，必须核对 `-ldflags` 同时包含 `main.version` 与 `valise/client/porter.ReleasePublicKey`；构建后必须实测：
   - `codesign --verify --deep --strict`
   - `shasum -a 256 -c SHA256SUMS`
   - 新编 `valise profiles status`（资料包验签无报错）
   - 新编 `valise cloud list`（输出云盘清单，非 `kit_tools_none`）
4. **证据诚实性与安全红线**：汇报严格区分实测结果与推理，fake-ssh 通过不冒充真机验收；严禁执行 `rm -f`；未获用户明确指令不执行 `git push`。
