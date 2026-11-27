package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
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

func setupBundleCarryOverPair(t *testing.T, withSubAgent bool) (*Service, string, string, string) {
	t.Helper()
	origSettle := nativeServiceSettle
	nativeServiceSettle = 150 * time.Millisecond
	t.Cleanup(func() { nativeServiceSettle = origSettle })

	s, home1 := enrollWorld(t)
	ctx := context.Background()
	root := os.Getenv("FAKESSH_ROOT")
	home2 := filepath.Join(root, "hosts", "box2", "home")
	if err := os.MkdirAll(filepath.Join(home2, ".local/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	porterBin, err := os.ReadFile(filepath.Join(home1, ".local/bin/valise-porter"))
	if err != nil {
		t.Fatal(err)
	}
	writeExe := func(base, rel, body string) {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home2, ".local/bin/valise-porter"), porterBin, 0o755); err != nil {
		t.Fatal(err)
	}
	demoBin := `#!/bin/sh
if [ "$1" = "mcp" ]; then
  echo "$@" >> "$HOME/mcp-rewire.log"
  echo "rewired ok"
  exit 0
fi
echo demo 1.0
`
	fakeSystemctl := `#!/bin/sh
shift
if test "$1" = '--timestamp=unix'; then shift; fi
case "$1" in
 show)
  if [ "$2" = "demo.service" ]; then
   case "$*" in
    *--value*) printf 'loaded\n' ;;
    *)
     st=$(cat "$HOME/demo-svc-state" 2>/dev/null || echo active)
     printf 'LoadState=loaded\nActiveState=%s\nUnitFileState=enabled\n' "$st"
     ;;
   esac
  elif test -e "$HOME/.config/systemd/user/$2"; then
   if test -e "$HOME/.config/systemd/user/$2.enabled"; then
    printf 'LoadState=loaded\nActiveState=active\nUnitFileState=enabled\nNextElapseUSecRealtime=@1793934000\nLastTriggerUSec=@1793847600\n'
   else
    printf 'LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\nNextElapseUSecRealtime=n/a\nLastTriggerUSec=n/a\n'
   fi
  else printf 'LoadState=not-found\n'; fi ;;
 is-active)
  if [ "$2" = "demo.service" ]; then
   st=$(cat "$HOME/demo-svc-state" 2>/dev/null || echo active)
   printf '%s\n' "$st"
   [ "$st" = "active" ] && exit 0 || exit 3
  fi
  exit 3 ;;
 stop)
  if [ "$2" = "demo.service" ]; then echo inactive > "$HOME/demo-svc-state"; fi
  exit 0 ;;
 start)
  if [ "$2" = "demo.service" ]; then echo active > "$HOME/demo-svc-state"; fi
  exit 0 ;;
 enable)
  for a in "$@"; do
   case "$a" in *.timer) touch "$HOME/.config/systemd/user/$a.enabled" ;; esac
  done
  exit 0 ;;
 disable)
  for a in "$@"; do
   case "$a" in *.timer) rm "$HOME/.config/systemd/user/$a.enabled" 2>/dev/null || true ;; esac
  done
  exit 0 ;;
 daemon-reload|reset-failed) exit 0 ;;
 *) exit 2 ;;
esac
`
	fakeAnalyze := `#!/bin/sh
printf '  Normalized form: %s\n  Next elapse: Mon 2026-11-30 03:30:00 UTC\n' "$2"
`
	for _, h := range []string{home1, home2} {
		writeExe(h, ".local/bin/demo", demoBin)
		writeExe(h, ".local/bin/systemctl", fakeSystemctl)
		writeExe(h, ".local/bin/systemd-analyze", fakeAnalyze)
		writeExe(h, ".local/bin/loginctl", "#!/bin/sh\nprintf 'yes\\n'\n")
	}

	profYAML := `schema: valise-profile/1
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
  - { root: state, glob: "config.yaml", category: config }
  - { root: state, glob: "memory/**", category: memory }
  - { root: state, glob: "skills/**", category: memory }
  - { root: state, glob: "workspace/**", category: workspace }
provenance: { kind: binary, binary: { name: demo } }
service:
  candidates: [ { scope: user, unit: demo.service } ]
health:
  grace_seconds: 1
  checks: [ { kind: systemd_active } ]
agent_kit:
  skills_dir: { root: state, path: "skills/" }
  mcp_add_argv: [demo, mcp, add, valise, --command, "${PORTER}", "--arg=${INSTANCE}"]
`
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(profYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServers("box", "box2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	s.ProtectionReview = func(context.Context, string, protection.FrozenChange) (bool, error) { return true, nil }

	// Seed config.yaml with source $HOME reference, a custom skill file, and an external symlink under source $HOME.
	writeFile := func(base, rel, body string) {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(home1, ".demo/config.yaml", "data_dir: "+home1+"/data/cache\n")
	writeFile(home1, ".demo/skills/custom/SKILL.md", "user custom skill v1")
	writeFile(home1, "external-notes/readme.md", "notes")
	if err := os.MkdirAll(filepath.Join(home1, ".demo/workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home1, "external-notes"), filepath.Join(home1, ".demo/workspace/notes-link")); err != nil {
		t.Fatal(err)
	}

	if withSubAgent {
		writeFile(home1, ".demo-worker/demo.json", `{"worker":true}`)
		writeFile(home1, ".demo-worker/.env", "W=1")
		writeFile(home1, ".demo-worker/config.yaml", "worker_dir: "+home1+"/worker-data\n")
		writeFile(home1, ".demo-worker/memory/w.md", "worker-mem")
	}

	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if withSubAgent {
		if _, err := s.Enroll(ctx, "box", EnrollOptions{
			ProfileID:  "demo",
			InstanceID: "demo-worker",
			Name:       "worker",
			Accept:     true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.KeyRegister(ctx, "box", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "box2", "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: "shared-nas", Kind: "rsync", Dest: "nas:/shared"}); err != nil {
		t.Fatal(err)
	}
	if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: "src-only-nas", Kind: "rsync", Dest: "nas:/src-only"}); err != nil {
		t.Fatal(err)
	}
	if err := s.TargetSet(ctx, "box2", vaultrpc.Target{Name: "shared-nas", Kind: "rsync", Dest: "nas:/shared"}); err != nil {
		t.Fatal(err)
	}

	// Configure an accepted protection plan on box for demo.
	var srcBinding identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: "demo"}, &srcBinding); err != nil || srcBinding.Agent == nil {
		t.Fatalf("source binding: %+v %v", srcBinding, err)
	}
	on := true
	desired := protection.Desired{
		Scope: protection.Scope{Kind: "preset", Tier: vault.TierFull},
		Schedule: protection.Schedule{
			Enabled:  &on,
			DailyAt:  "04:00",
			Timezone: "UTC",
		},
		Period: protection.Period{Unit: "month", Timezone: "UTC", KeepAutoPeriods: 3},
		ServerCopies: []protection.ServerCopy{
			{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "shared-nas", AuthorityID: srcBinding.Agent.Ref.ServerUID}, Enabled: &on, MinimumInterval: "6h"},
			{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "src-only-nas", AuthorityID: srcBinding.Agent.Ref.ServerUID}, Enabled: &on, MinimumInterval: "6h"},
		},
	}
	frozen, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{
		AgentRef: srcBinding.Agent.Ref,
		Desired:  desired,
	})
	if err != nil {
		t.Fatalf("PrepareProtectionChange: %v", err)
	}
	ptask, err := s.StartProtectionChange(ctx, frozen.PlanID, "src-bundle-prot-init")
	if err != nil || ptask.State != "succeeded" {
		t.Fatalf("StartProtectionChange: %+v %v", ptask, err)
	}

	if withSubAgent {
		ms, err := s.CreateManualSavepoint(ctx, ManualSavepointRequest{
			Host:          "box",
			InstanceID:    "demo",
			Tier:          vault.TierFull,
			BundleMembers: []string{"demo", "demo-worker"},
			Note:          "bundle-carryover",
		})
		if err != nil {
			t.Fatalf("CreateManualSavepoint bundle: %v", err)
		}
		return s, home1, home2, ms.BundleID
	}
	rep, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierFull})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return s, home1, home2, rep.SnapshotID
}

