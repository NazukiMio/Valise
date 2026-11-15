package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/journey"
	"valise/core/protection"
	"valise/core/rpc"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func TestJourneyAllLocationsOverFakeSSH(t *testing.T) {
	s, home, _ := acceptedPlanWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := s.AddServers("box", "offline"); err != nil {
		t.Fatal(err)
	}
	if err := s.ServerNameSet(ctx, "box", "named-box"); err != nil {
		t.Fatal(err)
	}
	first, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: "full"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	leaf := filepath.Join("named-box", filepath.FromSlash(first.Leaf))
	for _, rel := range []string{vault.DescriptorFile, vault.MetaPath(first.SnapshotID), vault.SnapshotPath(first.SnapshotID)} {
		b, err := os.ReadFile(filepath.Join(home, ".local/share/valise/vaults", filepath.FromSlash(first.Leaf), rel))
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(root, leaf, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.Config.Repos = []config.Repo{{Name: "disk", Path: root}}
	second, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: "soul"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListTimeline(ctx, JourneyListRequest{AllLocations: true, Limit: 1})
	if err != nil || page.NextCursor == "" || !page.Partial {
		t.Fatalf("page %+v %v", page, err)
	}
	next, err := s.ListTimeline(ctx, JourneyListRequest{AllLocations: true, Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.ViewRevision != page.ViewRevision {
		t.Fatalf("next %+v %v", next, err)
	}
	points := append(page.Items, next.Items...)
	for _, p := range points {
		switch p.SnapshotRef.SnapshotID {
		case first.SnapshotID:
			if len(p.Copies) != 2 || p.MetadataState != "metadata_only" {
				t.Fatalf("duplicate wasn't merged: %+v", p)
			}
		case second.SnapshotID:
			if len(p.Copies) != 1 || p.Copies[0].LocationRef.Kind != "server_vault" {
				t.Fatalf("server-only point lost: %+v", p)
			}
		default:
			t.Fatalf("unexpected point %+v", p)
		}
	}
	local, err := s.ListTimeline(ctx, JourneyListRequest{})
	if err != nil || len(local.Items) != 1 {
		t.Fatalf("local selection changed: %+v %v", local, err)
	}
	// Conflicting copies remain one flagged point rather than two valid nodes.
	other := points[0]
	other.Copies = append([]journey.Copy(nil), other.Copies...)
	other.Copies[0].HeaderDigest = "different"
	merged := journey.Merge(journey.Result{Points: points}, journey.Result{Points: []journey.TimelinePoint{other}})
	if !merged.Partial || len(merged.Points) != 2 {
		t.Fatalf("conflict wasn't kept: %+v", merged)
	}
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")
	stale, err := s.ListTimeline(ctx, JourneyListRequest{AllLocations: true})
	if err != nil || len(stale.Items) != 2 || !stale.Partial {
		t.Fatalf("failed refresh erased observed history: %+v %v", stale, err)
	}
	for _, p := range stale.Items {
		if p.SnapshotRef.SnapshotID == second.SnapshotID && p.Copies[0].Observation.Status != "stale" {
			t.Fatalf("stale point presented as current: %+v", p)
		}
	}
}

func TestOneOffBackupOverFakeSSHLeavesPlanUnchanged(t *testing.T) {
	s, _, ref := acceptedPlanWorld(t)
	ctx := context.Background()
	if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: "unused", Kind: "rsync", Dest: "sink:/backup"}); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetProtection(ctx, "box", protection.GetParams{AgentRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	d := v.ServerPlan.Desired
	on := true
	d.ServerCopies = []protection.ServerCopy{{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "unused", AuthorityID: ref.ServerUID}, Enabled: &on, MinimumInterval: "1h"}}
	change, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{AgentRef: ref, ExpectedRevision: v.ServerPlan.Revision, Desired: d})
	if err != nil {
		t.Fatal(err)
	}
	if task, err := s.StartProtectionChange(ctx, change.PlanID, "copies"); err != nil || task.State != "succeeded" {
		t.Fatalf("change %+v %v", task, err)
	}
	before, err := s.GetProtection(ctx, "box", protection.GetParams{AgentRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	s.BackupReview = approveBackup
	selection := protection.BackupSelection{Tier: "soul", SkipCopies: true}
	p, err := s.PrepareOneOffBackup(ctx, "box", ref, selection)
	if err != nil {
		t.Fatal(err)
	}
	if p.IncludesCredentials || p.Selection != selection {
		t.Fatalf("wrong scope %+v", p)
	}
	task, err := s.StartBackup(ctx, p.PlanID, "one-off")
	if err != nil || task.State != "succeeded" {
		t.Fatalf("task %+v %v", task, err)
	}
	if task.Run.Request.Selection != selection || task.Run.Components[1].State != "skipped" {
		t.Fatalf("copy ran despite selection: %+v", task.Run)
	}
	status, err := s.VaultStatus(ctx, "box")
	if err != nil || len(status.Snapshots) != 1 || status.Snapshots[0].Tier != "soul" {
		t.Fatalf("scope %+v %v", status, err)
	}
	after, err := s.GetProtection(ctx, "box", protection.GetParams{AgentRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	bd, _ := protection.Digest(before.ServerPlan)
	ad, _ := protection.Digest(after.ServerPlan)
	if bd != ad {
		t.Fatal("one-off changed the accepted protection plan")
	}
	regular, err := s.PrepareBackup(ctx, "box", ref)
	if err != nil || regular.Selection != (protection.BackupSelection{}) || regular.ServerPlan.Scope.Tier != "full" {
		t.Fatalf("selection leaked: %+v %v", regular, err)
	}
}

func TestOneOffCapabilityCannotBeSilentlyDropped(t *testing.T) {
	old := &rpc.HelloResult{Capabilities: []string{protection.BackupCapability, protection.BackupCapabilityV2}}
	p := protection.BackupParams{Selection: protection.BackupSelection{Tier: "soul", SkipCopies: true}}
	if err := n2Compatibility(old, protection.MethodBackup, p); err == nil {
		t.Fatal("one-off sent to old porter")
	}
	old.Capabilities = append(old.Capabilities, protection.BackupCapabilityV3)
	if err := n2Compatibility(old, protection.MethodBackup, p); err != nil {
		t.Fatal(err)
	}
}
