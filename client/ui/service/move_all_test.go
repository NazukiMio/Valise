package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
)

func TestBundleNotStandaloneError(t *testing.T) {
	err := newBundleNotStandaloneError("b_01TEST", "agent-main", []string{"agent-main", "agent-sub1", "agent-sub2"})
	ue, ok := valise.As(err)
	if !ok || ue.Code != "bundle_not_standalone" {
		t.Fatalf("expected bundle_not_standalone UserError, got %v", err)
	}
	var be *BundleNotStandaloneError
	if !errors.As(err, &be) {
		t.Fatalf("expected BundleNotStandaloneError")
	}
	if be.BundleID != "b_01TEST" || !slices.Equal(be.Others, []string{"agent-sub1", "agent-sub2"}) {
		t.Fatalf("unexpected bundle error fields: %+v", be)
	}
}

func TestOrderBundleMembersMainFirst(t *testing.T) {
	in := []BundleRestoreMember{
		{InstanceID: "z-sub", Name: "sub-z", SnapshotID: "s_1"},
		{InstanceID: "a-sub", Name: "sub-a", SnapshotID: "s_2"},
		{InstanceID: "m-main", Name: "", SnapshotID: "s_3"},
	}
	got := orderBundleMembers(in)
	wantOrder := []string{"m-main", "a-sub", "z-sub"}
	for i, want := range wantOrder {
		if got[i].InstanceID != want {
			t.Fatalf("pos %d: got %s, want %s (full: %+v)", i, got[i].InstanceID, want, got)
		}
	}
}

func TestBuildHarnessMoveGroups(t *testing.T) {
	now := time.Now().UTC()
	instances := []enroll.InstanceSummary{
		{InstanceID: "h1-sub", ProfileID: "harness-alpha", Name: "worker"},
		{InstanceID: "h1-main", ProfileID: "harness-alpha", Name: ""},
		{InstanceID: "h2-only", ProfileID: "harness-beta", Name: ""},
		{InstanceID: "h3-only", ProfileID: "harness-gamma", Name: ""},
		{InstanceID: "h1-old", ProfileID: "harness-alpha", Superseded: &enroll.Supersession{By: "h1-main"}},
	}
	snapshots := []vault.Meta{
		// harness-alpha has a complete full bundle covering both active members.
		{SnapshotID: "s_01", InstanceID: "h1-main", ProfileID: "harness-alpha", Tier: vault.TierFull, CreatedAt: now, BundleID: "b_1", BundleMembers: []string{"h1-main", "h1-sub"}},
		{SnapshotID: "s_02", InstanceID: "h1-sub", ProfileID: "harness-alpha", Tier: vault.TierFull, CreatedAt: now, BundleID: "b_1", BundleMembers: []string{"h1-main", "h1-sub"}},
		// harness-beta has only a soul snapshot -> NOT ready!
		{SnapshotID: "s_03", InstanceID: "h2-only", ProfileID: "harness-beta", Tier: vault.TierSoul, CreatedAt: now},
		// harness-gamma has a standalone raw snapshot -> ready!
		{SnapshotID: "s_04", InstanceID: "h3-only", ProfileID: "harness-gamma", Tier: vault.TierRaw, CreatedAt: now},
	}

	groups := buildHarnessMoveGroups(instances, snapshots)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d: %+v", len(groups), groups)
	}
	// Sorted by ProfileID: harness-alpha, harness-beta, harness-gamma.
	g0 := groups[0]
	if g0.ProfileID != "harness-alpha" || !g0.Ready || g0.BundleID != "b_1" {
		t.Fatalf("unexpected group 0: %+v", g0)
	}
	if len(g0.Members) != 2 || g0.Members[0].InstanceID != "h1-main" || g0.Members[1].InstanceID != "h1-sub" {
		t.Fatalf("group 0 should have main first and skip superseded: %+v", g0.Members)
	}
	g1 := groups[1]
	if g1.ProfileID != "harness-beta" || g1.Ready || g1.Reason == "" {
		t.Fatalf("group 1 without full/raw must not be ready: %+v", g1)
	}
	g2 := groups[2]
	if g2.ProfileID != "harness-gamma" || !g2.Ready || g2.Members[0].Snapshot.SnapshotID != "s_04" {
		t.Fatalf("unexpected group 2: %+v", g2)
	}
}

