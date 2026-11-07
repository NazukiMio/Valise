package protection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/identity"
)

var missedAgent = identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "g1"}

func missedPlan(daily, tz string, accepted time.Time) ServerPlan {
	yes := true
	d := NewDefault()
	d.Schedule = Schedule{Enabled: &yes, DailyAt: daily, Timezone: tz}
	d.Period = Period{Unit: "week", Timezone: "UTC"}
	return ServerPlan{SchemaVersion: Schema, AgentRef: missedAgent, Revision: 1, Desired: d, AcceptedAt: accepted}
}
func timerRun(at time.Time, state string) BackupRun {
	return BackupRun{Schema: BackupRunSchema, AgentRef: missedAgent, Request: BackupParams{Reason: "timer"}, State: state, AcceptedAt: at}
}
func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

func TestScheduledTimesTimezoneAndDST(t *testing.T) {
	enabled := true
	ny := Schedule{Enabled: &enabled, DailyAt: "02:30", Timezone: "America/New_York"}
	// 2026-03-08 02:30 does not exist in New York; systemd skips it.
	got, err := ScheduledTimes(ny, utc(2026, 3, 6, 12, 0), utc(2026, 3, 10, 12, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{utc(2026, 3, 7, 7, 30), utc(2026, 3, 9, 6, 30), utc(2026, 3, 10, 6, 30)}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Fatal(i, got)
		}
	}
	// 2026-11-01 01:30 occurs twice; it is one slot at its first instant (EDT).
	ny.DailyAt = "01:30"
	got, _ = ScheduledTimes(ny, utc(2026, 10, 31, 0, 0), utc(2026, 11, 2, 23, 0))
	want = []time.Time{utc(2026, 10, 31, 5, 30), utc(2026, 11, 1, 5, 30), utc(2026, 11, 2, 6, 30)}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Fatal(i, got)
		}
	}
	// A zone east of UTC moves the local day across the UTC date line.
	sh := Schedule{Enabled: &enabled, DailyAt: "03:00", Timezone: "Asia/Shanghai"}
	got, _ = ScheduledTimes(sh, utc(2026, 11, 4, 18, 0), utc(2026, 11, 5, 18, 0))
	if len(got) != 1 || !got[0].Equal(utc(2026, 11, 4, 19, 0)) {
		t.Fatal(got)
	}
}

func TestComputeMissedAcceptanceWindowAndExpiry(t *testing.T) {
	now := utc(2026, 11, 7, 12, 0)
	// Accepted after today's 03:00 on 11-05: that slot is not counted.
	p := missedPlan("03:00", "UTC", utc(2026, 11, 5, 4, 0))
	got, doubt := ComputeMissed(p, nil, now)
	if doubt || len(got) != 2 || !got[0].ScheduledAt.Equal(utc(2026, 11, 6, 3, 0)) || got[0].State != "missed" || !got[1].ScheduledAt.Equal(utc(2026, 11, 7, 3, 0)) {
		t.Fatal(got)
	}
	// A failed timer run still started the slot; a later success expires
	// the missed one; a manual run is not a timer run.
	manual := timerRun(utc(2026, 11, 6, 3, 1), "succeeded")
	manual.Request.Reason = "manual"
	runs := []BackupRun{manual, timerRun(utc(2026, 11, 7, 3, 0), "failed")}
	got, _ = ComputeMissed(p, runs, now)
	if len(got) != 1 || got[0].State != "missed" {
		t.Fatal(got)
	}
	runs = append(runs, timerRun(utc(2026, 11, 7, 9, 0), "succeeded"))
	got, _ = ComputeMissed(p, runs, now)
	if len(got) != 1 || got[0].State != "expired" {
		t.Fatal(got)
	}
	// Another generation's runs do not count.
	other := timerRun(utc(2026, 11, 6, 3, 0), "succeeded")
	other.AgentRef.Generation = "g0"
	got, _ = ComputeMissed(p, append(runs, other), now)
	if len(got) != 1 || !got[0].ScheduledAt.Equal(utc(2026, 11, 6, 3, 0)) {
		t.Fatal(got)
	}
	// A slot inside the grace period is not yet missed.
	got, _ = ComputeMissed(p, nil, utc(2026, 11, 7, 3, 5))
	if len(got) != 1 || !got[0].ScheduledAt.Equal(utc(2026, 11, 6, 3, 0)) {
		t.Fatal(got)
	}
	// Disabled schedule: nothing is missed.
	no := false
	p.Schedule.Enabled = &no
	if got, _ = ComputeMissed(p, nil, now); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestComputeMissedThirtyDayWindowAndClock(t *testing.T) {
	now := utc(2026, 11, 7, 12, 0)
	p := missedPlan("03:00", "UTC", utc(2026, 1, 1, 0, 0))
	got, _ := ComputeMissed(p, nil, now)
	if len(got) != 30 || got[0].ScheduledAt.Before(now.Add(-MissedWindow)) || !got[29].ScheduledAt.Equal(utc(2026, 11, 7, 3, 0)) {
		t.Fatal(len(got), got[0])
	}
	// A run recorded in the future, or acceptance after now, flags the clock.
	if _, doubt := ComputeMissed(p, []BackupRun{timerRun(now.Add(time.Hour), "succeeded")}, now); !doubt {
		t.Fatal("future record not flagged")
	}
	if _, doubt := ComputeMissed(missedPlan("03:00", "UTC", now.Add(24*time.Hour)), nil, now); !doubt {
		t.Fatal("future acceptance not flagged")
	}
}

func TestComputeMissedAcrossDST(t *testing.T) {
	// Daily 02:30 New York across the spring-forward night: 03-08 does not exist.
	p := missedPlan("02:30", "America/New_York", utc(2026, 3, 6, 12, 0))
	runs := []BackupRun{timerRun(utc(2026, 3, 7, 7, 30), "succeeded")}
	got, _ := ComputeMissed(p, runs, utc(2026, 3, 10, 12, 0))
	if len(got) != 2 || !got[0].ScheduledAt.Equal(utc(2026, 3, 9, 6, 30)) || !got[1].ScheduledAt.Equal(utc(2026, 3, 10, 6, 30)) {
		t.Fatal(got)
	}
}

func TestReadTimerRunsReadOnlyAndTolerant(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, v any) {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	id := strings.Repeat("a", 32)
	r := timerRun(utc(2026, 11, 6, 3, 0), "succeeded")
	r.RunID = id
	write(id+".json", r)
	m := r
	m.RunID = strings.Repeat("b", 32)
	m.Request.Reason = "manual"
	write(m.RunID+".json", m)
	write(strings.Repeat("c", 32)+".json", map[string]string{"schema": "other"})
	write("runs.lock", nil)
	runs, bad, err := ReadTimerRuns(dir, missedAgent)
	if err != nil || bad != 1 || len(runs) != 1 || runs[0].RunID != id {
		t.Fatal(runs, bad, err)
	}
	if runs, bad, err := ReadTimerRuns(filepath.Join(dir, "missing"), missedAgent); err != nil || bad != 0 || runs != nil {
		t.Fatal(runs, bad, err)
	}
}

func TestPrepareParamsCatchUpAndStrictDesired(t *testing.T) {
	base := `{"agent_ref":{},"expected_revision":"0","desired":{"scope":{"kind":"preset","tier":"full"},"schedule":{"enabled":true,"daily_at":"03:00","timezone":"UTC"%s},"period":{"unit":"week","timezone":"UTC"},"server_copies":[]}}`
	var p PrepareParams
	if err := json.Unmarshal([]byte(strings.Replace(base, "%s", "", 1)), &p); err != nil || p.Desired.Schedule.CatchUp != nil || p.Desired.Schedule.CatchUpEnabled() {
		t.Fatal("protection-plan/1 request changed meaning", err)
	}
	if err := json.Unmarshal([]byte(strings.Replace(base, "%s", `,"catch_up":true`, 1)), &p); err != nil || !p.Desired.Schedule.CatchUpEnabled() {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.Replace(base, "%s", `,"catch_up_v3":true`, 1)), &p); err == nil {
		t.Fatal("unknown desired field silently dropped")
	}
	// A plan without catch_up serialises exactly as before.
	b, _ := json.Marshal(Schedule{DailyAt: "03:00", Timezone: "UTC"})
	if strings.Contains(string(b), "catch_up") {
		t.Fatal(string(b))
	}
}

