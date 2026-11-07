package journey

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/core/vault"
)

func writeJSON(t *testing.T, p string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	write(t, p, b)
}
func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T, root, vid, sid string, at time.Time) {
	t.Helper()
	writeJSON(t, filepath.Join(root, "VAULT"), vault.Descriptor{Format: vault.FormatVault, VaultID: vid})
	writeJSON(t, filepath.Join(root, vault.MetaPath(sid)), vault.Meta{Format: vault.FormatMeta, SnapshotID: sid, InstanceID: "sample", Tier: "full", CreatedAt: at, Epoch: 1})
	write(t, filepath.Join(root, vault.SnapshotPath(sid)), []byte("encrypted header fixture"))
}
func source(root, id string) Source {
	return Source{Root: root, LocationRef: LocationRef{Kind: "local_repo", ID: id, AuthorityID: "local"}}
}

func TestScanPairsDedupAndConflicts(t *testing.T) {
	now := time.Now().UTC()
	vid, sid := vault.NewID("v", now), vault.NewID("s", now)
	a, b := t.TempDir(), t.TempDir()
	fixture(t, a, vid, sid, now)
	fixture(t, b, vid, sid, now)
	result, err := Scan(context.Background(), []Source{source(a, "a"), source(b, "b")})
	if err != nil || result.Partial || len(result.Points) != 1 || len(result.Points[0].Copies) != 2 {
		t.Fatalf("unexpected result: %+v %v", result, err)
	}
	if p := result.Points[0]; p.MetadataState != "metadata_only" || p.Copies[0].MaterialStatus != "unknown" {
		t.Fatalf("overclaimed evidence: %+v", p)
	}
	write(t, filepath.Join(b, vault.SnapshotPath(sid)), []byte("different header"))
	result, err = Scan(context.Background(), []Source{source(a, "a"), source(b, "b")})
	if err != nil || !result.Partial || result.Points[0].MetadataState != "conflict" || result.Points[0].Copies[0].MetadataState != "conflict" {
		t.Fatalf("conflict lost: %+v %v", result, err)
	}
	// The stable identity includes the vault, even when snapshot IDs coincide.
	fixture(t, b, vault.NewID("v", now), sid, now)
	result, _ = Scan(context.Background(), []Source{source(a, "a"), source(b, "b")})
	if len(result.Points) != 2 {
		t.Fatal("merged different vaults")
	}
}

func TestScanOrphansInvalidAndUnavailable(t *testing.T) {
	now := time.Now().UTC()
	vid, sid := vault.NewID("v", now), vault.NewID("s", now)
	root := t.TempDir()
	fixture(t, root, vid, sid, now)
	// No delete command: rename the published header out of the allowed set.
	if err := os.Rename(filepath.Join(root, vault.SnapshotPath(sid)), filepath.Join(root, "held")); err != nil {
		t.Fatal(err)
	}
	orphan := vault.NewID("s", now)
	write(t, filepath.Join(root, vault.SnapshotPath(orphan)), []byte("orphan"))
	write(t, filepath.Join(root, "snapshots", ".temporary.meta.json"), []byte("{}"))
	result, err := Scan(context.Background(), []Source{source(root, "a"), source(filepath.Join(t.TempDir(), "missing"), "offline")})
	if err != nil || len(result.Points) != 0 || !result.Partial || len(result.Gaps) != 2 || result.Observations[1].Status != "unknown" {
		t.Fatalf("orphan became saved: %+v %v", result, err)
	}
}

func TestScanTreePartialAndSymlink(t *testing.T) {
	now := time.Now().UTC()
	vid, sid := vault.NewID("v", now), vault.NewID("s", now)
	root := t.TempDir()
	leaf := filepath.Join(root, "server", "sample", "2026-11-01_2026-11-30")
	fixture(t, leaf, vid, sid, now)
	write(t, leaf+".partial.json", []byte("{}"))
	outside := t.TempDir()
	fixture(t, outside, vault.NewID("v", now), vault.NewID("s", now), now)
	if err := os.Symlink(outside, filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	result, err := Scan(context.Background(), []Source{source(root, "repo")})
	if err != nil || len(result.Points) != 1 || !result.Partial || result.Points[0].Copies[0].Observation.Status != "unknown" {
		t.Fatalf("unsafe discovery: %+v %v", result, err)
	}
}

func TestScanValidatesMetadataAndCancellation(t *testing.T) {
	now := time.Now().UTC()
	vid, sid := vault.NewID("v", now), vault.NewID("s", now)
	for _, change := range []func(*vault.Meta){func(m *vault.Meta) { m.SnapshotID = vid }, func(m *vault.Meta) { m.Format = "future" }, func(m *vault.Meta) { m.Tier = "custom" }, func(m *vault.Meta) { m.Epoch = 0 }, func(m *vault.Meta) { m.FileCount = -1 }} {
		root := t.TempDir()
		fixture(t, root, vid, sid, now)
		m := vault.Meta{Format: vault.FormatMeta, SnapshotID: sid, InstanceID: "sample", Tier: "full", CreatedAt: now, Epoch: 1}
		change(&m)
		writeJSON(t, filepath.Join(root, vault.MetaPath(sid)), m)
		result, err := Scan(context.Background(), []Source{source(root, "a")})
		if err != nil || len(result.Points) != 0 || !result.Partial {
			t.Fatalf("invalid metadata accepted: %+v", result)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, []Source{source(t.TempDir(), "a")}); err != context.Canceled {
		t.Fatal(err)
	}
}
