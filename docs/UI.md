# UI 规范（TUI 与 mygo 桌面）

> UI 分两步：M7a 终端 UI（第 8 节），M7b 桌面 UI（第 2–7 节，mygo）。第 1 节的分层原则全程有效：服务层不依赖 UI。

## 1. 分层原则

- 所有业务逻辑都写在 `client/ui` 的服务层（纯 Go）中，CLI、TUI 和桌面 UI 共用同一套服务层。**TUI 和桌面 UI 都只是很薄的适配层，没有它们，CLI 也必须完整可用。**
- 这样做也是对框架风险的控制：mygo 是一个由个人维护的年轻项目。即使将来需要更换 UI 框架，也只需要重写适配层和前端，服务层不受影响。

## 2. 资料来源

- 官方文档：https://github.com/egoist/mygo/tree/main/docs ，阅读时以锁定 tag 下的版本为准。
- Go API：`go doc -all github.com/egoist/mygo`
- 开发机检查：`go tool mygo doctor`
- 不确定的 API 一律查阅文档或源码，**不要凭印象编写**。

## 3. 版本锁定与 CLI

- 锁定版本：v0.7.3（用户于 2026-10-31 指定，提交 `6f28d7b`；M7b 开工时写入依赖并复核本文约定）。锁定后，升级必须经过用户同意。
- 把 mygo CLI 作为 Go tool 依赖加入，保证 CLI 和库的版本一致：`go get -tool github.com/egoist/mygo/cmd/mygo@<锁定版本>`。之后统一使用 `go tool mygo <命令>` 调用 CLI。
- 前端依赖中的 `mygo-runtime` 也锁定为与锁定版本对应的版本。

## 4. 绑定约定

- **生成客户端**：`go tool mygo generate`，输出路径为 `client/ui/src/mygo.ts`（在 `mygo.config.ts` 的 `bindings` 中配置）。生成的文件不得手工修改，也不计入"单文件 400 行"的限制。
- **生成期间会执行 main**：`mygo generate` 会在设置 `MYGO_GENERATE` 的情况下运行程序，直到 `App.Run` 返回。因此建立 SSH 连接、读取 keystore、启动隧道等任何有副作用的初始化，都必须放进 `App.WhenReady`。main 函数中只允许调用 `mygo.Bind` 和注册事件。
- **服务划分**：按领域绑定，例如 `Servers`、`Instances`、`Vault`、`Revive`、`Relay`、`Keystore`、`Settings`。每个服务对应 `client/ui` 服务层中的一个 Go 类型，方法只负责参数转换。
- **进度流**：封存、同步、复活、登录中继等长时间操作，通过 `*mygo.Channel[T]` 参数把进度推送给前端。方法的第一个参数接收 `context.Context`，页面关闭时操作随之取消（复活进入交换阶段之后不响应取消，见第 6 节）。
- **全局状态变化**：例如实例的健康状态、隧道的三个状态，使用 `mygo.NewEvent` 声明的事件进行广播。
- **字段命名**：所有结构体字段必须带 `json` 标签。可能超过 2⁵³ 的整数（目前没有）使用 `json:",string"`。

## 5. 错误约定

mygo 会把 Go 方法返回的 error 文本作为前端 `CallError` 的 message。约定如下：

- 服务层的错误统一为 `valise.UserError{Code, Reason, Suggestion, Detail}`，它的 `Error()` 返回该结构的 JSON 文本。
- 前端在 `src/errors.ts` 中统一解析：能解析时，展示"原因 + 建议"，细节默认折叠；不能解析时，展示"发生了未预期的错误"，并附上原文。
- 禁止在前端直接展示未经解析的原始错误文本。

## 6. 安全铁律

- **入口一律用系统浏览器打开**（通过 mygo 的 shell API 打开 URL），**绝不在 mygo 窗口中加载 agent 的 Web 页面**。原因：开发构建会信任 localhost 页面，经隧道转发的 agent 页面也位于 localhost，在应用窗口中加载它，就等于允许它调用 Go 方法，包括解锁密钥和复活。
- 禁止设置 `TrustedOrigins: ["*"]`。应用窗口只加载自己的前端。
- 口令输入框不得把值写进任何前端状态持久化或日志中，提交后立即清空。
- 复活一旦进入交换阶段，关闭窗口或页面不得中断进行中的交换。服务层持有独立的 context，前端重新打开后，通过事件恢复显示进度。

## 7. 前端结构

```text
client/ui/src/
├── mygo.ts            # 生成文件，禁止手改
├── main.ts            # 入口与路由
├── state.ts           # 全局状态（订阅 events）
├── errors.ts          # UserError 解析
├── views/
│   ├── instances.ts   # 实例总览
│   ├── vault.ts       # 金库与快照
│   ├── revive.ts      # 复活向导
│   ├── relay.ts       # 登录中继
│   └── settings.ts    # 设置与信任根
└── components/        # 状态徽标（三态分离）、进度条、确认对话框等
```

单个手写 `.ts` 文件不得超过 400 行。`package.json` 中的 `typecheck` 和 `build:web` 脚本由本项目自己定义：`typecheck` 执行 `tsc --noEmit`，`build:web` 只构建前端。打包应用使用 `go tool mygo build`。

## 8. 终端 UI（M7a）

- **框架与版本**：Bubble Tea 生态，锁定 `charm.land/bubbletea/v2 v2.1.1`、`charm.land/bubbles/v2 v2.2.1`、`charm.land/lipgloss/v2 v2.0.6`（2026-10-31 的最新稳定版）。升级须经用户同意。
- **入口**：`valise tui`，与 CLI 同一个二进制；共用 `--config-dir`。
- **代码位置**：`client/ui/tui/`，按视图拆分（如 `model.go`、`instances.go`、`vault.go`、`revive.go`、`relay.go`、`kit.go`、`settings.go`、`styles.go`）。单个 `.go` 文件不得超过 400 行。TUI 只调用服务层，不直接发 RPC、不读写服务器文件。
- **耗时操作**：一律放进 `tea.Cmd` 在后台执行，界面显示进度；服务层回调（如复活的阶段、登录状态）转成消息送回界面。复活进入交换阶段后，界面上的"取消"不得中断交换。
- **需要终端的操作**：登录中继、口令输入等需要原始终端的步骤，用 Bubble Tea 的暂停机制把终端交还给对应的 CLI 流程，结束后恢复 TUI。口令不得保存在 TUI 的状态里，用完即弃。
- **确认**：所有会改动服务器的操作（封存、同步、复活、删除 prev、清理旧实例、接受降级提议、安装技能）都要有明确的确认步骤，文案与 CLI 一致；降级提议逐个确认。
- **错误**：服务层的 `UserError` 显示为"原因 + 建议"，技术细节默认折叠。
- **入口**：Web 入口一律用系统浏览器打开（与第 6 节相同）。
- **测试**：模型的状态迁移与渲染用单元测试覆盖（直接调用 `Update`/`View`，不依赖真实终端）；涉及服务器的部分用 fake-ssh 替身。
