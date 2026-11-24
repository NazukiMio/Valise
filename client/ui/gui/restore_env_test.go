package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

func TestRestoreOriginReadOnlyAndMigrateTargets(t *testing.T) {
	a, _ := world(t)
	a.vaultNames["cloud"] = "cloudbox"
	tt := ui.NewTester(a.View, 1180, 1400)

	// Origin vault name maps back to canonical host key in a.hosts().
	pt := testTimelinePoint()
	pt.Copies = []journey.Copy{{
		LocationRef:   journey.LocationRef{Kind: "server_vault", ID: "cloud"},
		SourceLocator: "cloudbox/alan/2026-11",
		HeaderDigest:  "hdr",
	}}
	a.openRestore(pt, "cloudbox")
	if a.restore == nil || a.restore.origin != "cloud" {
		t.Fatalf("vault name did not resolve to host key: %+v", a.restore)
	}

	// Empty origin resolves via SourceLocator prefix against a.vaultNames / a.hosts().
	a.restore = nil
	a.openRestore(pt, "")
	if a.restore == nil || a.restore.origin != "cloud" {
		t.Fatalf("empty origin did not resolve via vaultNames: %+v", a.restore)
	}

	// Two-server world ("home" and "cloud") so migrating from "cloud" has sole target "home".
	a.agents.value.Observations = a.agents.value.Observations[:2]
	// Clean default target directory uses restore.place.default_clean and hides redundant replace_default.
	r := a.restore
	r.sources[0].Copy.Metadata.ProfileID = "demo"
	a.inspectPlacement = func(_ context.Context, _ service.TargetPlacementRequest) (*service.TargetPlacement, error) {
		return &service.TargetPlacement{
			TargetHome:          "/home/target",
			DefaultStateDir:     "/home/target/.demo",
			RecommendedStateDir: "/home/target/.demo",
			CanReplaceDefault:   true,
			Relocatable:         true,
		}, nil
	}
	r.allowVersionMismatch, r.skipService, r.draft = true, true, true
	tt.Frame()
	click(t, tt, T("restore.mode.migrate"))
	// Switching mode resets opt-in flags and auto-selects the sole remaining host ("home").
	if r.allowVersionMismatch || r.skipService || r.draft {
		t.Fatalf("mode switch did not reset opt-ins: mismatch=%v skip=%v draft=%v", r.allowVersionMismatch, r.skipService, r.draft)
	}
	if r.target != "home" {
		t.Fatalf("migrate did not auto-select sole target host: got %q, want home", r.target)
	}
	// Origin is rendered read-only (clicking it does not open a select menu).
	_ = tt.Click("cloud")
	if menu := tt.Menu(); len(menu) != 0 {
		t.Fatalf("origin opened a select menu instead of being read-only: %v", menu)
	}
	hasAll(t, tt.Texts(), T("restore.origin"), "cloud", T("restore.place.default_clean"))
	if tt.HasText(T("restore.place.replace_default", "dir", "/home/target/.demo")) {
		t.Fatalf("redundant replace_default shown when default dir is clean: %q", tt.Texts())
	}

	// With only one server, migrate shows migrate_no_other_server and disables view_plan.
	a.agents.value.Items = a.agents.value.Items[:1]
	a.agents.value.Observations = a.agents.value.Observations[:1]
	r.origin = "home"
	r.target = "home"
	tt.Frame()
	hasAll(t, tt.Texts(), T("restore.migrate_no_other_server"))
	if r.target != "" {
		t.Fatalf("migrate kept origin as target: %q", r.target)
	}
	called := 0
	a.prepareRestoreFn = func(context.Context, service.RestoreRequest) (*service.PreparedRestore, error) {
		called++
		return nil, nil
	}
	_ = tt.Click(T("restore.view_plan"))
	if called != 0 {
		t.Fatal("disabled view_plan triggered prepareRestore")
	}
}

