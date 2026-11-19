// Command valise is the Valise CLI. All logic lives in the service layer.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"valise/client/config"
	"valise/core/valise"
	"valise/ui/service"
)

// version is set at release build time with -ldflags "-X main.version=…".
var version = "0.0.0-dev"

const usage = `用法：valise [--config-dir 目录] [-v] [--accept-risk] <命令>
  --accept-risk  没有放在别处的备份时，自己承担数据丢失风险继续这一次（会再确认）

开始使用与日常管理
  install <服务器> --profile <id> [--show-script] [--yes] [--advanced] [--with <可选步骤 id>]...
                                                 按官方方案安装 harness：先检测依赖、核对脚本哈希并展示，确认后在交互终端里执行
  native <服务器> --instance <id> [--session|--shell]  打开原生引导（默认）、原生会话（--session）或实例交互终端（--shell，独立的管理连接）
  enroll <服务器> [--profile id] [--name 命名实例] [--id 实例] [--assign root:glob=类别]... [--report-json] [--accept]
                                                 登记 agent：展示登记报告，加 --accept 才写入实例记录
  tui                                            终端界面：服务器、agent、备份、健康、管理页、登录授权、设置
  agent name <服务器> <实例> [名字]                 在 Valise 里给 agent 起名字；不写名字就跟随 harness 里的名字
  profiles status                                查看 harness 资料包与本地 profile
  profiles install [--tag profiles-…] [--file <文件>] [--yes]
                                                 安装 harness 资料包（在线拉取最新版，或本地文件；未签名的只能作为本地 profile）
  server add <别名> --host <地址> [--port 22] [--user 用户] [--login password|key:<私钥路径>] [--admin-key]
         [--trust-fingerprint SHA256:…] [--name 名字] [--release 版本 | --local 文件]
                                                 手动添加服务器：确认身份 → 用密码登录一次装好日常钥匙 → 检查 → 部署 porter → 起名
  server rekey <服务器> [--admin-key]              在这台电脑上重新安装日常钥匙（导入配置后、换电脑后；需要用服务器密码登录一次）
  server admin-key <服务器> [--remove]             创建管理钥匙（单独的管理口令；需要用服务器密码登录一次），或移除它
  server migrate <别名> [--admin-key]              把 ~/.ssh/config 中的服务器改用 Valise 日常钥匙
  server setup <别名> [--name 名字] [--release 版本 | --local 文件]
                                                 用你自己的 SSH 设置添加服务器：检查 → 部署 porter（缺少时）→ 登记钥匙 → 起名 → 记录
  server name <别名> <名字>                       给服务器起名（备份位置里的文件夹名）
  server rename <别名> <新名字> [--yes]            改服务器名：备份位置里的文件夹一并改名
  server list | add <别名>… | remove <别名> | import [<别名>…]
                                                 已记录服务器（add/import 记录 ~/.ssh/config 中的别名）
  login <服务器> <实例> [--method id]              登录授权：在本机浏览器中完成服务器上 agent 的登录
  open <服务器> <实例> [--entry id] [--idle 30m] [--no-browser]
         [--change-port [--to 端口] [--no-seal] [--yes]]   本机端口被占用时两边一起改端口
  forward <服务器> <地址|端口> [--idle 30m] [--no-browser]
                                                 在本机打开服务器回环地址上的页面（同端口，地址里的令牌不显示）
                                                 打开管理页：经隧道转到本机，需要令牌时用 harness 自己的命令取得
  shell <服务器> [--profile id]                   打开服务器终端（系统 OpenSSH 的交互式登录 shell）；带 --profile 时该 harness 的命令直接可用
  version                                        显示版本

备份与异地副本
  seal <服务器> <实例> [--tier soul|soul-secrets|full|raw] [--reason 原因] [--no-copy]
                                                 建立一个备份（soul=轻装、soul-secrets=标准、full=完整、raw=原样）；
                                                 之后在前台补到本机仓库与这台电脑登录的云盘，--no-copy 跳过
  vault status <服务器> [--source …] [--json]      按周期列出备份与备份位置状态（不需要私钥）；--source 只读副本，不需要服务器
  vault copies <服务器> <周期> [--packs] [--skip 位置,…] [--json]
                                                 核对一个周期在服务器、备份位置与本机仓库的副本（--packs 连数据包内容一起核对）
  vault delete <服务器> <周期> [--skip 位置,…] [--yes]  删除服务器上一整个已结束的周期（先核对其他各处副本；--skip 跳过的位置按未核对处理）
  target add <服务器> <名称> --rsync <主机:路径> [--auto 1d] [--force]
  target remove <服务器> <名称>                     移除服务器上不再使用的备份位置记录（不删除那里已有的备份；被保护计划使用时拒绝）
                                                 添加复制位置：先从服务器测试能否登录，连不上时不保存（--force 仍然保存；--auto：备份后顺带复制的最短间隔）
  target auto <服务器> <名称> <间隔|off>            开关自动同步
  target list <服务器> <名称> [--json]              列出备份位置上的周期，并与服务器对照
  target delete <服务器> <名称> <周期> [--skip 位置,…] [--yes]  删除备份位置上一整个已结束的周期（先核对其他各处副本）
  sync <服务器> <目标>                            把服务器备份库同步到备份位置
  cloud list                                     列出可以添加的云盘（经 rclone）
  cloud rclone                                   准备这台电脑的 rclone（PATH 中已有就直接用）
  cloud add <服务器> <名称> --cloud <id> [--bucket <桶名>] [--headless] [--allow-pending]
                                                 在服务器上登录云盘并保存为备份位置（授权留在服务器的 rclone 配置里）
  cloud add --here <名称> --cloud <id> [--bucket <桶名>] [--allow-pending]
                                                 在这台电脑上登录云盘（复制经这台电脑中转，只在 Valise 打开时进行）
  cloud remove <服务器> <名称> | --here <名称>       移除云盘位置与登录（网盘里的备份不删除）
  pull <服务器> [--leaf 周期] [--to 目录]          把备份库拉取到本机（默认全部周期）
  period <服务器> <实例> week|month|quarter [--tz 时区]
                                                 设置备份分组（从下一组开始生效）
  timer <服务器> <实例> <间隔|off|status>          定时备份（systemd 用户定时器）
  station add <名称> --address <地址> --user <用户> --path <路径> [--port 22] [--login password|key:<私钥>] [--any-source]
  station list | station remove <名称>               Valise 管理的远程备份位置（服务器连不上时经这台电脑中转）
  repo add <名称> <文件夹> | repo list | repo remove <名称>    本机仓库（备份另存到本机文件夹）
  repo sync <服务器> [名称]                          把服务器上的备份补到仓库（不指定名称时同步全部仓库）

恢复与迁移
  revive <服务器> --snapshot <id> [--mode rollback|migrate|clone] [--from <服务器>] [--id 新实例]
         [--source repo:<仓库>|dir:<文件夹>|cloud:<云盘>] [--root id=路径]... [--allow-draft-profile] [--allow-snapshot-profile]
         [--backup-first] [--dry-run] [--yes]
                                                 恢复（rollback=恢复当前实例、migrate=迁移到另一台服务器、clone=创建副本（新的 agent））：先出计划，暂存校验后再确认交换；
                                                 --source 从副本读取备份（来源服务器连不上时用；迁移时不停止它、不带独占凭据）
  revive resume <服务器> <op> --continue|--back   处理未完成的恢复
  revive lost [--settle <op>]                    列出来源连不上时做的迁移；来源又连上且仍有该实例时提醒；处理好后记为完毕
  export <服务器> <备份> --to <空文件夹> [--source …] [--include-secrets] [--json]
                                                 把一个备份解密成普通文件放到这台电脑（不启动；默认不含凭据）
  prev list <服务器> | prev drop <服务器> <op>    查看 / 删除交换前保留的旧状态
  old list <服务器> | old cleanup <服务器> <实例>  查看 / 清理被迁移或克隆取代的旧实例
  restore-test <服务器> <实例> [--snapshot id]     恢复测试：两边各解开一次备份并核对，不切换，过后删除

检查与修复
  doctor <服务器> [--json]                       检查服务器：架构、porter 版本、可用工具
  porter check <服务器>                           检查服务器上的 porter 有没有新版本（更新用 porter deploy --release）
  vault verify <服务器>                           在服务器上做不需要私钥的校验
  verify --vault <目录> [--key]                   校验本机备份库；--key 用私钥解密并逐个文件校验
  checkup <服务器> <实例> [--json]                 会话与记忆检查（只读）
  health <服务器> <实例> [--json]                  健康：备份、恢复测试、服务、定时备份、审批（只读）
  protection resolve <服务器> <实例> --verify|--rollback
                                                 确认一次没完成的保护设置修改：核实后完成，或放弃并恢复旧设置（只在新设置写入前可放弃）
  protection copies <服务器> <实例> [--repo 名 [--after-backup on|off] [--on-open on|off]]...
                                                 显示或修改这台电脑何时把该实例的备份补到本机仓库
  health fix <服务器> <实例> <linger|clock> [--advanced] [--yes] [--json]
                                                 修复 linger 或服务器时钟：先列出要执行的命令与改动，再次同意后执行并复核
  proposals <服务器> <实例> [--accept id,…|all] [--accept-demotion id,…] [--reject id,…|all]
                                                 查看 / 确认 agent 提出的归类提议
  action <服务器> <实例> [<动作>] [--yes] [--no-seal]
                                                 管理动作：不带动作名列出可用动作；机密结果只复制到剪贴板，20 秒后清除
  action status <服务器> <操作>                    查看配置操作的分项结果
  action resume <服务器> <操作> --continue|--restore-services [--yes]
                                                 继续发布已复核的暂存配置，或恢复原服务状态（结果不明的步骤不重跑）
  action cleanup <服务器> <操作> [--yes]           列出残留目录，确认后清理；保留操作记录

钥匙、工具与设置
  porter deploy <服务器> --release <版本>         从 GitHub Release 下载、校验并部署 porter
  porter deploy <服务器> --local <文件>           上传本地构建的 porter
  key register <服务器> [--label 说明]            把本机钥匙登记到服务器（之后的备份都能用它打开）
  kit install <服务器> <实例>                      安装 Agent 保护技能，并给出接入 MCP 的方法
  kit status <服务器> <实例>                       查看技能安装状态与最近一次 agent 经 MCP 的调用
  kit uninstall <服务器> <实例> [--force]          删除 Agent 保护技能（被修改过的需 --force），并给出撤除 MCP 的方法
  kit tools <服务器>                               Kit 工具：这台服务器上有没有、用途、不装会怎样
  kit tool install|uninstall <服务器> <名称> [--yes]   安装或卸载 Kit 工具（增强级要 Valise 口令；专家级要高级模式）
  config export <文件> | config import <文件>       导出 / 导入 Valise 配置：服务器列表、主机指纹、设置（不含任何钥匙）
  config red-dismiss-days <天数>                  忽略红色健康提醒后多久再在主页提醒（30–365，默认 30）
  config detach on|off                           分离模式：服务器支持时按计划备份在服务器后台执行（默认关闭）
  config takeover <服务器> on|off                 接管模式：终端界面里用 sudo -n 直接执行需要 sudo 的修复（默认关闭）
  admin passphrase                               修改管理口令（重新加密所有管理钥匙）
  key unregister <服务器> [--recipient age1…]     从服务器移除一把钥匙（以前的备份仍可被它打开）
  key init                                       生成新的密钥（口令至少 12 个字符）
  key import --file <身份文件>                    导入已有的 age 身份
  key show                                       显示公钥与指纹
  key export-kit <目录>                           导出离线恢复套件
  key import-kit <目录>                           从离线恢复套件导入`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type cli struct {
	svc     *service.Service
	in      io.Reader
	out     io.Writer
	errOut  io.Writer
	verbose bool
	lines   *bufio.Reader
	// configDir is the --config-dir given on the command line ("" for the
	// default), passed on to CLI steps the TUI runs.
	configDir string
	// acceptRisk: --accept-risk, go on without an escape pod elsewhere
	// (docs/INSTALL.md 4.2, "自担风险继续").
	acceptRisk bool
}

