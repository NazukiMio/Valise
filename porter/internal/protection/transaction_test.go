package protection

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/identity"
	cp "valise/core/protection"
	porterenroll "valise/porter/internal/enroll"
)

const fakeSystemctl = `#!/bin/sh
printf '%s\n' "$*" >> "$HOME/commands"
shift
if test "$1" = '--timestamp=unix'; then shift; fi
op="$1"
shift
case "$op" in
 show)
  unit="$1"
  if test -e "$HOME/.config/systemd/user/$unit"; then
   printf 'LoadState=loaded\n'
   if test -e "$HOME/fail-verify" && test -e "$HOME/.config/valise/protection/agent.json"; then printf 'ActiveState=failed\n'; elif test -e "$HOME/active"; then printf 'ActiveState=active\n'; else printf 'ActiveState=inactive\n'; fi
   if test -e "$HOME/enabled"; then printf 'UnitFileState=enabled\n'; else printf 'UnitFileState=disabled\n'; fi
   printf 'NextElapseUSecRealtime=@1793934000\nLastTriggerUSec=@1793847600\n'
  else printf 'LoadState=not-found\n'; fi
  ;;
 disable) /bin/rm "$HOME/active" "$HOME/enabled" 2>/dev/null; exit 0 ;;
 enable) : > "$HOME/enabled" ;;
 start)
  if test -e "$HOME/fail-start"; then /bin/rm "$HOME/fail-start"; exit 1; fi
  : > "$HOME/active"
  ;;
 daemon-reload) exit 0 ;;
 *) exit 2 ;;
esac
`

