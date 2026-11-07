package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/rpc"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

const n2Systemctl = `#!/bin/sh
shift
if test "$1" = '--timestamp=unix'; then shift; fi
case "$1" in
 show)
  if test -e "$HOME/.config/systemd/user/$2"; then
   printf 'LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\n'
  else printf 'LoadState=not-found\n'; fi ;;
 disable|daemon-reload) exit 0 ;;
 *) exit 2 ;;
esac
`

func TestN2ProtectionFakeSSHFacade(t *testing.T) {
	s, home := enrollWorld(t)
	ctx := context.Background()
	for name, body := range map[string]string{"systemctl": n2Systemctl, "loginctl": "#!/bin/sh\nprintf 'yes\\n'\n"} {
		if err := os.WriteFile(filepath.Join(home, ".local/bin", name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	var b identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: "demo"}, &b); err != nil {
		t.Fatal(err)
	}
	if b.IdentityState != identity.Bound || b.Agent == nil {
		t.Fatalf("binding: %+v", b)
	}
	desired := protection.NewDefault()
	desired.Schedule.DailyAt = "12:00"
	desired.Schedule.Timezone = "UTC"
	desired.Period = protection.Period{Unit: "month", Timezone: "Asia/Shanghai"}
	req := protection.PrepareParams{AgentRef: b.Agent.Ref, Desired: desired}
	plan, err := s.PrepareProtectionChange(ctx, "box", req)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.StartProtectionChange(ctx, plan.PlanID, "first-confirmation")
	if err != nil || task.State != "waiting" {
		t.Fatalf("missing human: %+v %v", task, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config/valise/protection/demo.json")); !os.IsNotExist(err) {
		t.Fatal("waiting changed server plan")
	}
	// A human adapter is attached to a new process, which must prepare again.
	next := newService(t, s.Config.SSHBinary)
	next.Dirs = s.Dirs
	reviews := 0
	next.ProtectionReview = func(_ context.Context, host string, p protection.FrozenChange) (bool, error) {
		reviews++
		if host != "box" || p.Request.Desired.Scope.Tier != "full" {
			t.Fatal("wrong reviewed plan")
		}
		return true, nil
	}
	approved, err := next.PrepareProtectionChange(ctx, "box", req)
	if err != nil {
		t.Fatal(err)
	}
	done, err := next.StartProtectionChange(ctx, approved.PlanID, "accepted-change")
	if err != nil || done.State != "succeeded" || done.Change == nil || !done.Change.Committed {
		t.Fatalf("apply: %+v %v", done, err)
	}
	if reviews != 1 {
		t.Fatalf("reviews=%d", reviews)
	}
	repeat, err := next.StartProtectionChange(ctx, approved.PlanID, "accepted-change")
	if err != nil || repeat.TaskRef != done.TaskRef || reviews != 1 {
		t.Fatalf("duplicate: %+v %v", repeat, err)
	}
	reloaded := newService(t, s.Config.SSHBinary)
	reloaded.Dirs = s.Dirs
	observed, err := reloaded.GetProtectionTask(ctx, done.TaskRef)
	if err != nil || observed.State != "succeeded" {
		t.Fatalf("reload: %+v %v", observed, err)
	}
	view, err := next.GetProtection(ctx, "box", protection.GetParams{AgentRef: b.Agent.Ref})
	if err != nil || view.ServerPlan == nil || view.ServerPlan.Revision != 1 || view.ServerPlan.Scope.Tier != "full" {
		t.Fatalf("plan: %+v %v", view, err)
	}
	_, err = next.PrepareProtectionChange(ctx, "box", req)
	wantCode(t, err, "revision_conflict")
	err = next.PeriodSet(ctx, "box", "demo", vault.PeriodSetting{Unit: "week", TZ: "UTC"})
	wantCode(t, err, "revision_conflict")
	_, err = next.TimerSet(ctx, "box", vaultrpc.TimerSetParams{InstanceID: "demo"})
	wantCode(t, err, "revision_conflict")
	if err := next.TargetSet(ctx, "box", vaultrpc.Target{Name: "new", Kind: "ssh", Dest: "elsewhere:/vault"}); err == nil {
		t.Fatal("old target write bypassed plan")
	}
	if _, err := next.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := next.KeyRegister(ctx, "box", "n2"); err != nil {
		t.Fatal(err)
	}
	// Planned backup through the facade: unreviewed waits, reviewed runs full.
	bp, err := next.PrepareBackup(ctx, "box", b.Agent.Ref)
	if err != nil || bp.ServerPlan.Scope.Tier != "full" || !bp.IncludesCredentials {
		t.Fatalf("backup plan: %+v %v", bp, err)
	}
	bt, err := next.StartBackup(ctx, bp.PlanID, "unreviewed-backup")
	if err != nil || bt.State != "waiting" {
		t.Fatalf("backup without review: %+v %v", bt, err)
	}
	next.BackupReview = func(context.Context, string, BackupPlan) (bool, error) { return true, nil }
	next.backupController = nil
	bp, err = next.PrepareBackup(ctx, "box", b.Agent.Ref)
	if err != nil {
		t.Fatal(err)
	}
	bt, err = next.StartBackup(ctx, bp.PlanID, "reviewed-backup")
	if err != nil || bt.State != "succeeded" || bt.Run == nil || bt.Run.Snapshot == nil || bt.Run.Plan.Scope.Tier != "full" {
		t.Fatalf("planned backup: %+v %v", bt, err)
	}
	var bind protection.SnapshotBinding
	if err := next.call(ctx, "box", protection.MethodSnapshotBinding, protection.SnapshotBindingParams{SnapshotRef: *bt.Run.Snapshot}, &bind); err != nil || bind.State != "bound" || bind.AgentRef == nil || *bind.AgentRef != b.Agent.Ref {
		t.Fatalf("snapshot binding: %+v %v", bind, err)
	}
	st, err := next.VaultStatus(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, snap := range st.Snapshots {
		if snap.SnapshotID == bt.Run.Snapshot.SnapshotID {
			found = true
			if snap.Tier != "full" || snap.FileCount != 4 {
				t.Fatalf("full content: %+v", snap)
			}
		}
	}
	if !found {
		t.Fatal("snapshot not listed")
	}
}

func TestN2CapabilitiesAndTiers(t *testing.T) {
	for _, method := range []string{identity.MethodGet, protection.MethodGet, protection.MethodPrepare, protection.MethodStatus, protection.MethodBackup, protection.MethodRunStatus} {
		if rpc.TierOf(method) != rpc.TierDaily {
			t.Fatalf("read/backup tier: %s", method)
		}
		wantCode(t, n2Compatibility(&rpc.HelloResult{}, method), "unsupported_capability")
	}
	for _, method := range []string{protection.MethodApply, protection.MethodReconcile} {
		if rpc.TierOf(method) != rpc.TierOwner {
			t.Fatalf("mutation tier: %s", method)
		}
		wantCode(t, n2Compatibility(nil, method), "unsupported_capability")
	}
}