var (
	errUsage    = errors.New("usage")
	errProblems = errors.New("verification problems")
)

func run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("valise", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configDir := fs.String("config-dir", "", "")
	verbose := fs.Bool("v", false, "")
	acceptRisk := fs.Bool("accept-risk", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() == 0 {
		fmt.Fprintln(errOut, usage)
		return 2
	}
	args = fs.Args()
	if args[0] == "version" {
		fmt.Fprintf(out, "valise %s\n", version)
		return 0
	}
	dirs, err := config.DefaultDirs()
	if *configDir != "" {
		dirs, err = config.Dirs{Data: *configDir, Cache: filepath.Join(*configDir, "cache")}, nil
	}
	c := &cli{in: in, out: out, errOut: errOut, verbose: *verbose, configDir: *configDir,
		acceptRisk: *acceptRisk || os.Getenv(service.RiskAcceptedEnv) == "1"}
	if err == nil {
		c.svc, err = service.New(dirs, version)
	}
	if err == nil {
		c.svc.AdminAuth.Passphrase = c.adminPassphrase
		c.svc.OwnerAuth.Passphrase = c.ownerPassphrase
		c.svc.OwnerAuth.PIN = func(string) (string, error) { return c.passphrase("本机 6 位数字 PIN 码：") }
		err = c.dispatch(ctx, args)
		// A normal exit lets in-flight work reach a safe boundary; a detached
		// server backup goes on without this process.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		_ = c.svc.Shutdown(sctx)
		cancel()
		c.svc.Close()
	}
	return c.report(err)
}

func (c *cli) dispatch(ctx context.Context, args []string) error {
	switch {
	case args[0] == "doctor":
		return c.doctor(ctx, args[1:])
	case args[0] == "enroll":
		return c.enroll(ctx, args[1:])
	case args[0] == "porter" && len(args) > 1 && args[1] == "deploy":
		return c.deploy(ctx, args[2:])
	case args[0] == "porter" && len(args) > 1 && args[1] == "check":
		return c.porterCheck(ctx, args[2:])
	case args[0] == "export":
		return c.export(ctx, args[1:])
	case args[0] == "revive":
		return c.revive(ctx, args[1:])
	case args[0] == "prev" && len(args) > 1:
		return c.prev(ctx, args[1:])
	case args[0] == "protection" && len(args) > 1 && args[1] == "resolve":
		return c.protectionResolve(ctx, args[2:])
	case args[0] == "protection" && len(args) > 1 && args[1] == "copies":
		return c.protectionCopies(ctx, args[2:])
	case args[0] == "old" && len(args) > 1:
		return c.old(ctx, args[1:])
	case args[0] == "agent" && len(args) > 1 && args[1] == "name":
		return c.agentName(ctx, args[2:])
	case args[0] == "health":
		return c.health(ctx, args[1:])
	case args[0] == "restore-test":
		return c.restoreTest(ctx, args[1:])
	case args[0] == "checkup":
		return c.checkup(ctx, args[1:])
	case args[0] == "proposals":
		return c.proposals(ctx, args[1:])
	case args[0] == "tui" && len(args) == 1:
		return c.tui(ctx)
	case args[0] == "profiles":
		return c.profilesCmd(ctx, args[1:])
	case args[0] == "server":
		return c.server(ctx, args[1:])
	case args[0] == "config" && len(args) == 4 && args[1] == "takeover":
		return c.configTakeover(args[2], args[3])
	case args[0] == "config" && len(args) == 3:
		return c.configCmd(args[1], args[2])
	case args[0] == "admin":
		return c.admin(ctx, args[1:])
	case args[0] == "install":
		return c.install(ctx, args[1:])
	case args[0] == "native":
		return c.native(ctx, args[1:])
	case args[0] == "period":
		return c.period(ctx, args[1:])
	case args[0] == "timer":
		return c.timer(ctx, args[1:])
	case args[0] == "shell":
		return c.shell(ctx, args[1:])
	case args[0] == "kit":
		return c.kit(ctx, args[1:])
	case args[0] == "cloud":
		return c.cloud(ctx, args[1:])
	case args[0] == "login":
		return c.login(ctx, args[1:])
	case args[0] == "open":
		return c.open(ctx, args[1:])
	case args[0] == "action":
		return c.action(ctx, args[1:])
	case args[0] == "forward":
		return c.forward(ctx, args[1:])
	case args[0] == "key" && len(args) > 1 && args[1] == "unregister":
		return c.keyUnregister(ctx, args[2:])
	case args[0] == "key" && len(args) > 1 && args[1] == "register":
		return c.keyRegister(ctx, args[2:])
	case args[0] == "key" && len(args) > 1:
		return c.key(args[1], args[2:])
	case args[0] == "seal":
		return c.seal(ctx, args[1:])
	case args[0] == "vault" && len(args) > 1:
		return c.vaultCmd(ctx, args[1:])
	case args[0] == "target" && len(args) > 1:
		return c.target(ctx, args[1:])
	case args[0] == "sync":
		return c.sync(ctx, args[1:])
	case args[0] == "station" && len(args) > 1:
		return c.station(ctx, args[1:])
	case args[0] == "repo" && len(args) > 1:
		return c.repo(ctx, args[1:])
	case args[0] == "pull":
		return c.pull(ctx, args[1:])
	case args[0] == "verify":
		return c.verify(args[1:])
	}
	return errUsage
}

func (c *cli) report(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, errUsage) {
		fmt.Fprintln(c.errOut, usage)
		return 2
	}
	if errors.Is(err, errProblems) {
		return 1
	}
	c.printProblem(err)
	return 1
}

