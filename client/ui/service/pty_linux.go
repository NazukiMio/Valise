//go:build linux

package service

import (
	"fmt"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openPTYMaster() (int, string, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, "", fmt.Errorf("pty: open /dev/ptmx: %w", err)
	}
	var unlock int32
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCSPTLCK), uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		_ = unix.Close(fd)
		return -1, "", fmt.Errorf("pty: unlock ptmx: %w", errno)
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		_ = unix.Close(fd)
		return -1, "", fmt.Errorf("pty: pts number: %w", err)
	}
	return fd, "/dev/pts/" + strconv.FormatUint(uint64(n), 10), nil
}
