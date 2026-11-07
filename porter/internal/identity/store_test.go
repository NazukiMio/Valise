package identity

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	model "valise/core/identity"
)

func input(id string) AcceptInput {
	return AcceptInput{InstallID: "demo", InstanceID: id, Roots: map[string]string{"state": "/home/test/state"}, ProfileDigest: "profile-digest"}
}

func TestReadDoesNotInitialize(t *testing.T) {
	home := t.TempDir()
	b, err := Read(home, "demo")
	if err != nil || b.IdentityState != model.LegacyUnbound || b.Agent != nil {
		t.Fatalf("%+v %v", b, err)
	}
	s, err := ReadServer(home)
	if err != nil || s != nil {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := os.Stat(Dir(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created identity: %v", err)
	}
}

func TestInterruptedCommitIsHiddenAndExplicitRetryKeepsGeneration(t *testing.T) {
	home := t.TempDir()
	in := input("demo")
	boom := errors.New("dossier write failed")
	if a, err := Accept(home, in, func() error { return boom }); a != nil || !errors.Is(err, boom) {
		t.Fatalf("%+v %v", a, err)
	}
	c, err := readCommit(home)
	if err != nil {
		t.Fatal(err)
	}
	gen := c.Agent.Ref.Generation
	b, err := Read(home, "demo")
	if err == nil || b.IdentityState != model.NeedsReconciliation || b.Agent != nil {
		t.Fatalf("published pending: %+v %v", b, err)
	}
	other := in
	other.ProfileDigest = "other"
	called := false
	if _, err := Accept(home, other, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("different facts reconciled implicitly")
	}
	a, err := Accept(home, in, func() error { return nil })
	if err != nil || a.Ref.Generation != gen {
		t.Fatalf("retry rotated generation: %+v %v", a, err)
	}
	b, err = Read(home, "demo")
	if err != nil || b.Agent.Ref != a.Ref {
		t.Fatalf("%+v %v", b, err)
	}
	for _, path := range []string{Dir(home), filepath.Join(Dir(home), "agents")} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != 0700 {
			t.Fatalf("directory mode: %v %v", fi, err)
		}
	}
	for _, path := range []string{commitPath(home), agentPath(home, "demo"), filepath.Join(Dir(home), "server.json"), filepath.Join(Dir(home), "lock")} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != 0600 {
			t.Fatalf("file mode: %v %v", fi, err)
		}
	}
}

func TestIdentityWriteFailureAfterDossierRemainsPending(t *testing.T) {
	home := t.TempDir()
	in := input("demo")
	dossierSaved := false
	_, err := Accept(home, in, func() error {
		dossierSaved = true
		return os.Mkdir(agentPath(home, "demo"), 0700)
	})
	if err == nil || !dossierSaved {
		t.Fatal("expected failure after dossier persistence")
	}
	b, err := Read(home, "demo")
	if err == nil || b.Agent != nil || b.IdentityState != model.NeedsReconciliation {
		t.Fatalf("partial write was published: %+v %v", b, err)
	}
	c, err := readCommit(home)
	if err != nil {
		t.Fatal(err)
	}
	gen := c.Agent.Ref.Generation
	moved := filepath.Join(t.TempDir(), "blocker")
	a, err := Accept(home, in, func() error { return os.Rename(agentPath(home, "demo"), moved) })
	if err != nil || a.Ref.Generation != gen {
		t.Fatalf("explicit reconciliation failed: %+v %v", a, err)
	}
}

func TestConcurrentAcceptKeepsServerIdentity(t *testing.T) {
	home := t.TempDir()
	const n = 12
	var wg sync.WaitGroup
	refs := make(chan model.AgentRef, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			a, err := Accept(home, input(id), func() error { return nil })
			if err != nil {
				errs <- err
				return
			}
			refs <- a.Ref
		}(i)
	}
	wg.Wait()
	close(refs)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	server := ""
	generations := map[string]bool{}
	for ref := range refs {
		if server == "" {
			server = ref.ServerUID
		}
		if ref.ServerUID != server || generations[ref.Generation] {
			t.Fatalf("identity collision: %+v", ref)
		}
		generations[ref.Generation] = true
	}
	if len(generations) != n {
		t.Fatalf("accepted %d", len(generations))
	}
}

func TestSymlinkIdentityRejected(t *testing.T) {
	home := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Dir(home)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, Dir(home)); err != nil {
		t.Fatal(err)
	}
	if _, err := Accept(home, input("demo"), func() error { return nil }); err == nil {
		t.Fatal("accepted symlink directory")
	}
	if _, err := Read(home, "demo"); err == nil {
		t.Fatal("read followed symlink directory")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside identity: %v %v", entries, err)
	}
}
