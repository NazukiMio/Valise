package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// interactive reports whether stdin is a terminal.
func (c *cli) interactive() bool {
	f, ok := c.in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// passphrase reads a passphrase without echo from a terminal, or one line
// from non-terminal stdin (for scripts). The value is never logged.
func (c *cli) passphrase(prompt string) (string, error) {
	if c.interactive() {
		fmt.Fprint(c.errOut, prompt)
		b, err := term.ReadPassword(int(c.in.(*os.File).Fd()))
		fmt.Fprintln(c.errOut)
		return string(b), err
	}
	if c.lines == nil {
		c.lines = bufio.NewReader(c.in)
	}
	line, err := c.lines.ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("没有从标准输入读到口令")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
