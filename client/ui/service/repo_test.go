package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/vault"
)

// A repository is a local folder: the append-only copy of every period,
// verified, under <repo>/<server name>/ (docs/ARCHITECTURE.md section 7).
func TestRepoSync(t *testing.T) {
	s, _, soul, ss := sealedWorld(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "usb")
	if _, err := s.RepoAdd("Bad Name", dir); err == nil {
		t.Fatal("bad name accepted")
	}
	if _, err := s.RepoAdd("usb", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoSync(ctx, "box", "usb"); err == nil {
		t.Fatal("synced without a server name")
	}
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	res, err := s.RepoSync(ctx, "box", "usb")
	if err != nil || res.Downloaded == 0 || len(res.Snapshots) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "box", vault.TreeFile)); err != nil {
		t.Fatal(err)
	}
	again, err := s.RepoSync(ctx, "box", "usb")
	if err != nil || again.Downloaded != 0 {
		t.Fatalf("second sync copied again: %+v %v", again, err)
	}
	st := s.RepoStatuses("box", "box")
	if len(st) != 1 || !st[0].Available || !st[0].Has(soul) || !st[0].Has(ss) || st[0].State.LastOK == nil {
		t.Fatalf("%+v", st)
	}
	if err := s.RepoRemove("usb"); err != nil || len(s.Repos()) != 0 {
		t.Fatal("not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "box")); err != nil {
		t.Fatal("removing a repository must keep its files")
	}
}
