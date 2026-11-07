package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/protection"
)

// fakeSystemdRun stands in for systemd-run on the fake server: it records
// its argv and starts the given porter command in the background, the way a
// transient unit outlives the RPC connection.
const fakeSystemdRun = `#!/bin/sh
printf '%s\n' "$@" > "$HOME/systemd-run.args"
shift 4
"$@" >/dev/null 2>&1 </dev/null &
exit 0
`

func acceptedPlanWorld(t *testing.T) (*Service, string, identity.AgentRef) {
	t.Helper()
	s, home := enrollWorld(t)
	ctx := context.Background()
	for name, body := range map[string]string{"systemctl": n2Systemctl, "loginctl": "#!/bin/sh\nprintf 'yes\\n'\n", "systemd-run": fakeSystemdRun} {
		if err := os.WriteFile(filepath.Join(home, ".local/bin", name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	var b identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: "demo"}, &b); err != nil || b.Agent == nil {
		t.Fatalf("binding %+v %v", b, err)
	}
	desired := protection.NewDefault()
	desired.Schedule.DailyAt = "12:00"
	desired.Schedule.Timezone = "UTC"
	desired.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	s.ProtectionReview = func(context.Context, string, protection.FrozenChange) (bool, error) { return true, nil }
	plan, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{AgentRef: b.Agent.Ref, Desired: desired})
	if err != nil {
		t.Fatal(err)
	}
	if done, err := s.StartProtectionChange(ctx, plan.PlanID, "accept"); err != nil || done.State != "succeeded" {
		t.Fatalf("apply %+v %v", done, err)
	}
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "box", "n2"); err != nil {
		t.Fatal(err)
	}
	return s, home, b.Agent.Ref
}

// Over fake-ssh the deployed porter hands the accepted run to systemd-run,
// returns at once, and the unit's task-run finishes it. fake-ssh is not a
// real systemd --user acceptance.
func TestDetachedBackupOverFakeSSH(t *testing.T) {
	s, home, ref := acceptedPlanWorld(t)
	ctx := context.Background()
	// The hello of this porter build is wired by the main thread; the
	// capability is supplied here so the client takes the asynchronous path.
	c, err := NewBackupController(BackupControllerConfig{StateDir: s.Dirs.Data, RPC: s.call, Review: approveBackup, PollInterval: 50 * time.Millisecond,
		Capabilities: func(context.Context, string) ([]string, error) {
			return []string{protection.BackupCapability, protection.BackupCapabilityV2}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Shutdown(context.Background()) }()
	p, err := c.PrepareBackup(ctx, "box", ref)
	if err != nil {
		t.Fatal(err)
	}
	task, err := c.StartBackup(ctx, p.PlanID, "detached")
	if err != nil || task.State != "running" || !task.Async {
		t.Fatalf("start %+v %v", task, err)
	}
	ch, unsubscribe, err := c.WatchBackupTask(ctx, task.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	var done BackupTask
	deadline := time.After(60 * time.Second)
	for done.State == "" {
		select {
		case v, ok := <-ch:
			if !ok {
				t.Fatal("closed early")
			}
			if backupTerminal(v.State) || v.State == "reconciling" {
				done = v
			}
		case <-deadline:
			t.Fatal("detached run did not finish")
		}
	}
	if done.State != "succeeded" || done.Execution != protection.ExecutionDetached || done.Run == nil || done.Run.Snapshot == nil {
		t.Fatalf("done %+v", done)
	}
	args, err := os.ReadFile(filepath.Join(home, "systemd-run.args"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--user", "--collect", "--unit=valise-task-" + task.RunID, "--property=NoNewPrivileges=yes", filepath.Join(home, ".local/bin/valise-porter"), "task-run", "--id", task.RunID}
	got := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(got) != len(want) {
		t.Fatalf("argv %q", got)
	}
	for i := range want {
		if got[i] != want[i] && !(i == 4 && strings.HasSuffix(got[i], "/.local/bin/valise-porter")) {
			t.Fatalf("argv %q", got)
		}
	}
	st, err := s.VaultStatus(ctx, "box")
	if err != nil || len(st.Snapshots) != 1 {
		t.Fatalf("snapshots %+v %v", st, err)
	}
}
