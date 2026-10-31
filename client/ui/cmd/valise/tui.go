package main

import (
	"context"
	"os"
	"os/exec"

	"valise/ui/tui"
)

// tui starts the terminal UI. Steps that need the raw terminal run this
// same binary (with the same --config-dir) while the TUI is paused.
func (c *cli) tui(ctx context.Context) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return tui.Run(ctx, tui.Options{Service: c.svc, Command: func(args ...string) *exec.Cmd {
		var full []string
		if c.configDir != "" {
			full = append(full, "--config-dir", c.configDir)
		}
		cmd := exec.Command(self, append(full, args...)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd
	}})
}
