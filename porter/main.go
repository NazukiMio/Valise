// Command valise-porter is the server-side porter. It is started on demand
// by the system OpenSSH and never listens on any port.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata" // period time zones (docs/VAULT_FORMAT.md 10.2) on servers without zoneinfo
	"valise/porter/internal/recovery"

	"valise/core/identity"
	"valise/core/login"
	"valise/core/profile"
	"valise/core/protection"
	corerevive "valise/core/revive"
	"valise/core/rpc"
	"valise/core/vaultrpc"
	"valise/porter/internal/backuprun"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/forced"
	porterinstall "valise/porter/internal/install"
	"valise/porter/internal/kit"
	porterlogin "valise/porter/internal/login"
	"valise/porter/internal/mcp"
	porterprotection "valise/porter/internal/protection"
	porterrevive "valise/porter/internal/revive"
	"valise/porter/internal/seal"
	"valise/porter/internal/server"
	"valise/porter/internal/sysfix"
	"valise/porter/internal/timer"
)

// version is set at release build time with -ldflags "-X main.version=…".
var version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `用法：
  valise-porter version [--json]
  valise-porter rpc
  valise-porter exec [--path <目录>]... -- <命令>...（用登录环境执行原生界面、安装步骤）
  valise-porter exec [--path <目录>]... --shell（打开用户自己的交互 shell，PATH 中加入这些目录）
  valise-porter path-add --dir <目录> --label <标签>（把家目录下的目录加入登录 shell 的 PATH）
  valise-porter action-exec --instance <实例> --id <动作>（在当前终端运行登记过的、需要终端的管理动作）
  valise-porter serve --tier daily           （日常钥匙的强制命令）
  valise-porter seal-timer --instance <实例>（旧定时器）
  valise-porter protection-timer --instance <实例>（已接受保护计划的定时器）
  valise-porter task-run --id <运行标识>（一次性 systemd 单元执行已接受的备份运行记录）
  valise-porter autosync                    （由封存后自动启动，同步到期的备份位置）
  valise-porter vault-cat <周期> <备份库内的相对路径>
  valise-porter stage-write --op <op_id>
  valise-porter tool-put <名称> <sha256>
  valise-porter recovery-code --for <用户>（用 sudo 运行：找回 Valise 口令）
  valise-porter login-run <会话> [<列数> <行数>]
  valise-porter browser-shim <链接>...
  valise-porter mcp --instance <实例>`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "rpc":
		return serveRPC(stdin, stdout, stderr, "")
	case "serve":
		return serveForced(args[1:], stdin, stdout, stderr)
	case "exec":
		// Admin connections only: the daily key's forced command refuses it.
		argv := args[1:]
		var dirs []string
		for len(argv) >= 2 && argv[0] == "--path" {
			dirs, argv = append(dirs, argv[1]), argv[2:]
		}
		if len(argv) > 0 && argv[0] == "--" {
			argv = argv[1:]
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		unlock, err := (seal.Paths{Home: home}).LockFor("native")
		if err != nil {
			fmt.Fprintln(stderr, "exec:", err)
			return 1
		}
		defer unlock()
		ctx := context.Background()
		env := porterenroll.LoadEnv(ctx, home).For(&profile.Profile{Detect: &profile.Detect{BinDirs: dirs}})
		if len(argv) == 1 && argv[0] == "--shell" {
			// The user's own interactive shell, not a login shell: a login
			// shell's /etc/profile would reset PATH.
			sh := os.Getenv("SHELL")
			if sh == "" {
				sh = "/bin/sh"
			}
			argv = []string{sh, "-i"}
		}
		// Run as a recorded child, so a program left running after the
		// connection dropped can be found and ended (review F05).
		code, err := porterinstall.RunTracked(env, argv)
		if err != nil {
			fmt.Fprintln(stderr, "exec:", err)
		}
		return code
	case "action-exec":
		// Admin connections only: a terminal action from the instance's
		// stored profile, in the caller's terminal (docs/INSTALL.md 5).
		if len(args) != 5 || args[1] != "--instance" || args[3] != "--id" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		prof, err := porterenroll.ReadProfile(home, args[2])
		if err != nil {
			fmt.Fprintln(stderr, "action-exec:", err)
			return 1
		}
		a := prof.FindAction(args[4])
		if a == nil || !a.Terminal {
			fmt.Fprintln(stderr, "action-exec: 服务器上登记的 profile 里没有这个需要终端的动作")
			return 1
		}
		unlock, err := (seal.Paths{Home: home}).LockFor("terminal-action")
		if err != nil {
			fmt.Fprintln(stderr, "action-exec:", err)
			return 1
		}
		defer unlock()
		seal.Paths{Home: home}.AppendAudit(seal.AuditEntry{Time: time.Now().UTC(), Op: "action", Instance: args[2],
			Initiator: vaultrpc.InitiatorClient, Result: "started in terminal", Reason: fmt.Sprintf("%s (%s)", a.ID, a.Risk)})
		ctx := context.Background()
		code, err := porterinstall.RunTracked(porterenroll.LoadEnv(ctx, home).For(prof), a.Argv)
		if err != nil {
			fmt.Fprintln(stderr, "action-exec:", err)
		}
		return code
	case "path-add":
		// Admin connections only (an install step the user opted in to).
		if len(args) != 5 || args[1] != "--dir" || args[3] != "--label" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		file, lines, added, err := porterinstall.PathAdd(home, os.Getenv("SHELL"), args[2], args[4])
		if err != nil {
			fmt.Fprintln(stderr, "path-add:", err)
			return 1
		}
		if added {
			fmt.Fprintf(stdout, "已在 %s 末尾加入：\n", file)
		} else {
			fmt.Fprintf(stdout, "%s 里已经有这段设置，没有重复添加：\n", file)
		}
		for _, l := range lines {
			fmt.Fprintf(stdout, "  %s\n", l)
		}
		fmt.Fprintln(stdout, "下次登录时生效；想撤销，删除这两行即可。")
		return 0
	case "stage-write":
		if len(args) != 3 || args[1] != "--op" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		n, err := porterrevive.StageWrite(porterrevive.Store{Home: home}, args[2], stdin)
		if err != nil {
			fmt.Fprintln(stderr, "stage-write:", err)
			return 1
		}
		fmt.Fprintf(stdout, "%d\n", n)
		return 0
	case "recovery-code":
		if len(args) != 3 || args[1] != "--for" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		code, err := recovery.Run(args[2])
		if err != nil {
			fmt.Fprintln(stderr, "recovery-code:", err)
			return 1
		}
		fmt.Fprintf(stdout, "验证码：%s %s\n在 Valise 里输入这 8 位数字。\n", code[:4], code[4:])
		return 0
	case "tool-put":
		if len(args) != 3 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := kit.ToolPut(home, args[1], args[2], stdin); err != nil {
			fmt.Fprintln(stderr, "tool-put:", err)
			return 1
		}
		return 0
	case "login-run":
		if len(args) != 2 && len(args) != 4 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		self, err := os.Executable()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		// Exec only returns on failure.
		cols, rows := 0, 0
		if len(args) == 4 {
			cols, _ = strconv.Atoi(args[2])
			rows, _ = strconv.Atoi(args[3])
		}
		fmt.Fprintln(stderr, "login-run:", porterlogin.Exec(home, args[1], self, cols, rows))
		return 1
	case "mcp":
		if len(args) != 3 || args[1] != "--instance" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		var env *porterenroll.Env
		t := &mcp.Tools{Home: home, Instance: args[2], Version: version, Env: func(ctx context.Context) *porterenroll.Env {
			if env == nil {
				env = porterenroll.LoadEnv(ctx, home)
			}
			return env
		}}
		if err := mcp.Serve(context.Background(), stdin, stdout, t); err != nil {
			fmt.Fprintln(stderr, "mcp:", err)
			return 1
		}
		return 0
	case "browser-shim":
		home, _ := os.UserHomeDir()
		if err := porterlogin.Shim(home, os.Getenv(login.SessionEnv), args[1:]); err != nil {
			fmt.Fprintln(stderr, "browser-shim:", err)
			return 1
		}
		return 0
	case "protection-timer":
		if len(args) != 3 || args[1] != "--instance" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return 1
		}
		ctx := context.Background()
		env := porterenroll.LoadEnv(ctx, home)
		validate := func(ctx context.Context, ref identity.AgentRef) error {
			return porterenroll.ValidateIdentity(ctx, home, ref)
		}
		plan, err := (porterprotection.Store{Home: home, Env: env, ValidateAgent: validate}).ReadAccepted(ctx, args[2])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		id, err := identity.NewID()
		if err != nil {
			return 1
		}
		run, err := (backuprun.Store{Home: home, Env: env, ValidateAgent: validate}).Run(ctx, protection.BackupParams{AgentRef: plan.AgentRef, ExpectedRevision: plan.Revision, RunID: id, Reason: "timer"})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if run.State != "succeeded" {
			fmt.Fprintln(stderr, "定时备份尚未全部完成，请查询运行记录核实")
			return 1
		}
		return 0
	case "task-run":
		// Started only by systemd-run for an accepted run: it reads the
		// frozen request from the run record and takes no other input.
		if len(args) != 3 || args[1] != "--id" || !protection.ValidID(args[2]) {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return 1
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		env := porterenroll.LoadEnv(ctx, home)
		validate := func(ctx context.Context, ref identity.AgentRef) error {
			return porterenroll.ValidateIdentity(ctx, home, ref)
		}
		run, err := (backuprun.Store{Home: home, Env: env, ValidateAgent: validate}).Execute(ctx, args[2])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		switch run.State {
		case "succeeded", "cancelled":
			return 0
		case "accepted", "running":
			// Another live executor owns this run; it is never replayed.
			return 0
		}
		fmt.Fprintln(stderr, "备份运行尚未全部完成，请查询运行记录核实")
		return 1
	case "seal-timer":
		if len(args) != 3 || args[1] != "--instance" {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return 1
		}
		ctx := context.Background()
		if err := timer.Seal(ctx, porterenroll.LoadEnv(ctx, home), home, args[2]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "autosync":
		home, err := os.UserHomeDir()
		if err != nil {
			return 1
		}
		ctx := context.Background()
		if err := seal.AutoSync(ctx, porterenroll.LoadEnv(ctx, home), seal.Paths{Home: home}); err != nil {
			return 1
		}
		return 0
	case "vault-cat":
		if len(args) != 3 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		f, err := seal.OpenVaultFile(seal.Paths{Home: home}, args[1], args[2])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer f.Close()
		if _, err := io.Copy(stdout, f); err != nil {
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

// serveRPC answers JSON-RPC on stdio at the given tier ("" is a full login).
func serveRPC(stdin io.Reader, stdout, stderr io.Writer, tier rpc.Tier) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(stderr, "无法确定 HOME：", err)
		return 1
	}
	cfg := server.Config{Version: version, Home: home, Tier: tier}
	if err := server.Serve(context.Background(), stdin, stdout, cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// serveForced is the daily key's forced command (docs/ARCHITECTURE.md 2.1):
// it runs only daily subcommands named in SSH_ORIGINAL_COMMAND, and rpc at
// the daily tier.
func serveForced(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "--tier" || args[1] != string(rpc.TierDaily) {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	words, err := forced.Daily(os.Getenv("SSH_ORIGINAL_COMMAND"))
	if err != nil {
		fmt.Fprintln(stderr, err.Error()+"；需要管理权限的操作请用管理钥匙或服务器密码登录。")
		return 126
	}
	if at, ok := forced.GrantSubcommands[words[0]]; ok {
		args, token, has := forced.TakeGrant(words)
		home, err := os.UserHomeDir()
		if !has || err != nil || len(args) <= at || forced.Consume(home, token, args[0], args[at]) != nil {
			fmt.Fprintln(stderr, forced.ErrGrant)
			return 126
		}
		return run(args, stdin, stdout, stderr)
	}
	if words[0] == "rpc" {
		if len(words) != 1 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		return serveRPC(stdin, stdout, stderr, rpc.TierDaily)
	}
	return run(words, stdin, stdout, stderr)
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 1 || (len(args) == 1 && !asJSON) {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if !asJSON {
		fmt.Fprintf(stdout, "valise-porter %s (protocol %s, %s/%s)\n", version, rpc.ProtocolVersion, runtime.GOOS, runtime.GOARCH)
		return 0
	}
	sum, err := selfSHA256()
	if err != nil {
		fmt.Fprintln(stderr, "无法读取自身文件：", err)
		return 1
	}
	info := rpc.VersionInfo{
		Name: "valise-porter", Version: version, Protocol: rpc.ProtocolVersion,
		OS: runtime.GOOS, Arch: runtime.GOARCH, SelfSHA256: sum,
		Capabilities: []string{identity.Capability, identity.ListCapability, protection.Capability, protection.CapabilityV2, protection.BackupCapability, protection.BackupCapabilityV2, sysfix.Capability, vaultrpc.CopyCheckCapability, vaultrpc.CopyVerifyCapability, vaultrpc.CloudCapability, identity.LostSourceCapability, corerevive.BodyDirsCapability},
	}
	if err := json.NewEncoder(stdout).Encode(info); err != nil {
		return 1
	}
	return 0
}

// selfSHA256 hashes the running executable so deploy can confirm the
// uploaded file is intact before it replaces the old porter.
func selfSHA256() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	f, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
