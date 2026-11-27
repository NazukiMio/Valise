package seal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

func setupBundleInstance(t *testing.T, home, instID, profID string) {
	t.Helper()
	stateDir := filepath.Join(home, "agents", instID)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "SOUL.md"), []byte("hello "+instID), 0o600); err != nil {
		t.Fatal(err)
	}
	prof := profile.Profile{
		ID:             profID,
		ProfileVersion: 1,
		Status:         "tested",
		Paths: []profile.PathRule{
			{Root: profile.StateRoot, Glob: "**", Category: "memory"},
		},
	}
	rec := enroll.Record{
		InstanceID:     instID,
		ProfileID:      profID,
		ProfileVersion: 1,
		Roots:          map[string]string{profile.StateRoot: stateDir},
		EnrolledAt:     time.Now().UTC(),
	}
	if _, err := porterenroll.WriteRecord(home, rec, prof); err != nil {
		t.Fatal(err)
	}
}

func TestSealBundleSameAndCrossHarness(t *testing.T) {
	home := t.TempDir()
	p := Paths{Home: home}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddRecipient(p, id.Recipient().String(), "test"); err != nil {
		t.Fatal(err)
	}
	setupBundleInstance(t, home, "inst-a", "harness-one")
	setupBundleInstance(t, home, "inst-b", "harness-one")
	setupBundleInstance(t, home, "inst-c", "harness-two")

	env := &porterenroll.Env{Home: home}
	// Cross-harness must be rejected before sealing anything.
	_, err = SealBundlePlanned(context.Background(), env, p, vaultrpc.SealBundleParams{
		Members: []string{"inst-a", "inst-c"},
		Tier:    vault.TierFull,
	}, vaultrpc.InitiatorClient)
	if ue, ok := valise.As(err); !ok || ue.Code != "bundle_cross_harness" {
		t.Fatalf("expected bundle_cross_harness, got %v", err)
	}

	// Same-harness bundle seal succeeds with shared BundleID and BundleMembers.
	res, err := SealBundlePlanned(context.Background(), env, p, vaultrpc.SealBundleParams{
		Members: []string{"inst-b", "inst-a"},
		Tier:    vault.TierFull,
		Note:    "bundle-note",
	}, vaultrpc.InitiatorClient)
	if err != nil {
		t.Fatalf("SealBundlePlanned: %v", err)
	}
	if res.BundleID == "" || len(res.Reports) != 2 {
		t.Fatalf("unexpected bundle result: %+v", res)
	}
	st, err := Status(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(st.Snapshots))
	}
	for _, m := range st.Snapshots {
		if m.BundleID != res.BundleID || len(m.BundleMembers) != 2 {
			t.Fatalf("snapshot missing bundle metadata: %+v", m)
		}
	}
}

func TestOnlyAutomaticRetiresManuallyEmptiedEndedPeriod(t *testing.T) {
	home := t.TempDir()
	p := Paths{Home: home}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddRecipient(p, id.Recipient().String(), "test"); err != nil {
		t.Fatal(err)
	}
	setupBundleInstance(t, home, "inst-a", "harness-one")

	// Create two ended monthly periods in the past: 2025-01 and 2025-02.
	mkEndedLeaf := func(start, end string) (string, string) {
		leaf := "inst-a/" + start + "_" + end
		dir := filepath.Join(p.Vaults(), "inst-a", start+"_"+end)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		per := vault.Period{Format: vault.PeriodFormat, InstanceID: "inst-a", Unit: vault.UnitMonth, TZ: "UTC", Start: start, End: end}
		b, _ := json.Marshal(per)
		if err := os.WriteFile(filepath.Join(dir, vault.PeriodFile), b, 0o600); err != nil {
			t.Fatal(err)
		}
		w, err := vault.OpenWriter(dir, []string{id.Recipient().String()}, p.DedupeKey, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		m, err := w.Commit(vault.Snapshot{
			InstanceID: "inst-a", Tier: vault.TierSoul,
			Profile:   vault.ProfileRef{ID: "harness-one", Version: 1},
			CreatedAt: time.Now().UTC(),
			Roots:     map[string]string{"state": "/s"},
		}, vault.Files{}, vault.Meta{Kind: vault.KindAuto})
		if err != nil {
			t.Fatal(err)
		}
		return leaf, m.SnapshotID
	}

	leaf1, snap1 := mkEndedLeaf("2025-01-01", "2025-01-31")
	leaf2, _ := mkEndedLeaf("2025-02-01", "2025-02-28")

	// Manually delete the only snapshot in leaf1 so leaf1 is now empty.
	delRes, err := DeleteSnapshots(p, vaultrpc.SnapshotDeleteParams{Leaf: leaf1, SnapshotIDs: []string{snap1}}, vaultrpc.InitiatorClient)
	if err != nil || delRes.Deleted != 1 {
		t.Fatalf("DeleteSnapshots: %+v %v", delRes, err)
	}

	// Retain keepPeriods=1: empty ended leaf1 must be retired without blocking!
	if err := RetireExpiredAutoLeaves(p, "inst-a", 1); err != nil {
		t.Fatalf("RetireExpiredAutoLeaves: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Vaults(), filepath.FromSlash(leaf1))); !os.IsNotExist(err) {
		t.Fatalf("expected emptied ended period %s to be retired", leaf1)
	}
	if _, err := os.Stat(filepath.Join(p.Vaults(), filepath.FromSlash(leaf2))); err != nil {
		t.Fatalf("expected kept ended period %s to remain: %v", leaf2, err)
	}
}
