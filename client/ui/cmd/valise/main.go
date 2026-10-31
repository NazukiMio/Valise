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
  enroll <服务器> [--profile id] [--id 实例] [--assign root:glob=类别]... [--report-json] [--accept]
                                                 登记 agent：展示登记报告，加 --accept 才写入实例记录
  porter deploy <服务器> --release <版本>         从 GitHub Release 下载、校验并部署 porter
  porter deploy <服务器> --local <文件>           上传本地构建的 porter
  key register <服务器> [--label 说明]            把本机公钥登记为服务器的封存收件人（说明写成注释）
  seal <服务器> <实例> [--tier soul|soul-secrets|full|raw] [--reason 原因]
                                                 封存一次
  vault status <服务器> [--json]                   列出快照与同步状态（不需要私钥）
  vault verify <服务器>                           在服务器上做不需要私钥的校验
  target add <服务器> <名称> --rsync <主机:路径>     添加异地同步目标
  sync <服务器> <目标>                            把服务器金库同步到异地
  pull <服务器> [--to 目录]                       把金库拉取到本机
  verify --vault <目录> [--key]                   校验本机金库；--key 用私钥解密并逐个文件校验
  revive <服务器> --snapshot <id> [--mode rollback|migrate|clone] [--from <服务器>] [--id 新实例]
         [--root id=路径]... [--allow-draft-profile] [--allow-snapshot-profile] [--dry-run] [--yes]
                                                 复活：先出计划，暂存校验后再确认交换
  revive resume <服务器> <op> --continue|--back   处理未完成的复活操作
  prev list <服务器> | prev drop <服务器> <op>    查看 / 删除交换前保留的旧状态
  old list <服务器> | old cleanup <服务器> <实例>  查看 / 清理被迁移或克隆取代的旧实例
  tui                                            终端界面：服务器与实例总览、封存、复活、登录中继、逃生舱技能、设置
  checkup <服务器> <实例> [--json]                 体检：会话、记忆文件、记忆机制（只读）
  proposals <服务器> <实例> [--accept id,…|all] [--accept-demotion id,…] [--reject id,…|all]
                                                 查看 / 确认 agent 提出的归类提议
  kit install <服务器> <实例>                      安装逃生舱技能，并给出接入 MCP 的方法
  kit status <服务器> <实例>                       查看技能安装状态与最近一次 agent 经 MCP 的调用
  kit uninstall <服务器> <实例> [--force]          删除逃生舱技能（被修改过的需 --force），并给出撤除 MCP 的方法
  server list | add <别名>… | remove <别名> | import [<别名>…]
                                                 已记录服务器（只存 SSH 别名；import 从 ~/.ssh/config 选择）
  login <服务器> <实例> [--method id]              登录中继：在本机浏览器中完成服务器上的登录
  open <服务器> <实例> [--entry id] [--idle 30m] [--no-browser]
                                                 Web 入口：把管理页面经隧道转到本机并打开
  key unregister <服务器> [--recipient age1…]     移除收件人公钥（旧快照仍可被它解开）
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
		err = c.dispatch(ctx, args)
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
	case args[0] == "server":
		return c.server(args[1:])
	case args[0] == "kit":
		return c.kit(ctx, args[1:])
	case args[0] == "login":
		return c.login(ctx, args[1:])
	case args[0] == "open":
		return c.open(ctx, args[1:])
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
	fmt.Fprintf(c.errOut, "失败：%s\n建议：%s\n", ue.Reason, ue.Suggestion)
	if c.verbose && ue.Detail != "" {
		fmt.Fprintf(c.errOut, "细节：\n%s\n", ue.Detail)
	} else if ue.Detail != "" {
		fmt.Fprintln(c.errOut, "（加上 -v 查看技术细节）")
	}
}

// parseArgs parses flags that may appear before or after one positional
// argument, e.g. `doctor host --json`.
func parseArgs(fs *flag.FlagSet, args []string, positional int) ([]string, error) {
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
	if len(pos) != positional {
		return nil, errUsage
	}
	return pos, nil
}

func (c *cli) printJSON(v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
