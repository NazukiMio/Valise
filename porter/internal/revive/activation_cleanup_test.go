package revive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/enroll"
	"valise/core/revive"
	"valise/core/valise"
	"valise/porter/internal/activationrecord"
	porterenroll "valise/porter/internal/enroll"
)

func TestOldCleanupTreeOnlyReadsMetadataAndDoesNotFollowExternalLinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(dir, "unreadable-agent-data"), "private contents", 0000)
	write(t, filepath.Join(outside, "VAULT"), `{"format":"valise-vault/1"}`, 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "VAULT"), filepath.Join(dir, "VAULT")); err != nil {
		t.Fatal(err)
	}
	first, files, bytes, err := oldCleanupTree(context.Background(), dir, true)
	if err != nil || len(first) != 64 || files != 3 || bytes != int64(len("private contents")) {
		t.Fatal("ordinary data read or outside symlink followed", files, bytes, err)
	}
	second, _, _, err := oldCleanupTree(context.Background(), dir, true)
	if err != nil || second != first {
		t.Fatal("unchanged metadata gave different review", err)
	}
	write(t, filepath.Join(dir, "added"), "new", 0600)
	changed, _, _, err := oldCleanupTree(context.Background(), dir, true)
	if err != nil || changed == first {
		t.Fatal("old tree change omitted from review", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := oldCleanupTree(ctx, dir, true); err != context.Canceled {
		t.Fatal("cancelled traversal continued", err)
	}
}

func TestOldCleanupTreeProtectsNestedBackupMetadataAndDamagedDescriptorLayout(t *testing.T) {
	for _, kind := range []string{"descriptor", "damaged-layout"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			backup := filepath.Join(dir, "nested")
			if kind == "descriptor" {
				write(t, filepath.Join(backup, "VAULT"), `{"format":"valise-vault/2"}`, 0600)
			} else {
				write(t, filepath.Join(backup, "VAULT"), "damaged public descriptor", 0600)
				for _, sub := range []string{"packs", "snapshots", "index"} {
					if err := os.Mkdir(filepath.Join(backup, sub), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, _, _, err := oldCleanupTree(context.Background(), dir, true)
			if e, ok := valise.As(err); !ok || e.Code != "environment_cleanup_contains_vault" {
				t.Fatal("nested backup allowed in old data cleanup", err)
			}
			// An owned, stopped probe reconstruction is temporary material,
			// separately bounded by creation claims and lifecycle locks.
			if _, _, _, err := oldCleanupTree(context.Background(), dir, false); err != nil {
				t.Fatal("private temporary reconstruction treated as registered backup", err)
			}
		})
	}
}

func TestOldCleanupProtectsRetiredRegistrationAndItsRealDirectoryAlias(t *testing.T) {
	w, p := preparationFixture(t)
	path, err := preparationResource(w.home, p, "program-original", "")
	if err != nil {
		t.Fatal(err)
	}
	otherRoot := filepath.Join(path, "retired-instance")
	write(t, filepath.Join(otherRoot, "keep"), "retained data", 0600)
	alias := filepath.Join(t.TempDir(), "registered-alias")
	if err := os.Symlink(otherRoot, alias); err != nil {
		t.Fatal(err)
	}
	rec := enroll.Record{InstanceID: "demo-archived", Name: "archived", ProfileID: w.p.ID, ProfileVersion: w.p.ProfileVersion, Roots: map[string]string{"state": alias}, Superseded: &enroll.Supersession{By: "another", OpID: p.Plan.OpID}}
	if _, err := porterenroll.WriteRecord(w.home, rec, w.p); err != nil {
		t.Fatal(err)
	}
	a := NewActivationRunner(w.r, nil)
	activation := &ActivationRecord{Plan: ActivationPlan{Agent: p.Params.Agent, Original: p.Original, Candidate: p.Candidate}}
	if err := a.protectOldCleanup(context.Background(), activation, path, "program"); err == nil {
		t.Fatal("retired registered alias allowed old-program deletion")
	}
	if read(t, filepath.Join(otherRoot, "keep")) != "retained data" {
		t.Fatal("retired registered data modified")
	}
}

func TestOldCleanupPrivateProgressKeepsLegacyReadAndRejectsUnprovenRemoval(t *testing.T) {
	w, rec := activationRecordFixture(t)
	if _, err := loadActivation(w.home, rec.Plan.Agent, rec.Plan.OperationID); err != nil {
		t.Fatal("legacy record without cleanup rejected", err)
	}
	rec.Cleanup = &activationrecord.Cleanup{Target: rec.Plan.Original, ProbeStopped: true,
		Items: []activationrecord.CleanupItem{{Kind: "data", Root: "state", Identity: revive.ManagedRoot{ID: "data:state", Inode: 1}}, {Kind: "program", Identity: revive.ManagedRoot{ID: "program:", Inode: 2}}, {Kind: "preparation", Identity: revive.ManagedRoot{ID: "preparation:", Inode: 3}}}}
	if err := saveActivation(w.home, rec); err != nil {
		t.Fatal("valid pending cleanup rejected", err)
	}
	for _, change := range []string{"removed-without-intent", "complete-before-removal", "missing-stopped-proof", "different-target", "duplicate-root"} {
		t.Run(change, func(t *testing.T) {
			copy, err := loadActivation(w.home, rec.Plan.Agent, rec.Plan.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "removed-without-intent":
				copy.Cleanup.Items[0].Removed = true
			case "complete-before-removal":
				copy.Cleanup.Complete = true
			case "missing-stopped-proof":
				copy.Cleanup.ProbeStopped = false
			case "different-target":
				copy.Cleanup.Target = rec.Plan.Candidate
			case "duplicate-root":
				copy.Cleanup.Items[1] = copy.Cleanup.Items[0]
			}
			if err := saveActivation(w.home, copy); err == nil {
				t.Fatal("unproven private cleanup accepted")
			}
		})
	}
}
