package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/vault"
	"valise/ui/service"
)

func TestScheduleTextAndBuildScheduleValidation(t *testing.T) {
	on, off := true, false
	catch := true
	if got := scheduleText(protection.Schedule{Enabled: &off}); got != "关闭（仅手动）" {
		t.Fatalf("off = %q", got)
	}
	if got := scheduleText(protection.Schedule{Enabled: &on, Cadence: "hourly", EveryHours: 6, Timezone: "UTC"}); got != "每 6 小时（UTC）" {
		t.Fatalf("hourly = %q", got)
	}
	if got := scheduleText(protection.Schedule{Enabled: &on, DailyAt: "04:40", Timezone: "Asia/Shanghai", CatchUp: &catch}); !strings.Contains(got, "每天 04:40（Asia/Shanghai）") || !strings.Contains(got, T("protect.catch_up")) {
		t.Fatalf("daily = %q", got)
	}
	if got := scheduleText(protection.Schedule{Enabled: &on, Cadence: "weekly", Weekdays: []string{"Wed", "Mon"}, DailyAt: "03:15", Timezone: "UTC"}); got != "每周周一、周三 03:15（UTC）" {
		t.Fatalf("weekly = %q", got)
	}
	if got := scheduleText(protection.Schedule{Enabled: &on, Cadence: "monthly", DayOfMonth: 15, DailyAt: "02:00", Timezone: "UTC"}); got != "每月 15 日 02:00（UTC）" {
		t.Fatalf("monthly = %q", got)
	}

	// Bug A: cadence "off" keeps valid DailyAt and Timezone and validates cleanly.
	offEdit := newSchedEdit(protection.NewDefault())
	s, ok := buildScheduleFromEdit(offEdit, protection.Schedule{})
	if !ok || s.Enabled == nil || *s.Enabled || s.DailyAt != "04:40" || s.Timezone == "" {
		t.Fatalf("off build = %+v ok=%v", s, ok)
	}
	d := protection.NewDefault()
	d.Schedule = s
	d.Period = protection.Period{Unit: "month", Timezone: s.Timezone}
	if err := d.Validate(); err != nil {
		t.Fatalf("off desired invalid: %v", err)
	}

	// Invalid weekly (no weekdays) or monthly (day 30) is rejected.
	if _, ok := buildScheduleFromEdit(&schedEdit{cadence: "weekly", at: "04:40", tz: "UTC", weekdays: map[string]bool{}}, protection.Schedule{}); ok {
		t.Fatal("accepted weekly with no weekdays")
	}
	if _, ok := buildScheduleFromEdit(&schedEdit{cadence: "monthly", dayOfMonth: "30", at: "04:40", tz: "UTC"}, protection.Schedule{}); ok {
		t.Fatal("accepted monthly day 30")
	}

	// Hourly schedule with empty DailyAt preserves "" through ensureScheduleDefaults.
	hourlySched, ok := buildScheduleFromEdit(&schedEdit{cadence: "hourly", everyHours: "4", at: "", tz: "UTC"}, protection.Schedule{})
	if !ok || hourlySched.DailyAt != "" {
		t.Fatalf("hourly build = %+v ok=%v", hourlySched, ok)
	}
	hourlyDesired := ensureScheduleDefaults(protection.Desired{Schedule: hourlySched})
	if hourlyDesired.Schedule.DailyAt != "" {
		t.Fatalf("ensureScheduleDefaults overwrote hourly DailyAt: %q", hourlyDesired.Schedule.DailyAt)
	}
}

func TestScheduleDialogAndCopiesTextAndLocalBindingSave(t *testing.T) {
	a, tt := locWorld(t)
	ref := identity.AgentRef{ServerUID: "u", InstallID: "openclaw", InstanceID: "xiaoman", Generation: "g"}
	a.agents.value.Items[0].IdentityState, a.agents.value.Items[0].AgentRef = identity.Bound, &ref

	on, off := true, false
	ps := &protState{
		ref:                 &ref,
		savepointsSupported: true,
		view: &protection.View{
			ServerPlan: &protection.ServerPlan{
				Revision: 1,
				Desired: protection.Desired{
					Scope:    protection.Scope{Kind: "preset", Tier: "full"},
					Schedule: protection.Schedule{Enabled: &on, DailyAt: "04:40", Timezone: "UTC"},
					Period:   protection.Period{Unit: "month", Timezone: "UTC", KeepAutoPeriods: 2},
					ServerCopies: []protection.ServerCopy{
						{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: "u"}, Enabled: &on, MinimumInterval: "24h"},
					},
				},
			},
		},
		localCopies: &service.LocalCopiesView{
			Planned: true,
			Repos:   []protection.LocalCopy{{RepoID: "usb", AfterBackup: &on, OnClientOpen: &off}},
			Clouds:  []protection.LocalCopy{{RepoID: "gdrive", AfterBackup: &on, OnClientOpen: &on}},
		},
	}
	gotCopies := a.copiesText(ps)
	for _, want := range []string{"服务器直传：nas", "本机：usb", "云盘：gdrive"} {
		if !strings.Contains(gotCopies, want) {
			t.Fatalf("copiesText = %q, missing %q", gotCopies, want)
		}
	}

	ps.sched = newSchedEdit(ps.desired())
	a.prots["home/xiaoman"] = ps
	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)

	hasAll(t, tt.Texts(), T("protect.sched.section_freq"), T("protect.sched.section_period"),
		"0 = 永久保留；设为 2 表示除当前自然月外保留过去 2 个已结束自然月的自动快照，手动存档点永远不受影响")
	click(t, tt, T("protect.period.week"))
	hasAll(t, tt.Texts(), "0 = 永久保留；设为 2 表示除当前自然周外保留过去 2 个已结束自然周的自动快照，手动存档点永远不受影响")
	ps.sched = nil
	tt.Frame()

	// Unplanned copyTo offers inline default plan enablement and renders clouds.
	ps.copyTo = &copyEdit{
		server: map[string]*serverCopyEdit{},
		local:  &service.LocalCopiesView{Planned: false},
		repos:  map[string]*[2]bool{"usb": {true, true}},
		clouds: map[string]*[2]bool{"gdrive": {true, true}},
	}
	tt.Frame()
	hasAll(t, tt.Texts(), T("copyto.enable_default_plan"))

	ps.copyTo.local = &service.LocalCopiesView{Planned: true}
	tt.Frame()
	hasAll(t, tt.Texts(), "usb", "gdrive", T("loc.kind.cloud"))
}

