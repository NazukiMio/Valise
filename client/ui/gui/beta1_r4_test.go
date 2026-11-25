package gui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/minisign/minisigntest"
	"valise/core/enroll"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

func TestRestoreSetupSingleSourceReadOnlyAndOriginStrip(t *testing.T) {
	_, tt, r := restoreWorld(t)
	// Single source: rendered as read-only fact, no radio button for source; origin shown once in top strip.
	texts := tt.Texts()
	originCount := 0
	for _, s := range texts {
		if s == T("restore.origin") {
			originCount++
		}
	}
	if originCount != 1 {
		t.Fatalf("expected restore.origin once (top strip only), got %d in %q", originCount, texts)
	}
	hasAll(t, texts, T("restore.read_from"), journeyLocation(r.sources[0].Copy.LocationRef))
	if r.carryProtection != true {
		t.Fatal("carryProtection should default to true on openRestore")
	}

	// Multiple sources: radio group switches active source.
	r.sources = append(r.sources, service.RestoreSource{
		Spec: "server:home",
		Copy: journey.Copy{LocationRef: journey.LocationRef{Kind: "server_vault", ID: "home"}, HeaderDigest: "h2"},
	})
	tt.Frame()
	click(t, tt, journeyLocation(r.sources[1].Copy.LocationRef))
	if r.source != 1 {
		t.Fatalf("multi-source radio did not select source 1: got %d", r.source)
	}
	if !r.carryProtection {
		t.Fatal("resetPlanOptIns should preserve carryProtection=true")
	}
}

func TestRestorePlanCarryProtectionDossierAndEmptyShellNotice(t *testing.T) {
	_, tt, r := restoreWorld(t)
	r.mode = revive.ModeMigrate
	r.origin, r.target = "home", "cloud"
	r.plan = &service.PreparedRestore{Revive: &service.RevivePlan{
		Target: "cloud", From: "home",
		Dossier: &service.DossierReviveSummary{
			LocalOverrides: 1,
			LocalRoots:     []enroll.LocalRoot{{ID: "ext-notes", Path: "/home/u/notes", Category: "memory"}},
		},
		ToolCheck: &service.ToolCheckResult{
			Present: []string{"git"},
			Missing: []enroll.ToolDep{{Name: "ffmpeg", Purpose: "audio"}},
		},
		SourceProtection: &service.SourceProtectionSummary{
			Enabled: true,
			Summary: "定时自动备份：每天 03:00（UTC）；档位 完整",
		},
		Plan: revive.Plan{Mode: revive.ModeMigrate, InstanceID: "xiaoman", Tier: "full", Body: revive.BodyPlan{Action: revive.BodyKeep}},
	}}
	r.step = "plan"
	tt.Frame()
	hasAll(t, tt.Texts(),
		T("restore.dossier_local_roots"),
		T("restore.dossier_local_roots_val", "n", 1, "list", "/home/u/notes"),
		T("restore.tool_check_label"),
		T("restore.tool_check_summary_missing", "present", 1, "missing", 1, "tools", "ffmpeg"),
		T("restore.carry_protection", "summary", "定时自动备份：每天 03:00（UTC）；档位 完整"),
	)
	click(t, tt, T("restore.carry_protection", "summary", "定时自动备份：每天 03:00（UTC）；档位 完整"))
	if r.carryProtection {
		t.Fatal("clicking carryProtection checkbox did not toggle it off")
	}

	// Body missing env card displays calm empty-shell explanation.
	r.step = "env"
	r.envNeeded = true
	r.plan.Revive.Blockers = []valise.UserError{{Code: "body_rebuild_unverified", Reason: "未安装"}}
	tt.Frame()
	hasAll(t, tt.Texts(), T("restore.env.empty_shell_note"))
}

