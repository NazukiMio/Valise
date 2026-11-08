package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/identity"
	"valise/core/protection"
)

// One trigger fills every wanted repository, says which folders are
// missing, and skips one another Valise is copying to
// (docs/ARCHITECTURE.md 7.4).
func TestAutoCopy(t *testing.T) {
	s, _, _, _ := sealedWorld(t)
	ctx := context.Background()
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	usb := filepath.Join(t.TempDir(), "usb")
	if _, err := s.RepoAdd("usb", usb); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "disk")
	if _, err := s.RepoAdd("disk", gone); err != nil {
		t.Fatal(err)
	}
	// Unplugged after it was added.
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	res, err := s.AutoCopy(ctx, "box", TriggerOpen, AutoCopyOptions{})
	if err != nil || len(res.Locations) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if o := res.Locations[0]; o.Location != "usb" || o.State != CopyCopied || o.Copied == 0 {
		t.Fatalf("%+v", o)
	}
	if o := res.Locations[1]; o.Location != "disk" || o.State != CopyUnmounted {
		t.Fatalf("%+v", o)
	}
	if !res.Changed() {
		t.Fatal("a copy is a change")
	}
	again, err := s.AutoCopy(ctx, "box", TriggerBackup, AutoCopyOptions{Instance: "inst"})
	if err != nil || again.Locations[0].State != CopyCurrent || again.Changed() {
		t.Fatalf("%+v %v", again, err)
	}

	// Another process holds usb: the automatic copy skips, a copy the
	// person started waits.
	unlock, held, err := tryFileLock(filepath.Join(s.Dirs.Data, "locks", "copy-repo-usb-box.lock"))
	if err != nil || held {
		t.Fatal(held, err)
	}
	busy, err := s.AutoCopy(ctx, "box", TriggerOpen, AutoCopyOptions{})
	if err != nil || busy.Locations[0].State != CopyBusy {
		t.Fatalf("%+v %v", busy, err)
	}
	wctx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	if _, err := s.RepoSync(wctx, "box", "usb"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("manual sync did not wait: %v", err)
	}
	unlock()
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
}

// The binding decides for instances that have one; any instance without
// one keeps the old rule (every repository).
func TestRepoRule(t *testing.T) {
	ctx := context.Background()
	repos := []config.Repo{{Name: "usb", Path: "/x"}, {Name: "nas", Path: "/y"}}
	store := NewProtectionBindingStore(t.TempDir(), repos)
	a := protectionRequest().AgentRef
	b := a
	b.InstanceID = "other"
	on, off := true, false
	if _, err := store.CompareAndSwap(ctx, a, 0, 1, []protection.LocalCopy{{RepoID: "usb", AfterBackup: &on, OnClientOpen: &off}, {RepoID: "nas", AfterBackup: &off, OnClientOpen: &on}}); err != nil {
		t.Fatal(err)
	}
	refs := map[string]identity.AgentRef{"a": a, "b": b}
	check := func(name string, rule func(string) bool, usb, nas bool) {
		t.Helper()
		if rule("usb") != usb || rule("nas") != nas {
			t.Fatalf("%s: usb=%v nas=%v", name, rule("usb"), rule("nas"))
		}
	}
	check("backup of a", repoRule(ctx, TriggerBackup, []string{"a"}, refs, store), true, false)
	check("open, only a", repoRule(ctx, TriggerOpen, []string{"a"}, refs, store), false, true)
	// b has no binding: the old rule for the whole server on open.
	check("open, a and b", repoRule(ctx, TriggerOpen, []string{"a", "b"}, refs, store), true, true)
	// An unbound instance (no identity) likewise.
	check("unbound", repoRule(ctx, TriggerBackup, []string{"c"}, refs, store), true, true)
}
