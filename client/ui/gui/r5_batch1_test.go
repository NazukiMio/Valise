package gui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/install"
	"valise/core/journey"
	"valise/core/profile"
	"valise/core/revive"
	"valise/ui/service"
)

func TestWP1QuickReenrollAndBatchEnroll(t *testing.T) {
	a, tt := world(t)
	savedAgents := a.agents.value
	a.scans["box"] = &serverScan{
		adopts:          map[string]*adoption{},
		selected:        map[string]bool{},
		batchEnablePlan: true,
		found: []service.DetectedInstance{
			{ID: "hermes", ProfileID: "hermes", StateDir: "/home/u/.hermes", ProfileVersion: 2},
			{ID: "openclaw", ProfileID: "openclaw", StateDir: "/home/u/.openclaw", Enrolled: "openclaw", ProfileVersion: 3, EnrolledProfileVersion: 2},
		},
	}
	var quickCalls []string
	a.quickReenrollFn = func(_ context.Context, host, inst string) (*service.EnrollBatchResult, error) {
		quickCalls = append(quickCalls, host+"/"+inst)
		return &service.EnrollBatchResult{Host: host, Enrolled: []string{inst}}, nil
	}
	var batchCalls [][]service.EnrollBatchItem
	a.enrollBatchFn = func(_ context.Context, host string, items []service.EnrollBatchItem) (*service.EnrollBatchResult, error) {
		batchCalls = append(batchCalls, items)
		return &service.EnrollBatchResult{
			Host:        host,
			Enrolled:    []string{"hermes", "openclaw"},
			PlanApplied: []string{"hermes"},
			PlanNotes:   map[string]string{"openclaw": "保留现有计划"},
		}, nil
	}

	a.router.Push("/servers/box/agents")
	tt.Frame()
	for _, want := range []string{
		T("onserver.batch.select_all"),
		T("onserver.batch.enable_plan"),
		T("adopt.update_profile"),
		T("adopt.review_first"),
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}

	// 1-click re-enroll on outdated card calls QuickReenroll directly.
	if err := tt.Click(T("adopt.update_profile")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(quickCalls) != 1 || quickCalls[0] != "box/openclaw" {
		t.Fatalf("expected 1-click QuickReenroll for box/openclaw, got %v", quickCalls)
	}

	// Select all + batch run invokes EnrollBatch once with correct per-item flags.
	a.scans["box"].found = []service.DetectedInstance{
		{ID: "hermes", ProfileID: "hermes", StateDir: "/home/u/.hermes", ProfileVersion: 2},
		{ID: "openclaw", ProfileID: "openclaw", StateDir: "/home/u/.openclaw", Enrolled: "openclaw", ProfileVersion: 3, EnrolledProfileVersion: 2},
	}
	a.scans["box"].busy = false
	a.scans["box"].err = nil
	tt.Frame()
	if err := tt.Click(T("onserver.batch.select_all")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("onserver.batch.run", "n", 2)); err != nil {
		t.Fatal(err)
	}
	a.scans["box"].busy = false
	a.scans["box"].err = nil
	tt.Frame()
	if len(batchCalls) != 1 || len(batchCalls[0]) != 2 {
		t.Fatalf("expected single batch call with 2 items, got %#v", batchCalls)
	}
	if !batchCalls[0][0].EnableDefaultPlan || batchCalls[0][0].ReactivatePlan {
		t.Fatalf("unenrolled item flags wrong: %#v", batchCalls[0][0])
	}
	if batchCalls[0][1].EnableDefaultPlan || !batchCalls[0][1].ReactivatePlan {
		t.Fatalf("enrolled item flags wrong: %#v", batchCalls[0][1])
	}
	if !tt.HasText(T("onserver.batch.summary", "enrolled", 2, "plans", 1, "failed", 0)) {
		t.Fatalf("missing batch summary in %q", tt.Texts())
	}

	// In-place 1-click re-enroll on Agent Detail (protect.no_ref and info.Stale).
	a.agents.value = savedAgents
	it0 := &a.agents.value.Items[0]
	it0.AgentRef = nil
	a.agentServiceHook = &agentServiceOps{
		status: func(context.Context, string, string) (*service.InstanceServiceInfo, error) {
			return &service.InstanceServiceInfo{Unit: "openclaw.service", Stale: true}, nil
		},
	}
	a.router.Push("/agents/" + it0.ConnectionID + "/" + it0.Instance.InstanceID)
	a.agentTab = tabOverview
	tt.Frame()
	if err := tt.Click(T("adopt.update_profile")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(quickCalls) != 2 {
		t.Fatalf("expected in-place QuickReenroll from stale service card, got %v", quickCalls)
	}
	a.agentTab = tabManage
	tt.Frame()
	if err := tt.Click(T("protect.reenroll")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(quickCalls) != 3 {
		t.Fatalf("expected in-place QuickReenroll from protect.no_ref card, got %v", quickCalls)
	}
}

func TestWP3FourWaySkillStatusAndDiffModal(t *testing.T) {
	a, tt := world(t)
	now := time.Now()
	installedAt := now.Add(-48 * time.Hour)
	diskMTime := now.Add(-2 * time.Hour)

	a.agentKitHook = &agentKitOps{
		status: func(context.Context, string, string) (*agentkit.KitStatusResult, error) {
			return &agentkit.KitStatusResult{
				Dir: "/home/u/.openclaw/skills",
				Skills: []agentkit.SkillState{
					{
						Skill: "valise-onboard", Path: "/home/u/.openclaw/skills/valise-onboard/SKILL.md",
						State: agentkit.SkillModified, ChangeKind: agentkit.SkillChangeOfficialUpdate,
						InstalledSHA256: "aaaa111122223333", DiskSHA256: "aaaa111122223333",
						DiskContent: "line1\nold official\n", ShippedContent: "line1\nnew official\n",
					},
					{
						Skill: "valise-backup", Path: "/home/u/.openclaw/skills/valise-backup/SKILL.md",
						State: agentkit.SkillModified, ChangeKind: agentkit.SkillChangeBothChanged,
						InstalledAt: &installedAt, DiskMTime: &diskMTime,
						DiskContent: "keep\nlocal edit\n", ShippedContent: "keep\nupstream edit\n",
					},
				},
			}, nil
		},
		guide: func(context.Context, string, string) (*service.KitGuide, error) {
			return &service.KitGuide{}, nil
		},
	}

	a.router.Push("/agents/box/openclaw")
	a.agentTab = tabManage
	tt.Frame()
	for _, want := range []string{
		T("kit.skill.official_update"),
		T("kit.compare_update_open"),
		T("kit.diff.open"),
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}

	// Open diff modal for valise-onboard.
	a.agentKits["box/openclaw"].diffSkill = "valise-onboard"
	tt.Frame()
	for _, want := range []string{
		T("kit.diff.title", "skill", "valise-onboard"),
		"- old official",
		"+ new official",
		T("kit.diff.close"),
		T("kit.diff.go_update"),
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing diff modal text %q in %q", want, tt.Texts())
		}
	}

	// Official-update-only modal should NOT show false local-modification warning.
	st := a.agentKits["box/openclaw"]
	st.diffSkill = ""
	st.status.Skills = st.status.Skills[:1]
	st.installOpen = true
	tt.Frame()
	if !tt.HasText(T("kit.preview.upgrade")) {
		t.Fatalf("missing official upgrade tag in %q", tt.Texts())
	}
	if tt.HasText(T("kit.install_backup_warn")) {
		t.Fatalf("official_update should not trigger local overwrite warning")
	}
}

func TestWP5ScrollableFindingsAndScriptPreview(t *testing.T) {
	a, _ := world(t)
	var copied string
	a.copyText = func(s string) error { copied = s; return nil }

	var findings []install.Finding
	for i := 0; i < 12; i++ {
		findings = append(findings, install.Finding{Reason: "uses curl", Line: i + 1, Snippet: "curl https://example.com"})
	}
	script := []byte("#!/bin/sh\n" + strings.Repeat("echo line\n", 40))

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Gap(8).Children(func() {
			a.renderScriptFindings(c, findings)
			a.scriptPreview(c, "wp5-test", script, true)
		})
	}, 800, 600)
	tt.Frame()
	if !tt.HasText(T("install.check_hits_count", "n", 12)) {
		t.Fatalf("missing findings count summary in %q", tt.Texts())
	}
	if err := tt.Click(T("install.copy_script")); err != nil {
		t.Fatal(err)
	}
	if copied != string(script) {
		t.Fatalf("copy script mismatch: got %d bytes, want %d", len(copied), len(script))
	}
}