func TestBundleRestorePostMigrateCarryOver(t *testing.T) {
	s, home1, home2, bundleID := setupBundleCarryOverPair(t, true)
	ctx := context.Background()

	bp, err := s.PrepareBundleRestore(ctx, BundleRestoreOptions{
		Target:                 "box2",
		From:                   "box",
		Mode:                   revive.ModeMigrate,
		BundleID:               bundleID,
		AllowDraft:             true,
		CarryMCP:               true,
		RemapHomeSymlinks:      true,
		RewriteConfigHomePaths: true,
		CarryProtection:        true,
	}, pass)
	if err != nil {
		t.Fatalf("PrepareBundleRestore: %v", err)
	}
	defer bp.Close()

	if len(bp.Plans) != 2 || !slices.Equal(bp.Order, []string{"demo", "demo-worker"}) {
		t.Fatalf("unexpected bundle plan order: %v", bp.Order)
	}
	mainPlan := bp.Plans[0]
	if !mainPlan.HomeChanged || len(mainPlan.ConfigHomeHits) == 0 || len(mainPlan.ExternalSymlinks) == 0 {
		t.Fatalf("expected HomeChanged/ConfigHomeHits/ExternalSymlinks populated on main plan: HomeChanged=%v hits=%d symlinks=%d",
			mainPlan.HomeChanged, len(mainPlan.ConfigHomeHits), len(mainPlan.ExternalSymlinks))
	}
	if mainPlan.SourceProtection == nil || !mainPlan.SourceProtection.Enabled {
		t.Fatalf("expected SourceProtection populated on main plan: %+v", mainPlan.SourceProtection)
	}

	var memberProgress []Progress
	pctx := WithProgress(ctx, func(p Progress) {
		if p.Stage == StageBundleMember {
			memberProgress = append(memberProgress, p)
		}
	})
	runRes, err := s.RunBundleRestore(pctx, bp, proceedConfirm())
	if err != nil {
		t.Fatalf("RunBundleRestore: %v", err)
	}
	if runRes.Partial || len(runRes.Members) != 2 {
		t.Fatalf("unexpected RunBundleRestore result: %+v", runRes)
	}
	if len(memberProgress) != 2 || memberProgress[0].Done != 1 || memberProgress[0].Total != 2 || memberProgress[1].Done != 2 || memberProgress[1].Total != 2 {
		t.Fatalf("expected bundle_member progress (1/2, 2/2), got %+v", memberProgress)
	}

	// Verify main member PostMigrateReport and actual target filesystem / protection / MCP.
	mainRes := runRes.Members[0]
	pm := mainRes.PostMigrate
	if pm == nil {
		t.Fatal("expected non-nil PostMigrate on main member")
	}
	if !pm.MCPAutoRan || pm.MCPWireOutcome != "wired" || pm.MCPWireResult == nil || !pm.MCPWireResult.OK {
		t.Fatalf("expected MCP auto-rewire succeeded: %+v", pm)
	}
	mcpLog := readFile(t, filepath.Join(home2, "mcp-rewire.log"))
	if !strings.Contains(mcpLog, filepath.Join(home2, ".local/bin/valise-porter")) || !strings.Contains(mcpLog, "--arg=demo") {
		t.Fatalf("unexpected mcp-rewire.log: %q", mcpLog)
	}
	if got := readFile(t, filepath.Join(home2, ".demo/skills/custom/SKILL.md")); got != "user custom skill v1" {
		t.Fatalf("custom user skill was modified or lost: %q", got)
	}

	if pm.RemappedSymlinks < 1 {
		t.Fatalf("expected RemappedSymlinks >= 1, got %+v", pm)
	}
	linkTarget, err := os.Readlink(filepath.Join(home2, ".demo/workspace/notes-link"))
	if err != nil || linkTarget != filepath.Join(home2, "external-notes") {
		t.Fatalf("remapped symlink target = %q (err=%v), want %q", linkTarget, err, filepath.Join(home2, "external-notes"))
	}

	if pm.RewrittenConfigPaths < 1 || pm.RemainingConfigHomeHits != 0 {
		t.Fatalf("expected RewrittenConfigPaths >= 1 and RemainingConfigHomeHits == 0, got %+v", pm)
	}
	if cfg := readFile(t, filepath.Join(home2, ".demo/config.yaml")); !strings.Contains(cfg, home2+"/data/cache") || strings.Contains(cfg, home1) {
		t.Fatalf("config.yaml not rewritten cleanly: %q", cfg)
	}
	if bak := readFile(t, filepath.Join(home2, ".demo/config.yaml"+homeBakSuffix)); !strings.Contains(bak, home1+"/data/cache") {
		t.Fatalf("config.yaml backup missing original content: %q", bak)
	}

	if !pm.ProtectionAttempted || !pm.ProtectionCarried || !strings.Contains(pm.ProtectionNote, "src-only-nas") {
		t.Fatalf("expected protection carry-over with filtered target note: %+v", pm)
	}
	targetAgent := s.resolveMigratedTargetAgent(ctx, "box2", "demo")
	if targetAgent == nil {
		t.Fatal("expected bound target agent identity on box2")
	}
	view, err := s.GetProtection(ctx, "box2", protection.GetParams{AgentRef: *targetAgent})
	if err != nil || view.Mode != "accepted" || view.ServerPlan == nil {
		t.Fatalf("target protection not carried over: %+v %v", view, err)
	}
	if len(view.ServerPlan.ServerCopies) != 1 || view.ServerPlan.ServerCopies[0].TargetRef.ID != "shared-nas" {
		t.Fatalf("unexpected target ServerCopies: %+v", view.ServerPlan.ServerCopies)
	}

	// Verify sub-agent also received PostMigrateReport and config rewrite.
	subRes := runRes.Members[1]
	if subRes.PostMigrate == nil {
		t.Fatal("expected non-nil PostMigrate on sub-agent member")
	}
	if subRes.PostMigrate.RewrittenConfigPaths < 1 || subRes.PostMigrate.RemainingConfigHomeHits != 0 {
		t.Fatalf("expected sub-agent config home path rewritten: %+v", subRes.PostMigrate)
	}
	if cfg := readFile(t, filepath.Join(home2, ".demo-worker/config.yaml")); !strings.Contains(cfg, home2+"/worker-data") {
		t.Fatalf("sub-agent config.yaml not rewritten: %q", cfg)
	}
}

