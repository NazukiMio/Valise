package service

import (
	"context"
	"errors"
	"io"
	"os/exec"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/install"
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

// InstanceNativeCommand uses a new management connection. Neither a local
// profile command nor a request-supplied argv can override the server's entry.
func (s *Service) InstanceNativeCommand(ctx context.Context, host, instance string) (*exec.Cmd, func(), error) {
	info, err := s.InstanceNativeInfo(ctx, host, instance)
	if err != nil {
		return nil, nil, err
	}
	if len(info.Argv) == 0 {
		return nil, nil, valise.New("native_cli_none", "这个 Agent 没有声明原生界面", "使用它已支持的管理页或登录方式")
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
	line := ssh.Quote(found.Path) + " native --instance " + ssh.Quote(instance)
	return t.TTYCommand(ctx, line), done, nil
}

type NativeIO struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// RunInstanceNative only connects streams. No recording, output parser,
// credential detection or chat inspection is installed on this path.
func (s *Service) RunInstanceNative(ctx context.Context, host, instance string, streams NativeIO) (int, error) {
	cmd, done, err := s.InstanceNativeCommand(ctx, host, instance)
	if err != nil {
		return -1, err
	}
	defer done()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
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
