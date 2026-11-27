package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"valise/client/config"
	"valise/core/protection"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func testServiceWithRepo(t *testing.T) (*Service, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	cacheDir := t.TempDir()
	repoDir := filepath.Join(t.TempDir(), "usb")
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Service{
		Dirs:   config.Dirs{Data: dataDir, Cache: cacheDir},
		Config: config.Config{Repos: []config.Repo{{Name: "usb", Path: repoDir}}},
	}
	return s, repoDir, "box1"
}

func sealRepoSnapshot(t *testing.T, leafDir, keyDir string, id *age.X25519Identity, inst string, content map[string][]byte, bundleID string, members []string) *vault.Meta {
	t.Helper()
	w, err := vault.OpenWriter(leafDir, []string{id.Recipient().String()}, func(v string) string {
		return filepath.Join(keyDir, v+".key")
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var files vault.Files
	for name, b := range content {
		blobs, sum, n, err := w.AddStream(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		files.Files = append(files.Files, vault.File{
			Root: "state", Path: name, Category: "memory", Mode: 0o644,
			MTime: time.Unix(1, 0).UTC(), Size: n, SHA256: sum, Blobs: blobs,
		})
	}
	m, err := w.Commit(vault.Snapshot{
		InstanceID: inst, Tier: vault.TierFull,
		Profile:   vault.ProfileRef{ID: "demo-harness", Version: 1},
		CreatedAt: time.Now().UTC(),
		Roots:     map[string]string{"state": "/s"},
	}, files, vault.Meta{
		Kind:          vault.KindSavepoint,
		BundleID:      bundleID,
		BundleMembers: members,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPreviewAndDeleteSnapshotsLocalRepo(t *testing.T) {
	s, repoDir, serverName := testServiceWithRepo(t)
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keyDir := t.TempDir()
	leafDir := filepath.Join(repoDir, serverName, "inst-a", vault.SavepointsPeriod)
	bid := vault.NewBundleID(time.Now().UTC())
	m1 := sealRepoSnapshot(t, leafDir, keyDir, id, "inst-a", map[string][]byte{"a.md": []byte("alpha-data")}, bid, []string{"inst-a", "inst-b"})
	m2 := sealRepoSnapshot(t, leafDir, keyDir, id, "inst-a", map[string][]byte{"b.md": []byte("beta-data")}, "", nil)

	req := SnapshotDeleteRequest{
		Leaf:        "inst-a/" + vault.SavepointsPeriod,
		SnapshotIDs: []string{m1.SnapshotID},
		Locations:   []SnapshotDeleteLocation{{Kind: SnapshotLocationLocalRepo, Name: "usb"}},
		ServerName:  serverName,
	}
	prev, err := s.PreviewSnapshotDelete(context.Background(), req)
	if err != nil {
		t.Fatalf("PreviewSnapshotDelete: %v", err)
	}
	if len(prev.BundleImpacts) != 1 || prev.BundleImpacts[0].BundleID != bid || len(prev.BundleImpacts[0].BundleMembers) != 2 {
		t.Fatalf("unexpected bundle impacts: %+v", prev.BundleImpacts)
	}

	res, err := s.DeleteSnapshots(context.Background(), req)
	if err != nil {
		t.Fatalf("DeleteSnapshots: %v", err)
	}
	if res.Partial || len(res.Results) != 1 {
		t.Fatalf("unexpected batch result: %+v", res)
	}
	r := res.Results[0]
	if r.Deleted != 1 || r.PacksRemoved == 0 || r.FreedBytes == 0 {
		t.Fatalf("unexpected location result: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(leafDir, vault.MetaPath(m2.SnapshotID))); err != nil {
		t.Fatalf("remaining snapshot lost: %v", err)
	}
	if probs, err := vault.VerifyNoKey(leafDir); err != nil || len(probs) != 0 {
		t.Fatalf("vault verify failed: %v %v", probs, err)
	}
}

func TestDeleteLastSnapshotLocalRepoIdempotent(t *testing.T) {
	s, repoDir, serverName := testServiceWithRepo(t)
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keyDir := t.TempDir()
	leafDir := filepath.Join(repoDir, serverName, "inst-a", vault.SavepointsPeriod)
	m1 := sealRepoSnapshot(t, leafDir, keyDir, id, "inst-a", map[string][]byte{"only.md": []byte("only-data")}, "", nil)

	req := SnapshotDeleteRequest{
		Leaf:        "inst-a/" + vault.SavepointsPeriod,
		SnapshotIDs: []string{m1.SnapshotID},
		Locations:   []SnapshotDeleteLocation{{Kind: SnapshotLocationLocalRepo, Name: "usb"}},
		ServerName:  serverName,
	}
	res1, err := s.DeleteSnapshots(context.Background(), req)
	if err != nil || res1.Partial || res1.Results[0].Deleted != 1 {
		t.Fatalf("first delete last snapshot: %+v %v", res1, err)
	}
	// Repeating deletion on now-empty leaf must succeed idempotently.
	res2, err := s.DeleteSnapshots(context.Background(), req)
	if err != nil || res2.Partial {
		t.Fatalf("second delete on empty leaf failed: %+v %v", res2, err)
	}
	if r := res2.Results[0]; r.Deleted != 0 || len(r.Missing) != 1 || r.Missing[0] != m1.SnapshotID {
		t.Fatalf("unexpected idempotent result: %+v", r)
	}
}

func TestCapabilitiesV4AndNewRPCs(t *testing.T) {
	oldHello := &rpc.HelloResult{Capabilities: []string{
		protection.Capability, protection.CapabilityV2, protection.CapabilityV3,
		protection.BackupCapability, protection.BackupCapabilityV2, protection.BackupCapabilityV3,
		vaultrpc.SavepointsCapability,
	}}
	newHello := &rpc.HelloResult{Capabilities: append(append([]string(nil), oldHello.Capabilities...),
		protection.CapabilityV4, protection.BackupCapabilityV4,
		vaultrpc.SnapshotDeleteCapability, vaultrpc.BundleSealCapability,
	)}

	// V4 prepare with FullCycle or BundleMembers must fail on old porter, pass on new porter.
	enabled := true
	prepV4 := protection.PrepareParams{
		Desired: protection.Desired{
			Schedule:      protection.Schedule{Enabled: &enabled, FullCycle: protection.FullCycleWeekly},
			BundleMembers: []string{"a", "b"},
		},
	}
	if err := n2Compatibility(oldHello, protection.MethodPrepare, prepV4); err == nil {
		t.Fatal("old porter accepted v4 prepare")
	}
	if err := n2Compatibility(newHello, protection.MethodPrepare, prepV4); err != nil {
		t.Fatalf("new porter rejected v4 prepare: %v", err)
	}

	// Backup V4 with explicit Targets must fail on old porter, pass on new porter.
	bakV4 := protection.BackupParams{
		Selection: protection.BackupSelection{Tier: "full", Targets: []string{"nas"}},
	}
	if err := n2Compatibility(oldHello, protection.MethodBackup, bakV4); err == nil {
		t.Fatal("old porter accepted backup-runs/4 targets")
	}
	if err := n2Compatibility(newHello, protection.MethodBackup, bakV4); err != nil {
		t.Fatalf("new porter rejected backup-runs/4 targets: %v", err)
	}

	// snapshot_delete, target_snapshot_delete, sync_record(ForgetSnapshots), seal_bundle.
	for _, tc := range []struct {
		method string
		params any
	}{
		{vaultrpc.MethodSnapshotDelete, vaultrpc.SnapshotDeleteParams{Leaf: "a/savepoints", SnapshotIDs: []string{"s_01"}}},
		{vaultrpc.MethodTargetSnapshotDelete, vaultrpc.TargetSnapshotDeleteParams{Target: "nas", Leaf: "a/savepoints", SnapshotIDs: []string{"s_01"}}},
		{vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: "nas", ForgetSnapshots: []string{"s_01"}}},
		{vaultrpc.MethodSealBundle, vaultrpc.SealBundleParams{Members: []string{"a", "b"}, Tier: "full"}},
	} {
		if err := n2Compatibility(oldHello, tc.method, tc.params); err == nil {
			t.Errorf("old porter accepted %s", tc.method)
		} else if ue, ok := valise.As(err); !ok || ue.Code != "unsupported_capability" {
			t.Errorf("%s unexpected error: %v", tc.method, err)
		}
		if err := n2Compatibility(newHello, tc.method, tc.params); err != nil {
			t.Errorf("new porter rejected %s: %v", tc.method, err)
		}
	}

	// Verify JSON round-trip of SnapshotDeleteRequest does not lose fields.
	b, _ := json.Marshal(SnapshotDeleteRequest{Host: "h", Leaf: "a/savepoints", SnapshotIDs: []string{"s_1"}, Locations: []SnapshotDeleteLocation{{Kind: SnapshotLocationServerVault}}})
	if len(b) == 0 {
		t.Fatal("empty json")
	}
}
