package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func TestMapRootsLocalRootsAndDotDirFallback(t *testing.T) {
	head := &vault.Snapshot{
		Roots: map[string]string{
			profile.StateRoot: "/home/alice/.demo",
			"ext-notes":       "/home/alice/MioSaid",
			"ext-cfg":         "/home/alice/.config/magpie",
			"ext-nested":      "/home/alice/.demo/extra",
		},
		LocalRoots: []enroll.LocalRoot{
			{ID: "ext-notes", Path: "/home/alice/MioSaid", Category: profile.CategoryMemory},
			{ID: "ext-cfg", Path: "/home/alice/.config/magpie", Category: profile.CategoryConfig},
			{ID: "ext-nested", Path: "/home/alice/.demo/extra", Category: profile.CategoryWorkspace},
		},
	}
	first := revive.InspectResult{
		Home:     "/home/bob",
		StateDir: "/home/bob/.demo",
	}

	// Even without Detect.StateDirCandidates, dot-dir fallback derives oldHome = /home/alice.
	roots, err := mapRoots(ReviveOptions{Mode: revive.ModeMigrate}, head, first, profile.Profile{ID: "demo"})
	if err != nil {
		t.Fatalf("mapRoots failed: %v", err)
	}
	got := map[string]string{}
	for _, r := range roots {
		got[r.ID] = r.Path
	}
	want := map[string]string{
		profile.StateRoot: "/home/bob/.demo",
		"ext-notes":       "/home/bob/MioSaid",
		"ext-cfg":         "/home/bob/.config/magpie",
		"ext-nested":      "/home/bob/.demo/extra",
	}
	for id, wantPath := range want {
		if got[id] != wantPath {
			t.Fatalf("root %q = %q, want %q (all=%+v)", id, got[id], wantPath, got)
		}
	}

	// Out-of-home root cannot be guessed without an explicit override.
	outOfHome := &vault.Snapshot{
		Roots: map[string]string{
			profile.StateRoot: "/home/alice/.demo",
			"ext-opt":         "/opt/shared-data",
		},
		LocalRoots: []enroll.LocalRoot{
			{ID: "ext-opt", Path: "/opt/shared-data", Category: profile.CategoryWorkspace},
		},
	}
	_, err = mapRoots(ReviveOptions{Mode: revive.ModeMigrate}, outOfHome, first, profile.Profile{ID: "demo"})
	wantCode(t, err, "root_unmapped")

	// With an explicit --root override, the out-of-home root maps cleanly.
	roots, err = mapRoots(ReviveOptions{
		Mode:  revive.ModeMigrate,
		Roots: map[string]string{"ext-opt": "/srv/shared-data"},
	}, outOfHome, first, profile.Profile{ID: "demo"})
	if err != nil {
		t.Fatalf("mapRoots with override failed: %v", err)
	}
	foundOpt := false
	for _, r := range roots {
		if r.ID == "ext-opt" && r.Path == "/srv/shared-data" {
			foundOpt = true
		}
	}
	if !foundOpt {
		t.Fatalf("override not applied: %+v", roots)
	}
}