func TestHarnessMoveSingleGroupPostMigrateCarryOver(t *testing.T) {
	s, home1, home2, _ := setupBundleCarryOverPair(t, false)
	ctx := context.Background()

	plan, err := s.PrepareHarnessMove(ctx, HarnessMoveOptions{
		From:                   "box",
		Target:                 "box2",
		AllowDraft:             true,
		CarryMCP:               true,
		RemapHomeSymlinks:      true,
		RewriteConfigHomePaths: true,
		CarryProtection:        true,
	}, pass)
	if err != nil {
		t.Fatalf("PrepareHarnessMove: %v", err)
	}
	defer plan.Close()

	if len(plan.Groups) != 1 || plan.Groups[0].SinglePlan == nil {
		t.Fatalf("expected 1 single-plan harness group, got %+v", plan.Groups)
	}
	res, err := s.RunHarnessMove(ctx, plan, proceedConfirm())
	if err != nil {
		t.Fatalf("RunHarnessMove: %v", err)
	}
	if res.Partial || len(res.Groups) != 1 || !res.Groups[0].Succeeded || len(res.Groups[0].Members) != 1 {
		t.Fatalf("unexpected RunHarnessMove result: %+v", res)
	}
	pm := res.Groups[0].Members[0].PostMigrate
	if pm == nil {
		t.Fatal("expected non-nil PostMigrate for single-instance HarnessMove group")
	}
	if !pm.MCPAutoRan || pm.MCPWireOutcome != "wired" {
		t.Fatalf("expected MCP auto-rewire on single-instance HarnessMove: %+v", pm)
	}
	if pm.RemappedSymlinks < 1 || pm.RewrittenConfigPaths < 1 || pm.RemainingConfigHomeHits != 0 {
		t.Fatalf("expected home symlink & config rewrite on single-instance HarnessMove: %+v", pm)
	}
	if !pm.ProtectionAttempted || !pm.ProtectionCarried {
		t.Fatalf("expected protection carry-over on single-instance HarnessMove: %+v", pm)
	}
	if cfg := readFile(t, filepath.Join(home2, ".demo/config.yaml")); !strings.Contains(cfg, home2+"/data/cache") || strings.Contains(cfg, home1) {
		t.Fatalf("single-instance HarnessMove config.yaml not rewritten: %q", cfg)
	}
	if got := readFile(t, filepath.Join(home2, ".demo/skills/custom/SKILL.md")); got != "user custom skill v1" {
		t.Fatalf("custom skill overwritten on single-instance HarnessMove: %q", got)
	}
}

