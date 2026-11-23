# Harness Profile 编写与实机验证指南

本指南面向希望为新 Agent（harness）编写**本机资料**或向官方**harness 资料包**提交 PR 的用户与维护者。完整字段定义与校验规则见 [PROFILES.md](PROFILES.md)。

---

## 1. 心智模型与职责边界

在 Valise 中，识别和保护一台服务器上的 Agent 由三层机制协作完成，**不要把单机差异硬塞进 Profile**：

| 层级 | 作用范围 | 负责什么 | 不负责什么 |
| :--- | :--- | :--- | :--- |
| **Profile (`*.yaml`)** | 跨用户、跨机器通用 | 探测（`detect`）、根目录（`roots`）、显式 `secrets` / `exclusive_secrets` 白名单、`body.paths`、安装来源（`provenance`）、核心 SQLite 与 `secret_tables`、高置信 `cache` / `ignore`、`service` / `entries` / `health` / `native_cli` / `agent_kit` | 用户自装第三方插件、个人工作区子目录、临时或零散小文件、单机自定义路径 |
| **Dossier + `valise-onboard`** | 单台服务器 / 单个已接入 Agent | 接入时记录真实路径、安装来源与服务状态；后续通过 `valise-onboard` 报备单机新增的本地路径覆写（`local_overrides`）、本地 SQLite（`local_sqlite`）与外部引用（`external_refs`） | 替代 Profile 里的通用 `secrets` 白名单或降低凭据优先级 |
| **`full` 默认备份兜底** | 每次默认备份 | 除明确归为 `body` / `cache` / `ignore` 外，**所有 roots 内的未分类文件（`unclassified`）默认按敏感层一并保存** | 重建目录外程序本体（仍需核实 `provenance` 与恢复演练） |

> **核心原则**：宁缺毋滥。对于官方文档或源码里看不准的零散目录，留给 `unclassified` 由 `full` 兜底即可，绝不凭文件名猜成 `cache` 或 `ignore`，更不凭黑名单猜测 `secrets`。

---

## 2. 从零编写最小可用 Profile

可直接复制 `skills/valise-profile-author/references/minimal-template.yaml` 起步。一份最小可用 Profile 由 6 个核心段落组成：

1. **元信息与能力声明（`schema` / `id` / `profile_version` / `status` / `capabilities`）**
   - `schema` 固定为 `valise-profile/1`；`id` 仅用小写字母、数字和连字符（如 `my-agent`）。
   - 每次修改递增 `profile_version`；只要还有未实机核实的条目就保持 `status: draft`。
   - 如实填写 `capabilities`（`install` / `manage` / `backup` / `restore` / `login` / `kit`），未经实机验证一律标 `status: untested`。
2. **探测与多实例（`detect` / `roots` / `relocation`）**
   - `binaries` 与 `version_argv`（只读、不联网）确认程序存在与版本。
   - `state_dir_candidates` 按优先级列出状态目录候选（如 `["${env:MY_HOME}", "${HOME}/.my-agent"]`），`markers_any` 填写能唯一确认这是该 harness 的标记文件，避免误把空目录认成实例。
   - 若工作区独立于状态目录，在 `roots` 中声明 `workspace`（优先用只读 `discover_argv`，配合 `default` 回退）。
3. **路径分类与凭据白名单（`paths` / `exclusive_secrets` / `consistency`）**
   - 显式列出凭据文件（`category: secrets`），如 `.env`、认证 JSON、可能内联 API Key 的配置文件。
   - 列出跨设备不可直接共用的独占凭据（`exclusive_secrets`，如机器人 Token、已配对设备凭证），防止创建分身时发生冲突。
   - 若有 SQLite 数据库，在 `consistency.sqlite` 中声明（含 `secret_tables` 表级剥离清单），备份时会自动走一致性热备并同步处理 `-wal`/`-shm` 伴生文件。
   - 只把高置信可再生内容标为 `cache` / `ignore`（如 `cache/**`、`logs/**`、`*.lock`、`*.pid`）。
4. **程序本体与来源（`provenance` / `body`）**
   - `provenance` 声明安装方式（`npm_global` / `git` / `uv_tool` / `pipx` / `binary`）。
   - 若程序本体目录嵌套在状态目录内部（例如 git 检出目录或私有运行时），必须将其声明在 `body.paths` 中，避免打进常规备份并确保恢复交换时正确搬动。
5. **运行形态、管理页、交互入口与入门管理动作（`service` / `entries` / `health` / `native_cli` / `native_session_cli` / `actions`）**
   - 有 systemd 用户服务就填 `service.candidates`，并在官方文档/源码有自带安装命令时一并写出 `service.install_argv`（未实机核实标 `# verify: pending`）；前台运行留空 `[]`。
   - **不要遗漏官方文档/源码已确认的交互入口**：只要官方文档或源码明确存在本地 Web 管理页/Dashboard 端口（`entries`，host 只能是 `127.0.0.1` / `localhost`）、交互式初始化向导（`native_cli`）、日常交互会话命令（`native_session_cli`）或安全的入门管理动作（`actions`，如 `status`、`restart`、`doctor`），就应直接写入并标 `# verify: pending` / `verify: pending`，让用户导入后立刻拥有可用的管理入口与命令行向导，而不是一张空白卡片。注意：按需启动的 Dashboard 端口不要加进常驻 `health.checks`。
