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
