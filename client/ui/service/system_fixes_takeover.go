package service

import (
	"context"
	"slices"
	"strings"

	"valise/client/config"
	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/valise"
)

// Takeover mode and destructive steps (docs/SERVICE_CONTRACTS.md
// 16.F2 and 16.F3). With takeover on for a server, the terminal UI runs a fix
// that needs sudo itself, after the user turned advanced mode on there
// (both passphrases and the consent sentence, INSTALL.md 4.1), and only with
// sudo -n: Valise never collects sudo's password; a server that asks for it
// is sent to the terminal. Off, sudo fixes go to the terminal as before.

// takeoverAccess is the confirmation's line on how the steps run.
const takeoverAccess = "接管模式：每条命令新开一次管理登录，用 sudo -n 在这里执行；执行前先用 sudo -n true 确认这台服务器允许免密 sudo。需要 sudo 密码时不会在这里询问，会给出在终端里执行的命令"

// SudoLabel marks every sudo command on the takeover confirmation.
const SudoLabel = "sudo 命令（以管理员身份运行）"

// DestructiveLabel marks a destructive step on a confirmation.
const DestructiveLabel = "破坏性：会删除或覆盖数据，不能简单撤销；执行前会检查别处是否有能独立恢复的备份"

// Takeover reports whether takeover mode is on for host (default off).
func (s *Service) Takeover(host string) bool { return host != "" && s.Config.Takeover[host] }

// SetTakeover turns takeover mode on or off for one recorded server.
func (s *Service) SetTakeover(host string, on bool) error {
	if list, err := s.Servers(); err != nil {
		return err
	} else if !slices.Contains(list, host) {
		return valise.New("server_unknown", host+" 不在已记录的服务器中", "valise server list 查看已记录的服务器")
	}
	if err := config.SetTakeover(s.Dirs.Data, host, on); err != nil {
		return err
	}
	if s.Config.Takeover == nil {
		s.Config.Takeover = map[string]bool{}
	}
	if on {
		s.Config.Takeover[host] = true
	} else {
		delete(s.Config.Takeover, host)
	}
	return nil
}

// Destructive reports whether the plan has a destructive step.
func (f *SystemFix) Destructive() bool { return hasDestructive(f.Steps) }

func hasDestructive(steps []SystemFixStep) bool {
	for _, st := range steps {
		if st.Destructive {
			return true
		}
	}
	return false
}

// IsSudo reports whether a step's command runs through sudo.
func IsSudo(argv []string) bool { return len(argv) > 0 && argv[0] == "sudo" }

// destructiveGate is the INSTALL.md 4.2 precondition (an escape pod kept
// elsewhere, for every agent on the server), checked only when a step is
// destructive; non-destructive fixes are not held back by it.
func (s *Service) destructiveGate(ctx context.Context, host string, steps []SystemFixStep) error {
	if !hasDestructive(steps) {
		return nil
	}
	return s.RequireGate(ctx, host, "")
}

// TerminalFixCommand is the CLI command that runs the fix in a terminal,
// where sudo can ask for its password.
func TerminalFixCommand(f *SystemFix) string {
	return "valise health fix " + ssh.Quote(f.Host) + " " + ssh.Quote(f.Instance) + " " + f.ID + " --advanced"
}

// sudoNeedsPassword fills the result when sudo -n refused to run.
func (s *Service) sudoNeedsPassword(f *SystemFix, res *SystemFixResult) {
	res.SudoPassword = true
	res.Terminal = TerminalFixCommand(f)
	res.Next = "这台服务器的 sudo 需要密码；Valise 不在终端界面里收集 sudo 密码。到终端里执行：" + res.Terminal + "（sudo 密码在终端里输入，Valise 不读取、不保存），或者自己在服务器上执行：" + joinCommands(f.Manual)
}

// sudoWithoutPassword runs `sudo -n true` on a fresh admin connection.
func (s *Service) sudoWithoutPassword(ctx context.Context, host string) (bool, error) {
	code, _, err := s.runSudoN(ctx, host, sudo(asSudoN, "true"))
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// runSudoN runs one sudo -n command on a fresh admin connection without a
// terminal, in advanced mode only. askedPassword is set when sudo said it
// needs a password.
func (s *Service) runSudoN(ctx context.Context, host string, argv []string) (code int, askedPassword bool, err error) {
	if len(argv) < 2 || argv[0] != "sudo" || argv[1] != "-n" {
		return -1, false, valise.New("system_fix_sudo", "接管模式只用 sudo -n 执行命令："+strings.Join(argv, " "), "")
	}
	if s.HasAdminKey(host) {
		if _, err := s.unlockAdmin(ctx, host); err != nil {
			return -1, false, err
		}
	}
	if !s.advancedOn(host) {
		return -1, false, valise.New("advanced_off", "高级模式已经关闭，后面的命令没有执行", "")
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return -1, false, err
	}
	defer done()
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return -1, false, err
	}
	r, err := t.Run(ctx, execLine(found.Path, nil, argv), nil)
	if err != nil {
		return -1, false, err
	}
	if r.ExitCode == ssh.ExitConnFailed {
		return r.ExitCode, false, t.ConnError("")
	}
	return r.ExitCode, r.ExitCode != 0 && sudoAskedPassword(string(r.Stderr)), nil
}

// sudoAskedPassword recognises sudo -n's refusal ("a password is
// required"); `sudo -n true` before the steps is the language-independent
// check.
func sudoAskedPassword(stderr string) bool {
	return strings.Contains(stderr, "password is required") || strings.Contains(stderr, "需要密码")
}
