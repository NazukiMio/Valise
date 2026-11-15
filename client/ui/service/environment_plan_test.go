package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func TestReadOnlyPlanBindsExactInputAndCannotBeTreatedAsReadiness(t *testing.T) {
	p := revive.EnvironmentPlanParams{Agent: identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "one"}, SettingsDigest: strings.Repeat("c", 64), Input: environment.TestInput{VaultID: "vault", Header: vault.Snapshot{SnapshotID: "snapshot", CreatedAt: time.Now(), Tier: "full"}}}
	v := revive.EnvironmentPlan{Agent: p.Agent, Snapshot: identity.SnapshotRef{VaultID: p.Input.VaultID, SnapshotID: p.Input.Header.SnapshotID}, CreatedAt: p.Input.Header.CreatedAt, Tier: "full", Mode: environment.Parallel, InputDigest: planInputDigest(p), Digest: strings.Repeat("a", 64), NativeRunning: true, NativeUnit: "fixture.service", ProgramMatches: true, ProgramVersion: "1.2.3", ProgramFiles: 1, ProgramBytes: 100, Files: 2, Bytes: 20, LogicalRoots: 2, PhysicalRoots: 1, Space: []revive.EnvironmentSpace{{NeedBytes: 1000, FreeBytes: 2000}}}
	if err := validateEnvironmentPlan(v, p); err != nil {
		t.Fatal(err)
	}
	managed := v
	managed.ManagedSource = true
	managed.Original, managed.Candidate = &environment.Ref{Slot: environment.B, Revision: 1}, &environment.Ref{Slot: environment.A, Revision: 2}
	if err := validateEnvironmentPlan(managed, p); err != nil {
		t.Fatal("managed source preview rejected", err)
	}
	missing := managed
	missing.Candidate = nil
	if validateEnvironmentPlan(missing, p) == nil {
		t.Fatal("managed preview guessed missing target revision")
	}
	missing = managed
	missing.ManagedSource = false
	if validateEnvironmentPlan(missing, p) == nil {
		t.Fatal("native preview accepted managed source references")
	}
	for _, change := range []func(*revive.EnvironmentPlan){
		func(v *revive.EnvironmentPlan) { v.Snapshot.SnapshotID = "other" },
		func(v *revive.EnvironmentPlan) { v.Agent.Generation = "other" },
		func(v *revive.EnvironmentPlan) { v.InputDigest = strings.Repeat("b", 64) },
		func(v *revive.EnvironmentPlan) { v.NativeRunning = false },
		func(v *revive.EnvironmentPlan) { v.ProgramMatches = false },
		func(v *revive.EnvironmentPlan) { v.Mode = environment.Direct },
		func(v *revive.EnvironmentPlan) { v.LogicalRoots = 0 },
		func(v *revive.EnvironmentPlan) { v.Baseline = &environment.DataOrigin{} },
	} {
		modified := v
		change(&modified)
		if validateEnvironmentPlan(modified, p) == nil {
			t.Fatal("unbound preview accepted", modified)
		}
	}
	if n2Compatibility(&rpc.HelloResult{}, environment.MethodPlan, p) == nil {
		t.Fatal("plan sent to old porter")
	}
	if err := n2Compatibility(&rpc.HelloResult{Capabilities: []string{environment.PlanCapability}}, environment.MethodPlan, p); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentPreviewFacadeOnlyReadsSelectedMetadataOverFakeSSH(t *testing.T) {
	s, home := environmentServiceWorld(t)
	ctx := context.Background()
	sealed, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: "full"})
	if err != nil {
		t.Fatal(err)
	}
	bindTestPoint(t, s, home, sealed, "d", false)
	page, err := s.ListTimeline(ctx, JourneyListRequest{AllLocations: true, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	var req RestoreRequest
	for _, point := range page.Items {
		if point.SnapshotRef.SnapshotID == sealed.SnapshotID {
			req = RestoreRequest{Options: ReviveOptions{From: "box", Target: "box", Mode: revive.ModeRollback, SnapshotID: sealed.SnapshotID}, Snapshot: point.SnapshotRef, Source: s.RestoreSources(point)[0]}
		}
	}
	if req.Snapshot.SnapshotID == "" {
		t.Fatal("selected point not found")
	}
	view, err := s.PreviewEnvironmentRestore(ctx, req)
	// The fixture has no real Linux user unit. Reaching this typed error
	// proves the new read-only protocol was called after exact decryption.
	if view != nil || journeyCode(err) != "environment_plan_platform" && journeyCode(err) != "environment_plan_native" {
		t.Fatal(view, err)
	}
	if _, ok := s.LastRestoreTest("box", "demo"); ok {
		t.Fatal("preview created a test execution record")
	}
	if tasks, err := s.ListRestoreTasks(); err != nil || len(tasks) != 0 {
		t.Fatal("preview created a restore task", tasks, err)
	}
	for _, dir := range []string{"journal", "programs", "activations", "environment-tests", "environments"} {
		if _, err := os.Stat(filepath.Join(home, ".local/state/valise", dir)); !os.IsNotExist(err) {
			t.Fatal("preview wrote operation state", dir, err)
		}
	}
	if got := readFile(t, filepath.Join(home, ".demo/memory/a.md")); got != "m" {
		t.Fatal("preview changed active data", got)
	}
	req.Source.Copy.HeaderDigest = "changed-copy"
	if view, err := s.PreviewEnvironmentRestore(ctx, req); err == nil || view != nil {
		t.Fatal("preview accepted another copy fingerprint", view, err)
	}
}