6. **Agent 保护技能（`agent_kit`）**
   - 声明 `skills_dir` 以及 `mcp_add_argv` / `mcp_remove_argv` / `mcp_config_snippet` / `mcp_config_doc`（当提供 `mcp_add_argv` / `mcp_remove_argv` 时，Valise 会在安装/卸载技能前预览命令并默认经用户确认后在独立管理连接执行，同时在折叠区保留手动复制兜底；两项均纳入 `--commands` 命令审计，层级为 `admin`）。
   - **不要**在新 Profile 中添加已弃用的 `checkup` 或 `handoff` 块（记忆治理与交接已延后至 ValiseMemo / v0.2+）。

### 五大高频踩坑

| 坑点 | 常见错误写法 | 正确做法与原因 |
| :--- | :--- | :--- |
| **(a) `body.paths[].glob` 格式受限** | `glob: "runtime"` 或 `glob: "tools/*/*.bin"` | 必须写成 `<字面目录>/**`（例如 `tools/**`、`hermes-agent/**`）。恢复期目录交换引擎只按整目录搬动，否则 `profilelint` 直接报硬错误。且不要在 `paths` 里重复再写一遍 `category: body`。 |
| **(b) `paths` 匹配顺序与 `secrets` 优先** | 把 `skills/**` 放在 `skills/special/**` 前面，以为后者仍能命中 | 普通规则按**自上而下首条命中**，宽规则放前面会遮蔽后面的细规则（触发 `shadowed_path` 警告）；但 **`secrets` 规则永远拥有最高优先级**，且绝不可与 `body`/`cache`/`ignore` 冲突。 |
| **(c) YAML 严格解码 `KnownFields(true)`** | 把 `identity.name` 错写成 `identity.path`，或写进草稿自定义字段 | 解析器开启 `KnownFields(true)`，任何未在 `valise-profile/1` 中定义的键都会直接阻断加载。遇到报错先核对 [PROFILES.md](PROFILES.md) 第 2 节或 `skills/valise-profile-author/references/schema-v1.md`。 |
| **(d) 误解 `status: draft` 与恢复门槛** | 以为整份 Profile 必须变成 `status: verified` 才能正常原地恢复 | 恢复门槛**只看 `capabilities.restore`**。只要 `restore.status: tested`（附版本与环境），即便其他条目仍有 `verify: pending` 也不会拦截恢复；反之未测试时，界面会明确提示「恢复尚未在此版本测试」及影响（CLI 需显式传 `--allow-draft-profile`）。 |
| **(e) 命令写成 shell 字符串或套壳** | `argv: ["sh", "-c", "foo --version"]` 或在参数里写 `$(which foo)` | 所有命令必须是固定 `argv` 数组，禁止 `sh -c` / `bash -lc` 等 shell 包装器，禁止 `$(...)` 与反引号，且只能使用字段白名单内的占位符（如 `${HOME}`、`${STATE_DIR}`）。 |

---

## 3. 使用 AI 技能从文档 / 源码生成初稿

无需先在服务器上部署，即可把 `skills/valise-profile-author/` 交给编程 Agent（Claude Code、Codex、Antigravity、OpenClaw 等），让它**纯根据官方文档或源码仓库**（Mode A）生成保守、合规的初稿，或在已有安装环境下做只读核验（Mode B）。

1. 将目标 harness 的官方文档链接或源码仓库地址交给 Agent，并提示它按 `skills/valise-profile-author/SKILL.md` 执行。
2. 技能会强制 Agent 采用「Core-First、宁缺毋滥」策略：不编造未下载核验的 `install.steps` 哈希，不编造未实机验收的 B03 配置事务动作，也不生成已弃用的 `checkup`/`handoff` 字段。
3. **人类审阅 AI 初稿的 4 项必查清单**：
   - **凭据白名单（`secrets` / `exclusive_secrets` / `secret_tables`）**：是否覆盖了 `.env`、配置文件内联密钥、OAuth/渠道令牌与 SQLite 凭据表？是否与 `body`/`cache`/`ignore` 零冲突？
   - **完整命令审计表**：运行 `--commands` 检查全部可执行入口，确认没有联网探测副作用、没有多余提权、没有 shell 注入或基础疑点。
   - **证据注释**：每条关键规则是否都带有 `DOC:`（文档路径/链接）、`SRC:`（源码文件与符号）或 `OBS:`（实机观察）注释，杜绝“拍脑袋”路径？
   - **`verify: pending` 清单**：所有未在真实机器上核实过的路径、表名或命令是否都诚实标了 `verify: pending`，且对应 `capabilities` 保持 `untested`？

---