func TestRestoreMultiStepWizardAndAutoSkip(t *testing.T) {
	a, tt, r := restoreWorld(t)
	a.run = func(f func()) { f() }
	r.mode = revive.ModeMigrate
	r.origin, r.target = "home", "cloud"

	stage := 0
	var lastReq service.RestoreRequest
	a.prepareRestoreFn = func(_ context.Context, req service.RestoreRequest) (*service.PreparedRestore, error) {
		lastReq = req
		stage++
		switch stage {
		case 1:
			// Step 1: body missing + service missing -> enters "env", shows ONLY body missing card.
			return &service.PreparedRestore{Revive: &service.RevivePlan{
				Target: "cloud", From: "home",
				Plan: revive.Plan{Mode: revive.ModeMigrate, InstanceID: "xiaoman", Tier: "full", Unit: "demo.service", Profile: profile.Profile{ID: "demo"}, Body: revive.BodyPlan{Action: revive.BodyKeep}},
				Blockers: []valise.UserError{
					{Code: "body_rebuild_unverified", Reason: "缺少程序本体"},
					{Code: "service_missing", Reason: "缺少服务"},
				},
			}}, nil
		case 2:
			// Step 2: body installed with version diff -> still in "env", shows ONLY version mismatch card.
			return &service.PreparedRestore{Revive: &service.RevivePlan{
				Target: "cloud", From: "home",
				BodySummary: "来源 v1；目标 v2",
				Plan:        revive.Plan{Mode: revive.ModeMigrate, InstanceID: "xiaoman", Tier: "full", Unit: "demo.service", Profile: profile.Profile{ID: "demo"}, Body: revive.BodyPlan{Action: revive.BodyKeep}},
				Blockers: []valise.UserError{
					{Code: "body_version_mismatch", Reason: "版本不同"},
					{Code: "service_missing", Reason: "缺少服务"},
				},
			}}, nil
		case 3:
			// Step 3: body accepted -> only service_missing remains with stale local override.
			return &service.PreparedRestore{Revive: &service.RevivePlan{
				Target: "cloud", From: "home",
				ProfileOverrideStale:    true,
				EffectiveProfileVersion: 2,
				OfficialProfileVersion:  3,
				Plan:                    revive.Plan{Mode: revive.ModeMigrate, InstanceID: "xiaoman", Tier: "full", Unit: "demo.service", Profile: profile.Profile{ID: "demo"}, Body: revive.BodyPlan{Action: revive.BodyKeep}},
				Blockers:                []valise.UserError{{Code: "service_missing", Reason: "缺少服务"}},
			}}, nil
		default:
			// Step 4: all env blockers resolved with InstallService -> auto-skips to "plan".
			return &service.PreparedRestore{Revive: &service.RevivePlan{
				Target: "cloud", From: "home",
				InstallArgv: []string{"demo", "gateway", "install"},
				Plan:        revive.Plan{Mode: revive.ModeMigrate, InstanceID: "xiaoman", Tier: "full", Unit: "demo.service", InstallService: true, Profile: profile.Profile{ID: "demo"}, Body: revive.BodyPlan{Action: revive.BodyKeep}},
			}}, nil
		}
	}

	// Stage 1: enters "env" with ONLY body missing card visible.
	a.prepareRestore(r)
	tt.Frame()
	if r.step != "env" || !r.envNeeded || r.envAutoSkipped {
		t.Fatalf("stage 1 state: step=%q envNeeded=%v autoSkipped=%v", r.step, r.envNeeded, r.envAutoSkipped)
	}
	hasAll(t, tt.Texts(),
		T("restore.stage.env"),
		T("restore.env.body_missing_title"),
		T("restore.inline_install.title"),
		T("restore.env.continue_data_only"),
	)
	if tt.HasText(T("restore.env.service_missing_title")) {
		t.Fatalf("stage 1 leaked service_missing card: %q", tt.Texts())
	}

	// Stage 2: simulate recheck after body install -> body mismatch card.
	a.prepareRestore(r)
	tt.Frame()
	hasAll(t, tt.Texts(), T("restore.env.body_mismatch_title"), "来源 v1；目标 v2", T("restore.allow_version_mismatch"), T("restore.env.next"))
	if tt.HasText(T("restore.env.service_missing_title")) {
		t.Fatalf("stage 2 leaked service_missing card: %q", tt.Texts())
	}
	click(t, tt, T("restore.allow_version_mismatch"))
	click(t, tt, T("restore.env.next"))
	tt.Advance(time.Second)

	// Stage 3: allowVersionMismatch was sent, now only service_missing with stale override card is shown.
	if !lastReq.Options.AllowVersionMismatch || r.step != "env" {
		t.Fatalf("stage 3 req=%+v step=%q", lastReq.Options, r.step)
	}
	hasAll(t, tt.Texts(),
		T("restore.env.service_missing_title"),
		T("agent.service.local_override_stale", "id", "demo", "local", 2, "official", 3),
		T("restore.env.restore_official_recheck"),
	)

	// Stage 4: recheck resolves service via InstallService -> auto-skips to "plan" with envAutoSkipped == true.
	click(t, tt, T("restore.env.recheck"))
	tt.Advance(time.Second)
	if r.step != "plan" || r.envNeeded || !r.envAutoSkipped {
		t.Fatalf("stage 4 state: step=%q envNeeded=%v autoSkipped=%v", r.step, r.envNeeded, r.envAutoSkipped)
	}
	hasAll(t, tt.Texts(),
		T("restore.stage.env_skipped"),
		T("restore.env.auto_skipped_note"),
		T("restore.service_auto_install", "unit", "demo.service", "cmd", "demo gateway install"),
		T("restore.prepare"),
	)

	// Clicking restore.edit resets opt-ins and returns to setup; direct clean plan goes straight to "plan".
	click(t, tt, T("restore.edit"))
	if r.step != "setup" || r.allowVersionMismatch || r.envAutoSkipped {
		t.Fatalf("edit did not reset: step=%q mismatch=%v autoSkipped=%v", r.step, r.allowVersionMismatch, r.envAutoSkipped)
	}
	a.prepareRestore(r)
	tt.Frame()
	if r.step != "plan" || r.envNeeded || r.envAutoSkipped {
		t.Fatalf("direct clean plan state: step=%q envNeeded=%v autoSkipped=%v", r.step, r.envNeeded, r.envAutoSkipped)
	}
	hasAll(t, tt.Texts(), T("restore.stage.env_skipped"), T("restore.prepare"))
}
