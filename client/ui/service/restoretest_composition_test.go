package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func bindTestPoint(t *testing.T, s *Service, home string, sealed *vaultrpc.SealReport, id string, wrongGeneration bool) identity.SnapshotRef {
	t.Helper()
	var binding identity.Binding
	if err := s.call(context.Background(), "box", identity.MethodGet, identity.GetParams{InstanceID: "demo"}, &binding); err != nil || binding.Agent == nil {
		t.Fatal("fixture has no accepted Agent", err)
	}
	a := binding.Agent.Ref
	if wrongGeneration {
		a.Generation = "another-generation"
	}
	request := protection.BackupParams{AgentRef: a, RunID: strings.Repeat(id, 32), ExpectedRevision: 1, Reason: "manual"}
	digest, err := protection.Digest(request)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.VaultStatus(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	var ref identity.SnapshotRef
	for _, leaf := range status.Leaves {
		if leaf.Leaf == sealed.Leaf {
			ref = identity.SnapshotRef{VaultID: leaf.VaultID, SnapshotID: sealed.SnapshotID}
		}
	}
	if ref.VaultID == "" {
		t.Fatal("fixture vault missing")
	}
	desired := protection.NewDefault()
	desired.Schedule.DailyAt = "03:00"
	desired.Schedule.Timezone = "UTC"
	desired.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	now := time.Now().UTC()
	run := protection.BackupRun{Schema: protection.BackupRunSchema, RunID: request.RunID, Request: request, RequestDigest: digest, AgentRef: a, Snapshot: &ref, State: "succeeded", AcceptedAt: now, UpdatedAt: now,
		Plan: protection.ServerPlan{SchemaVersion: protection.Schema, AgentRef: a, Revision: 1, AcceptedAt: now, Desired: desired}, Components: []protection.BackupComponent{{Kind: "server_backup", State: "succeeded"}}}
	b, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local/state/valise/runs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, request.RunID+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestComposedTestSelectsNearestPastSameAgentAndPreservesDeletion(t *testing.T) {
	s, home := environmentServiceWorld(t)
	ctx := context.Background()
	write := func(rel, value string) {
		t.Helper()
		p := filepath.Join(home, ".demo", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	seal := func(tier, id string, wrong bool) identity.SnapshotRef {
		t.Helper()
		rep, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		return bindTestPoint(t, s, home, rep, id, wrong)
	}
	write(".env", "older-base")
	write("memory/deleted", "removed-at-target")
	seal("full", "a", false)
	write(".env", "chosen-base")
	expected := seal("full", "b", false)
	write(".env", "another-generation")
	seal("full", "c", true)
	if err := os.Rename(filepath.Join(home, ".demo/memory/deleted"), filepath.Join(t.TempDir(), "removed")); err != nil {
		t.Fatal(err)
	}
	write("memory/a.md", "selected-target")
	write(".env", "at-target-live")
	target := seal("soul", "d", false)
	write(".env", "future-live")
	seal("full", "e", false)
	if _, err := s.AddServers("offline"); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKESSH_LOG", log)
	var points JourneyPage
	if err := s.ScanTimelineInScope(ctx, TimelineScanRequest{ConnectionIDs: []string{"box"}}, func(u TimelineUpdate) { points = u.Page }); err != nil {
		t.Fatal(err)
	}
	var req SelectedRestoreTest
	for _, point := range points.Items {
		if point.SnapshotRef.SnapshotID == target.SnapshotID {
			req = SelectedRestoreTest{Depth: "isolated", Target: "box", Instance: "demo", Snapshot: point.SnapshotRef, Source: s.RestoreSources(point)[0]}
		}
	}
	if result, err := s.RunSelectedRestoreTest(ctx, req); err == nil || result != nil {
		t.Fatal("composition bypassed human material review")
	}
	req.ReviewMaterials = func(TestMaterialReview) bool { return false }
	if result, err := s.RunSelectedRestoreTest(ctx, req); err == nil || result != nil {
		t.Fatal("cancelled material plan executed")
	}
	if _, ok := s.LastRestoreTest("box", "demo"); ok {
		t.Fatal("cancelled plan created an execution record")
	}
	reviews := 0
	req.ReviewMaterials = func(review TestMaterialReview) bool {
		reviews++
		if review.Baseline == nil || review.Baseline.Snapshot != expected || review.TargetTier != "soul" || review.Files == 0 || !review.BaselineCredentials {
			t.Fatal("wrong composition review", review)
		}
		if pending, _ := filepath.Glob(filepath.Join(home, ".local/state/valise/journal", "*.json")); len(pending) > 0 {
			t.Fatal("test staged before material confirmation")
		}
		// Deterministically stop before any fake kernel isolation. The real
		// Linux lab separately exercises both full and composed gateways.
		if err := os.WriteFile(filepath.Join(home, ".demo/bin/gateway"), []byte("changed program"), 0700); err != nil {
			t.Fatal(err)
		}
		return true
	}
	result, err := s.RunSelectedRestoreTest(ctx, req)
	if err != nil || result == nil || result.OK || !result.LocalVerified || !result.ServerVerified || result.Cleanup != "removed" || result.Materials == nil || result.Environment == nil || result.Environment.FailureCode != "environment_source_unavailable" || reviews != 1 {
		t.Fatalf("composition execution %+v %v", result, err)
	}
	if result.Environment.Baseline == nil || result.Environment.Baseline.Snapshot != expected {
		t.Fatal("server lost actual baseline provenance")
	}
	o, err := s.ownerPorter(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	var journal revive.Journal
	if err := s.callOwner(ctx, o, nil, revive.MethodJournal, revive.OpParams{OpID: result.OpID}, &journal); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range journal.Manifest {
		if entry.Path == "state/memory/deleted" {
			t.Fatal("deleted in-scope file resurrected")
		}
		if entry.Path == "state/.env" {
			found = entry.SHA256 == vault.SHA256Hex([]byte("chosen-base"))
		}
	}
	if !found || readFile(t, filepath.Join(home, ".demo/.env")) != "future-live" || readFile(t, filepath.Join(home, ".demo/memory/a.md")) != "selected-target" {
		t.Fatal("base reader used or changed active content")
	}
	if bytes, err := os.ReadFile(log); err != nil || strings.Contains(string(bytes), "offline") {
		t.Fatal("composition scanned unrelated server", err)
	}
}
