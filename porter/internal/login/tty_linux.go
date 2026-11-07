package login

import "golang.org/x/sys/unix"

// saneTerminal gives the login command a cooked terminal. A PTY requested
// through an ssh ControlMaster by a client whose own stdin is not a
// terminal (Valise relays keystrokes through a pipe) starts with every
// mode cleared: no output newline translation, no echo, no line editing,
// and Enter (\r) never ends a line. Prompts then stair-step and no answer
// reaches the command. Modes a real terminal set are kept.
func saneTerminal(fd int) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return
	}
	t.Iflag |= unix.ICRNL | unix.IXON | unix.IUTF8
	t.Iflag &^= unix.INLCR | unix.IGNCR
	t.Oflag |= unix.OPOST | unix.ONLCR
	t.Lflag |= unix.ICANON | unix.ISIG | unix.IEXTEN | unix.ECHO | unix.ECHOE | unix.ECHOK | unix.ECHOCTL | unix.ECHOKE
	t.Cflag |= unix.CREAD
	if t.Cflag&unix.CSIZE == 0 {
		t.Cflag |= unix.CS8
	}
	for i, c := range map[int]uint8{
		unix.VINTR: 3, unix.VQUIT: 28, unix.VERASE: 127, unix.VKILL: 21, unix.VEOF: 4,
		unix.VSUSP: 26, unix.VWERASE: 23, unix.VLNEXT: 22, unix.VREPRINT: 18, unix.VMIN: 1,
	} {
		if t.Cc[i] == 0 {
			t.Cc[i] = c
		}
	}
	unix.IoctlSetTermios(fd, unix.TCSETS, t)
}
