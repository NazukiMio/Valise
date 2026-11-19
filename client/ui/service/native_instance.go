package service

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/enroll"
	"valise/core/install"
	"valise/core/revive"
	"valise/core/valise"
)

func (s *Service) InstanceNativeInfo(ctx context.Context, host, instance string) (*install.NativeInfo, error) {
	var info install.NativeInfo
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, install.MethodNativeInfo, install.NativeInfoParams{InstanceID: instance}, &info)
	})
	if err != nil {
		return nil, err
	}
	if info.InstanceID != instance || info.ProfileID == "" {
		return nil, valise.New("native_info_mismatch", "收到的命令行与向导资料不属于这个 Agent", "重新读取这个 Agent 后再试")
	}
	return &info, nil
}

// NativeMode is what a native session runs: the accepted onboarding CLI,
// the daily session CLI, or the instance's interactive shell.
type NativeMode string

const (
	NativeCLI     NativeMode = ""
	NativeSession NativeMode = "session"
	NativeShell   NativeMode = "shell"
)

// nativeLine is the remote command of a native session. It names the
// porter, the instance and at most one fixed switch (--session or --shell):
// there is no place for a command of the caller's.
func nativeLine(porterPath, instance string, mode NativeMode) string {
	line := ssh.Quote(porterPath) + " native --instance " + ssh.Quote(instance)
	switch mode {
	case NativeShell:
		line += " --shell"
	case NativeSession:
		line += " --session"
	}
	return line
}

// InstanceNativeCommand uses a new management connection. Neither a local
// profile command nor a request-supplied argv can override the server's entry.
func (s *Service) InstanceNativeCommand(ctx context.Context, host, instance string, mode NativeMode) (*exec.Cmd, func(), error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	info, err := s.InstanceNativeInfo(ctx, host, instance)
	if err != nil {
		return nil, nil, err
	}
	switch mode {
	case NativeCLI:
		if len(info.Argv) == 0 {
			return nil, nil, valise.New("native_cli_none", "这个 Agent 没有声明初始化配置向导（Onboard）", "改用 Agent 终端会话（TUI）或系统命令行（Shell）")
		}
	case NativeSession:
		if len(info.SessionArgv) == 0 {
			return nil, nil, valise.New("native_session_none", "这个 Agent 没有声明 Agent 终端会话（TUI）", "改用系统命令行（Shell），或更新资料后重新接入")
		}
		if !info.Session {
			return nil, nil, valise.New("unsupported_capability", "服务器上的 porter 尚不能打开这个 Agent 的终端会话（TUI）", "在当前服务器的设置中更新 porter 后再试")
		}
	case NativeShell:
		if !info.Shell {
			return nil, nil, valise.New("unsupported_capability", "服务器上的 porter 尚不能打开这个 Agent 的系统命令行（Shell）", "在当前服务器的设置中更新 porter 后再试")
		}
	default:
		return nil, nil, valise.New("native_mode_invalid", "不认识的命令行与向导方式", "")
	}
	if err := s.RequireGate(ctx, host, instance); err != nil {
		return nil, nil, err
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return nil, nil, err
	}
	var closeOnce sync.Once
	asyncDone := func() {
		closeOnce.Do(func() {
			closeMasterAsync(done)
		})
	}
	found, err := porter.Probe(ctx, t)
	if err != nil {
		asyncDone()
		return nil, nil, err
	}
	if !found.Supports(install.NativeCapability) {
		asyncDone()
		return nil, nil, valise.New("unsupported_capability", "服务器上的 porter 尚不能按 Agent 打开命令行与向导", "在当前服务器的设置中更新 porter 后再试")
	}
	cmd := t.TTYCommand(ctx, nativeLine(found.Path, instance, mode))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = killProcessGroupCancel(cmd)
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd, asyncDone, nil
}

func killProcessGroupCancel(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

func runInteractiveCmd(ctx context.Context, cmd *exec.Cmd, streams NativeIO, connFailedErr, startFailedErr *valise.UserError) (int, error) {
	if streams.PTY {
		return runSSHWithPTY(ctx, cmd, streams, connFailedErr, startFailedErr)
	}
	return runSSHWithPipes(ctx, cmd, streams, connFailedErr, startFailedErr)
}

func runSSHWithPipes(ctx context.Context, cmd *exec.Cmd, streams NativeIO, connFailedErr, startFailedErr *valise.UserError) (int, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = killProcessGroupCancel(cmd)
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Stdout, cmd.Stderr = streams.Stdout, streams.Stderr
	if streams.Stdin != nil {
		if _, isFile := streams.Stdin.(*os.File); isFile {
			cmd.Stdin = streams.Stdin
		} else {
			// An interactive pipe (such as the desktop terminal's installConn)
			// stays open until the caller closes it after exit; copying via
			// StdinPipe keeps cmd.Wait from blocking on Stdin EOF after the
			// remote command exits.
			pw, err := cmd.StdinPipe()
			if err != nil {
				return -1, startFailedErr.WithDetail(err.Error())
			}
			go func() {
				_, _ = io.Copy(pw, streams.Stdin)
				_ = pw.Close()
			}()
		}
	}
	err := cmd.Run()
	return mapInteractiveExit(ctx, err, connFailedErr, startFailedErr)
}

func mapInteractiveExit(ctx context.Context, err error, connFailedErr, startFailedErr *valise.UserError) (int, error) {
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if exit.ExitCode() == ssh.ExitConnFailed {
			return exit.ExitCode(), connFailedErr
		}
		return exit.ExitCode(), nil
	}
	return -1, startFailedErr
}