func TestBundleMigrateWithListeningEntryPortNoHarnessRunning(t *testing.T) {
	// Start a real loopback listener to simulate the main agent's gateway
	// listening on its declared Entries port after main swaps and starts.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	listenPort := ln.Addr().(*net.TCPAddr).Port

	s, _, home2, _ := setupBundleCarryOverPair(t, true)
	ctx := context.Background()

	// Update demo.yaml to include a declared entry listening on listenPort.
	profPath := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	rawProf := readFile(t, profPath)
	entryBlock := fmt.Sprintf("\nentries:\n  - { id: web, display_name: Web, kind: web, host: 127.0.0.1, port: %d, path: \"/\" }\n", listenPort)
	if err := os.WriteFile(profPath, []byte(rawProf+entryBlock), 0o644); err != nil {
		t.Fatal(err)
	}
	// Re-enroll box instances with the entry-bearing profile and create a fresh bundle.
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatalf("re-enroll main: %v", err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", InstanceID: "demo-worker", Name: "worker", Accept: true}); err != nil {
		t.Fatalf("re-enroll sub: %v", err)
	}
	ms, err := s.CreateManualSavepoint(ctx, ManualSavepointRequest{
		Host:          "box",
		InstanceID:    "demo",
		Tier:          vault.TierFull,
		BundleMembers: []string{"demo", "demo-worker"},
		Note:          "bundle-entry-port",
	})
	if err != nil {
		t.Fatalf("CreateManualSavepoint: %v", err)
	}

	bp, err := s.PrepareBundleRestore(ctx, BundleRestoreOptions{
		Target:          "box2",
		From:            "box",
		Mode:            revive.ModeMigrate,
		BundleID:        ms.BundleID,
		AllowDraft:      true,
		CarryProtection: true,
	}, pass)
	if err != nil {
		t.Fatalf("PrepareBundleRestore: %v", err)
	}
	defer bp.Close()

	runRes, err := s.RunBundleRestore(ctx, bp, proceedConfirm())
	if err != nil {
		t.Fatalf("RunBundleRestore: %v", err)
	}
	if runRes.Partial || len(runRes.Members) != 2 {
		t.Fatalf("expected clean bundle migration without harness_running rollback, got %+v", runRes)
	}
	for _, m := range runRes.Members {
		if m.Error != "" || m.Result == nil || m.Result.State == revive.StateRolledBack {
			t.Fatalf("member %s failed or rolled back: %+v", m.InstanceID, m)
		}
	}

	// Sub-agent record on box2 must NOT inherit main agent's Entries.
	insts, err := s.Instances(ctx, "box2")
	if err != nil {
		t.Fatalf("Instances(box2): %v", err)
	}
	var subSummary *enroll.InstanceSummary
	for i := range insts {
		if insts[i].InstanceID == "demo-worker" {
			subSummary = &insts[i]
		}
	}
	if subSummary == nil {
		t.Fatal("demo-worker not enrolled on box2")
	}
	if len(subSummary.Entries) != 0 {
		t.Fatalf("named sub-agent record polluted with main Entries: %+v", subSummary.Entries)
	}

	// Installation dossier profile on box2 must stay intact (not downgraded by sub-agent).
	profBytes, err := os.ReadFile(filepath.Join(home2, ".config/valise/dossiers/demo.profile.json"))
	if err != nil {
		t.Fatalf("read dossier profile on box2: %v", err)
	}
	var savedProf profile.Profile
	if err := json.Unmarshal(profBytes, &savedProf); err != nil {
		t.Fatalf("unmarshal dossier profile: %v", err)
	}
	if len(savedProf.Entries) != 1 || savedProf.Entries[0].Port != listenPort || savedProf.Provenance == nil {
		t.Fatalf("installation dossier profile was downgraded by sub-agent: entries=%+v prov=%+v", savedProf.Entries, savedProf.Provenance)
	}

	// ValidateIdentity for main agent's carried protection must succeed with no identity_conflict.
	mainPM := runRes.Members[0].PostMigrate
	if mainPM == nil || !mainPM.ProtectionAttempted || !mainPM.ProtectionCarried {
		t.Fatalf("expected carried protection to pass ValidateIdentity without conflict: %+v", mainPM)
	}
}