func TestRunHarnessMovePartialFailureDoesNotBlockRemaining(t *testing.T) {
	s := &Service{}
	plan := &HarnessMovePlan{
		From:   "src",
		Target: "dst",
		Groups: []HarnessMoveGroupPlan{
			{ProfileID: "harness-alpha"},
			{ProfileID: "harness-beta", PlanError: "缺少完整备份", PlanErrorCode: "harness_not_ready"},
			{ProfileID: "harness-gamma"},
		},
	}
	var executed []string
	res, err := s.runHarnessMoveWith(context.Background(), plan, ReviveConfirm{}, func(_ context.Context, gp HarnessMoveGroupPlan, _ ReviveConfirm) HarnessMoveGroupResult {
		executed = append(executed, gp.ProfileID)
		if gp.ProfileID == "harness-alpha" {
			return HarnessMoveGroupResult{ProfileID: gp.ProfileID, Succeeded: false, Error: "network blip", ErrorCode: "copy_failed"}
		}
		return HarnessMoveGroupResult{ProfileID: gp.ProfileID, Succeeded: true}
	})
	if err != nil {
		t.Fatalf("runHarnessMoveWith: %v", err)
	}
	if !res.Partial {
		t.Fatal("expected Partial=true when some harnesses failed")
	}
	if !slices.Equal(executed, []string{"harness-alpha", "harness-gamma"}) {
		t.Fatalf("expected harness-gamma to still run after harness-alpha failed; executed=%v", executed)
	}
	if len(res.Groups) != 3 || res.Groups[0].Succeeded || res.Groups[1].Succeeded || !res.Groups[2].Succeeded {
		t.Fatalf("unexpected group results: %+v", res.Groups)
	}
}

const demoBundleProfile = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect:
  binaries: [demo]
  state_dir_candidates: ["${HOME}/.demo"]
  markers_any: [demo.json]
  named:
    - glob: "${HOME}/.demo-*"
      env: { DEMO_HOME: "${STATE_DIR}" }
paths:
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "demo.json", category: config, verify: pending }
  - { root: state, glob: "memory/**", category: memory }
