package protection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

func code(err error) string {
	if ue, ok := valise.As(err); ok {
		return ue.Code
	}
	return ""
}

func TestRollbackFailureReturnsReconcileChangeWithoutError(t *testing.T) {
	s, a, d := world(t)
	ctx := context.Background()
	// The old timer was active, so restoring it needs a start that keeps failing.
	_, tmr := names(a.InstanceID)
	if err := atomicBytes(filepath.Join(s.Home, ".config", "systemd", "user", tmr), []byte("OnUnitActiveSec=3600s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"active", "enabled"} {
		if err := os.WriteFile(filepath.Join(s.Home, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	yes := true
	d.Schedule.Enabled = &yes
	p := prepare(t, s, a, d, 0)
	bin := filepath.Join(s.Home, "bin", "systemctl")
	body, _ := os.ReadFile(bin)
	failing := strings.Replace(string(body), " start)\n", " start)\n  exit 1\n", 1)
	if err := os.WriteFile(bin, []byte(failing), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: strings.Repeat("4", 32)})
	if err != nil || r == nil || r.Committed || r.RollbackSucceeded || !r.ReconcileRequired || r.ErrorCode != "timer_failed" {
		t.Fatal(r, err)
	}
	if err := s.CheckPending(a.InstanceID); code(err) != "result_unknown" {
		t.Fatal("unsettled rollback did not block:", err)
	}
	// Nothing recorded at all is still an error: an invalid change id.
	if r, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: "bad"}); err == nil || r != nil {
		t.Fatal(r, err)
	}
}

func TestUnitExecutableWritesFormalPath(t *testing.T) {
	home := t.TempDir()
	formal := DeployedPorter(home)
	if err := os.MkdirAll(filepath.Dir(formal), 0700); err != nil {
		t.Fatal(err)
	}
	versioned := filepath.Join(home, ".local", "lib", "valise", "porter-1.3")
	if err := os.MkdirAll(filepath.Dir(versioned), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(versioned, formal); err != nil {
		t.Fatal(err)
	}
	old := executable
	t.Cleanup(func() { executable = old })
	cases := []struct {
		exe, want string
		fail      bool
	}{
		{exe: formal, want: formal},
		// Linux reports the resolved binary; the unit must still name the formal path.
		{exe: versioned, want: formal},
		{exe: "/opt/build/valise-porter", want: "/opt/build/valise-porter"},
		{exe: "/opt/my build/valise-porter", fail: true},
		{exe: "/opt/build/%n", fail: true},
	}
	for _, c := range cases {
		executable = func() (string, error) { return c.exe, nil }
		got, err := UnitExecutable(home)
		if c.fail {
			if code(err) != "timer_path" {
				t.Fatal(c.exe, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatal(c.exe, got, err)
		}
	}
	executable = func() (string, error) { return versioned, nil }
	s := Store{Home: home}
	d := cp.NewDefault()
	yes := true
	d.Schedule = cp.Schedule{Enabled: &yes, DailyAt: "03:00", Timezone: "UTC"}
	files, err := s.timerFiles("agent", d)
	if err != nil {
		t.Fatal(err)
	}
	svc, tmr := names("agent")
	dir := filepath.Join(home, ".config", "systemd", "user")
	if !strings.Contains(string(files[filepath.Join(dir, svc)].Data), "ExecStart="+formal+" protection-timer --instance agent\n") {
		t.Fatal(string(files[filepath.Join(dir, svc)].Data))
	}
	timer := string(files[filepath.Join(dir, tmr)].Data)
	if strings.Contains(timer, "Persistent") {
		t.Fatal("catch_up absent but Persistent written:", timer)
	}
	// catch_up=false keeps the protection-plan/1 unit bytes.
	no := false
	d.Schedule.CatchUp = &no
	files, _ = s.timerFiles("agent", d)
	if string(files[filepath.Join(dir, tmr)].Data) != timer {
		t.Fatal("catch_up=false changed the unit")
	}
	d.Schedule.CatchUp = &yes
	files, _ = s.timerFiles("agent", d)
	if got := string(files[filepath.Join(dir, tmr)].Data); !strings.Contains(got, "[Timer]\nOnCalendar=*-*-* 03:00:00 UTC\nPersistent=true\n\n[Install]") {
		t.Fatal(got)
	}
}

func TestCatchUpAppliedAndUnknownDesiredFieldRefused(t *testing.T) {
	s, a, d := world(t)
	yes := true
	d.Schedule.Enabled = &yes
	d.Schedule.CatchUp = &yes
	r := apply(t, s, prepare(t, s, a, d, 0))
	if !r.Committed {
		t.Fatal(r)
	}
	_, tmr := names(a.InstanceID)
	b, _ := os.ReadFile(filepath.Join(s.Home, ".config", "systemd", "user", tmr))
	if !strings.Contains(string(b), "Persistent=true") {
		t.Fatal(string(b))
	}
	plan, _ := s.load(a.InstanceID)
	if !plan.Schedule.CatchUpEnabled() {
		t.Fatal(plan)
	}
	raw := `{"agent_ref":{},"expected_revision":"0","desired":{"scope":{"kind":"preset","tier":"full"},"schedule":{"enabled":true,"daily_at":"03:00","timezone":"UTC","replay_all":true},"period":{"unit":"week","timezone":"UTC"},"server_copies":[]}}`
	var p cp.PrepareParams
	if err := json.Unmarshal([]byte(raw), &p); err == nil {
		t.Fatal("unknown desired field silently accepted")
	}
}

func writeRun(t *testing.T, s Store, a identity.AgentRef, id string, at time.Time, reason, state string) {
	t.Helper()
	r := cp.BackupRun{Schema: cp.BackupRunSchema, RunID: id, AgentRef: a, Request: cp.BackupParams{AgentRef: a, RunID: id, Reason: reason, ExpectedRevision: 1}, State: state, AcceptedAt: at, UpdatedAt: at}
	b, _ := json.Marshal(r)
	if err := os.MkdirAll(s.runsDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.runsDir(), id+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGetReportsMissedRunsReadOnly(t *testing.T) {
	s, a, d := world(t)
	yes := true
	d.Schedule = cp.Schedule{Enabled: &yes, DailyAt: "03:00", Timezone: "UTC"}
	apply(t, s, prepare(t, s, a, d, 0))
	plan, _ := s.load(a.InstanceID)
	// Pretend the plan was accepted on 2026-11-01 01:00 UTC; now is 11-05 12:00.
	plan.AcceptedAt = time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
	if err := atomic(s.planPath(a.InstanceID), plan); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) }
	writeRun(t, s, a, strings.Repeat("1", 32), time.Date(2026, 11, 1, 3, 0, 5, 0, time.UTC), "timer", "succeeded")
	writeRun(t, s, a, strings.Repeat("2", 32), time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), "manual", "succeeded")
	writeRun(t, s, a, strings.Repeat("3", 32), time.Date(2026, 11, 4, 3, 0, 1, 0, time.UTC), "timer", "succeeded")
	if err := os.WriteFile(filepath.Join(s.runsDir(), "runs.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(s.runsDir())
	v, err := s.Get(context.Background(), cp.GetParams{AgentRef: a})
	if err != nil {
		t.Fatal(err)
	}
	// 11-02 and 11-03 had no timer run (the manual one does not count) and
	// the 11-04 timer run succeeded later: both expired. 11-05 ran nothing yet: missed.
	want := []cp.MissedRun{
		{ScheduledAt: time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC), State: "expired"},
		{ScheduledAt: time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC), State: "expired"},
		{ScheduledAt: time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC), State: "missed"},
	}
	got, _ := json.Marshal(v.MissedRuns)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) || !v.ServerNow.Equal(s.Now()) {
		t.Fatal(string(got), v.ServerNow)
	}
	after, _ := os.ReadDir(s.runsDir())
	if len(before) != len(after) {
		t.Fatal("get wrote run records")
	}
	// A record from the future flags the server clock.
	writeRun(t, s, a, strings.Repeat("5", 32), time.Date(2026, 11, 9, 3, 0, 0, 0, time.UTC), "timer", "succeeded")
	v, err = s.Get(context.Background(), cp.GetParams{AgentRef: a})
	if err != nil || !strings.Contains(strings.Join(v.Warnings, ";"), "服务器时钟") {
		t.Fatal(v, err)
	}
}

// targetWorld adopts a plan copying to nas and returns it.
func targetWorld(t *testing.T) (Store, identity.AgentRef, cp.Desired) {
	t.Helper()
	s, a, d := world(t)
	if err := atomicBytes(s.targetsPath(), []byte(`{"targets":[{"name":"nas","kind":"rsync","dest":"nas-old:/srv/vault/"},{"name":"other","kind":"rsync","dest":"o:/x/"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	yes := true
	d.ServerCopies = []cp.ServerCopy{{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: a.ServerUID}, Enabled: &yes, MinimumInterval: "1h"}}
	apply(t, s, prepare(t, s, a, d, 0))
	return s, a, d
}
func newNas() vaultrpc.Target {
	return vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "nas-new:/srv/vault/"}
}
func currentNas(t *testing.T, s Store) vaultrpc.Target {
	t.Helper()
	im, _ := image(s.targetsPath())
	list, err := readTargetList(im)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.Name == "nas" {
			return x
		}
	}
	t.Fatal("nas missing")
	return vaultrpc.Target{}
}

func TestTargetChangeSuccessBumpsEveryReferencingPlan(t *testing.T) {
	s, a, _ := targetWorld(t)
	ctx := context.Background()
	if _, err := s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: vaultrpc.Target{Name: "other", Kind: "rsync", Dest: "o:/y/"}}); code(err) != "target_unreferenced" {
		t.Fatal("unreferenced target took the versioned path:", err)
	}
	if _, err := s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: vaultrpc.Target{Name: "nas2", Kind: "rsync", Dest: "n:/y/"}}); code(err) != "target_unknown" {
		t.Fatal("rename/new target accepted:", err)
	}
	if _, err := s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "-oProxyCommand=x:/y/"}}); code(err) != "target_dest_invalid" {
		t.Fatal("seal validation bypassed:", err)
	}
	before, _ := s.load(a.InstanceID)
	frozen, err := s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: newNas()})
	if err != nil || len(frozen.Plans) != 1 || frozen.Plans[0].Revision != 1 || frozen.Before.Dest != "nas-old:/srv/vault/" {
		t.Fatal(frozen, err)
	}
	if currentNas(t, s).Dest != "nas-old:/srv/vault/" {
		t.Fatal("prepare wrote targets")
	}
	if es, _ := os.ReadDir(s.targetState()); len(es) != 0 {
		t.Fatal("prepare left files:", es)
	}
	r, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: strings.Repeat("7", 32)})
	if err != nil || !r.Committed || r.State != "committed" || r.ReconcileRequired {
		t.Fatal(r, err)
	}
	if currentNas(t, s).Dest != "nas-new:/srv/vault/" {
		t.Fatal("target not written")
	}
	after, _ := s.load(a.InstanceID)
	x, _ := cp.Digest(before.Desired)
	y, _ := cp.Digest(after.Desired)
	if after.Revision != 2 || x != y || !after.AcceptedAt.After(before.AcceptedAt) {
		t.Fatal(after)
	}
	// Legacy target_set stays refused for the referenced target.
	if err := s.CheckLegacyTargetsWrite("nas"); err == nil {
		t.Fatal("legacy write accepted")
	}
	if err := s.CheckPending(a.InstanceID); err != nil {
		t.Fatal(err)
	}
	// The same change id returns the recorded result without writing again.
	again, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: strings.Repeat("7", 32)})
	if err != nil || !again.Committed {
		t.Fatal(again, err)
	}
	if p, _ := s.load(a.InstanceID); p.Revision != 2 {
		t.Fatal("replayed bump", p.Revision)
	}
}

func TestTargetChangeConflictWritesNothing(t *testing.T) {
	s, a, d := targetWorld(t)
	ctx := context.Background()
	frozen, err := s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: newNas()})
	if err != nil {
		t.Fatal(err)
	}
	// Another client changes the referencing plan in between.
	d.Schedule.DailyAt = "04:00"
	apply(t, s, prepare(t, s, a, d, 1))
	targets, _ := image(s.targetsPath())
	r, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: strings.Repeat("8", 32)})
	if code(err) != "revision_conflict" || r != nil {
		t.Fatal(r, err)
	}
	now, _ := image(s.targetsPath())
	if !sameImage(now, targets) {
		t.Fatal("targets written on conflict")
	}
	if p, _ := s.load(a.InstanceID); p.Revision != 2 {
		t.Fatal(p.Revision)
	}
	// A changed target definition is a conflict too.
	frozen, _ = s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: newNas()})
	if err := atomicBytes(s.targetsPath(), []byte(`{"targets":[{"name":"nas","kind":"rsync","dest":"nas-other:/srv/"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: strings.Repeat("9", 32)}); code(err) != "revision_conflict" {
		t.Fatal(err)
	}
	// An unfinished protection transaction blocks the change.
	frozen, _ = s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: newNas()})
	var rec record
	es, _ := os.ReadDir(s.state())
	for _, e := range es {
		if filepath.Ext(e.Name()) == ".json" {
			if err := read(filepath.Join(s.state(), e.Name()), &rec); err != nil {
				t.Fatal(err)
			}
			rec.ReconcileRequired = true
			if err := s.persist(&rec); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if _, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: strings.Repeat("a", 32)}); code(err) != "result_unknown" {
		t.Fatal(err)
	}
}

func TestTargetChangeCrashLeavesBlockingRecordAndResumes(t *testing.T) {
	s, a, _ := targetWorld(t)
	ctx := context.Background()
	frozen, err := s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: newNas()})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 32)
	// Simulate a crash after targets.json was written but before the plan:
	// run the real apply, then restore the old plan and mark the record pending.
	oldPlan, _ := s.load(a.InstanceID)
	if _, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: id}); err != nil {
		t.Fatal(err)
	}
	if err := atomic(s.planPath(a.InstanceID), oldPlan); err != nil {
		t.Fatal(err)
	}
	var r targetRecord
	if err := read(filepath.Join(s.targetState(), id+".json"), &r); err != nil {
		t.Fatal(err)
	}
	r.State, r.Committed = "pending", false
	if err := s.persistTarget(&r); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckPending(a.InstanceID); code(err) != "result_unknown" {
		t.Fatal("crash record did not block planned backup:", err)
	}
	if err := s.CheckLegacyTargetsWrite("other"); code(err) != "result_unknown" {
		t.Fatal("crash record did not block target writes:", err)
	}
	if _, err := s.Prepare(ctx, cp.PrepareParams{AgentRef: a, ExpectedRevision: 1, Desired: oldPlan.Desired}); err == nil {
		t.Fatal("protection change allowed during unfinished target change")
	}
	// Explicit retry of the same change completes it from the recorded bytes.
	done, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: id})
	if err != nil || !done.Committed || done.ReconcileRequired {
		t.Fatal(done, err)
	}
	if p, _ := s.load(a.InstanceID); p.Revision != 2 {
		t.Fatal(p.Revision)
	}
	if err := s.CheckPending(a.InstanceID); err != nil {
		t.Fatal(err)
	}

	// A crash whose files match neither side stays blocked.
	frozen, err = s.PrepareTargetChange(ctx, cp.TargetChangePrepareParams{Target: vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "nas-third:/srv/vault/"}})
	if err != nil {
		t.Fatal(err)
	}
	id = strings.Repeat("c", 32)
	if _, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: id}); err != nil {
		t.Fatal(err)
	}
	if err := read(filepath.Join(s.targetState(), id+".json"), &r); err != nil {
		t.Fatal(err)
	}
	r.State, r.Committed = "pending", false
	if err := s.persistTarget(&r); err != nil {
		t.Fatal(err)
	}
	if err := atomicBytes(s.targetsPath(), []byte(`{"targets":[{"name":"nas","kind":"rsync","dest":"elsewhere:/x/"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	stuck, err := s.ApplyTargetChange(ctx, cp.TargetChangeApplyParams{Plan: *frozen, ChangeID: id})
	if err != nil || stuck.Committed || !stuck.ReconcileRequired || stuck.ErrorCode != "result_unknown" {
		t.Fatal(stuck, err)
	}
	if err := s.CheckPending(a.InstanceID); code(err) != "result_unknown" {
		t.Fatal(err)
	}
}

func TestTargetChangeCorruptRecordFailsClosed(t *testing.T) {
	s, a, _ := targetWorld(t)
	if err := atomicBytes(filepath.Join(s.targetState(), strings.Repeat("d", 32)+".json"), []byte(`{"state":"pending"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckPending(a.InstanceID); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("corrupt target change record ignored")
	}
}
