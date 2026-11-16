package gui

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
	"time"
	"valise/client/config"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

func restoreWorld(t *testing.T) (*App, *ui.Tester, *restoreFlow) {
	t.Helper()
	a, tt := world(t)
	a.svc.Config.Repos = []config.Repo{{Name: "usb", Path: t.TempDir()}}
	p := a.timeline.value.Items[0]
	p.Copies[0].HeaderDigest = "sample-header"
	a.openRestore(p, "home")
	tt.Frame()
	tt.Advance(time.Second)
	return a, tt, a.restore
}

func TestRestoreSetupUsesSnapshotScopeAndSeparateSource(t *testing.T) {
	a, tt, r := restoreWorld(t)
	shot(t, tt, "restore-setup")
	for _, text := range []string{T("restore.mode.rollback"), T("restore.mode.migrate"), T("restore.mode.clone"), T("restore.read_from"), T("restore.view_plan")} {
		if !tt.HasText(text) {
			t.Errorf("missing %q", text)
		}
	}
	if len(r.sources) != 1 || r.sources[0].Spec != "repo:usb/home" {
		t.Fatalf("sources %+v", r.sources)
	}
	click(t, tt, T("restore.mode.clone"))
	if !tt.HasText(T("restore.copy_id")) {
		t.Fatal("no explicit clone id")
	}
	// A conflicting point or an unadapted remote cannot enter execution.
	p := r.point
	p.MetadataState = "conflict"
	a.restore = nil
	a.openRestore(p, "home")
	if a.restore != nil {
		t.Fatal("conflict entered restore")
	}
	p.MetadataState = ""
	p.Copies[0].LocationRef = journey.LocationRef{Kind: "legacy_target", ID: "nas"}
	a.openRestore(p, "home")
	if a.restore != nil {
		t.Fatal("unadapted target entered restore")
	}
}

func TestRestorePlanKeepsAllowancesSeparateFromExecution(t *testing.T) {
	a, tt, r := restoreWorld(t)
	r.step = "plan"
	r.plan = &service.PreparedRestore{Revive: &service.RevivePlan{Target: "home", From: "home", RestoreSecret: true, Plan: revive.Plan{Mode: revive.ModeRollback, InstanceID: "xiaoman", Tier: "full", Body: revive.BodyPlan{Action: revive.BodyKeep}}, Blockers: []valise.UserError{{Code: "profile_draft", Reason: "尚未测试"}, {Code: "profile_from_snapshot", Reason: "快照资料"}}}}
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("restore.allow_draft")) || !tt.HasText(T("restore.allow_snapshot")) || tt.HasText(T("restore.prepare")) {
		t.Fatalf("allowance bypass: %q", tt.Texts())
	}
	if !tt.HasText(T("restore.no_service")) {
		t.Fatal("foreground plan claims stopping a service")
	}
	shot(t, tt, "restore-plan")
	// Even a plan without those blockers still needs a separate second decision.
	r.plan.Revive.Blockers = nil
	r.step = "confirm"
	r.reply = make(chan restoreSwapReply, 1)
	r.deadline = time.Now().Add(time.Minute)
	r.task = service.RestoreTask{Target: "home", Instance: "xiaoman"}
	r.verify = &revive.StageVerifyResult{Files: 4, Bytes: 128}
	tt.Frame()
	tt.Advance(time.Second)
	select {
	case <-r.reply:
		t.Fatal("first confirmation auto-swapped")
	default:
	}
	click(t, tt, T("restore.swap_confirm"))
	click(t, tt, T("restore.execute.rollback"))
	select {
	case reply := <-r.reply:
		if !reply.approve {
			t.Fatal("declined")
		}
	default:
		t.Fatal("second confirmation not delivered")
	}
	r.step = "confirm"
	r.deadline = time.Now().Add(-time.Second)
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("restore.execute.rollback")) || !tt.HasText(T("restore.recheck_stage")) {
		t.Fatal("expired stage offers swap")
	}
	shot(t, tt, "restore-expired")
	_ = a
}

func TestRestoreCloseWaitsAndFileOnlyDoesNotClaimHealth(t *testing.T) {
	a, tt, r := restoreWorld(t)
	r.j.busy = true
	r.step = "swap"
	closed := false
	if a.RequestClose(func() { closed = true }) || closed {
		t.Fatal("closed during swap")
	}
	r.j.busy = false
	a.restoreSafeExit(r)
	if !closed {
		t.Fatal("safe close not delivered")
	}
	r.step = "result"
	r.task = service.RestoreTask{Target: "cloud", Instance: "copy", State: "succeeded", SwapState: revive.StateRestored, Verified: true}
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("restore.no_start")) || tt.HasText(T("restore.health_checked")) || tt.HasText(T("restore.relation_checked")) {
		t.Fatalf("fake runtime evidence: %q", tt.Texts())
	}
	shot(t, tt, "restore-file-result")
}

func TestRestoreRootMappingRejectsAmbiguity(t *testing.T) {
	for _, bad := range []string{"state=relative", "state=/a\nstate=/b", "bad root=/a"} {
		if _, err := parseRestoreRoots(bad); err == nil {
			t.Fatal("accepted " + bad)
		}
	}
	roots, err := parseRestoreRoots("state=/home/a\nworkspace=/home/b")
	if err != nil || !strings.HasPrefix(roots["state"], "/") {
		t.Fatal(roots, err)
	}
}

// With one server, a copy can still be made: it stays on the agent's own
// server, which is offered and chosen, and asks for its directories
// outright (docs/UI.md 7.11). Only a migration leaves the origin out.
func TestCloneOnTheOnlyServer(t *testing.T) {
	a, tt, r := restoreWorld(t)
	a.agents.value.Items = a.agents.value.Items[:1]
	a.agents.value.Observations = a.agents.value.Observations[:1]
	if hosts := a.hosts(); len(hosts) != 1 || hosts[0] != "home" || r.origin != "home" {
		t.Fatalf("not a one-server world: %q, origin %q", hosts, r.origin)
	}
	if got := restoreTargets(a.hosts(), "home", revive.ModeClone); len(got) != 1 || got[0] != "home" {
		t.Fatalf("a copy cannot stay on its server: %q", got)
	}
	if got := restoreTargets(a.hosts(), "home", revive.ModeMigrate); len(got) != 0 {
		t.Fatalf("a migration offered its own origin: %q", got)
	}
	click(t, tt, T("restore.mode.migrate"))
	if r.target != "" || tt.HasText(T("restore.same_server_hint")) {
		t.Fatalf("migration kept the origin as its target: %q", r.target)
	}
	click(t, tt, T("restore.mode.clone"))
	if r.mode != revive.ModeClone || r.target != "home" {
		t.Fatalf("clone did not choose the origin: mode %q target %q", r.mode, r.target)
	}
	if !tt.HasText(T("restore.same_server_hint")) || !tt.HasText(T("restore.copy_id")) || tt.HasText(T("restore.path_hint")) {
		t.Fatalf("same-server copy does not ask for directories outright: %q", tt.Texts())
	}
	shot(t, tt, "restore-clone-same-server")
	// The plan request goes out with the origin as its target.
	var started int
	a.run = func(func()) { started++ }
	r.id, r.roots = "xiaoman-copy", "state=/home/me/.openclaw-copy"
	a.prepareRestore(r)
	if r.j.err != nil || !r.j.busy || started != 1 || r.target != r.origin {
		t.Fatalf("copy plan not started: err %v busy %v started %d", r.j.err, r.j.busy, started)
	}
	r.j.cancel()
}
