package service

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

// The retest's trigger (F03): a cold cache, a small pod, and a large older
// pod in the same period. Only the small pod's packs come over; a second
// pull reuses them; the copy is marked partial until the whole period is
// pulled.
func TestPullSnapshotOnlyItsPacks(t *testing.T) {
	s, home := enrollWorld(t)
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
	big := make([]byte, 40<<20) // incompressible: several packs
	rand.Read(big)
	bigPath := filepath.Join(home, ".demo", "memory", "big.bin")
	if err := os.WriteFile(bigPath, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierFull}); err != nil {
		t.Fatal(err)
	}
	os.Remove(bigPath)
	small, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.Unlock(pass)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := k.Identity()
	v, r, err := s.PullSnapshot(ctx, "box", small.Leaf, small.SnapshotID, id)
	if err != nil {
		t.Fatal(err)
	}
	allPacks, _ := filepath.Glob(filepath.Join(home, ".local/share/valise/vaults/demo/*/packs/*/*.pack"))
	if r.PeriodBytes < 40<<20 || r.Downloaded >= r.PeriodBytes || !r.Partial || r.Packs == 0 || r.Packs >= len(allPacks) || r.Reused != 0 {
		t.Fatalf("cold: %+v of %d packs", r, len(allPacks))
	}
	t.Logf("period %d, pod %d, packs %d of %d (%d bytes), downloaded %d", r.PeriodBytes, r.PodBytes, r.Packs, len(allPacks), r.PackBytes, r.Downloaded)
	if _, err := os.Stat(partialFile(r.Dir)); err != nil {
		t.Fatal("partial copy not marked")
	}
	// Every file of the pod can be written and checked.
	_, files, err := v.Snapshot(small.SnapshotID, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files.Files {
		if err := v.WriteFile(f, discard{}, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.VerifyLocal(r.Dir, nil); err == nil {
		t.Fatal("a partial copy verified as a whole period")
	}
	// Warm: nothing more to download.
	_, r2, err := s.PullSnapshot(ctx, "box", small.Leaf, small.SnapshotID, id)
	if err != nil || r2.Downloaded != 0 || r2.Reused != r.PackBytes {
		t.Fatalf("warm: %+v %v", r2, err)
	}
	// A damaged pack here is fetched again.
	packs, _ := filepath.Glob(filepath.Join(r.Dir, "packs", "*", "*.pack"))
	os.WriteFile(packs[0], []byte("damaged"), 0o600)
	if _, r3, err := s.PullSnapshot(ctx, "box", small.Leaf, small.SnapshotID, id); err != nil || r3.Downloaded == 0 {
		t.Fatalf("damaged: %+v %v", r3, err)
	}
	// The whole period: the mark goes.
	if _, err := s.Pull(ctx, "box", small.Leaf, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partialFile(r.Dir)); err == nil {
		t.Fatal("mark kept after the whole period was pulled")
	}
}

type discard struct{}

func (discard) Write(b []byte) (int, error) { return len(b), nil }
