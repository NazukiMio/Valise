package gui

import (
	"context"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/environment"
	"valise/core/revive"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

func TestRestoreFlowWaitsBeforeChangingLiveDirectory(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
	dir := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".demo/demo.json"), "before")
	write(filepath.Join(home, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")
	write(filepath.Join(dir, "profiles/demo.yaml"), `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: demo } }
`)
	svc := &service.Service{Dirs: config.Dirs{Data: dir, Cache: t.TempDir()}, Config: config.Config{SSHBinary: e.SSH}, Home: dir}
	ctx := context.Background()
	if _, err := svc.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.KeyInit("restore-test-passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enroll(ctx, "box", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.KeyRegister(ctx, "box", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: "soul"}); err != nil {
		t.Fatal(err)
	}
	svc.OwnerAuth.Passphrase = func(string) (string, error) { return "restore-test-passphrase", nil }
	settings, err := svc.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != environment.Parallel || settings.Managed {
		t.Fatal("new Agent not default parallel", settings)
	}
	if err := svc.BackUpKeyLater(); err != nil {
		t.Fatal(err)
	}
	settingsApp := New(svc)
	settingsApp.run = func(f func()) { f() }
	ps := &protState{ref: &settings.Agent, restoreMode: &restoreModeState{view: &settings, draft: settings.Mode, started: true}}
	modeTester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(800).Padding(16).Children(func() { settingsApp.restoreModeCard(c, &agentRow{Server: "box", Instance: "demo"}, ps) })
	}, 860, 560)
	modeTester.Frame()
	modeTester.Advance(time.Second)
	click(t, modeTester, T("environment.edit"))
	click(t, modeTester, T("environment.mode.direct"))
	beforeSave, err := svc.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil || beforeSave.Mode != environment.Parallel {
		t.Fatal("radio saved before confirmation", beforeSave, err)
	}
	click(t, modeTester, T("environment.save"))
	if ps.restoreMode.j.err != nil || !modeTester.HasText(T("environment.saved")) {
		t.Fatal("GUI save failed", ps.restoreMode.j.err, modeTester.Texts())
	}
	settings, err = svc.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil || settings.Mode != environment.Direct {
		t.Fatal("GUI did not persist server policy", settings, err)
	}
	timeline, err := svc.ListTimeline(ctx, service.JourneyListRequest{AllLocations: true, Limit: 200})
	if err != nil || len(timeline.Items) != 1 {
		t.Fatalf("timeline %+v %v", timeline, err)
	}
	a := New(svc)
	a.run = func(f func()) { f() }
	a.openRestore(timeline.Items[0], "box")
	r := a.restore
	r.draft = true
	a.prepareRestore(r)
	if r.j.err != nil || r.plan == nil {
		t.Fatal(r.j.err)
	}
	r.sourceChoice = "proceed"
	write(filepath.Join(home, ".demo/demo.json"), "live")
	var mu sync.Mutex
	confirmed := make(chan struct{}, 1)
	finished := make(chan struct{}, 1)
	a.update = func(f func()) {
		mu.Lock()
		defer mu.Unlock()
		f()
		if r.step == "confirm" {
			select {
			case confirmed <- struct{}{}:
			default:
			}
		}
		if r.step == "result" || r.step == "reconcile" {
			select {
			case finished <- struct{}{}:
			default:
			}
		}
	}
	a.run = func(f func()) { go f() }
	a.runRestore(r)
	select {
	case <-confirmed:
	case <-time.After(20 * time.Second):
		t.Fatal("no second confirmation")
	}
	b, err := os.ReadFile(filepath.Join(home, ".demo/demo.json"))
	if err != nil || string(b) != "live" {
		t.Fatalf("live directory changed before second decision: %q %v", b, err)
	}
	mu.Lock()
	reply, deadline := r.reply, r.deadline
	mu.Unlock()
	reply <- restoreSwapReply{approve: true, deadline: deadline}
	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("restore did not settle")
	}
	mu.Lock()
	result, runErr := r.task, r.j.err
	mu.Unlock()
	if runErr != nil || result.SwapState != revive.StateRestored || result.State != "succeeded" {
		t.Fatalf("result %+v %v", result, runErr)
	}
	b, err = os.ReadFile(filepath.Join(home, ".demo/demo.json"))
	if err != nil || string(b) != "before" {
		t.Fatalf("not restored %q %v", b, err)
	}
}
