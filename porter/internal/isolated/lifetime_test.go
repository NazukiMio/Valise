package isolated

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestProbeLifetimeRemainsHeldAfterParentCopyCloses(t *testing.T) {
	w, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := probeLifetime(w)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Dup(int(lease.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	inherited := os.NewFile(uintptr(fd), "inherited sandbox lifetime")
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if unlocked, err := HoldProbeStopped(filepath.Dir(w.ProgramDir())); err == nil {
		unlocked.Close()
		t.Fatal("parent close released sandbox lifetime")
	}
	if err := inherited.Close(); err != nil {
		t.Fatal(err)
	}
	unlocked, err := HoldProbeStopped(filepath.Dir(w.ProgramDir()))
	if err != nil {
		t.Fatal("stopped lifetime remained locked", err)
	}
	unlocked.Close()
}