// A run that starts late within the slot's window (Persistent=true after the
// server came back) keeps a caught_up mark with its start; one inside the
// grace period is an ordinary on-time run.
func TestComputeMissedCaughtUp(t *testing.T) {
	now := utc(2026, 11, 7, 12, 0)
	p := missedPlan("03:00", "UTC", utc(2026, 11, 5, 4, 0))
	runs := []BackupRun{timerRun(utc(2026, 11, 6, 7, 30), "succeeded"), timerRun(utc(2026, 11, 7, 3, 10), "succeeded")}
	got, _ := ComputeMissed(p, runs, now)
	if len(got) != 1 || got[0].State != "caught_up" || !got[0].ScheduledAt.Equal(utc(2026, 11, 6, 3, 0)) ||
		got[0].RanAt == nil || !got[0].RanAt.Equal(utc(2026, 11, 6, 7, 30)) {
		t.Fatal(got)
	}
	// The earliest run in the window decides.
	runs = append(runs, timerRun(utc(2026, 11, 6, 3, 1), "failed"))
	if got, _ = ComputeMissed(p, runs, now); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestComputeCopyStatus(t *testing.T) {
	p := missedPlan("03:00", "UTC", utc(2026, 11, 1, 0, 0))
	nas := LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: "a"}
	p.ServerCopies = []ServerCopy{{TargetRef: nas}, {TargetRef: LocationRef{Kind: "legacy_target", ID: "never", AuthorityID: "a"}}}
	ok := timerRun(utc(2026, 11, 5, 3, 0), "succeeded")
	ok.Components = []BackupComponent{{Kind: "server_copy", TargetRef: &nas, State: "succeeded"}}
	ok.UpdatedAt = utc(2026, 11, 5, 3, 1)
	bad := timerRun(utc(2026, 11, 6, 3, 0), "partial")
	bad.RunID = "r2"
	bad.UpdatedAt = utc(2026, 11, 6, 3, 1)
	bad.Components = []BackupComponent{{Kind: "server_copy", TargetRef: &nas, State: "failed", FailureCause: "connect_timeout"}}
	skipped := timerRun(utc(2026, 11, 6, 9, 0), "succeeded")
	skipped.Components = []BackupComponent{{Kind: "server_copy", TargetRef: &nas, State: "skipped"}}
	got := ComputeCopyStatus(p, []BackupRun{ok, bad, skipped})
	if len(got) != 1 || got[0].State != "failed" || got[0].FailureCause != "connect_timeout" || got[0].RunID != "r2" || !got[0].At.Equal(utc(2026, 11, 6, 3, 1)) {
		t.Fatal(got)
	}
}
