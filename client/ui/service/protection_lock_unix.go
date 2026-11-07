//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package service

import (
	"os"
	"syscall"
)

func protectionStoreLock(p string) (func(), error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, protectionError("record_corrupt", "无法打开保护任务索引锁")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, protectionError("resource_busy", "另一个客户端进程正在更新保护任务索引")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