func TestPlanRecordCarriesDossierMetadataAndDisplayName(t *testing.T) {
	prof := profile.Profile{ID: "demo", ProfileVersion: 1}
	head := &vault.Snapshot{
		DisplayName: "SnapName",
		Roots: map[string]string{
			profile.StateRoot: "/home/alice/.demo",
			"ext-notes":       "/home/alice/MioSaid",
		},
		LocalRoots: []enroll.LocalRoot{
			{ID: "ext-notes", Path: "/home/alice/MioSaid", Category: profile.CategoryMemory, Reason: "notes"},
		},
		ToolDeps: []enroll.ToolDep{
			{Name: "tool-a", Purpose: "sync"},
			{Name: "tool-b", Purpose: "index"},
		},
		ExternalRefs: []enroll.ExternalRef{
			{Path: "/opt/ref", Kind: "external_dir"},
		},
	}
	roots := []revive.RootPlan{
		{ID: profile.StateRoot, Path: "/home/bob/.demo"},
		{ID: "ext-notes", Path: "/home/bob/MioSaid"},
	}
	rp := &RevivePlan{DisplayName: "Traveler"}
	rec := planRecord("demo", prof, roots, head, revive.InspectResult{}, rp, revive.Plan{Mode: revive.ModeMigrate}, ReviveOptions{Mode: revive.ModeMigrate})
	if rec.DisplayName != "Traveler" {
		t.Fatalf("DisplayName = %q, want Traveler", rec.DisplayName)
	}
	if len(rec.LocalRoots) != 1 || rec.LocalRoots[0].Path != "/home/bob/MioSaid" || rec.LocalRoots[0].Category != profile.CategoryMemory {
		t.Fatalf("LocalRoots not remapped: %+v", rec.LocalRoots)
	}
	if len(rec.ToolDeps) != 2 || rec.ToolDeps[0].Name != "tool-a" || rec.ToolDeps[1].Name != "tool-b" {
		t.Fatalf("ToolDeps not carried: %+v", rec.ToolDeps)
	}
	if rec.Roots["ext-notes"] != "/home/bob/MioSaid" {
		t.Fatalf("Roots map missing remapped ext-notes: %+v", rec.Roots)
	}
}

func TestPlanReviveToolCheckAndDossierSummary(t *testing.T) {
	deps := []enroll.ToolDep{
		{Name: "tool-present", Purpose: "installed"},
		{Name: "tool-missing", Purpose: "not installed"},
	}
	tc := computeToolCheck(deps, map[string]bool{"tool-present": true, "tool-missing": false})
	if tc == nil || !slices.Equal(tc.Present, []string{"tool-present"}) || len(tc.Missing) != 1 || tc.Missing[0].Name != "tool-missing" {
		t.Fatalf("unexpected ToolCheck: %+v", tc)
	}

	head := &vault.Snapshot{
		LocalRoots: []enroll.LocalRoot{{ID: "ext-notes", Path: "/home/a/MioSaid", Category: profile.CategoryMemory}},
		ToolDeps:   deps,
	}
	rp := &RevivePlan{}
	rp.summarizeDossier(head)
	if rp.Dossier == nil || len(rp.Dossier.LocalRoots) != 1 || len(rp.Dossier.ToolDeps) != 2 {
		t.Fatalf("summarizeDossier missing LocalRoots/ToolDeps: %+v", rp.Dossier)
	}
	if len(rp.Warnings) != 0 {
		t.Fatalf("expected no ExternalRefs warning when ExternalRefs is empty, got %v", rp.Warnings)
	}
}