func TestSharedServiceSubAgentMigrateToEnrolledMainNoIdentityConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	listenPort := ln.Addr().(*net.TCPAddr).Port

	s, _, home2, _ := setupBundleCarryOverPair(t, true)
	ctx := context.Background()

	// Declare both service: shared on named instances AND an entry listening on listenPort.
	profPath := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	rawProf := readFile(t, profPath)
	sharedProf := strings.Replace(
		rawProf,
		`    - glob: "${HOME}/.demo-*"`,
		"    - glob: \"${HOME}/.demo-*\"\n      service: shared",
		1,
	)
	sharedProf += fmt.Sprintf("\nentries:\n  - { id: web, display_name: Web, kind: web, host: 127.0.0.1, port: %d, path: \"/\" }\n", listenPort)
	if err := os.WriteFile(profPath, []byte(sharedProf), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-enroll box main + sub with the shared-service + entries profile.
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatalf("re-enroll box main: %v", err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", InstanceID: "demo-worker", Name: "worker", Accept: true}); err != nil {
		t.Fatalf("re-enroll box sub: %v", err)
	}

	// Pre-enroll same-version main instance on target box2 so dossiers/demo.profile.json holds P_full.
	for rel, body := range map[string]string{
		".demo/demo.json":   `{"main":true}`,
		".demo/.env":        "M=1",
		".demo/memory/a.md": "main-mem",
	} {
		p := filepath.Join(home2, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enroll(ctx, "box2", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatalf("enroll box2 main: %v", err)
	}

	// Configure an accepted protection plan on box for demo-worker.
	var srcSubBinding identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: "demo-worker"}, &srcSubBinding); err != nil || srcSubBinding.Agent == nil {
		t.Fatalf("source sub binding: %+v %v", srcSubBinding, err)
	}
	on := true
	subFrozen, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{
		AgentRef: srcSubBinding.Agent.Ref,
		Desired: protection.Desired{
			Scope:        protection.Scope{Kind: "preset", Tier: vault.TierFull},
			Schedule:     protection.Schedule{Enabled: &on, DailyAt: "04:30", Timezone: "UTC"},
			Period:       protection.Period{Unit: "month", Timezone: "UTC", KeepAutoPeriods: 2},
			ServerCopies: []protection.ServerCopy{},
		},
	})
	if err != nil {
		t.Fatalf("PrepareProtectionChange(sub): %v", err)
	}
	if ptask, err := s.StartProtectionChange(ctx, subFrozen.PlanID, "src-sub-prot-init"); err != nil || ptask.State != "succeeded" {
		t.Fatalf("StartProtectionChange(sub): %+v %v", ptask, err)
	}

	// Seal demo-worker standalone and migrate it independently to box2.
	rep, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo-worker", Tier: vault.TierFull})
	if err != nil {
		t.Fatalf("Seal(demo-worker): %v", err)
	}
	rp, err := s.PlanRevive(ctx, ReviveOptions{
		Target:     "box2",
		From:       "box",
		SnapshotID: rep.SnapshotID,
		Mode:       revive.ModeMigrate,
		Roots:      map[string]string{profile.StateRoot: filepath.Join(home2, ".demo-worker")},
		AllowDraft: true,
	}, pass)
	if err != nil {
		t.Fatalf("PlanRevive(demo-worker): %v", err)
	}
	defer rp.Close()
	if rp.DataOnly || rp.Plan.Unit != "demo.service" {
		t.Fatalf("expected shared-service sub-agent to plan demo.service (non-DataOnly), got DataOnly=%v Unit=%q", rp.DataOnly, rp.Plan.Unit)
	}
	swapRes, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
	if err != nil {
		t.Fatalf("ExecuteReviveConfirmed(demo-worker): %v", err)
	}
	if swapRes.State != revive.StateHealthy {
		t.Fatalf("expected StateHealthy for shared-service sub-agent, got %+v", swapRes)
	}

	// Carry over protection and verify ValidateIdentity succeeds with zero conflict for both main and sub.
	pm := s.finalizeMigratedMember(ctx, "box2", "box", revive.ModeMigrate, rp, true)
	if pm == nil || !pm.ProtectionAttempted || !pm.ProtectionCarried {
		t.Fatalf("expected sub-agent protection carry-over without identity_conflict, got %+v", pm)
	}
	for _, id := range []string{"demo", "demo-worker"} {
		ref := s.resolveMigratedTargetAgent(ctx, "box2", id)
		if ref == nil {
			t.Fatalf("missing bound target agent identity for %s on box2", id)
		}
		if _, err := s.GetProtection(ctx, "box2", protection.GetParams{AgentRef: *ref}); err != nil {
			t.Fatalf("GetProtection/ValidateIdentity(%s) failed: %v", id, err)
		}
	}
	profBytes, err := os.ReadFile(filepath.Join(home2, ".config/valise/dossiers/demo.profile.json"))
	if err != nil {
		t.Fatalf("read dossier profile on box2: %v", err)
	}
	var savedProf profile.Profile
	if err := json.Unmarshal(profBytes, &savedProf); err != nil {
		t.Fatalf("unmarshal dossier profile: %v", err)
	}
	if len(savedProf.Entries) != 1 || savedProf.Entries[0].Port != listenPort || savedProf.Provenance == nil {
		t.Fatalf("dossier profile on box2 downgraded: entries=%+v prov=%+v", savedProf.Entries, savedProf.Provenance)
	}
}

