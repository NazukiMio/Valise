# 桌面 UI 规范（mygo）

> 本文件在 M7 阶段使用。M1 至 M6 只需遵守第 1 节的原则：服务层不依赖 UI。

## 1. 分层原则

- 所有业务逻辑都写在 `client/ui` 的服务层（纯 Go）中，CLI 和桌面 UI 共用同一套服务层。**桌面 UI 只是一层很薄的绑定适配，没有它，CLI 也必须完整可用。**
- 这样做也是对框架风险的控制：mygo 是一个由个人维护的年轻项目。即使将来需要更换 UI 框架，也只需要重写适配层和前端，服务层不受影响。

## 2. 资料来源

- 官方文档：https://github.com/egoist/mygo/tree/main/docs ，阅读时以锁定 tag 下的版本为准。
- Go API：`go doc -all github.com/egoist/mygo`
- 开发机检查：`go tool mygo doctor`
- 不确定的 API 一律查阅文档或源码，**不要凭印象编写**。

## 3. 版本锁定与 CLI

- 锁定版本：`<M7 开工时填写>`（编写本文档时的最新 tag 为 v0.7.3，迭代很快）。锁定后，升级必须经过用户同意。
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
