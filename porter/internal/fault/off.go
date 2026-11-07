//go:build !faultinject

// Package fault is a no-op in release builds; see fault.go (faultinject tag).
package fault

// Enabled reports whether this build can inject faults.
const Enabled = false

// Hit does nothing without the faultinject build tag.
func Hit(home, point string) {}
