package main

import (
	"context"
	"os"
	"os/exec"

	"valise/ui/tui"
)

// tui starts the terminal UI. Steps that need the raw terminal run this
// same binary (with the same --config-dir) while the TUI is paused. The TUI
// reads Ctrl-C as a key; the SIGINT a paused CLI step receives (closing a
// web entry) must not end the TUI, so its context ignores the signal.
func (c *cli) tui(ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return tui.Run(ctx, tui.Options{Service: c.svc, Command: func(args ...string) *exec.Cmd {
		var full []string
		if c.configDir != "" {
			full = append(full, "--config-dir", c.configDir)
		}
		return exec.Command(self, append(full, args...)...)
	}})
}
