package gui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/ui/service"
)

func TestAgentForgetStateMissingBannerManageAndCleanup(t *testing.T) {
	a, _ := world(t)
	tt := ui.NewTester(a.View, 1180, 1400)
	a.agents.value.Items[0].Instance.MissingRoots = []string{profile.StateRoot}

	// Home page & agent detail verdict show missing-directory warning.
	tt.Frame()
	hasAll(t, tt.Texts(), T("agent.state_missing.short"))

	var forgot []string
	a.forgetInstanceFn = func(_ context.Context, host, inst string) (*enroll.InstanceForgetResult, error) {
		forgot = append(forgot, host+"/"+inst)
		return &enroll.InstanceForgetResult{InstanceID: inst}, nil
	}

	// Server settings cleanup lists missing instances with forget action.
	a.cleanupHook = &cleanupOps{
		prevList:   func(context.Context, string) (*revivePrevList, error) { return nil, nil },
		oldList:    func(context.Context, string) (*reviveOldList, error) { return nil, nil },
		prevDrop:   func(context.Context, string, string) ([]string, error) { return nil, nil },
		oldCleanup: func(context.Context, string, string) ([]string, error) { return nil, nil },
	}
	a.serverTab = 1
	a.router.Push("/servers/home")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("agent.forget.cleanup_title"), T("agent.forget.cleanup_btn"))

	// On-server foundCard shows "移出旧记录…" for enrolled instances.
	a.scans["home"] = &serverScan{
		found: []service.DetectedInstance{{
			Candidate: enroll.Candidate{ProfileID: "openclaw", StateDir: "/home/me/.openclaw"},
			ID:        "xiaoman",
			Enrolled:  "xiaoman",
		}},
		adopts: map[string]*adoption{},
	}
	a.router.Push("/servers/home/agents")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("agent.forget.stale_record"))

	// Agent detail page shows danger banner & manage row; confirming forget clears caches & navigates to /agents.
	a.prots["home/xiaoman"] = &protState{}
	a.agentServices["home/xiaoman"] = &agentServiceState{started: true}
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("agent.verdict.state_missing"), T("agent.forget.missing_banner"), T("agent.forget.open"))

	click(t, tt, T("agent.forget.open"))
	hasAll(t, tt.Texts(), T("agent.forget.title", "name", "小满"), T("agent.forget.explain"), T("need.valise"))
	click(t, tt, T("agent.forget.confirm"))
	tt.Advance(time.Second)

	if len(forgot) != 1 || forgot[0] != "home/xiaoman" {
		t.Fatalf("forgot = %v", forgot)
	}
	if a.router.Path() != "/agents" {
		t.Fatalf("path after forget = %q, want /agents", a.router.Path())
	}
	if _, ok := a.prots["home/xiaoman"]; ok {
		t.Fatal("prots cache not cleared by forgetInstance")
	}
	if _, ok := a.agentServices["home/xiaoman"]; ok {
		t.Fatal("agentServices cache not cleared by forgetInstance")
	}
}

