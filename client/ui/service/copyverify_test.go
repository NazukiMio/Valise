package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

func TestVerifyCopiesServerAndRepository(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "box", ""); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	if err != nil {
		t.Fatal(err)
	}
	s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoulSecrets})
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "repo")
	if _, err := s.RepoAdd("home", dir); err != nil {
		t.Fatal(err)
	}
	rs, err := s.RepoSync(ctx, "box", "home")
	if err != nil || len(rs.Checks) != 1 || rs.Checks[0].State != CopyListed {
		t.Fatalf("after-copy check: %+v %v", rs, err)
	}
	if ev := s.CopyEvidenceFor("box", "home", rep.Leaf); len(ev) != 2 || ev[0].Result != "passed" || ev[1].Result != "unknown" {
		t.Fatalf("%+v", ev)
	}
	cr, err := s.VerifyCopies(ctx, "box", rep.Leaf, VerifyOptions{Packs: true})
	if err != nil || cr.Basis == "" || cr.BasisSnapshots != 2 || !cr.Current || len(cr.Checks) != 2 {
		t.Fatalf("%+v %v", cr, err)
	}
	for _, c := range cr.Checks {
		if c.State != CopyComplete || c.Snapshots != 2 {
			t.Fatalf("%+v", c)
		}
	}
	if !cr.CompleteElsewhere("server") {
		t.Fatal("repository copy not counted")
	}
	// Same snapshot count, one pack gone: the repository is incomplete and
	// the pack is named; the old count rule would have called it complete.
	leafDir := filepath.Join(dir, "box", filepath.FromSlash(rep.Leaf))
	var pack string
	filepath.WalkDir(filepath.Join(leafDir, vault.PacksDir), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			pack = p
		}
		return nil
	})
	os.Remove(pack)
	cr, _ = s.VerifyCopies(ctx, "box", rep.Leaf, VerifyOptions{Packs: true})
	repo := cr.Checks[1]
	if repo.State != CopyIncomplete || repo.Snapshots != 2 || len(repo.Diff.Missing) != 1 || cr.CompleteElsewhere("server") {
		t.Fatalf("%+v", repo)
	}
	if ev := s.CopyEvidenceFor("box", "home", rep.Leaf); len(ev) != 2 || ev[0].Result != "failed" || ev[0].SourceRevision != cr.Basis {
		t.Fatalf("%+v", ev)
	}
	// A skipped place is unchecked, never complete.
	cr, _ = s.VerifyCopies(ctx, "box", rep.Leaf, VerifyOptions{Skip: map[string]bool{"home": true}})
	if cr.Checks[1].State != CopyUnchecked {
		t.Fatalf("%+v", cr.Checks[1])
	}
	// A partial restore cache is never a whole period.
	os.WriteFile(leafDir+".partial.json", []byte("{}"), 0o600)
	cr, _ = s.VerifyCopies(ctx, "box", rep.Leaf, VerifyOptions{})
	if cr.Checks[1].State != CopyIncomplete || cr.Checks[1].Reason == "" {
		t.Fatalf("%+v", cr.Checks[1])
	}
}
