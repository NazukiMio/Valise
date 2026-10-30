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
  porter deploy <服务器> --release <版本>         从 GitHub Release 下载、校验并部署 porter
  porter deploy <服务器> --local <文件>           上传本地构建的 porter
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
}

var errUsage = errors.New("usage")

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
	c := &cli{in: in, out: out, errOut: errOut, verbose: *verbose}
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
	case args[0] == "porter" && len(args) > 1 && args[1] == "deploy":
		return c.deploy(ctx, args[2:])
	case args[0] == "key" && len(args) > 1:
		return c.key(args[1], args[2:])
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
