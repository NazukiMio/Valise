//go:build !linux

package login

// saneTerminal is Linux-only: porter runs on Linux servers.
func saneTerminal(int) {}