func setupMigrationPair(t *testing.T, targetAnalyzeOK bool) (*Service, string, string, string) {
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
	fakeAnalyzeFail := `#!/bin/sh
echo "calendar rejected" >&2
exit 1
`
	writeExe(home1, ".local/bin/demo", demoBin)
	writeExe(home2, ".local/bin/demo", demoBin)
	writeExe(home1, ".local/bin/systemctl", fakeSystemctl)
	writeExe(home1, ".local/bin/systemd-analyze", fakeAnalyze)
	writeExe(home1, ".local/bin/loginctl", "#!/bin/sh\nprintf 'yes\\n'\n")
	writeExe(home2, ".local/bin/systemctl", fakeSystemctl)
	writeExe(home2, ".local/bin/loginctl", "#!/bin/sh\nprintf 'yes\\n'\n")
	if targetAnalyzeOK {
		writeExe(home2, ".local/bin/systemd-analyze", fakeAnalyze)
	} else {
		writeExe(home2, ".local/bin/systemd-analyze", fakeAnalyzeFail)
	}
	// Install one of the two companion tools on box2; leave the other missing.
	writeExe(home2, ".local/bin/tool-present", "#!/bin/sh\necho ok\n")

	// Profile with service + health check (so migration reaches StateHealthy and binds target identity)
	// and agent_kit MCP wiring command.
	kitProf := demoProfile + `service:
  candidates: [ { scope: user, unit: demo.service } ]
health:
  grace_seconds: 1
  checks: [ { kind: systemd_active } ]
agent_kit:
  skills_dir: { root: state, path: "skills/" }
  mcp_add_argv: [demo, mcp, add, valise, --command, "${PORTER}", "--arg=${INSTANCE}"]
`
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(kitProf), 0o644); err != nil {
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

	// Enroll demo on box, register keys, and configure sync targets on box and box2.
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentLabel(ctx, "box", "demo", "Traveler"); err != nil {
		t.Fatal(err)
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
	if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: "source-only-nas", Kind: "rsync", Dest: "nas:/only-src"}); err != nil {
		t.Fatal(err)
	}
	if err := s.TargetSet(ctx, "box2", vaultrpc.Target{Name: "shared-nas", Kind: "rsync", Dest: "nas:/shared"}); err != nil {
		t.Fatal(err)
	}

	// Set an accepted protection plan on box referencing both targets.
	var srcBinding identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: "demo"}, &srcBinding); err != nil || srcBinding.Agent == nil {
		t.Fatalf("source binding: %+v %v", srcBinding, err)
	}
	on := true
	desired := protection.Desired{
		Scope: protection.Scope{Kind: "preset", Tier: vault.TierFull},
		Schedule: protection.Schedule{
			Enabled:  &on,
			DailyAt:  "03:30",
			Timezone: "UTC",
		},
		Period: protection.Period{Unit: "month", Timezone: "UTC", KeepAutoPeriods: 2},
		ServerCopies: []protection.ServerCopy{
			{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "shared-nas", AuthorityID: srcBinding.Agent.Ref.ServerUID}, Enabled: &on, MinimumInterval: "6h"},
			{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "source-only-nas", AuthorityID: srcBinding.Agent.Ref.ServerUID}, Enabled: &on, MinimumInterval: "6h"},
		},
	}
	frozen, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{
		AgentRef: srcBinding.Agent.Ref,
		Desired:  desired,
	})
	if err != nil {
		t.Fatalf("source PrepareProtectionChange: %v", err)
	}
	ptask, err := s.StartProtectionChange(ctx, frozen.PlanID, "src-prot-init")
	if err != nil || ptask.State != "succeeded" {
		t.Fatalf("source StartProtectionChange: %+v %v", ptask, err)
	}

	// Attach local root & tool deps on box dossier before sealing.
	extraDir := filepath.Join(home1, "MioSaid")
	if err := os.MkdirAll(extraDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extraDir, "journal.md"), []byte("hello migration"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDossier(ctx, "box", enroll.DossierUpdateParams{
		InstanceID: "demo",
		AddRoots: []enroll.LocalRoot{
			{ID: "ext-notes", Path: extraDir, Category: profile.CategoryMemory, Reason: "external notes"},
		},
		AddToolDeps: []enroll.ToolDep{
			{Name: "tool-present", Purpose: "present on target"},
			{Name: "tool-missing", Purpose: "missing on target"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Seal on box so the snapshot captures DisplayName, LocalRoots, and ToolDeps.
	rep, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierFull})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return s, home1, home2, rep.SnapshotID
}

func TestRunRestorePostMigrateReportAndCarryOver(t *testing.T) {
	t.Run("success_with_target_filtering_and_mcp_rewire", func(t *testing.T) {
		s, _, home2, snapID := setupMigrationPair(t, true)
		ctx := context.Background()

		// Simulate GUI behavior where s.ProtectionReview rejects unknown PlanIDs,
		// proving CarryProtection uses its own in-process human confirmation.
		s.ProtectionReview = func(context.Context, string, protection.FrozenChange) (bool, error) {
			return false, nil
		}

		page, err := s.ListTimeline(ctx, JourneyListRequest{AllLocations: true, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		var point journey.TimelinePoint
		for _, pt := range page.Items {
			if pt.SnapshotRef.SnapshotID == snapID {
				point = pt
			}
		}
		if point.SnapshotRef.VaultID == "" {
			t.Fatalf("snapshot %s missing in timeline", snapID)
		}
		sources := s.RestoreSources(point)
		if len(sources) == 0 {
			t.Fatal("no restore source found")
		}

		prep, err := s.PrepareRestore(ctx, RestoreRequest{
			Options: ReviveOptions{
				From:       "box",
				Target:     "box2",
				SnapshotID: snapID,
				Mode:       revive.ModeMigrate,
				AllowDraft: true,
			},
			Snapshot: point.SnapshotRef,
			Source:   sources[0],
		})
		if err != nil {
			t.Fatalf("PrepareRestore: %v", err)
		}
		rp := prep.Revive
		if len(rp.Blockers) != 0 {
			t.Fatalf("unexpected blockers: %+v", rp.Blockers)
		}
		if rp.DisplayName != "Traveler" {
			t.Fatalf("rp.DisplayName = %q, want Traveler", rp.DisplayName)
		}
		if rp.ToolCheck == nil || !slices.Equal(rp.ToolCheck.Present, []string{"tool-present"}) ||
			len(rp.ToolCheck.Missing) != 1 || rp.ToolCheck.Missing[0].Name != "tool-missing" {
			t.Fatalf("unexpected rp.ToolCheck: %+v", rp.ToolCheck)
		}
		if rp.SourceProtection == nil || !rp.SourceProtection.Enabled || rp.SourceProtection.Tier != vault.TierFull ||
			rp.SourceProtection.KeepAutoPeriods != 2 || len(rp.SourceProtection.Targets) != 2 || rp.SourceProtection.Summary == "" {
			t.Fatalf("unexpected rp.SourceProtection: %+v", rp.SourceProtection)
		}
		wantNotes := filepath.Join(home2, "MioSaid")
		if len(rp.Plan.LocalRoots) != 1 || rp.Plan.LocalRoots[0].Path != wantNotes {
			t.Fatalf("rp.Plan.LocalRoots = %+v, want %q", rp.Plan.LocalRoots, wantNotes)
		}
		if rp.Plan.Record == nil || rp.Plan.Record.DisplayName != "Traveler" || len(rp.Plan.Record.ToolDeps) != 2 {
			t.Fatalf("rp.Plan.Record missing dossier fields: %+v", rp.Plan.Record)
		}

		confirm := proceedConfirm()
		confirm.CarryProtection = true
		tsk, err := s.RunRestore(ctx, prep, confirm, nil)
		if err != nil {
			t.Fatalf("RunRestore: %v", err)
		}
		if tsk.State != "succeeded" || tsk.TargetAgent == nil {
			t.Fatalf("unexpected task: %+v", tsk)
		}
		if readFile(t, filepath.Join(home2, "MioSaid", "journal.md")) != "hello migration" {
			t.Fatal("remapped local root file was not migrated to target")
		}
		pm := tsk.PostMigrate
		if pm == nil {
			t.Fatal("expected PostMigrate report")
		}
		if !slices.Equal(pm.ToolPresent, []string{"tool-present"}) || len(pm.ToolMissing) != 1 || pm.ToolMissing[0].Name != "tool-missing" {
			t.Fatalf("unexpected PostMigrate tools: %+v", pm)
		}
		if len(pm.LocalRoots) != 1 || pm.LocalRoots[0].Path != wantNotes {
			t.Fatalf("unexpected PostMigrate LocalRoots: %+v", pm.LocalRoots)
		}
		if !pm.ProtectionAttempted || !pm.ProtectionCarried || !strings.Contains(pm.ProtectionNote, "source-only-nas") {
			t.Fatalf("unexpected PostMigrate protection carry-over: %+v", pm)
		}
		if !pm.MCPChecked || pm.MCPWired || !pm.MCPCanWire || !pm.MCPNeedsRewire || pm.MCPNote == "" {
			t.Fatalf("unexpected PostMigrate MCP status: %+v", pm)
		}

		// Verify target protection plan was actually committed with filtered targets and target ServerUID.
		view, err := s.GetProtection(ctx, "box2", protection.GetParams{AgentRef: *tsk.TargetAgent})
		if err != nil || view.Mode != "accepted" || view.ServerPlan == nil {
			t.Fatalf("target protection not carried over: %+v %v", view, err)
		}
		if len(view.ServerPlan.ServerCopies) != 1 ||
			view.ServerPlan.ServerCopies[0].TargetRef.ID != "shared-nas" ||
			view.ServerPlan.ServerCopies[0].TargetRef.AuthorityID != tsk.TargetAgent.ServerUID {
			t.Fatalf("unexpected target ServerCopies: %+v", view.ServerPlan.ServerCopies)
		}

		// Verify target dossier carried DisplayName, remapped LocalRoots, and ToolDeps.
		targetDossier, err := s.InstanceDossier(ctx, "box2", "demo")
		if err != nil {
			t.Fatalf("target InstanceDossier: %v", err)
		}
		if targetDossier.DisplayName != "Traveler" || len(targetDossier.LocalRoots) != 1 || targetDossier.LocalRoots[0].Path != wantNotes || len(targetDossier.ToolDeps) != 2 {
			t.Fatalf("target dossier mismatch: %+v", targetDossier)
		}

		// RewireMigratedMCP wires MCP on target pointing at box2's porter path.
		kitPlan, err := s.RewireMigratedMCP(ctx, "box2", "demo")
		if err != nil {
			t.Fatalf("RewireMigratedMCP: %v", err)
		}
		if kitPlan.MCPWire == nil || !kitPlan.MCPWire.Attempted || !kitPlan.MCPWire.OK {
			t.Fatalf("unexpected RewireMigratedMCP outcome: %+v", kitPlan.MCPWire)
		}
		mcpLog := readFile(t, filepath.Join(home2, "mcp-rewire.log"))
		if !strings.Contains(mcpLog, filepath.Join(home2, ".local/bin/valise-porter")) || !strings.Contains(mcpLog, "--arg=demo") {
			t.Fatalf("unexpected mcp-rewire.log: %q", mcpLog)
		}
	})

	t.Run("carryover_failure_never_breaks_succeeded_restore", func(t *testing.T) {
		// targetHasSystemctl=false causes target protection_prepare to fail when enabling timer.
		s, _, home2, snapID := setupMigrationPair(t, false)
		ctx := context.Background()

		page, err := s.ListTimeline(ctx, JourneyListRequest{AllLocations: true, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		var point journey.TimelinePoint
		for _, pt := range page.Items {
			if pt.SnapshotRef.SnapshotID == snapID {
				point = pt
			}
		}
		sources := s.RestoreSources(point)
		prep, err := s.PrepareRestore(ctx, RestoreRequest{
			Options: ReviveOptions{
				From:       "box",
				Target:     "box2",
				SnapshotID: snapID,
				Mode:       revive.ModeMigrate,
				AllowDraft: true,
			},
			Snapshot: point.SnapshotRef,
			Source:   sources[0],
		})
		if err != nil {
			t.Fatalf("PrepareRestore: %v", err)
		}

		confirm := proceedConfirm()
		confirm.CarryProtection = true
		tsk, err := s.RunRestore(ctx, prep, confirm, nil)
		if err != nil {
			t.Fatalf("RunRestore must stay succeeded even when protection carry-over fails: %v", err)
		}
		if tsk.State != "succeeded" {
			t.Fatalf("tsk.State = %q, want succeeded", tsk.State)
		}
		if readFile(t, filepath.Join(home2, "MioSaid", "journal.md")) != "hello migration" {
			t.Fatal("migrated data missing")
		}
		if tsk.PostMigrate == nil || !tsk.PostMigrate.ProtectionAttempted || tsk.PostMigrate.ProtectionCarried || tsk.PostMigrate.ProtectionNote == "" {
			t.Fatalf("expected honest protection carry-over failure note: %+v", tsk.PostMigrate)
		}
	})
}