func TestSaveCopyToPersistsInitialBindingForReposAndClouds(t *testing.T) {
	w := newNativeFlowWorld(t)
	a, s := w.a, w.a.svc
	a.run = func(f func()) { f() }
	a.update = func(f func()) { f() }
	s.OwnerAuth.Passphrase = func(string) (string, error) { return w.pass, nil }
	for name, body := range map[string]string{
		"systemctl": "#!/bin/sh\nprintf 'LoadState=not-found\\n'\n",
		"loginctl":  "#!/bin/sh\necho yes\n",
	} {
		p := filepath.Join(w.home, ".local/bin", name)
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	repoDir := filepath.Join(t.TempDir(), "usb")
	s.Config.Repos = []config.Repo{{Name: "usb", Path: repoDir}}
	s.Config.Clouds = []config.Cloud{{Name: "gdrive", Cloud: "google-drive"}}
	ctx := context.Background()

	// Adopt and enable a protection plan so LocalCopies is Planned with nil Binding.
	list, err := s.ListAgents(ctx, service.AgentListRequest{ConnectionIDs: []string{"vps"}})
	if err != nil || len(list.Items) == 0 || list.Items[0].AgentRef == nil {
		t.Fatalf("list agents: %v %+v", err, list)
	}
	ref := *list.Items[0].AgentRef
	fc, err := s.PrepareProtectionChange(ctx, "vps", protection.PrepareParams{
		AgentRef: ref,
		Desired:  defaultProtectionDesired(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	a.confirm(fc.PlanID)
	task, err := s.StartProtectionChange(ctx, fc.PlanID, "init-plan")
	if err != nil || task.State != "succeeded" {
		t.Fatalf("start plan: %+v %v", task, err)
	}
	lc, err := s.LocalCopies(ctx, "vps", "demo")
	if err != nil || !lc.Planned || lc.Binding != nil {
		t.Fatalf("initial local copies = %+v err=%v", lc, err)
	}

	ps := &protState{ref: &ref, view: &protection.View{ServerPlan: &protection.ServerPlan{Revision: 1, Desired: defaultProtectionDesired(false)}}}
	ps.copyTo = &copyEdit{
		server: map[string]*serverCopyEdit{},
		local:  lc,
		repos:  map[string]*[2]bool{"usb": {true, true}},
		clouds: map[string]*[2]bool{"gdrive": {true, true}},
	}
	a.saveCopyTo(&agentRow{Server: "vps", Instance: "demo"}, ps)
	if ps.copyTo != nil {
		t.Fatalf("copyTo did not close after save: err=%v", ps.copyTo.saveJ.err)
	}
	after, err := s.LocalCopies(ctx, "vps", "demo")
	if err != nil || after.Binding == nil || len(after.Binding.LocalCopies) != 1 || len(after.Binding.CloudCopies) != 1 {
		t.Fatalf("binding not persisted for both repo and cloud: %+v err=%v", after, err)
	}
	_ = vault.TierFull
}

func TestAdoptPlanCardVisibleWhenWritten(t *testing.T) {
	a, tt := world(t)
	sc := &serverScan{
		found: []service.DetectedInstance{{ID: "demo", ProfileID: "openclaw", StateDir: "/home/me/.openclaw"}},
		adopts: map[string]*adoption{
			"demo": {written: true},
		},
	}
	a.scans["home"] = sc
	a.router.Push("/servers/home/agents")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("adopt.plan_title"), T("adopt.plan_daily_ask"), T("adopt.plan_enable"), T("adopt.view_agent"))
}