func closeMasterAsync(closeMaster func()) {
	if closeMaster == nil {
		return
	}
	go closeMaster()
}

type NativeIO struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// PTY opens a local pseudo-terminal for the interactive ssh child and
	// exports standard terminal environment variables while preserving
	// SSH_ASKPASS. OnResize receives a window-size callback for live SIGWINCH.
	PTY      bool
	OnResize func(func(cols, rows, widthPx, heightPx int))
}

// RunInstanceNative only connects streams. No recording, output parser,
// credential detection or chat inspection is installed on this path.
func (s *Service) RunInstanceNative(ctx context.Context, host, instance string, mode NativeMode, streams NativeIO) (int, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	cmd, done, err := s.InstanceNativeCommand(ctx, host, instance, mode)
	if err != nil {
		return -1, err
	}
	defer done()
	return runInteractiveCmd(ctx, cmd, streams,
		valise.New("terminal_conn_failed", "和服务器的命令行与向导连接已断开", "检查网络后重新打开；不要把连接结束当作 Agent 已停止"),
		valise.New("native_start_failed", "命令行与向导没有启动", "重新检查服务器连接和管理登录后再试"),
	)
}

// NativeServiceRestoreResult keeps starting paused service units separate from
// live service/entry state and the subsequent health verification.
type NativeServiceRestoreResult struct {
	StartedUnits []string              `json:"started_units,omitempty"`
	Checked      bool                  `json:"checked"`
	Service      []enroll.ServiceState `json:"service,omitempty"`
	Entries      []enroll.EntryState   `json:"entries,omitempty"`
	Verification *HealthVerification   `json:"verification,omitempty"`
}

var nativeServiceSettle = 20 * time.Second

// PauseInstanceServices stops running managed systemd user units declared for
// instance after checking the offsite backup gate, returning the unit names
// actually stopped by this call.
func (s *Service) PauseInstanceServices(ctx context.Context, host, instance string) ([]string, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	if err := s.RequireGate(ctx, host, instance); err != nil {
		return nil, err
	}
	list, err := s.Instances(ctx, host)
	if err != nil {
		return nil, err
	}
	var active []string
	for _, in := range list {
		if in.InstanceID != instance {
			continue
		}
		for _, u := range in.Service {
			if u.Exists() && (u.Active == "active" || u.Active == "activating" || u.Active == "reloading") {
				active = append(active, u.Unit)
			}
		}
	}
	if len(active) == 0 {
		return nil, nil
	}
	var stopped []string
	err = s.withSession(ctx, host, func(c caller) error {
		for _, unit := range active {
			if err := c.Call(ctx, revive.MethodService, revive.ServiceParams{Unit: unit, Action: "stop"}, nil); err != nil {
				return err
			}
			stopped = append(stopped, unit)
		}
		return nil
	})
	return stopped, err
}

// RestoreInstanceServices starts service units paused before native
// onboarding, polls live state, and verifies service/entry health separately.
func (s *Service) RestoreInstanceServices(ctx context.Context, host, instance string, units []string) (*NativeServiceRestoreResult, error) {
	res := &NativeServiceRestoreResult{}
	if len(units) == 0 {
		return res, nil
	}
	err := s.withSession(ctx, host, func(c caller) error {
		for _, unit := range units {
			if err := c.Call(ctx, revive.MethodService, revive.ServiceParams{Unit: unit, Action: "start"}, nil); err != nil {
				return err
			}
			res.StartedUnits = append(res.StartedUnits, unit)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	s.settleNativeService(ctx, host, instance, res)
	r, herr := s.Health(ctx, host, instance)
	res.Verification = verifyService(r, herr)
	return res, nil
}

func (s *Service) settleNativeService(ctx context.Context, host, instance string, res *NativeServiceRestoreResult) {
	deadline := time.Now().Add(nativeServiceSettle)
	for {
		list, err := s.Instances(ctx, host)
		if err != nil {
			return
		}
		for _, in := range list {
			if in.InstanceID == instance {
				res.Checked, res.Service, res.Entries = true, in.Service, in.Entries
			}
		}
		healthy := res.Checked
		for _, sv := range res.Service {
			healthy = healthy && (sv.LoadState != "loaded" || sv.Active == "active")
		}
		for _, e := range res.Entries {
			healthy = healthy && e.Listening
		}
		if healthy || time.Now().After(deadline) {
			return
		}
		wait := 2 * time.Second
		if rem := time.Until(deadline); rem < wait {
			wait = max(rem, 10*time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