func TestAgentServiceControlInstallAndQuickActions(t *testing.T) {
	a, _ := world(t)
	var mu sync.Mutex
	a.run = func(f func()) { go f() }
	a.update = func(f func()) {
		mu.Lock()
		defer mu.Unlock()
		f()
	}
	tt := ui.NewTester(a.View, 1180, 1400)
	step := func(f func()) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		f()
		tt.Frame()
		tt.Advance(time.Second)
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var ok bool
			step(func() { ok = cond() })
			if ok {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	var svcMu sync.Mutex
	svcInfo := &service.InstanceServiceInfo{
		Instance:    "alan",
		ProfileID:   "hermes",
		Unit:        "hermes.service",
		LoadState:   "not-found",
		HasUnit:     false,
		InstallArgv: []string{"hermes", "gateway", "install"},
	}
	var controls []string
	var ranActions []string
	a.agentServiceHook = &agentServiceOps{
		status: func(context.Context, string, string) (*service.InstanceServiceInfo, error) {
			svcMu.Lock()
			defer svcMu.Unlock()
			cp := *svcInfo
			return &cp, nil
		},
		control: func(_ context.Context, _, _, act string) (*service.InstanceServiceInfo, error) {
			svcMu.Lock()
			defer svcMu.Unlock()
			controls = append(controls, act)
			svcInfo.Active = "active"
			if act == "stop" {
				svcInfo.Active = "inactive"
			}
			cp := *svcInfo
			return &cp, nil
		},
		install: func(_ context.Context, _, _ string, tio service.InstallIO) (int, *service.InstanceServiceInfo, error) {
			_, _ = tio.Stdout.Write([]byte("service installed\n"))
			svcMu.Lock()
			defer svcMu.Unlock()
			svcInfo.LoadState = "loaded"
			svcInfo.HasUnit = true
			svcInfo.Active = "inactive"
			cp := *svcInfo
			return 0, &cp, nil
		},
		actions: func(context.Context, string, string) (*service.ActionList, error) {
			return &service.ActionList{
				Instance:  "alan",
				ProfileID: "hermes",
				Actions: []profile.Action{
					{ID: "status", Name: "查看网关状态", Explain: "查看运行状态", Risk: profile.RiskRead, Argv: []string{"hermes", "gateway", "status"}, Output: profile.OutputText},
					{ID: "secret", Name: "秘密动作", Risk: profile.RiskRead, Output: profile.OutputSecret},
				},
			}, nil
		},
		runAct: func(_ context.Context, _, _, id string, _ service.ActionOptions) (*service.ActionResult, error) {
			svcMu.Lock()
			ranActions = append(ranActions, id)
			svcMu.Unlock()
			return &service.ActionResult{
				RunResult: profileActionRun(0, "gateway ok"),
				Checked:   true,
				Service:   []enroll.ServiceState{{Unit: "hermes.service", LoadState: "loaded", Active: "active"}},
			}, nil
		},
		access: func(_ string, _ profile.Action) string { return "日常钥匙即可" },
	}

	step(func() { a.router.Push("/agents/cloud/alan") })
	waitFor("service status loaded", func() bool {
		st := a.agentServices["cloud/alan"]
		return st != nil && !st.read.busy && st.info != nil && st.actions != nil
	})
	mu.Lock()
	texts := tt.Texts()
	hasSecret := tt.HasText("秘密动作")
	mu.Unlock()
	hasAll(t, texts, T("agent.service.title"), T("agent.service.not_installed"), T("agent.service.install"), "查看网关状态")
	if hasSecret {
		t.Fatal("secret-output action should not be shown as quick action")
	}

	// Install service: shows exit code and live status separately.
	step(func() { click(t, tt, T("agent.service.install")) })
	mu.Lock()
	texts = tt.Texts()
	mu.Unlock()
	hasAll(t, texts, "hermes gateway install", T("need.admin"))
	step(func() { click(t, tt, T("agent.service.install_go")) })
	waitFor("service install done", func() bool {
		st := a.agentServices["cloud/alan"]
		return st != nil && st.installRun != nil && !st.installRun.running
	})
	mu.Lock()
	texts = tt.Texts()
	mu.Unlock()
	hasAll(t, texts, T("agent.service.install_exited", "code", 0), T("service.stopped"))
	step(func() { click(t, tt, T("common.close")) })

	// Now unit is loaded & stopped: all three buttons visible; start -> active -> restart.
	mu.Lock()
	texts = tt.Texts()
	mu.Unlock()
	hasAll(t, texts, T("agent.service.start"), T("agent.service.restart"), T("agent.service.stop"))
	step(func() { click(t, tt, T("agent.service.start")) })
	waitFor("service start done", func() bool {
		st := a.agentServices["cloud/alan"]
		return st != nil && !st.ctrlJ.busy && st.info != nil && st.info.Active == "active"
	})
	svcMu.Lock()
	gotControls := append([]string(nil), controls...)
	svcMu.Unlock()
	if len(gotControls) != 1 || gotControls[0] != "start" {
		t.Fatalf("controls = %v", gotControls)
	}
	mu.Lock()
	texts = tt.Texts()
	mu.Unlock()
	// While active, start stays visible (disabled) alongside restart and stop.
	hasAll(t, texts, T("service.running"), T("agent.service.start"), T("agent.service.restart"), T("agent.service.stop"))
	step(func() { _ = tt.Click(T("agent.service.start")) })
	svcMu.Lock()
	gotControls = append([]string(nil), controls...)
	svcMu.Unlock()
	if len(gotControls) != 1 {
		t.Fatalf("disabled start triggered control: %v", gotControls)
	}
	step(func() { click(t, tt, T("agent.service.restart")) })
	waitFor("service restart done", func() bool {
		st := a.agentServices["cloud/alan"]
		return st != nil && !st.ctrlJ.busy
	})
	svcMu.Lock()
	gotControls = append([]string(nil), controls...)
	svcMu.Unlock()
	if len(gotControls) != 2 || gotControls[1] != "restart" {
		t.Fatalf("controls = %v", gotControls)
	}

	// Quick action modal shows command and selectable output.
	step(func() { click(t, tt, "查看网关状态") })
	mu.Lock()
	texts = tt.Texts()
	mu.Unlock()
	hasAll(t, texts, "hermes gateway status", T("health.run"))
	step(func() { click(t, tt, T("health.run")) })
	waitFor("quick action done", func() bool {
		st := a.agentServices["cloud/alan"]
		return st != nil && st.actionAsk != nil && !st.actionAsk.running && st.actionAsk.res != nil
	})
	svcMu.Lock()
	gotRan := append([]string(nil), ranActions...)
	svcMu.Unlock()
	if len(gotRan) != 1 || gotRan[0] != "status" {
		t.Fatalf("ranActions = %v", gotRan)
	}
	mu.Lock()
	texts = tt.Texts()
	mu.Unlock()
	hasAll(t, texts, T("health.exited", "code", 0), "gateway ok")
	step(func() { click(t, tt, T("common.close")) })

	// forgetServer clears all per-server maps.
	a.scans["cloud"] = &serverScan{}
	a.installs["cloud/hermes"] = &installRun{}
	a.customInstalls["cloud"] = &customInstallRun{}
	a.prots["cloud/alan"] = &protState{}
	a.natives["cloud/alan"] = &nativeState{}
	a.agentKits["cloud/alan"] = &agentKitState{}
	a.health["cloud/alan"] = &healthState{}
	a.proposals["cloud/alan"] = &proposalsState{}
	a.entries["cloud/alan"] = &entryState{}
	a.settings["cloud"] = &settingsState{}
	a.passes["cloud"] = &passState{}
	a.forgetServer("cloud")
	if len(a.scans) > 0 && a.scans["cloud"] != nil ||
		a.installs["cloud/hermes"] != nil ||
		a.customInstalls["cloud"] != nil ||
		a.prots["cloud/alan"] != nil ||
		a.natives["cloud/alan"] != nil ||
		a.agentKits["cloud/alan"] != nil ||
		a.health["cloud/alan"] != nil ||
		a.proposals["cloud/alan"] != nil ||
		a.entries["cloud/alan"] != nil ||
		a.agentServices["cloud/alan"] != nil ||
		a.settings["cloud"] != nil ||
		a.passes["cloud"] != nil {
		t.Fatal("forgetServer left cached per-server state behind")
	}
	_ = strings.Contains
}

func TestAgentServiceRecoveryNotices(t *testing.T) {
	a, _ := world(t)
	a.run = func(f func()) { f() }
	tt := ui.NewTester(a.View, 1180, 1400)

	// Case 1: !HasUnit && LocalOverrideStale shows warning + restore official + settings link.
	statusCalls := 0
	info := &service.InstanceServiceInfo{
		Instance:           "alan",
		ProfileID:          "demo",
		Unit:               "demo.service",
		HasUnit:            false,
		LocalOverrideStale: true,
		EffectiveVersion:   2,
		OfficialVersion:    3,
	}
	a.agentServiceHook = &agentServiceOps{
		status: func(context.Context, string, string) (*service.InstanceServiceInfo, error) {
			statusCalls++
			cp := *info
			return &cp, nil
		},
		actions: func(context.Context, string, string) (*service.ActionList, error) {
			return &service.ActionList{Instance: "alan", ProfileID: "demo"}, nil
		},
	}
	a.router.Push("/agents/cloud/alan")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(),
		T("agent.service.local_override_stale", "id", "demo", "local", 2, "official", 3),
		T("profiles.restore_official"),
		T("agent.service.go_profiles"),
	)
	click(t, tt, T("agent.service.go_profiles"))
	if a.router.Path() != "/settings" || a.setTab != 3 {
		t.Fatalf("go_profiles routed to path=%q setTab=%d", a.router.Path(), a.setTab)
	}

	// Case 2: !HasUnit && len(InstallArgv) == 0 && !LocalOverrideStale shows no_install_cmd + go_manage.
	info.LocalOverrideStale = false
	delete(a.agentServices, "cloud/alan")
	a.agentTab = tabOverview
	a.router.Push("/agents/cloud/alan")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(),
		T("agent.service.no_install_cmd", "unit", "demo.service"),
		T("agent.service.go_manage"),
	)
	click(t, tt, T("agent.service.go_manage"))
	if a.agentTab != tabManage {
		t.Fatalf("go_manage set agentTab=%d, want %d", a.agentTab, tabManage)
	}
	if statusCalls < 2 {
		t.Fatalf("expected status reloads, got %d", statusCalls)
	}
}
