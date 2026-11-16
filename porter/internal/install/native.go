package install

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"valise/core/enroll"
	"valise/core/install"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

const (
	ModeOnboard = ""
	ModeShell   = "shell"
	ModeSession = "session"
)

func nativeUnavailable() error {
	return valise.New("native_current_unverified", "无法核实这个 Agent 当前使用的程序，没有启动命令", "重新检查这个 Agent；若正在恢复，请先查看原恢复操作的结果")
}

// NativeInfo returns the accepted native CLI metadata for one enrolled instance.
func NativeInfo(home, id string) (*install.NativeInfo, error) {
	d, p, err := porterenroll.ReadInstallation(home, id)
	if err != nil {
		return nil, err
	}
	if d == nil || p == nil || d.Record(id) == nil {
		return nil, valise.New("instance_not_enrolled", "没有找到这个 Agent 已接受的接入资料", "重新检查这个 Agent 的接入状态")
	}
	r := d.Record(id)
	effective := p.ForAgent(r.Name)
	hasNative := len(effective.NativeCLI) > 0 || len(effective.NativeSessionCLI) > 0
	return &install.NativeInfo{
		InstanceID:  id,
		ProfileID:   effective.ID,
		Argv:        effective.NativeCLI,
		SessionArgv: effective.NativeSessionCLI,
		Hints:       effective.NativeHints,
		Shell:       hasNative,
		Session:     len(effective.NativeSessionCLI) > 0,
	}, nil
}

// NativeServices reads the current systemd state of an enrolled instance's
// accepted service candidates.
func NativeServices(ctx context.Context, env *porterenroll.Env, home, id string) []enroll.ServiceState {
	d, p, err := porterenroll.ReadInstallation(home, id)
	if err != nil || d == nil || p == nil || d.Record(id) == nil {
		return nil
	}
	r := d.Record(id)
	effective := p.ForAgent(r.Name)
	r.ApplyEntryPorts(&effective)
	svc, _ := porterenroll.Live(ctx, env, &effective)
	return svc
}

// NativeCommand resolves what `native --instance` runs for one enrolled
// instance: its accepted native onboarding CLI, native session CLI (--session),
// or the user's own interactive login shell (--shell). All get the instance's
// environment (its state directory variable and the profile's command
// directories on PATH). The instance must be enrolled, current and declare a
// matching native interface; no command comes from the request.
func NativeCommand(ctx context.Context, home, id, mode string, lock *os.File) (*exec.Cmd, []string, error) {
	if enroll.ValidInstanceID(id) != nil || lock == nil {
		return nil, nil, nativeUnavailable()
	}
	d, p, err := porterenroll.ReadInstallation(home, id)
	if err != nil || d == nil || p == nil || d.Record(id) == nil || d.Record(id).Superseded != nil {
		return nil, nil, nativeUnavailable()
	}
	r := d.Record(id)
	selected := p.ForAgent(r.Name)
	r.ApplyEntryPorts(&selected)
	env := porterenroll.LoadEnv(ctx, home).ForInstance(&selected, r)
	var display []string
	var cmd *exec.Cmd
	switch mode {
	case ModeOnboard:
		if len(selected.NativeCLI) == 0 {
			return nil, nil, valise.New("native_cli_none", "这个 Agent 的接入资料没有声明原生引导", "改用原生会话或实例交互终端")
		}
		display = selected.NativeCLI
		bin, out, err := resolve(env, selected.NativeCLI)
		if err != nil {
			return nil, nil, err
		}
		cmd = exec.Command(bin, out[1:]...)
	case ModeSession:
		if len(selected.NativeSessionCLI) == 0 {
			return nil, nil, valise.New("native_session_none", "这个 Agent 的接入资料没有声明原生会话", "改用实例交互终端")
		}
		display = selected.NativeSessionCLI
		bin, out, err := resolve(env, selected.NativeSessionCLI)
		if err != nil {
			return nil, nil, err
		}
		cmd = exec.Command(bin, out[1:]...)
	case ModeShell:
		if len(selected.NativeCLI) == 0 && len(selected.NativeSessionCLI) == 0 {
			return nil, nil, valise.New("native_cli_none", "这个 Agent 的接入资料没有声明原生界面", "使用它已支持的管理页或登录方式")
		}
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		display = loginShell(sh)
		cmd = exec.Command(display[0], display[1:]...)
		// A login shell's profile resets PATH; the instance's is handed
		// over beside it and put back in front (loginShell).
		for _, kv := range env.Vars {
			if v, ok := strings.CutPrefix(kv, "PATH="); ok {
				env.Vars = append(env.Vars, instancePathVar+"="+v)
			}
		}
	default:
		return nil, nil, valise.New("native_mode_invalid", "不认识的原生界面方式", "")
	}
	cmd.Env, cmd.Dir, cmd.ExtraFiles = env.Vars, env.Home, []*os.File{lock}
	return cmd, display, nil
}

// instancePathVar carries the instance's PATH past a login shell's profile.
const instancePathVar = "VALISE_INSTANCE_PATH"

// loginShell is the fixed command of the instance shell: the user's own
// shell as an interactive login shell. A POSIX shell runs its login
// profile first, then puts the instance's command directories back in
// front of PATH (a profile such as /etc/profile sets PATH anew) and
// becomes the interactive shell. Other shells are started as they take
// it; nothing here comes from a request.
func loginShell(sh string) []string {
	switch filepath.Base(sh) {
	case "sh", "bash", "zsh", "dash", "ksh", "ash":
		return []string{sh, "-l", "-c", `PATH="$` + instancePathVar + `:$PATH"; export PATH; unset ` + instancePathVar + `; exec "$0" -i`, sh}
	case "fish":
		return []string{sh, "-l", "-i"} // reads no /etc/profile: PATH stays
	}
	return []string{sh, "-i"}
}
