package actiontxn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	porterenroll "valise/porter/internal/enroll"
)

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("approval output exceeds limit")
	}
	return b.Buffer.Write(p)
}

func command(ctx context.Context, e *porterenroll.Env, lock *os.File, dir string, argv []string, capture bool) ([]byte, int, error) {
	if len(argv) == 0 {
		return nil, -1, reject("命令为空")
	}
	bin := argv[0]
	if !filepath.IsAbs(bin) {
		var err error
		bin, err = e.LookPath(bin)
		if err != nil {
			return nil, -1, reject("找不到固定配置命令")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Env = e.Vars
	cmd.Dir = dir
	configureProcess(cmd)
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 3 * time.Second
	buf := &boundedOutput{limit: 1 << 20}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if capture {
		cmd.Stdout = buf
	}
	err := cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if cmd.Process != nil && syscall.Kill(-cmd.Process.Pid, 0) == nil {
		return nil, code, errChildAlive
	}
	if ctx.Err() != nil {
		return nil, code, reject("配置命令超时或连接已取消")
	}
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return nil, code, reject("固定命令未能完成")
	}
	return buf.Bytes(), code, nil
}
