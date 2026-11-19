//go:build darwin

package service

import (
	"bytes"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// macOS ioctl constants from sys/ttycom.h (matching mygo's pty implementation).
const (
	tiocptygrant = 0x20007454 // _IO('t', 84)
	tiocptyunlk  = 0x20007452 // _IO('t', 82)
	tiocptygname = 0x40807453 // _IOC(IOC_OUT, 't', 83, 128)
)

func openPTYMaster() (int, string, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, "", fmt.Errorf("pty: open /dev/ptmx: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), tiocptygrant, 0); errno != 0 {
		_ = unix.Close(fd)
		return -1, "", fmt.Errorf("pty: grantpt: %w", errno)
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), tiocptyunlk, 0); errno != 0 {
		_ = unix.Close(fd)
		return -1, "", fmt.Errorf("pty: unlockpt: %w", errno)
	}
	var name [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), tiocptygname, uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		_ = unix.Close(fd)
		return -1, "", fmt.Errorf("pty: ptsname: %w", errno)
	}
	n := bytes.IndexByte(name[:], 0)
	if n < 0 {
		n = len(name)
	}
	return fd, string(name[:n]), nil
}