provenance: { kind: binary, binary: { name: demo } }
`

func TestPrepareAndRunBundleRestoreAutoNamedRootAndIncomplete(t *testing.T) {
	s, e, homeVPS := addVPS(t, pass)
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Dirs.Data, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(demoBundleProfile), 0o644); err != nil {
		t.Fatal(err)
	}

	homeSrc := e.AddHost("src")
	e.InstallPorter("src")
	seedDemoFiles(t, homeSrc)
	// Seed a named sub-agent directory ~/.demo-worker.
	for rel, body := range map[string]string{
		".demo-worker/demo.json":   `{"worker":true}`,
		".demo-worker/.env":        "W=1",
		".demo-worker/memory/w.md": "worker-mem",
	} {
		p := filepath.Join(homeSrc, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enroll(ctx, "src", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "src", EnrollOptions{
		ProfileID:  "demo",
		InstanceID: "demo-worker",
		Name:       "worker",
		Accept:     true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ServerNameSet(ctx, "src", "src"); err != nil {
		t.Fatal(err)
	}
	// Create a same-harness bundle savepoint (also exercises EnsureKeyRegistered).
	ms, err := s.CreateManualSavepoint(ctx, ManualSavepointRequest{
		Host:          "src",
		InstanceID:    "demo",
		Tier:          "full",
		BundleMembers: []string{"demo", "demo-worker"},
		Note:          "move-bundle",
	})
	if err != nil {
		t.Fatalf("CreateManualSavepoint bundle: %v", err)
	}
	if ms.BundleID == "" || len(ms.Reports) != 2 {
		t.Fatalf("unexpected bundle savepoint: %+v", ms)
	}

	// Prepare target VPS with demo binary (no .demo state dirs yet) and registered key.
	seedDemoFiles(t, homeVPS)
	os.RemoveAll(filepath.Join(homeVPS, ".demo"))
	if _, _, err := s.KeyRegister(ctx, "vps", "test"); err != nil {
		t.Fatal(err)
	}

	// 1. PrepareBundleRestore with ONLY BundleID (no Members, no RootsByInstance).
	bp, err := s.PrepareBundleRestore(ctx, BundleRestoreOptions{
		Target:     "vps",
		From:       "src",
		Mode:       revive.ModeMigrate,
		BundleID:   ms.BundleID,
		AllowDraft: true,
	}, pass)
	if err != nil {
		t.Fatalf("PrepareBundleRestore: %v", err)
	}
	defer bp.Close()
	if len(bp.Plans) != 2 || !slices.Equal(bp.Order, []string{"demo", "demo-worker"}) {
		t.Fatalf("unexpected bundle plan order: %v (plans=%d)", bp.Order, len(bp.Plans))
	}
	subPlan := bp.Plans[1]
	if !subPlan.DataOnly || subPlan.Plan.Unit != "" || subPlan.StopSource != "" {
		t.Fatalf("sub-agent must skip service and clear StopSource: DataOnly=%v Unit=%q StopSource=%q",
			subPlan.DataOnly, subPlan.Plan.Unit, subPlan.StopSource)
	}
	wantSubDir := filepath.Join(homeVPS, ".demo-worker")
	gotSubDir := ""
	for _, r := range subPlan.Plan.Roots {
		if r.ID == profile.StateRoot {
			gotSubDir = r.Path
		}
	}
	if gotSubDir != wantSubDir {
		t.Fatalf("auto-derived sub-agent state dir = %q, want %q", gotSubDir, wantSubDir)
	}

	// 2. RunBundleRestore with real non-nil ReviveConfirm.Swap.
	runRes, err := s.RunBundleRestore(ctx, bp, proceedConfirm())
	if err != nil {
		t.Fatalf("RunBundleRestore: %v", err)
	}
	if runRes.Partial || len(runRes.Members) != 2 {
		t.Fatalf("RunBundleRestore unexpected result: %+v", runRes)
	}
	if _, err := os.Stat(filepath.Join(homeVPS, ".demo/memory/a.md")); err != nil {
		t.Fatalf("main agent file not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeVPS, ".demo-worker/memory/w.md")); err != nil {
		t.Fatalf("sub-agent file not migrated: %v", err)
	}

	// 3. Off-site Source support + bundle_incomplete refusal when one member is missing.
	repoDir := filepath.Join(t.TempDir(), "usb")
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoAdd("usb", repoDir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoSync(ctx, "src", "usb"); err != nil {
		t.Fatalf("RepoSync: %v", err)
	}
	bpSrc, err := s.PrepareBundleRestore(ctx, BundleRestoreOptions{
		Target:     "vps",
		From:       "src",
		Mode:       revive.ModeMigrate,
		BundleID:   ms.BundleID,
		Source:     "repo:usb/src",
		AllowDraft: true,
	}, pass)
	if err != nil {
		t.Fatalf("PrepareBundleRestore from repo source: %v", err)
	}
	bpSrc.Close()

	// Delete demo-worker's snapshot from the repo so the bundle is incomplete.
	var workerSnapID string
	for _, r := range ms.Reports {
		if r.InstanceID == "demo-worker" {
			workerSnapID = r.SnapshotID
		}
	}
	if _, err := vault.DeleteSnapshots(filepath.Join(repoDir, "src"), "demo-worker", vault.SavepointsPeriod, []string{workerSnapID}); err != nil {
		t.Fatalf("delete worker snapshot from repo: %v", err)
	}
	_, err = s.PrepareBundleRestore(ctx, BundleRestoreOptions{
		Target:     "vps",
		From:       "src",
		Mode:       revive.ModeMigrate,
		BundleID:   ms.BundleID,
		Source:     "repo:usb/src",
		AllowDraft: true,
	}, pass)
	if ue, ok := valise.As(err); !ok || ue.Code != "bundle_incomplete" {
		t.Fatalf("expected bundle_incomplete error, got %v", err)
	}
}
