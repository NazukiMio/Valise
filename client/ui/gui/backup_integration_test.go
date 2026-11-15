package gui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/protection"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

// Exercise the actual GUI follower over fake-ssh, with a synchronous porter
// call held inside copying. Querying and reconciling must not seal again.
func TestBackupFollowerQueriesAttachedRunWithoutReplay(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
	dir := t.TempDir()
	s := &service.Service{Dirs: config.Dirs{Data: dir, Cache: t.TempDir()}, Config: config.Config{SSHBinary: e.SSH}, Home: dir}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".demo/demo.json"), "{}")
	write(filepath.Join(home, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")
	write(filepath.Join(home, ".local/bin/systemctl"), "#!/bin/sh\nprintf 'LoadState=not-found\\n'\n")
	write(filepath.Join(home, ".local/bin/loginctl"), "#!/bin/sh\necho yes\n")
	write(filepath.Join(home, ".local/bin/rsync"), `#!/bin/sh
while ! test -e "$HOME/release-copy"; do sleep 0.05; done
echo 'Connection timed out' >&2
exit 255
`)
	write(filepath.Join(dir, "profiles/demo.yaml"), `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: demo } }
`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := s.Enroll(ctx, "box", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	agents, err := s.ListAgents(ctx, service.AgentListRequest{ConnectionIDs: []string{"box"}})
	if err != nil || len(agents.Items) != 1 || agents.Items[0].AgentRef == nil {
		t.Fatalf("agents %+v %v", agents, err)
	}
	ref := *agents.Items[0].AgentRef
	if _, err = s.KeyInit("test-passphrase-1234"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.KeyRegister(ctx, "box", "test"); err != nil {
		t.Fatal(err)
	}
	if err = s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	if err = s.TargetSet(ctx, "box", vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "sink:/backup"}); err != nil {
		t.Fatal(err)
	}
	a := New(s)
	d := protection.NewDefault()
	d.Schedule.DailyAt, d.Schedule.Timezone = "04:40", "UTC"
	d.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	on := true
	d.ServerCopies = []protection.ServerCopy{{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: ref.ServerUID}, Enabled: &on, MinimumInterval: "1h"}}
	change, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{AgentRef: ref, Desired: d})
	if err != nil {
		t.Fatal(err)
	}
	a.confirm(change.PlanID)
	if task, err := s.StartProtectionChange(ctx, change.PlanID, "accept"); err != nil || task.State != "succeeded" {
		t.Fatalf("protection %+v %v", task, err)
	}
	plan, err := s.PrepareBackup(ctx, "box", ref)
	if err != nil {
		t.Fatal(err)
	}
	a.confirm(plan.PlanID)
	var mu sync.Mutex
	br := &backupRun{}
	a.update = func(f func()) { mu.Lock(); defer mu.Unlock(); f() }
	done := make(chan error, 1)
	go func() { done <- a.followStartedBackup(ctx, br, plan.PlanID) }()
	// Inspect the UI's published task without touching its goroutine state.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		mu.Lock()
		copying := br.task != nil && br.task.Run != nil && br.task.Run.Stage == protection.StageCopying
		mu.Unlock()
		if copying {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Start finished before in-flight copy was visible: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	write(filepath.Join(home, "release-copy"), "go")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	mu.Lock()
	task := *br.task
	mu.Unlock()
	if task.State != "partial" || task.Run.Snapshot == nil || task.Run.Components[1].FailureCause != "connect_timeout" {
		t.Fatalf("lost partial result: %+v", task)
	}
	if comp := task.Run.Components[1]; comp.StartedAt == nil || comp.CompletedAt == nil {
		t.Fatalf("failed copy lost timestamps: %+v", comp)
	}
	repo := t.TempDir()
	if _, err := s.RepoAdd("local", repo); err != nil {
		t.Fatal(err)
	}
	if err := a.copyAfterBackup(ctx, br, plan.PlanID, "box", "demo"); err != nil {
		t.Fatal(err)
	}
	if br.localCopies == nil || len(br.localCopies.Locations) != 1 || br.localCopies.Locations[0].State != service.CopyCopied {
		t.Fatalf("desktop did not run shared local-copy policy: %+v", br.localCopies)
	}
	// Simulate a client's response loss, then reopen and query the original
	// run through the same GUI action. The fake server remains untouched.
	task.State, task.Run, task.Error = "reconciling", nil, nil
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "backup-tasks", task.TaskRef.TaskID+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	nextService := &service.Service{Dirs: s.Dirs, Config: s.Config, Home: dir}
	other := New(nextService)
	other.run = func(f func()) { f() }
	ps := &protState{ref: &ref}
	tasks, err := nextService.ListBackupTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other.restoreBackupCards(ps, "box", tasks)
	other.reconcileBackup(ps.backup)
	if ps.backup.task.State != "partial" {
		t.Fatalf("query did not recover result: %+v", ps.backup.task)
	}
	status, err := nextService.VaultStatus(ctx, "box")
	if err != nil || len(status.Snapshots) != 1 || ps.backup.task.Run.AgentRef != ref {
		t.Fatalf("query replayed or rebound backup: %+v %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "box", filepath.FromSlash(status.LeafOf(status.Snapshots[0].SnapshotID)), "snapshots", status.Snapshots[0].SnapshotID+".age")); err != nil {
		t.Fatal("local backup was not actually copied:", err)
	}
}
