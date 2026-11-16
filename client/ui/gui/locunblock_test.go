package gui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

type multiHostRemoveWorld struct {
	a     *App
	tt    *ui.Tester
	s     *service.Service
	homes map[string]string
	refs  map[string]identity.AgentRef
}

func setupMultiHostRemoveWorld(t *testing.T, hosts map[string][]string) *multiHostRemoveWorld {
	t.Helper()
	e := fakesshenv.New(t)
	dir := t.TempDir()
	s := &service.Service{Dirs: config.Dirs{Data: dir, Cache: t.TempDir()}, Config: config.Config{SSHBinary: e.SSH}, Home: dir, Version: "test"}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"demo-a", "demo-b", "demo-c"} {
		write(filepath.Join(dir, "profiles", id+".yaml"), `schema: valise-profile/1
id: `+id+`
display_name: `+id+`
profile_version: 1
status: draft
detect: { binaries: [`+id+`], state_dir_candidates: ["${HOME}/.`+id+`"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: `+id+` } }
`)
	}
	homes := map[string]string{}
	var hostNames []string
	for host, profiles := range hosts {
		hostNames = append(hostNames, host)
		home := e.AddHost(host)
		homes[host] = home
		e.InstallPorter(host)
		write(filepath.Join(home, ".local/bin/systemctl"), "#!/bin/sh\nprintf 'LoadState=not-found\\n'\n")
		write(filepath.Join(home, ".local/bin/loginctl"), "#!/bin/sh\necho yes\n")
		for _, id := range profiles {
			write(filepath.Join(home, "."+id+"/demo.json"), "{}")
			write(filepath.Join(home, ".local/bin/"+id), "#!/bin/sh\necho "+id+" 1.0\n")
		}
	}
	slices.Sort(hostNames)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := s.AddServers(hostNames...); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KeyInit("test-passphrase-1234"); err != nil {
		t.Fatal(err)
	}
	for _, host := range hostNames {
		if _, _, err := s.KeyRegister(ctx, host, "test"); err != nil {
			t.Fatal(err)
		}
		if err := s.ServerNameSet(ctx, host, host); err != nil {
			t.Fatal(err)
		}
		for _, tg := range []string{"nas", "other"} {
			if err := s.TargetSet(ctx, host, vaultrpc.Target{Name: tg, Kind: "rsync", Dest: "sink:/backup/" + tg}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := config.SaveStations(dir, []config.Station{{Name: "nas", Address: "nas.home", User: "valise", Path: "/volume1/valise"}}); err != nil {
		t.Fatal(err)
	}
	a := New(s)
	var updateMu sync.Mutex
	a.run = func(f func()) { f() }
	a.update = func(f func()) { updateMu.Lock(); defer updateMu.Unlock(); f() }
	refs := map[string]identity.AgentRef{}
	for _, host := range hostNames {
		for _, id := range hosts[host] {
			if _, err := s.Enroll(ctx, host, service.EnrollOptions{ProfileID: id, InstanceID: id, Accept: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.refreshAgents()
	for _, it := range a.agents.value.Items {
		if it.AgentRef != nil {
			refs[it.ConnectionID+"/"+it.Instance.InstanceID] = *it.AgentRef
		}
	}
	return &multiHostRemoveWorld{a: a, tt: ui.NewTester(a.View, 1180, 900), s: s, homes: homes, refs: refs}
}

func acceptTestPlan(t *testing.T, w *multiHostRemoveWorld, host, instance string, targets []string) {
	t.Helper()
	ctx := context.Background()
	ref := w.refs[host+"/"+instance]
	on := true
	d := protection.NewDefault()
	d.Schedule.DailyAt, d.Schedule.Timezone = "04:40", "UTC"
	d.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	d.ServerCopies = []protection.ServerCopy{}
	for _, tg := range targets {
		d.ServerCopies = append(d.ServerCopies, protection.ServerCopy{
			TargetRef: protection.LocationRef{Kind: "legacy_target", ID: tg, AuthorityID: ref.ServerUID},
			Enabled:   &on, MinimumInterval: "1h",
		})
	}
	fc, err := w.s.PrepareProtectionChange(ctx, host, protection.PrepareParams{AgentRef: ref, Desired: d})
	if err != nil {
		t.Fatal(err)
	}
	w.a.confirm(fc.PlanID)
	if task, err := w.s.StartProtectionChange(ctx, fc.PlanID, "init-"+host+"-"+instance); err != nil || task.State != "succeeded" {
		t.Fatalf("plan %s/%s: %+v %v", host, instance, task, err)
	}
}

func TestRemoveLocationListsAllBlockersAndUnblocksOneClick(t *testing.T) {
	w := setupMultiHostRemoveWorld(t, map[string][]string{
		"box1": {"demo-a", "demo-b", "demo-c"},
		"box2": {"demo-a"},
	})
	ctx := context.Background()
	if err := w.s.SetAgentLabel(ctx, "box1", "demo-b", "小乙"); err != nil {
		t.Fatal(err)
	}
	w.a.refreshAgents()
	acceptTestPlan(t, w, "box1", "demo-a", []string{"nas"})
	acceptTestPlan(t, w, "box1", "demo-b", []string{"nas", "other"})
	acceptTestPlan(t, w, "box1", "demo-c", []string{"other"})
	acceptTestPlan(t, w, "box2", "demo-a", []string{"nas"})
	w.a.readLocations()

	// Removing station "nas" lists all 3 blocking agents across box1 and box2.
	w.a.router.Push("/locations/nas")
	w.tt.Frame()
	w.tt.Advance(time.Second)
	click(t, w.tt, T("loc.remove_open"))
	hasAll(t, w.tt.Texts(),
		T("loc.remove_title", "name", "nas"),
		T("loc.remove_blocked", "n", 3, "name", "nas"),
		"box1 / demo-a",
		"box1 / 小乙（demo-b）",
		"box2 / demo-a",
		T("loc.remove_manual_hint"),
		T("loc.remove_unblock_go"),
	)
	if w.tt.HasText("demo-c") || slices.Contains(w.tt.Texts(), T("loc.remove_go")) {
		t.Fatalf("unexpected non-blocker or plain remove button: %q", w.tt.Texts())
	}

	// One click unblocks all 3 plans and removes nas from both servers + locally.
	click(t, w.tt, T("loc.remove_unblock_go"))
	if w.a.locs().remove != "" || w.a.locs().removeJ.err != nil || w.a.router.Path() != "/locations" {
		t.Fatalf("after unblock remove: remove=%q err=%v path=%q", w.a.locs().remove, w.a.locs().removeJ.err, w.a.router.Path())
	}
	if len(w.s.Stations()) != 0 {
		t.Fatalf("station still recorded: %+v", w.s.Stations())
	}
	for host, want := range map[string]string{"box1": "other", "box2": ""} {
		b, _ := os.ReadFile(filepath.Join(w.homes[host], ".config/valise/targets.json"))
		if strings.Contains(string(b), `"nas"`) || (want != "" && !strings.Contains(string(b), `"`+want+`"`)) {
			t.Fatalf("%s targets.json: %s", host, b)
		}
	}
	v1B, err := w.s.GetProtection(ctx, "box1", protection.GetParams{AgentRef: w.refs["box1/demo-b"]})
	if err != nil || v1B.ServerPlan == nil || len(v1B.ServerPlan.Desired.ServerCopies) != 1 || v1B.ServerPlan.Desired.ServerCopies[0].TargetRef.ID != "other" {
		t.Fatalf("box1/demo-b plan: %+v err=%v", v1B, err)
	}

	// Same one-click unblock works for an SSH location ("other" used by demo-b and demo-c on box1).
	w.a.router.Push("/locations/other")
	w.tt.Frame()
	w.tt.Advance(time.Second)
	click(t, w.tt, T("loc.remove_open"))
	hasAll(t, w.tt.Texts(),
		T("loc.remove_blocked", "n", 2, "name", "other"),
		"box1 / 小乙（demo-b）",
		"box1 / demo-c",
		T("loc.remove_unblock_go"),
	)
	click(t, w.tt, T("loc.remove_unblock_go"))
	if w.a.locs().remove != "" || w.a.locs().removeJ.err != nil {
		t.Fatalf("ssh unblock remove: remove=%q err=%v", w.a.locs().remove, w.a.locs().removeJ.err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.homes["box1"], ".config/valise/targets.json")); strings.Contains(string(b), `"other"`) {
		t.Fatalf("box1 targets.json still has other: %s", b)
	}
}

func TestRemoveLocationStopsOnPartialFailureWithoutDeletingLocal(t *testing.T) {
	w := setupMultiHostRemoveWorld(t, map[string][]string{
		"box1": {"demo-a"},
		"box2": {"demo-a"},
	})
	ctx := context.Background()
	acceptTestPlan(t, w, "box1", "demo-a", []string{"nas"})
	acceptTestPlan(t, w, "box2", "demo-a", []string{"nas"})
	w.a.readLocations()

	w.a.router.Push("/locations/nas")
	w.tt.Frame()
	w.tt.Advance(time.Second)
	click(t, w.tt, T("loc.remove_open"))
	hasAll(t, w.tt.Texts(), "box1 / demo-a", "box2 / demo-a", T("loc.remove_unblock_go"))

	// Bump box2's plan revision behind the dialog's back so box2 fails with revision_conflict.
	v2, err := w.s.GetProtection(ctx, "box2", protection.GetParams{AgentRef: w.refs["box2/demo-a"]})
	if err != nil || v2.ServerPlan == nil {
		t.Fatalf("get box2 plan: %+v %v", v2, err)
	}
	bumped := v2.ServerPlan.Desired
	bumped.Schedule.DailyAt = "05:15"
	fc, err := w.s.PrepareProtectionChange(ctx, "box2", protection.PrepareParams{
		AgentRef: w.refs["box2/demo-a"], ExpectedRevision: v2.ServerPlan.Revision, Desired: bumped,
	})
	if err != nil {
		t.Fatal(err)
	}
	w.a.confirm(fc.PlanID)
	if task, err := w.s.StartProtectionChange(ctx, fc.PlanID, "bump-box2"); err != nil || task.State != "succeeded" {
		t.Fatalf("bump box2: %+v %v", task, err)
	}

	click(t, w.tt, T("loc.remove_unblock_go"))
	if w.a.locs().remove != "nas" || w.a.locs().removeJ.err == nil {
		t.Fatalf("expected failure keeping dialog open, got remove=%q err=%v", w.a.locs().remove, w.a.locs().removeJ.err)
	}
	if !strings.Contains(w.a.locs().removeJ.err.Error(), "box2 / demo-a") {
		t.Fatalf("error should identify box2 / demo-a: %v", w.a.locs().removeJ.err)
	}
	if len(w.s.Stations()) != 1 || w.s.Stations()[0].Name != "nas" {
		t.Fatalf("station must not be deleted on failure: %+v", w.s.Stations())
	}
	for _, host := range []string{"box1", "box2"} {
		if b, _ := os.ReadFile(filepath.Join(w.homes[host], ".config/valise/targets.json")); !strings.Contains(string(b), `"nas"`) {
			t.Fatalf("%s targets.json deleted prematurely: %s", host, b)
		}
	}
	if rem := w.a.locs().removeScan.Blockers; len(rem) != 1 || rem[0].Host != "box2" {
		t.Fatalf("remaining blockers should only have box2, got %+v", rem)
	}
}
