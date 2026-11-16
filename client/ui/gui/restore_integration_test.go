package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

func restoreIntegrationWorld(t *testing.T, extraProfile string) (string, *service.Service, journey.TimelinePoint) {
	t.Helper()
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
`+extraProfile)
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
	if err := svc.BackUpKeyLater(); err != nil {
		t.Fatal(err)
	}
	timeline, err := svc.ListTimeline(ctx, service.JourneyListRequest{AllLocations: true, Limit: 200})
	if err != nil || len(timeline.Items) != 1 {
		t.Fatalf("timeline %+v %v", timeline, err)
	}
	return home, svc, timeline.Items[0]
}

func runConfirmedRestore(t *testing.T, a *App, r *restoreFlow, beforeConfirm func()) {
	t.Helper()
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
	if beforeConfirm != nil {
		beforeConfirm()
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
}

func TestRestoreFlowWaitsBeforeChangingLiveDirectory(t *testing.T) {
	home, svc, pt := restoreIntegrationWorld(t, "")
	a := New(svc)
	a.run = func(f func()) { f() }
	a.openRestore(pt, "box")
	r := a.restore
	r.draft = true
	a.prepareRestore(r)
	if r.j.err != nil || r.plan == nil {
		t.Fatal(r.j.err)
	}
	r.sourceChoice = "proceed"
	liveFile := filepath.Join(home, ".demo/demo.json")
	os.WriteFile(liveFile, []byte("live"), 0o700)
	runConfirmedRestore(t, a, r, func() {
		b, err := os.ReadFile(liveFile)
		if err != nil || string(b) != "live" {
			t.Fatalf("live directory changed before second decision: %q %v", b, err)
		}
	})
	b, err := os.ReadFile(liveFile)
	if err != nil || string(b) != "before" {
		t.Fatalf("not restored %q %v", b, err)
	}
}

// Same-server clone end-to-end in GUI over fake-ssh (Stage A completion):
// taken id/directory is blocked without a confirm button; valid demo-copy2
// plans without "会先停止服务" and restores into its own directory while
// leaving the original instance intact and not superseded.
func TestSameServerCloneFlowCompletesWithoutStoppingService(t *testing.T) {
	home, svc, pt := restoreIntegrationWorld(t, "relocation: { env: DEMO_HOME }\n")
	a := New(svc)
	a.run = func(f func()) { f() }
	tt := ui.NewTester(a.View, 1180, 900)
	a.openRestore(pt, "box")
	r := a.restore
	r.mode = revive.ModeClone
	r.draft = true

	// Same id as original is refused outright; new id with original directory
	// produces conflict blockers without an execute button.
	r.id = "demo"
	r.roots = "state=" + filepath.Join(home, ".demo")
	a.prepareRestore(r)
	if r.j.err == nil || r.plan != nil || !strings.Contains(r.j.err.Error(), "demo") {
		t.Fatalf("expected copy_same_id error, got err=%v plan=%+v", r.j.err, r.plan)
	}
	r.id = "demo-copy2"
	a.prepareRestore(r)
	if r.j.err != nil || r.plan == nil || len(r.plan.Revive.Blockers) == 0 {
		t.Fatalf("expected conflict blockers, got err=%v plan=%+v", r.j.err, r.plan)
	}
	tt.Frame()
	if tt.HasText(T("restore.prepare")) {
		t.Fatalf("conflict plan offered execute button: %q", tt.Texts())
	}

	// New id demo-copy2 and clean directory plan cleanly without stopping any service.
	r.step = "setup"
	r.id = "demo-copy2"
	copyDir := filepath.Join(home, ".demo-copy2")
	r.roots = "state=" + copyDir
	a.prepareRestore(r)
	if r.j.err != nil || r.plan == nil || len(r.plan.Revive.Blockers) != 0 {
		t.Fatalf("expected clean clone plan, got err=%v plan=%+v", r.j.err, r.plan)
	}
	warnings := strings.Join(r.plan.Revive.Warnings, "\n")
	if strings.Contains(warnings, "会先停止服务") || !strings.Contains(warnings, "不启动、不停止任何服务") {
		t.Fatalf("unexpected warnings for no-service clone: %q", warnings)
	}
	tt.Frame()
	if !tt.HasText(T("restore.prepare")) {
		t.Fatalf("clean plan did not offer execute button: %q", tt.Texts())
	}

	r.sourceChoice = "proceed"
	liveFile := filepath.Join(home, ".demo/demo.json")
	os.WriteFile(liveFile, []byte("live"), 0o700)
	runConfirmedRestore(t, a, r, nil)

	if b, err := os.ReadFile(filepath.Join(copyDir, "demo.json")); err != nil || string(b) != "before" {
		t.Fatalf("clone dir not restored: %q %v", b, err)
	}
	if b, err := os.ReadFile(liveFile); err != nil || string(b) != "live" {
		t.Fatalf("original live dir altered: %q %v", b, err)
	}
	list, err := svc.Instances(context.Background(), "box")
	if err != nil || len(list) != 2 {
		t.Fatalf("instances %+v %v", list, err)
	}
	roots := map[string]string{}
	for _, inst := range list {
		if inst.Superseded != nil {
			t.Fatalf("instance %s marked superseded: %+v", inst.InstanceID, inst.Superseded)
		}
		roots[inst.InstanceID] = inst.Roots["state"]
	}
	if roots["demo"] != filepath.Join(home, ".demo") || roots["demo-copy2"] != copyDir {
		t.Fatalf("unexpected instance roots: %+v", roots)
	}
}
