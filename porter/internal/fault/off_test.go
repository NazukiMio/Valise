//go:build !faultinject

package fault

import (
	"os"
	"path/filepath"
	"testing"
)

// A release build ignores a trigger file entirely.
func TestReleaseBuildIgnoresTrigger(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".local", "state", "valise", "fault-inject")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("protection-after-intent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	Hit(home, "protection-after-intent")
	if Enabled {
		t.Fatal("release build reports fault injection enabled")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("release build touched the trigger file")
	}
}