func TestPrepareBundleRestoreSubAgentOnlyOfflineClearsServiceMissing(t *testing.T) {
	s, home1, home2, _ := setupBundleCarryOverPair(t, true)
	ctx := context.Background()

	// Declare service: shared + entries (and no install_argv) so missing service would trigger service_missing.
	profPath := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	rawProf := readFile(t, profPath)
	sharedProf := strings.Replace(
		rawProf,
		`    - glob: "${HOME}/.demo-*"`,
		"    - glob: \"${HOME}/.demo-*\"\n      service: shared",
		1,
	)
	sharedProf += "\nentries:\n  - { id: web, display_name: Web, kind: web, host: 127.0.0.1, port: 18080, path: \"/\" }\n"
	if err := os.WriteFile(profPath, []byte(sharedProf), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed a second named sub-agent on box so we have a sub-agent-only bundle (hasMain == false).
	for rel, body := range map[string]string{
		".demo-helper/demo.json":   `{"helper":true}`,
		".demo-helper/.env":        "H=1",
		".demo-helper/memory/h.md": "helper-mem",
	} {
		p := filepath.Join(home1, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatalf("re-enroll main: %v", err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", InstanceID: "demo-worker", Name: "worker", Accept: true}); err != nil {
		t.Fatalf("re-enroll worker: %v", err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", InstanceID: "demo-helper", Name: "helper", Accept: true}); err != nil {
		t.Fatalf("enroll helper: %v", err)
	}

	ms, err := s.CreateManualSavepoint(ctx, ManualSavepointRequest{
		Host:          "box",
		InstanceID:    "demo-worker",
		Tier:          vault.TierFull,
		BundleMembers: []string{"demo-worker", "demo-helper"},
		Note:          "sub-only-bundle",
	})
	if err != nil {
		t.Fatalf("CreateManualSavepoint sub-only bundle: %v", err)
	}

	// Sync to an off-site repo so PrepareBundleRestore can read it while From is unreachable.
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatalf("ServerNameSet: %v", err)
	}
	repoDir := filepath.Join(t.TempDir(), "usb")
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoAdd("usb", repoDir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatalf("RepoSync: %v", err)
	}

	// Make target box2 report demo.service as not-found so planService emits service_missing when mem.Name == "".
	missingSystemctl := "#!/bin/sh\nprintf 'LoadState=not-found\\n'\n"
	if err := os.WriteFile(filepath.Join(home2, ".local/bin/systemctl"), []byte(missingSystemctl), 0o755); err != nil {
		t.Fatal(err)
	}

	// Pass ONLY BundleID + off-site Source with unreachable From so mem.Name is initially empty.
	bp, err := s.PrepareBundleRestore(ctx, BundleRestoreOptions{
		Target:     "box2",
		From:       "offline-unreachable",
		Mode:       revive.ModeMigrate,
		BundleID:   ms.BundleID,
		Source:     "repo:usb/box",
		AllowDraft: true,
	}, pass)
	if err != nil {
		t.Fatalf("PrepareBundleRestore(sub-only offline): %v", err)
	}
	defer bp.Close()

	if len(bp.Plans) != 2 {
		t.Fatalf("expected 2 sub-agent plans, got %d", len(bp.Plans))
	}
	for i, rp := range bp.Plans {
		if rp.profileName == "" {
			t.Fatalf("plan[%d] expected named sub-agent, got empty profileName", i)
		}
		if !rp.DataOnly || rp.Plan.Unit != "" || rp.Plan.InstallService || len(rp.InstallArgv) != 0 || rp.StopSource != "" {
			t.Fatalf("plan[%d] (%s) not converged to DataOnly: DataOnly=%v Unit=%q InstallService=%v InstallArgv=%v StopSource=%q",
				i, rp.Plan.InstanceID, rp.DataOnly, rp.Plan.Unit, rp.Plan.InstallService, rp.InstallArgv, rp.StopSource)
		}
		for _, b := range rp.Blockers {
			if b.Code == "service_missing" {
				t.Fatalf("plan[%d] (%s) still has stale service_missing blocker: %+v", i, rp.Plan.InstanceID, rp.Blockers)
			}
		}
	}
}