func TestSettingsProfilesPackAutoClearStaleOverrides(t *testing.T) {
	a, tt := settingsWorld(t)
	signer := minisigntest.New()
	lines := strings.Split(strings.TrimSpace(signer.PublicKey), "\n")
	a.svc.Release.PublicKey = lines[len(lines)-1]

	// Install official pack v2 first, then import local override v2 (stale vs v3 pack).
	v2Pack := writeSignedPack(t, signedProfileYAML, "", "2026.11.20", signer)
	pf2, err := a.svc.ReadPackFile(v2Pack)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.svc.InstallPack(pf2); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(t.TempDir(), "demo.yaml")
	if err := os.WriteFile(localPath, []byte(signedProfileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	lf, err := a.svc.ReadLocalProfileFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.svc.InstallLocalProfile(lf); err != nil {
		t.Fatal(err)
	}

	// Pick signed pack v3 -> preview shows checked-by-default clear-stale checkbox.
	v3YAML := strings.Replace(signedProfileYAML, "profile_version: 2", "profile_version: 3", 1)
	v3Pack := writeSignedPack(t, v3YAML, "", "2026.11.25", signer)
	a.SetFilePicker(func(string, string) (string, error) { return v3Pack, nil })

	a.router.Push("/settings")
	tt.Frame()
	click(t, tt, T("settings.tab.profiles"))
	click(t, tt, T("profiles.pick_file"))

	cbLabel := T("profiles.preview_clear_stale", "list", "demo")
	hasAll(t, tt.Texts(), cbLabel, T("profiles.install_go"))
	if !a.profiles.clearStaleOnInstall {
		t.Fatal("clearStaleOnInstall should default to true")
	}

	click(t, tt, T("profiles.install_go"))
	hasAll(t, tt.Texts(), T("profiles.install_done_cleared", "version", "2026.11.25", "n", 1))
	if tt.HasText(T("profiles.override_banner", "n", 1)) {
		t.Fatalf("stale override still present after auto-clear: %q", tt.Texts())
	}
}

func TestAgentRouteReentryAndBidirectionalServiceSync(t *testing.T) {
	a, _ := world(t)
	tt := ui.NewTester(a.View, 1180, 1400)

	statusCalls := 0
	liveActive := "inactive"
	a.agentServiceHook = &agentServiceOps{
		status: func(context.Context, string, string) (*service.InstanceServiceInfo, error) {
			statusCalls++
			return &service.InstanceServiceInfo{
				Instance:  "alan",
				ProfileID: "hermes",
				Unit:      "hermes.service",
				Scope:     "user",
				LoadState: "loaded",
				Active:    liveActive,
				Enabled:   "enabled",
				HasUnit:   true,
			}, nil
		},
		actions: func(context.Context, string, string) (*service.ActionList, error) {
			return &service.ActionList{Instance: "alan", ProfileID: "hermes"}, nil
		},
	}

	// First visit loads once; extra frames do NOT poll.
	a.navigate("/agents/cloud/alan")
	tt.Frame()
	tt.Frame()
	if statusCalls != 1 {
		t.Fatalf("first visit statusCalls = %d, want 1", statusCalls)
	}
	// Sync InstanceServiceInfo -> agents.value.Items updated cloud/alan to inactive.
	if got := a.agents.value.Items[1].Instance.Service[0].Active; got != "inactive" {
		t.Fatalf("service -> agents sync active = %q, want inactive", got)
	}

	// Leave agent page and re-enter: triggers fresh read once.
	liveActive = "active"
	a.navigate("/agents")
	tt.Frame()
	if a.lastAgentRoute != "" {
		t.Fatalf("leaving agent page did not clear lastAgentRoute: %q", a.lastAgentRoute)
	}
	a.navigate("/agents/cloud/alan")
	tt.Frame()
	tt.Frame()
	if statusCalls != 2 {
		t.Fatalf("re-entry statusCalls = %d, want 2", statusCalls)
	}
	if got := a.agents.value.Items[1].Instance.Service[0].Active; got != "active" {
		t.Fatalf("re-entry sync active = %q, want active", got)
	}

	// Sync agents.value.Items -> agentServices updates live state in place.
	a.syncAgentsToServiceStates([]service.AgentSummary{{
		ConnectionID: "cloud",
		Instance: enroll.InstanceSummary{
			InstanceID:     "alan",
			ProfileVersion: 1,
			Service:        []enroll.ServiceState{{Unit: "hermes.service", LoadState: "loaded", Active: "failed", Enabled: "enabled"}},
		},
	}})
	st := a.agentServices["cloud/alan"]
	if st.info.Active != "failed" || !st.started {
		t.Fatalf("agents -> service sync got active=%q started=%v", st.info.Active, st.started)
	}

	// Unit change invalidates st.started so next view reloads full metadata.
	a.syncAgentsToServiceStates([]service.AgentSummary{{
		ConnectionID: "cloud",
		Instance: enroll.InstanceSummary{
			InstanceID:     "alan",
			ProfileVersion: 1,
			Service:        []enroll.ServiceState{{Unit: "hermes-v2.service", LoadState: "loaded", Active: "active"}},
		},
	}})
	if st.started {
		t.Fatal("unit change did not invalidate st.started")
	}
}

func TestRestoreResultPostMigrateCardAndQueryVisibility(t *testing.T) {
	a, _, r := restoreWorld(t)
	tt := ui.NewTester(a.View, 1180, 1800)
	var copied string
	a.SetClipboard(func(s string) error { copied = s; return nil })
	oldSvc := &agentServiceState{started: true}
	a.agentServices["cloud/alan"] = oldSvc

	// Succeeded migration: hides restore.query, shows open_target_agent and PostMigrateReport.
	r.step = "result"
	r.task = service.RestoreTask{
		OpID:      "op-123",
		Target:    "cloud",
		Instance:  "alan",
		State:     "succeeded",
		SwapState: revive.StateHealthy,
		Verified:  true,
		Relation:  true,
		PostMigrate: &service.PostMigrateReport{
			ProtectionAttempted: true,
			ProtectionCarried:   true,
			ProtectionNote:      "已沿用原保护计划",
			LocalRoots:          []enroll.LocalRoot{{ID: "ext-kb", Path: "/home/cloud/kb", Category: "memory"}},
			MCPChecked:          true,
			MCPWired:            false,
			MCPCanWire:          true,
			MCPNeedsRewire:      true,
			MCPNote:             "主目录变化，建议重新接入 MCP",
			ToolPresent:         []string{"git"},
			ToolMissing:         []enroll.ToolDep{{Name: "jq", Purpose: "json"}, {Name: "ffmpeg", Purpose: "media"}},
			ExternalRefs:        []enroll.ExternalRef{{Path: "/srv/shared", Kind: "external_dir", Reason: "manual"}},
		},
	}
	var rewired []string
	a.rewireMigratedMCP = func(_ context.Context, host, inst string) (*service.KitPlan, error) {
		rewired = append(rewired, host+"/"+inst)
		return &service.KitPlan{
			MCPWire:        &service.KitStepOutcome{Attempted: true, OK: true, ExitCode: 0},
			ServiceRestart: &service.KitServiceRestartOutcome{Attempted: true, OK: true, Healthy: true, Unit: "demo.service", ExitCode: 0},
		}, nil
	}

	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("restore.query")) {
		t.Fatalf("succeeded result must not show restore.query: %q", tt.Texts())
	}
	hasAll(t, tt.Texts(),
		T("restore.open_target_agent"),
		T("restore.post.title"),
		"已沿用原保护计划",
		"/home/cloud/kb",
		T("restore.post.rewire_mcp"),
		"git", "jq", "ffmpeg", "/srv/shared",
		T("restore.post.copy_missing_tools"),
	)

	// Copy missing tool names.
	click(t, tt, T("restore.post.copy_missing_tools"))
	if copied != "jq ffmpeg" {
		t.Fatalf("copied = %q, want %q", copied, "jq ffmpeg")
	}

	// 1-click MCP rewire displays exit code and restart health separately.
	click(t, tt, T("restore.post.rewire_mcp"))
	if !slices.Equal(rewired, []string{"cloud/alan"}) {
		t.Fatalf("rewired = %v", rewired)
	}
	hasAll(t, tt.Texts(),
		T("kit.outcome.mcp_ok", "code", 0),
		T("kit.outcome.restart_ok", "unit", "demo.service", "code", 0),
		T("kit.outcome.health_ok", "unit", "demo.service"),
	)

	// Reconciling state still offers restore.query; failed state hides it.
	r.task.State = "reconciling"
	tt.Frame()
	if !tt.HasText(T("restore.query")) {
		t.Fatalf("reconciling result missing restore.query: %q", tt.Texts())
	}
	r.progress = service.Progress{Stage: service.StagePull, Done: 50, Total: 100}
	r.j.busy = false
	a.run = func(func()) {}
	a.queryRestore(r)
	if r.progress != (service.Progress{}) {
		t.Fatalf("queryRestore did not clear stale progress: %+v", r.progress)
	}
	tt.Frame()
	hasAll(t, tt.Texts(), T("restore.querying"))

	r.j.busy = false
	r.task.State = "failed"
	tt.Frame()
	if tt.HasText(T("restore.query")) {
		t.Fatalf("failed result must not show restore.query: %q", tt.Texts())
	}

	// Open target agent button clears cached service state and navigates to /agents/cloud/alan.
	r.task.State = "succeeded"
	a.run = func(func()) {}
	tt.Frame()
	click(t, tt, T("restore.open_target_agent"))
	if a.router.Path() != "/agents/cloud/alan" {
		t.Fatalf("open_target_agent routed to %q", a.router.Path())
	}
	if a.agentServices["cloud/alan"] == oldSvc {
		t.Fatal("open_target_agent did not drop cached agentServices entry")
	}
}
