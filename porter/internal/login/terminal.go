package login

import "golang.org/x/sys/unix"

// PrepareTerminal applies the same cooked PTY setup for native interfaces and
// login relay. Piped GUI keystrokes must retain Enter, echo and line editing.
func PrepareTerminal(cols, rows int) {
	saneTerminal(0)
	if cols > 0 && rows > 0 && cols < 10000 && rows < 10000 {
		_ = unix.IoctlSetWinsize(0, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
		return
	}
	if size, err := unix.IoctlGetWinsize(0, unix.TIOCGWINSZ); err == nil && (size.Col == 0 || size.Row == 0) {
		if size.Col == 0 {
			size.Col = 80
		}
		if size.Row == 0 {
			size.Row = 24
		}
		_ = unix.IoctlSetWinsize(0, unix.TIOCSWINSZ, size)
	}
}
