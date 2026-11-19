package service

import (
	"context"
	"sync"

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

	return runInteractiveCmd(ctx, t.ForceTTYShellCommand(ctx), streams,
		valise.New("terminal_conn_failed", "和服务器的命令行连接已断开", "检查网络后重新打开；不要把连接结束当作 Agent 已停止"),
		valise.New("server_shell_failed", "服务器命令行没有启动", "重新检查服务器连接和管理登录后再试"),
	)
}
