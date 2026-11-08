package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/revive"
	"valise/core/vault"
)

// lostWorld is sealedWorld with the vault copied to repository "usb" and a
// second server box2 ready to receive.
func lostWorld(t *testing.T) (*Service, string, string) {
	t.Helper()
	s, _, soul, ss := sealedWorld(t)
	ctx := context.Background()
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoAdd("usb", filepath.Join(t.TempDir(), "usb")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("FAKESSH_ROOT")
	home2 := filepath.Join(root, "hosts", "box2", "home")
	os.MkdirAll(filepath.Join(home2, ".local/bin"), 0o755)
	porterBin, _ := os.ReadFile(filepath.Join(root, "hosts", "box", "home", ".local/bin/valise-porter"))
	os.WriteFile(filepath.Join(home2, ".local/bin/valise-porter"), porterBin, 0o755)
	os.WriteFile(filepath.Join(home2, ".local/bin/demo"), []byte("#!/bin/sh\necho demo 1.0\n"), 0o755)
	_ = soul
	return s, ss, home2
}

// With the source server unreachable, a restore stops and names the copies
// that hold the backup; with --source it migrates from the copy without
// touching the source, and reminds once the source answers again
// (docs/ARCHITECTURE.md 8.2.3).
func TestLostSourceMigration(t *testing.T) {
	s, ss, home2 := lostWorld(t)
	ctx := context.Background()
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")

	_, err := s.PlanRevive(ctx, ReviveOptions{Target: "box2", From: "box", SnapshotID: ss, Mode: revive.ModeMigrate, AllowDraft: true}, pass)
	wantCode(t, err, "source_unreachable")
	if !strings.Contains(err.Error(), "repo:usb") {
		t.Fatalf("copies not named: %v", err)
	}
	_, err = s.PlanRevive(ctx, ReviveOptions{Target: "box", From: "box", SnapshotID: ss, Mode: revive.ModeRollback, AllowDraft: true, Source: "repo:usb"}, pass)
	wantCode(t, err, "source_lost")

	rp, err := s.PlanRevive(ctx, ReviveOptions{Target: "box2", From: "box", SnapshotID: ss, Mode: revive.ModeMigrate, AllowDraft: true, Source: "repo:usb"}, pass)
	if err != nil || len(rp.Blockers) != 0 {
		t.Fatalf("%+v %v", rp, err)
	}
	if !rp.SourceLost || rp.Source == nil || rp.StopSource != "" || rp.UnverifiedSource == nil || rp.Transfer.Reused == 0 {
		t.Fatalf("lost plan: lost=%v source=%+v stop=%q unverified=%+v transfer=%+v", rp.SourceLost, rp.Source, rp.StopSource, rp.UnverifiedSource, rp.Transfer)
	}
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
	if err != nil || res.State != revive.StateRestored {
		t.Fatalf("%+v %v", res, err)
	}
	if readFile(t, filepath.Join(home2, ".demo/memory/a.md")) == "" {
		t.Fatal("not restored")
	}
	if l := s.LostMigrations(); len(l) != 1 || l[0].From != "box" || l[0].Target != "box2" || l[0].Instance != "demo" {
		t.Fatalf("%+v", l)
	}

	// The source answers again and still has demo: remind, until settled.
	t.Setenv("FAKESSH_FAIL_CONNECT", "")
	w := s.LostSourceWarnings(ctx, "box")
	if len(w) != 1 || !strings.Contains(w[0].Text, "两边可能都在运行") {
		t.Fatalf("%+v", w)
	}
	if err := s.SettleLostMigration(w[0].OpID); err != nil || len(s.LostMigrations()) != 0 || len(s.LostSourceWarnings(ctx, "box")) != 0 {
		t.Fatalf("settle: %v %+v", err, s.LostMigrations())
	}
}

// A copy with a damaged pack is refused, not half used; listing a copy
// needs neither the server nor the key.
func TestCopySourceChecksPacks(t *testing.T) {
	s, ss, _ := lostWorld(t)
	ctx := context.Background()
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")
	src, err := s.ParseCopySource("box", "repo:usb")
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.SourceStatus(ctx, src)
	if err != nil || !st.Exists || st.LeafOf(ss) == "" {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := s.ParseCopySource("box", "repo:usb/other"); err == nil || !strings.Contains(err.Error(), "box") {
		t.Fatalf("missing server folder: %v", err)
	}
	dirSrc, err := s.ParseCopySource("x", "dir:"+src.Dir)
	if err != nil || dirSrc.Server != "box" {
		t.Fatalf("%+v %v", dirSrc, err)
	}
	packs, _ := filepath.Glob(filepath.Join(src.Dir, "*", "*", vault.PacksDir, "*", "*.pack"))
	for _, p := range packs {
		os.WriteFile(p, []byte("damaged"), 0o600)
	}
	_, err = s.PlanRevive(ctx, ReviveOptions{Target: "box2", From: "box", SnapshotID: ss, Mode: revive.ModeMigrate, AllowDraft: true, Source: "repo:usb"}, pass)
	wantCode(t, err, "source_packs_bad")
}

// Export writes plain files without starting anything; credentials only
// when asked; every path stays inside the folder.
func TestExport(t *testing.T) {
	s, ss, _ := lostWorld(t)
	ctx := context.Background()
	k, err := s.Unlock(pass)
	if err != nil {
		t.Fatal(err)
	}
	id, err := k.Identity()
	if err != nil {
		t.Fatal(err)
	}
	s.rememberOwner(id)
	out := filepath.Join(t.TempDir(), "export")
	res, err := s.Export(ctx, ExportOptions{Host: "box", SnapshotID: ss, To: out})
	if err != nil || res.Files == 0 || len(res.Secrets) == 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(out, "state", ".env")); !os.IsNotExist(err) {
		t.Fatal("a credential file was exported without --include-secrets")
	}
	if !strings.Contains(readFile(t, filepath.Join(out, exportNotes)), "state:.env") {
		t.Fatal("left-out credentials not listed")
	}
	if _, err := s.Export(ctx, ExportOptions{Host: "box", SnapshotID: ss, To: out}); err == nil {
		t.Fatal("exported into a non-empty folder")
	}
	// From the copy, with the server unreachable, credentials included.
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")
	out2 := filepath.Join(t.TempDir(), "export2")
	res, err = s.Export(ctx, ExportOptions{Host: "box", SnapshotID: ss, To: out2, Source: "repo:usb", IncludeSecrets: true})
	if err != nil || readFile(t, filepath.Join(out2, "state", ".env")) != "K=v" {
		t.Fatalf("%+v %v", res, err)
	}
	if st, _ := os.Stat(filepath.Join(out2, "state", ".env")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}
