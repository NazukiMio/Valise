//go:build !darwin && !linux

package service

import (
	"context"
	"os/exec"

	"valise/core/valise"
)

func runSSHWithPTY(ctx context.Context, cmd *exec.Cmd, streams NativeIO, connFailedErr, startFailedErr *valise.UserError) (int, error) {
	return runSSHWithPipes(ctx, cmd, streams, connFailedErr, startFailedErr)
}
