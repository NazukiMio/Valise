//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package service

func protectionStoreLock(string) (func(), error) {
	return nil, protectionError("unsupported_capability", "当前系统尚未实现私有任务索引的跨进程锁")
}