func (c *cli) printProblem(err error) {
	ue, ok := valise.As(err)
	if !ok {
		ue = valise.New("unexpected", "发生了未预期的错误", "加上 -v 查看细节，并向项目报告").WithDetail(err.Error())
	}
	fmt.Fprintf(c.errOut, "失败：%s\n", service.PlainWording(ue.Reason))
	if ue.Suggestion != "" {
		fmt.Fprintf(c.errOut, "建议：%s\n", service.PlainWording(ue.Suggestion))
	}
	if c.verbose && ue.Detail != "" {
		fmt.Fprintf(c.errOut, "细节：\n%s\n", ue.Detail)
	} else if ue.Detail != "" {
		fmt.Fprintln(c.errOut, "（加上 -v 查看技术细节）")
	}
}

// parseArgs parses flags that may appear before or after one positional
// argument, e.g. `doctor host --json`.
func parseArgs(fs *flag.FlagSet, args []string, positional int) ([]string, error) {
	return parseArgsRange(fs, args, positional, positional)
}

// parseArgsRange accepts between min and max positional arguments.
func parseArgsRange(fs *flag.FlagSet, args []string, min, max int) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, errUsage
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(pos) < min || len(pos) > max {
		return nil, errUsage
	}
	return pos, nil
}

func (c *cli) printJSON(v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