func TestWP7MultiEntryAndForwardModal(t *testing.T) {
	a, tt := world(t)
	it0 := &a.agents.value.Items[0]
	it0.Instance.Entries = []enroll.EntryState{
		{ID: "dashboard", Port: 9119, Listening: true},
		{ID: "webapp", Port: 9120, Listening: true},
	}
	var openedEntry string
	a.entryOpener = func(_ context.Context, _, _ string, o service.OpenOptions) error {
		openedEntry = o.Entry
		if o.Status != nil {
			o.Status(service.EntryStatus{Step: service.EntryRemote, OK: false, Detail: "not listening"})
		}
		return nil
	}
	a.loopbackListenersFn = func(context.Context, string) ([]int, error) {
		return []int{59803, 9120}, nil
	}
	var forwardedPort int
	a.forwardPageFn = func(ctx context.Context, _ string, ft *service.ForwardTarget, o service.OpenOptions) error {
		forwardedPort = ft.Port
		if o.Status != nil {
			o.Status(service.EntryStatus{Step: service.EntryTunnel, OK: true, Detail: "ok"})
			o.Status(service.EntryStatus{Step: service.EntryRemote, OK: true, Detail: "127.0.0.1"})
			o.Status(service.EntryStatus{Step: service.EntryHealth, OK: true, Detail: "HTTP 200"})
		}
		if o.Ready != nil {
			o.Ready("http://127.0.0.1:59803/", ft.Display)
		}
		return nil
	}
	a.openURL = func(string) error { return nil }

	a.entryChoice[it0.ConnectionID+"/"+it0.Instance.InstanceID] = "webapp"
	a.navigate("/agents/" + it0.ConnectionID + "/" + it0.Instance.InstanceID)
	tt.Advance(time.Second)
	if !tt.HasText("webapp (:9120)") || !tt.HasText(T("forward.open")) {
		t.Fatalf("missing multi-entry selector or forward button in %q", tt.Texts())
	}
	if err := tt.Click(T("entry.open")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if openedEntry != "webapp" {
		t.Fatalf("expected Entry=webapp, got %q", openedEntry)
	}

	// Open loopback forward modal, click detected pill :59803, start forward.
	if err := tt.Click(T("forward.open")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(":59803") {
		t.Fatalf("missing detected port pill :59803 in %q", tt.Texts())
	}
	if err := tt.Click(":59803"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("forward.start")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if forwardedPort != 59803 {
		t.Fatalf("expected forwarded port 59803, got %d", forwardedPort)
	}
	if !tt.HasText(T("entry.step.tunnel")) || !tt.HasText(T("entry.step.health")) {
		t.Fatalf("missing forward steps in %q", tt.Texts())
	}
}

func TestWP7CrossHomeMigrationUI(t *testing.T) {
	a, tt := world(t)
	rp := &service.RevivePlan{
		From:        "box",
		Target:      "box2",
		HomeChanged: true,
		SourceHome:  "/home/old",
		TargetHome:  "/home/new",
		ExternalSymlinks: []service.ExternalSymlinkNote{
			{Root: "state", Path: "skills/ext", Target: "/home/old/shared/ext", UnderSourceHome: true},
		},
		ConfigHomeHits: []service.ConfigHomeHit{
			{Root: "state", Path: "config.yaml", Line: 12, OldPath: "/home/old/bin", NewPath: "/home/new/bin"},
		},
		Plan: revive.Plan{
			Mode:       revive.ModeMigrate,
			InstanceID: "openclaw",
			Profile: profile.Profile{
				ID:       "openclaw",
				AgentKit: &profile.AgentKit{MCPAddArgv: []string{"openclaw", "mcp", "add"}},
			},
		},
	}
	a.prepareRestoreFn = func(context.Context, service.RestoreRequest) (*service.PreparedRestore, error) {
		return &service.PreparedRestore{Revive: rp}, nil
	}
	r := &restoreFlow{
		origin:  "box",
		target:  "box2",
		mode:    revive.ModeMigrate,
		step:    "setup",
		sources: []service.RestoreSource{{Copy: journey.Copy{LocationRef: journey.LocationRef{Kind: "server_vault", ID: "box"}}}},
	}
	a.restore = r
	a.prepareRestore(r)
	if !r.remapHomeSymlinks || !r.carryMCP || r.rewriteConfigHomePaths {
		t.Fatalf("unexpected defaults: remap=%v carryMCP=%v rewrite=%v", r.remapHomeSymlinks, r.carryMCP, r.rewriteConfigHomePaths)
	}
	a.router.Push("/restore")
	tt.Frame()
	for _, want := range []string{
		T("restore.ext_symlinks.warn", "n", 1),
		T("restore.home.remap_symlinks", "old", "/home/old", "new", "/home/new"),
		T("restore.home.carry_mcp"),
		"state/config.yaml:L12: /home/old/bin -> /home/new/bin",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing migration home-path UI %q in %q", want, tt.Texts())
		}
	}

	// Verify postMigrateCard shows home-path counts and MCP auto-wire outcome.
	r.step = "result"
	r.task = service.RestoreTask{
		State: "succeeded", Target: "box2", Instance: "openclaw",
		PostMigrate: &service.PostMigrateReport{
			RemappedSymlinks:        2,
			RewrittenConfigPaths:    3,
			RemainingConfigHomeHits: 1,
			MCPAutoRan:              true,
			MCPWireOutcome:          "wired",
		},
	}
	tt.Frame()
	for _, want := range []string{
		T("restore.post.remapped_symlinks_val", "n", 2),
		T("restore.post.rewritten_paths_val", "n", 3),
		T("restore.post.remaining_home_hits", "n", 1),
		T("restore.post.mcp_auto_wired"),
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing post-migrate summary %q in %q", want, tt.Texts())
		}
	}

	// Separated MCP command vs service restart vs service health outcomes.
	r.task.PostMigrate.MCPWireResult = &service.KitStepOutcome{Attempted: true, OK: true, ExitCode: 0}
	r.task.PostMigrate.MCPServiceRestart = &service.KitServiceRestartOutcome{Attempted: true, OK: true, Healthy: false, Unit: "demo.service", ExitCode: 0}
	tt.Frame()
	for _, want := range []string{
		T("kit.outcome.mcp_ok", "code", 0),
		T("kit.outcome.restart_ok", "unit", "demo.service", "code", 0),
		T("kit.outcome.health_unverified", "unit", "demo.service"),
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing separated post-migrate outcome %q in %q", want, tt.Texts())
		}
	}
}
