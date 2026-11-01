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

	"valise/client/config"
	"valise/core/valise"
	"valise/ui/service"
)

// version is set at release build time with -ldflags "-X main.version=…".
var version = "0.0.0-dev"

const usage = `用法：valise [--config-dir 目录] [-v] <命令>

  doctor <服务器> [--json]                       检查服务器：架构、porter 版本、可用工具
  install <服务器> --profile <id> [--show-script] [--yes] [--full-access] [--with <可选步骤 id>]...
                                                 按官方方案安装 harness：先检测依赖、核对脚本哈希并展示，确认后在交互终端里执行
  native <服务器> --profile <id>                   打开 harness 自己的原生界面（独立的管理连接）
  enroll <服务器> [--profile id] [--id 实例] [--assign root:glob=类别]... [--report-json] [--accept]
                                                 登记 agent：展示登记报告，加 --accept 才写入实例记录
  porter deploy <服务器> --release <版本>         从 GitHub Release 下载、校验并部署 porter
  porter deploy <服务器> --local <文件>           上传本地构建的 porter
  porter check <服务器>                           检查服务器上的 porter 有没有新版本（更新用 porter deploy --release）
  key register <服务器> [--label 说明]            把本机钥匙登记到服务器（之后的逃生舱都能用它打开）
  seal <服务器> <实例> [--tier soul|soul-secrets|full|raw] [--reason 原因]
                                                 建立一个逃生舱（soul=轻装、soul-secrets=标准、full=完整、raw=原样）
  vault status <服务器> [--json]                   按周期列出逃生舱与云备份状态（不需要私钥）
  vault verify <服务器>                           在服务器上做不需要私钥的校验
  vault delete <服务器> <周期> [--yes]             删除服务器上一整个已结束的周期（先列出其他副本）
  target add <服务器> <名称> --rsync <主机:路径> [--auto 1d]
                                                 添加云备份位置（--auto：建立逃生舱后顺带同步的最短间隔）
  target auto <服务器> <名称> <间隔|off>            开关自动同步
  target list <服务器> <名称> [--json]              列出云备份上的周期，并与服务器对照
  target delete <服务器> <名称> <周期> [--yes]      删除云备份上一整个已结束的周期
  sync <服务器> <目标>                            把服务器金库同步到云备份
  pull <服务器> [--leaf 周期] [--to 目录]          把金库拉取到本机（默认全部周期）
  period <服务器> <实例> week|month|quarter [--tz 时区]
                                                 设置金库周期（从下一个周期开始生效）
  timer <服务器> <实例> <间隔|off|status>          定时建立逃生舱（systemd 用户定时器）
  verify --vault <目录> [--key]                   校验本机金库；--key 用私钥解密并逐个文件校验
  revive <服务器> --snapshot <id> [--mode rollback|migrate|clone] [--from <服务器>] [--id 新实例]
         [--root id=路径]... [--allow-draft-profile] [--allow-snapshot-profile] [--dry-run] [--yes]
                                                 逃生舱着陆（rollback=原地着陆、migrate=着陆到另一台服务器、clone=着陆成一个新的 agent）：先出计划，暂存校验后再确认交换
  revive resume <服务器> <op> --continue|--back   处理未完成的着陆
  prev list <服务器> | prev drop <服务器> <op>    查看 / 删除交换前保留的旧状态
  old list <服务器> | old cleanup <服务器> <实例>  查看 / 清理被迁移或克隆取代的旧实例
  tui                                            终端界面：服务器、agent、逃生舱、体检、管理页、登录授权、设置
  checkup <服务器> <实例> [--json]                 体检：会话、记忆文件、记忆机制（只读）
  proposals <服务器> <实例> [--accept id,…|all] [--accept-demotion id,…] [--reject id,…|all]
                                                 查看 / 确认 agent 提出的归类提议
  kit install <服务器> <实例>                      安装逃生舱技能，并给出接入 MCP 的方法
  kit status <服务器> <实例>                       查看技能安装状态与最近一次 agent 经 MCP 的调用
  kit uninstall <服务器> <实例> [--force]          删除逃生舱技能（被修改过的需 --force），并给出撤除 MCP 的方法
  profiles status                                查看 harness 资料包与本地 profile
  profiles install [--tag profiles-…] [--file <文件>] [--yes]
                                                 安装 harness 资料包（在线拉取最新版，或本地文件；未签名的只能作为本地 profile）
  server add <别名> --host <地址> [--port 22] [--user 用户] [--login password|key:<私钥路径>] [--admin-key]
         [--trust-fingerprint SHA256:…] [--name 名字] [--release 版本 | --local 文件]
                                                 手动添加服务器：确认身份 → 用密码登录一次装好日常钥匙 → 检查 → 部署 porter → 起名
  server admin-key <服务器>                        创建管理钥匙（单独的管理口令；需要用服务器密码登录一次）
  server migrate <别名> [--admin-key]              把 ~/.ssh/config 中的服务器改用 Valise 日常钥匙
  admin passphrase                               修改管理口令（重新加密所有管理钥匙）
  server setup <别名> [--name 名字] [--release 版本 | --local 文件]
                                                 用你自己的 SSH 设置添加服务器：检查 → 部署 porter（缺少时）→ 登记钥匙 → 起名 → 记录
  server name <别名> <名字>                       给服务器起名（空间站文件夹名，只能设一次）
  server list | add <别名>… | remove <别名> | import [<别名>…]
                                                 已记录服务器（add/import 记录 ~/.ssh/config 中的别名）
  login <服务器> <实例> [--method id]              登录授权：在本机浏览器中完成服务器上 agent 的登录
  open <服务器> <实例> [--entry id] [--idle 30m] [--no-browser]
         [--change-port [--to 端口] [--no-seal] [--yes]]   本机端口被占用时两边一起改端口
  action <服务器> <实例> [<动作>] [--yes] [--no-seal]
                                                 管理动作：不带动作名列出可用动作；机密结果只复制到剪贴板，20 秒后清除
  forward <服务器> <地址|端口> [--idle 30m] [--no-browser]
                                                 在本机打开服务器回环地址上的页面（同端口，地址里的令牌不显示）
                                                 打开管理页：经隧道转到本机，需要令牌时用 harness 自己的命令取得
  shell <服务器> [--profile id]                   打开服务器终端（系统 OpenSSH 的交互式登录 shell）；带 --profile 时该 harness 的命令直接可用
  key unregister <服务器> [--recipient age1…]     从服务器移除一把钥匙（以前的逃生舱仍可被它打开）
  key init                                       生成新的密钥（口令至少 12 个字符）
  key import --file <身份文件>                    导入已有的 age 身份
  key show                                       显示公钥与指纹
  key export-kit <目录>                           导出离线恢复套件
  key import-kit <目录>                           从离线恢复套件导入
  version                                        显示版本`

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
	c := &cli{in: in, out: out, errOut: errOut, verbose: *verbose, configDir: *configDir}
	if err == nil {
		c.svc, err = service.New(dirs, version)
	}
	if err == nil {
		c.svc.AdminAuth.Passphrase = c.adminPassphrase
		err = c.dispatch(ctx, args)
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
	case args[0] == "revive":
		return c.revive(ctx, args[1:])
	case args[0] == "prev" && len(args) > 1:
		return c.prev(ctx, args[1:])
	case args[0] == "old" && len(args) > 1:
		return c.old(ctx, args[1:])
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
	fmt.Fprintf(c.errOut, "失败：%s\n", ue.Reason)
	if ue.Suggestion != "" {
		fmt.Fprintf(c.errOut, "建议：%s\n", ue.Suggestion)
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
