package service

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
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
		return nil, valise.New("native_info_mismatch", "收到的原生界面资料不属于这个 Agent", "重新读取这个 Agent 后再试")
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
	info, err := s.InstanceNativeInfo(ctx, host, instance)
	if err != nil {
		return nil, nil, err
	}
	switch mode {
	case NativeCLI:
		if len(info.Argv) == 0 {
			return nil, nil, valise.New("native_cli_none", "这个 Agent 没有声明原生引导", "改用原生会话或实例交互终端")
		}
	case NativeSession:
		if len(info.SessionArgv) == 0 {
			return nil, nil, valise.New("native_session_none", "这个 Agent 没有声明原生会话", "改用实例交互终端，或更新资料后重新接入")
		}
		if !info.Session {
			return nil, nil, valise.New("unsupported_capability", "服务器上的 porter 尚不能打开这个 Agent 的原生会话", "在当前服务器的设置中更新 porter 后再试")
		}
	case NativeShell:
		if !info.Shell {
			return nil, nil, valise.New("unsupported_capability", "服务器上的 porter 尚不能打开这个 Agent 的交互终端", "在当前服务器的设置中更新 porter 后再试")
		}
	default:
		return nil, nil, valise.New("native_mode_invalid", "不认识的原生界面方式", "")
	}
	if err := s.RequireGate(ctx, host, instance); err != nil {
		return nil, nil, err
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return nil, nil, err
	}
	found, err := porter.Probe(ctx, t)
	if err != nil {
		done()
		return nil, nil, err
	}
	if !found.Supports(install.NativeCapability) {
		done()
		return nil, nil, valise.New("unsupported_capability", "服务器上的 porter 尚不能按 Agent 打开原生界面", "在当前服务器的设置中更新 porter 后再试")
	}
	return t.TTYCommand(ctx, nativeLine(found.Path, instance, mode)), done, nil
}

type NativeIO struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// RunInstanceNative only connects streams. No recording, output parser,
// credential detection or chat inspection is installed on this path.
func (s *Service) RunInstanceNative(ctx context.Context, host, instance string, mode NativeMode, streams NativeIO) (int, error) {
	cmd, done, err := s.InstanceNativeCommand(ctx, host, instance, mode)
	if err != nil {
		return -1, err
	}
	defer done()
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
				return -1, valise.New("native_start_failed", "原生界面没有启动", "重新检查服务器连接和管理登录后再试").WithDetail(err.Error())
			}
			go func() {
				_, _ = io.Copy(pw, streams.Stdin)
				_ = pw.Close()
			}()
		}
	}
	err = cmd.Run()
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if exit.ExitCode() == ssh.ExitConnFailed {
			return exit.ExitCode(), valise.New("terminal_conn_failed", "和服务器的原生界面连接已断开", "检查网络后重新打开；不要把连接结束当作 Agent 已停止")
		}
		return exit.ExitCode(), nil
	}
	return -1, valise.New("native_start_failed", "原生界面没有启动", "重新检查服务器连接和管理登录后再试")
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
