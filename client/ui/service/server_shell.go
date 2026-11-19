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

	"valise/client/ssh"
	"valise/core/valise"
)

// RunServerShell opens the server's own interactive shell over a fresh
// management connection (the offsite backup gate applies). It only connects
// streams: terminal bytes are never logged, recorded or parsed.
func (s *Service) RunServerShell(ctx context.Context, host string, streams NativeIO) (int, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	if err := s.RequireGate(ctx, host, ""); err != nil {
		return -1, err
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return -1, err
	}
	var closeOnce sync.Once
	defer closeOnce.Do(func() { closeMasterAsync(done) })

	cmd := t.ForceTTYShellCommand(ctx)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Stdout, cmd.Stderr = streams.Stdout, streams.Stderr
	if streams.Stdin != nil {
		if _, isFile := streams.Stdin.(*os.File); isFile {
			cmd.Stdin = streams.Stdin
		} else {
			pw, err := cmd.StdinPipe()
			if err != nil {
				return -1, valise.New("server_shell_failed", "服务器命令行没有启动", "重新检查服务器连接和管理登录后再试").WithDetail(err.Error())
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
			return exit.ExitCode(), valise.New("terminal_conn_failed", "和服务器的命令行连接已断开", "检查网络后重新打开；不要把连接结束当作 Agent 已停止")
		}
		return exit.ExitCode(), nil
	}
	return -1, valise.New("server_shell_failed", "服务器命令行没有启动", "重新检查服务器连接和管理登录后再试")
}