func world(t *testing.T) (Store, identity.AgentRef, cp.Desired) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"systemctl": fakeSystemctl, "loginctl": "#!/bin/sh\nprintf 'yes\\n'\n", "systemd-analyze": "#!/bin/sh\nprintf 'Next elapse: Fri 2026-11-06 03:00:00 UTC\\nDST preview checked\\n'\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	a := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "generation"}
	s := Store{Home: home, Env: &porterenroll.Env{Home: home, Vars: []string{"HOME=" + home, "PATH=" + bin, "USER=test"}}, ValidateAgent: func(_ context.Context, ref identity.AgentRef) error {
		if ref != a {
			return fail("identity_conflict", "测试世代不匹配")
		}
		return nil
	}}
	d := cp.NewDefault()
	d.Schedule.DailyAt = "02:30"
	d.Schedule.Timezone = "America/New_York"
	d.Period = cp.Period{Unit: "week", Timezone: "Asia/Shanghai"}
	return s, a, d
}
func prepare(t *testing.T, s Store, a identity.AgentRef, d cp.Desired, rev cp.Revision) *cp.FrozenChange {
	t.Helper()
	p, err := s.Prepare(context.Background(), cp.PrepareParams{AgentRef: a, ExpectedRevision: rev, Desired: d})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func apply(t *testing.T, s Store, p *cp.FrozenChange) *cp.Change {
	t.Helper()
	id, _ := identity.NewID()
	r, err := s.Apply(context.Background(), cp.ApplyParams{Plan: *p, ChangeID: id})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestPrepareReadOnlyAndCAS(t *testing.T) {
	s, a, d := world(t)
	ctx := context.Background()
	v, err := s.Get(ctx, cp.GetParams{AgentRef: a})
	if err != nil || v.Mode != "legacy" {
		t.Fatal(v, err)
	}
	one := prepare(t, s, a, d, 0)
	two := prepare(t, s, a, d, 0)
	if _, err := os.Stat(s.planPath(a.InstanceID)); !os.IsNotExist(err) {
		t.Fatal("prepare persisted a plan")
	}
	r := apply(t, s, one)
	if !r.Committed || r.ReconcileRequired || r.NewRevision != 1 {
		t.Fatal(r)
	}
	if _, err := s.Apply(ctx, cp.ApplyParams{Plan: *two, ChangeID: strings.Repeat("b", 32)}); err == nil {
		t.Fatal("stale CAS accepted")
	}
	if err := s.CheckLegacyWrite(a.InstanceID); err == nil {
		t.Fatal("legacy write accepted")
	}
	// A plan without server copies references no target: legacy target
	// writes stay legacy (their auto setting only drives legacy instances).
	if err := s.CheckLegacyTargetsWrite("unreferenced"); err != nil {
		t.Fatal("unreferenced target write refused:", err)
	}
	for _, path := range []string{s.planPath(a.InstanceID), filepath.Join(s.state(), r.ChangeID+".json"), s.periodPath(a.InstanceID)} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal(path, st, err)
		}
	}
}
func TestDailyCalendarAndFrozenPlanRead(t *testing.T) {
	s, a, d := world(t)
	enabled := true
	d.Schedule.Enabled = &enabled
	p := prepare(t, s, a, d, 0)
	if !strings.Contains(p.CalendarPreview, "DST") {
		t.Fatal(p)
	}
	r := apply(t, s, p)
	if !r.Committed {
		t.Fatal(r)
	}
	svc, tmr := names(a.InstanceID)
	dir := filepath.Join(s.Home, ".config", "systemd", "user")
	service, _ := os.ReadFile(filepath.Join(dir, svc))
	timer, _ := os.ReadFile(filepath.Join(dir, tmr))
	if !strings.Contains(string(service), "protection-timer --instance agent") || strings.Contains(string(service), "full") || strings.Contains(string(service), "CapabilityBoundingSet") {
		t.Fatal(string(service))
	}
	if !strings.Contains(string(timer), "OnCalendar=*-*-* 02:30:00 America/New_York") {
		t.Fatal(string(timer))
	}
	if err := s.WithAccepted(context.Background(), a, func(p *cp.ServerPlan) error {
		if p.Scope.Tier != "full" || p.Period.Timezone != "Asia/Shanghai" {
			t.Fatal(p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed := a
	changed.Generation = "wrong"
	if _, err := s.Accepted(context.Background(), changed); err == nil {
		t.Fatal("wrong generation accepted")
	}
	// A manually changed compatibility projection is not silently authoritative.
	if err := os.WriteFile(s.periodPath(a.InstanceID), []byte(`{"unit":"month","tz":"UTC"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.WithAccepted(context.Background(), a, func(*cp.ServerPlan) error { t.Fatal("ran with conflicting period"); return nil }); err == nil {
		t.Fatal("projection mismatch accepted")
	}
}
func TestFailedInstallRestoresLegacyTimerAndPeriod(t *testing.T) {
	s, a, d := world(t)
	ctx := context.Background()
	svc, tmr := names(a.InstanceID)
	dir := filepath.Join(s.Home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{filepath.Join(dir, svc): "ExecStart=old-soul\n", filepath.Join(dir, tmr): "OnUnitActiveSec=3600s\n", s.periodPath(a.InstanceID): `{"unit":"month","tz":"UTC"}`} {
		if err := atomicBytes(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"active", "enabled"} {
		if err := os.WriteFile(filepath.Join(s.Home, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.snapshots(a.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	d.Schedule.Enabled = &enabled
	p := prepare(t, s, a, d, 0)
	if err := os.WriteFile(filepath.Join(s.Home, "fail-start"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: strings.Repeat("c", 32)})
	if err == nil || r == nil || r.Committed || !r.RollbackSucceeded || r.State != "failed" {
		t.Fatal(r, err)
	}
	after, _ := s.snapshots(a.InstanceID)
	x, _ := cp.Digest(before)
	y, _ := cp.Digest(after)
	if x != y {
		t.Fatal("rollback did not restore exact old files")
	}
	if err := s.CheckPending(a.InstanceID); err != nil {
		t.Fatal(err)
	}
	v := s.observe(ctx, a.InstanceID)
	if v.ActiveState != "active" || v.UnitFileState != "enabled" {
		t.Fatal(v)
	}
}
func TestCrashPendingBlocksAndStatusNeverReplays(t *testing.T) {
	s, a, d := world(t)
	ctx := context.Background()
	old, _ := s.snapshots(a.InstanceID)
	next, _ := s.timerFiles(a.InstanceID, d)
	pb, _ := json.Marshal(struct {
		Unit string `json:"unit"`
		TZ   string `json:"tz"`
	}{d.Period.Unit, d.Period.Timezone})
	next[s.periodPath(a.InstanceID)] = fileImage{Exists: true, Data: pb}
	r := record{Change: cp.Change{Schema: changeSchema, ChangeID: strings.Repeat("d", 32), AgentRef: a, RequestDigest: "request", State: "pending", OldRevision: 0, NewRevision: 1}, Plan: cp.ServerPlan{SchemaVersion: cp.Schema, AgentRef: a, Revision: 1, Desired: d, AcceptedAt: time.Now().UTC()}, OldFiles: old, NewFiles: next}
	if err := s.persist(&r); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckPending(a.InstanceID); err == nil {
		t.Fatal("pending not blocked")
	}
	before, _ := os.ReadFile(filepath.Join(s.Home, "commands"))
	status, err := s.Status(ctx, cp.StatusParams{AgentRef: a, ChangeID: r.ChangeID})
	if err != nil || !status.ReconcileRequired {
		t.Fatal(status, err)
	}
	after, _ := os.ReadFile(filepath.Join(s.Home, "commands"))
	if string(before) != string(after) {
		t.Fatal("status executed systemctl")
	}
	recovered, err := s.Reconcile(ctx, cp.ReconcileParams{AgentRef: a, ChangeID: r.ChangeID, Action: "verify"})
	if err != nil || !recovered.RollbackSucceeded {
		t.Fatal(recovered, err)
	}
	if err := s.CheckPending(a.InstanceID); err != nil {
		t.Fatal(err)
	}
}
func TestPostPublishFailureKeepsNewVersion(t *testing.T) {
	s, a, d := world(t)
	yes := true
	d.Schedule.Enabled = &yes
	ctx := context.Background()
	p := prepare(t, s, a, d, 0)
	marker := filepath.Join(s.Home, "fail-verify")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: strings.Repeat("3", 32)})
	if err == nil || r == nil || !r.Committed || !r.ReconcileRequired || r.RollbackSucceeded {
		t.Fatal(r, err)
	}
	current, err := s.load(a.InstanceID)
	if err != nil || current.Revision != 1 {
		t.Fatal(current, err)
	}
	if err := s.CheckPending(a.InstanceID); err == nil {
		t.Fatal("postcommit failed verification did not block backup")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	r, err = s.Reconcile(ctx, cp.ReconcileParams{AgentRef: a, ChangeID: r.ChangeID, Action: "verify"})
	if err != nil || r.ReconcileRequired {
		t.Fatal(r, err)
	}
}

func TestPublishedCrashCannotRollbackAndVerifyClearsPending(t *testing.T) {
	s, a, d := world(t)
	ctx := context.Background()
	p := prepare(t, s, a, d, 0)
	done := apply(t, s, p)
	var r record
	if err := read(filepath.Join(s.state(), done.ChangeID+".json"), &r); err != nil {
		t.Fatal(err)
	}
	r.State = "pending"
	r.Committed = false
	r.ReconcileRequired = false
	if err := s.persist(&r); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, cp.StatusParams{AgentRef: a, ChangeID: r.ChangeID})
	if err != nil || !status.Committed {
		t.Fatal(status, err)
	}
	if _, err := s.Reconcile(ctx, cp.ReconcileParams{AgentRef: a, ChangeID: r.ChangeID, Action: "rollback"}); err == nil {
		t.Fatal("rollback after publication allowed")
	}
	verified, err := s.Reconcile(ctx, cp.ReconcileParams{AgentRef: a, ChangeID: r.ChangeID, Action: "verify"})
	if err != nil || !verified.Committed || verified.ReconcileRequired {
		t.Fatal(verified, err)
	}
}
func TestLegacyReviewAndExecutionLock(t *testing.T) {
	s, a, d := world(t)
	ctx := context.Background()
	_, tmr := names(a.InstanceID)
	if err := atomicBytes(filepath.Join(s.Home, ".config", "systemd", "user", tmr), []byte("OnUnitActiveSec=21600s\nOnActiveSec=21600s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := atomicBytes(filepath.Join(s.config(), "targets.json"), []byte(`{"targets":[{"name":"nas","auto":{"every":"6h"}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	view, err := s.Get(ctx, cp.GetParams{AgentRef: a})
	if err != nil {
		t.Fatal(err)
	}
	if view.LegacySettings == nil || view.LegacySettings.Scope.Tier != "soul" || view.LegacySettings.Every != "21600s" || len(view.LegacySettings.Copies) != 1 || !view.LegacySettings.Copies[0].Enabled {
		t.Fatal(view)
	}
	p := prepare(t, s, a, d, 0)
	if p.Before.LegacySettings == nil || p.Before.LegacySettings.Scope.Tier != "soul" {
		t.Fatal(p)
	}
	if err := s.WithLegacyWrite(a.InstanceID, func() error {
		_, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: strings.Repeat("1", 32)})
		if err == nil {
			t.Fatal("adoption raced legacy write lock")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	apply(t, s, p)
	if err := s.WithAccepted(ctx, a, func(*cp.ServerPlan) error {
		if err := s.WithLegacyTargetsWrite("nas", func() error { t.Fatal("legacy target mutation ran"); return nil }); err == nil {
			t.Fatal("execution lock not held")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestCorruptGetAndTargetReferenceRejection(t *testing.T) {
	s, a, d := world(t)
	ctx := context.Background()
	yes := true
	d.ServerCopies = []cp.ServerCopy{{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: "missing", AuthorityID: a.ServerUID}, Enabled: &yes, MinimumInterval: "1h"}}
	if _, err := s.Prepare(ctx, cp.PrepareParams{AgentRef: a, Desired: d}); err == nil {
		t.Fatal("nonexistent target accepted")
	}
	if err := atomicBytes(filepath.Join(s.state(), strings.Repeat("2", 32)+".json"), []byte(`{"state":"pending"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, cp.GetParams{AgentRef: a}); err == nil {
		t.Fatal("corrupt transaction reduced to warning")
	}
}

func TestDedupeExpiryTamperAndChangedDossier(t *testing.T) {
	s, a, d := world(t)
	ctx := context.Background()
	p := prepare(t, s, a, d, 0)
	id := strings.Repeat("e", 32)
	done, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: id})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: id})
	if err != nil || again.ChangeID != done.ChangeID {
		t.Fatal(again, err)
	}
	different := *p
	different.Request.Desired.Scope.Tier = "soul"
	different.PlanDigest, _ = different.Digest()
	if _, err := s.Apply(ctx, cp.ApplyParams{Plan: different, ChangeID: id}); err == nil {
		t.Fatal("same id different request accepted")
	}
	p = prepare(t, s, a, d, 1)
	p.ExpiresAt = time.Now().Add(-time.Minute)
	p.CreatedAt = p.ExpiresAt.Add(-10 * time.Minute)
	p.PlanDigest, _ = p.Digest()
	if _, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: strings.Repeat("f", 32)}); err == nil {
		t.Fatal("expired accepted")
	}
	p = prepare(t, s, a, d, 1)
	if err := atomicBytes(filepath.Join(s.config(), "dossiers", a.InstallID+".profile.json"), []byte(`{"changed":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, cp.ApplyParams{Plan: *p, ChangeID: strings.Repeat("f", 32)}); err == nil {
		t.Fatal("changed profile accepted")
	}
}

func TestReferencedTargetWriteRefused(t *testing.T) {
	s, a, d := world(t)
	if err := atomicBytes(filepath.Join(s.config(), "targets.json"), []byte(`{"targets":[{"name":"nas"},{"name":"other"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	yes := true
	d.ServerCopies = []cp.ServerCopy{{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: a.ServerUID}, Enabled: &yes, MinimumInterval: "1h"}}
	apply(t, s, prepare(t, s, a, d, 0))
	if err := s.CheckLegacyTargetsWrite("nas"); err == nil {
		t.Fatal("unversioned write to a plan-referenced target accepted")
	}
	if err := s.CheckLegacyTargetsWrite("other"); err != nil {
		t.Fatal("unreferenced target refused:", err)
	}
}