## 4. 离线校验三步闭环

在导入客户端或提交 PR 之前，先在本地完成「语法与规则校验 → 命令审计 → 样本路径分类试跑」三步闭环。

### 方式一：在 Valise 仓库内使用一键脚本

```bash
./scripts/verify-profile.sh [--strict] [--parent <parent.yaml>] [--sample <paths.txt|dir>] <your.yaml>
```

- 默认会同时执行严格 schema 检查与 `--commands` 全量命令审计。
- 传入 `--strict` 会把软警告（如 `shadowed_path`、`missing_capabilities`、`deprecated_field`）也视为失败。
- 传入 `--sample <paths.txt|dir>` 会把你从文档整理的文件列表（或测试目录）跑一遍分类模拟，输出各分类计数、备份档位落点与未命中规则。
- 校验子 profile 时，通过 `--parent <parent.yaml>` 一并传入父 profile。

### 方式二：使用已安装的独立 Valise CLI

```bash
valise profiles lint --strict --commands --classify <paths.txt> <your.yaml>
```

- 校验子 profile 时将父 profile 放在前面一同传入：`valise profiles lint --strict <parent.yaml> <child.yaml>`。
- 需要给脚本或 CI 消费结果时，附加 `--json`。

样本清单 `<paths.txt>` 支持 `#` 注释与 `<root>:<relpath>` 格式（省略前缀默认归属 `state`），可直接参考 `skills/valise-profile-author/references/doc-research-checklist.md` 中的模板。

---

## 5. 本机导入、同 ID 覆写与实机只读验证

离线校验通过后，把 Profile 作为**本机资料**导入客户端（界面始终标为「本地，未经审核」）。**允许本地文件显式覆写官方资料包中同 `id` 的 Profile**（方便调试新版本或抢先适配上游变更）：界面会醒目标注为「本地覆写，未经审核」，展示与官方版本的能力差异（capability diff），并支持一键移除本地覆写以立即恢复官方资料包版本，以及一键同步到已接入的对应实例。

### 本机资料存放路径（与 `os.UserConfigDir()` 对齐）

- **macOS**：`~/Library/Application Support/valise/profiles/<id>.yaml`
- **Linux**：`${XDG_CONFIG_HOME:-~/.config}/valise/profiles/<id>.yaml`

### 两种导入方式

1. **桌面端一键导入（推荐）**：打开「设置 › 资料包 › 导入本地资料 (.yaml)…」，选择你的 `.yaml` 文件；Valise 会先完成校验、展示与官方版本的能力差异（若同 `id` 覆写）及全部会被执行的命令清单，确认后自动写入上述目录，并在需要时引导一键同步到已接入实例。
2. **手动放入 + CLI 核对**：将文件复制到上述路径并确保文件名与 `id` 一致，然后运行：
   ```bash
   valise profiles status
   ```
   确认该 profile 已在本地列表中正常加载（同 `id` 时显示本地覆写标记）且无拒绝报错。

### 只读探测迭代

对已安装该 harness 的测试服务器运行**只读**探测，**先不要确认接入**，反复核对分类结果：

```bash
valise enroll <server> --profile <id> --report-json
```

重点检查输出中的：
- 探测到的版本、状态目录与额外 `roots` 是否准确；
- `unclassified` 列表中是否漏掉了凭据文件、SQLite 数据库或明显的大体积缓存；
- 是否有符号链接或嵌套目录需要特殊处理。

调整 `.yaml`（记得按需更新注释）后重新跑离线校验与只读探测，直到分类准确、无误判，再在桌面端「这台服务器上的 Agent」点「重新扫描」并正式接入。

---

## 6. 消除 `verify: pending` 与官方 PR 贡献清单

从「能用」走向「可收录进官方 harness 资料包」，按以下顺序收口：

1. **逐项消除 `verify: pending`**：在真实目标机上实际运行该 harness（初始化、配置凭据、发起会话、演练备份与原地恢复），把每条被核实的规则去掉 `verify: pending`，并将注释更新为 `OBS: <版本/环境/观察结论>`。
2. **如实填写 `capabilities`**：仅对**实际演练通过**的能力标 `status: tested`，并填全 `versions`（精确版本号）、`env`（OS/架构/安装形态）与 `date`；未测或部分测试的项诚实保持 `untested` / `partial` 并写清 `note`。
3. **官方 PR 贡献清单**：
   - [ ] 新文件放在 `profiles/<id>.yaml`（修改已有文件时已递增 `profile_version`）；
   - [ ] `./scripts/verify-profile.sh --strict profiles/<id>.yaml` 零硬错、零无意警告（新 profile 不含 `checkup`/`handoff`）；
   - [ ] 命令审计表全部条目均标「未发现基础疑点」，无 shell 包装或未声明副作用；
   - [ ] PR 说明中附上：测试所用精确 harness 版本、OS/架构与安装方式、`valise enroll --report-json` 输出（凭据路径可打码），以及每条已移除 `verify: pending` 的官方文档链接或实机观察依据。
