package protection

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"valise/core/identity"
)

const (
	// MissedWindow bounds how far back missed timer runs are reported.
	MissedWindow = 30 * 24 * time.Hour
	// missedGrace leaves room for systemd's default accuracy and start-up
	// before an unstarted slot is reported as missed.
	missedGrace = 15 * time.Minute
	// clockSkewTolerance is how far a record may lie in the future before the
	// server clock is reported as doubtful.
	clockSkewTolerance = 5 * time.Minute
)

// MissedRun is one scheduled time with no timer run started before the next
// scheduled time. State "expired" means a later timer run has succeeded.
// State "caught_up" means its run started late, after the grace period (the
// server was down and Persistent=true ran it once on return); RanAt is then
// the run's start, shown as "服务器从停机状态恢复".
type MissedRun struct {
	ScheduledAt time.Time  `json:"scheduled_at"`
	State       string     `json:"state"`
	RanAt       *time.Time `json:"ran_at,omitempty"`
}

var goWeekday = map[string]time.Weekday{
	"Mon": time.Monday,
	"Tue": time.Tuesday,
	"Wed": time.Wednesday,
	"Thu": time.Thursday,
	"Fri": time.Friday,
	"Sat": time.Saturday,
	"Sun": time.Sunday,
}

// ScheduledTimes lists the schedule's trigger times in (from, to]. A local
// time skipped by a DST change does not occur, as with systemd's OnCalendar;
// a repeated local time occurs once, at its first instant.
func ScheduledTimes(s Schedule, from, to time.Time) ([]time.Time, error) {
	yes := true
	check := s
	check.Enabled = &yes
	if err := check.validate(); err != nil {
		return nil, errors.New("schedule invalid")
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, err
	}
	cadence := s.EffectiveCadence()
	start := from.In(loc)
	end := to.In(loc)
	// Iterate calendar days at local noon, which no DST change crosses.
	day := time.Date(start.Year(), start.Month(), start.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, -1)
	last := time.Date(end.Year(), end.Month(), end.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, 1)
	var out []time.Time
	if cadence == "hourly" {
		mm := 0
		if s.DailyAt != "" {
			mm, _ = strconv.Atoi(s.DailyAt[3:])
		}
		for ; !day.After(last); day = day.AddDate(0, 0, 1) {
			for hh := 0; hh < 24; hh += s.EveryHours {
				t := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc)
				if t.Hour() != hh || t.Minute() != mm || t.Day() != day.Day() {
					continue // nonexistent local time
				}
				if t.After(from) && !t.After(to) {
					out = append(out, t.UTC())
				}
			}
		}
	} else {
		hh, _ := strconv.Atoi(s.DailyAt[:2])
		mm, _ := strconv.Atoi(s.DailyAt[3:])
		var allowedDays map[time.Weekday]bool
		if cadence == "weekly" {
			allowedDays = make(map[time.Weekday]bool, len(s.Weekdays))
			for _, w := range s.Weekdays {
				allowedDays[goWeekday[w]] = true
			}
		}
		for ; !day.After(last); day = day.AddDate(0, 0, 1) {
			if cadence == "weekly" && !allowedDays[day.Weekday()] {
				continue
			}
			if cadence == "monthly" && day.Day() != s.DayOfMonth {
				continue
			}
			t := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc)
			if t.Hour() != hh || t.Minute() != mm || t.Day() != day.Day() {
				continue // nonexistent local time
			}
			if t.After(from) && !t.After(to) {
				out = append(out, t.UTC())
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	dedup := out[:0]
	for _, t := range out {
		if len(dedup) == 0 || !dedup[len(dedup)-1].Equal(t) {
			dedup = append(dedup, t)
		}
	}
	return dedup, nil
}

// ComputeMissed is pure: it reports, oldest first, the scheduled times of the
// accepted plan after its acceptance and within MissedWindow before now that
// have no timer run started before the next scheduled time. runs must belong
// to the plan's Agent; non-timer runs are ignored. The bool is true when the
// records suggest the server clock is wrong.
func ComputeMissed(plan ServerPlan, runs []BackupRun, now time.Time) ([]MissedRun, bool) {
	out := []MissedRun{}
	clockDoubt := now.Before(plan.AcceptedAt.Add(-clockSkewTolerance))
	timerRuns := make([]BackupRun, 0, len(runs))
	for _, r := range runs {
		if r.Request.Reason != "timer" || r.AgentRef != plan.AgentRef {
			continue
		}
		if r.AcceptedAt.After(now.Add(clockSkewTolerance)) {
			clockDoubt = true
		}
		timerRuns = append(timerRuns, r)
	}
	if plan.Schedule.Enabled == nil || !*plan.Schedule.Enabled {
		return out, clockDoubt
	}
	from := plan.AcceptedAt
	if w := now.Add(-MissedWindow); w.After(from) {
		from = w
	}
	// Extra horizon gives the last reported slot its following slot.
	horizon := 48 * time.Hour
	switch plan.Schedule.EffectiveCadence() {
	case "weekly", "monthly":
		horizon = 35 * 24 * time.Hour
	}
	slots, err := ScheduledTimes(plan.Schedule, from, now.Add(horizon))
	if err != nil {
		return out, clockDoubt
	}
	for i, slot := range slots {
		if slot.Add(missedGrace).After(now) {
			break
		}
		var next time.Time
		if i+1 < len(slots) {
			next = slots[i+1]
		}
		var ranAt *time.Time
		laterSuccess := false
		for _, r := range timerRuns {
			if !r.AcceptedAt.Before(slot) && (next.IsZero() || r.AcceptedAt.Before(next)) {
				if t := r.AcceptedAt; ranAt == nil || t.Before(*ranAt) {
					ranAt = &t
				}
				continue
			}
			if r.AcceptedAt.After(slot) && r.State == "succeeded" {
				laterSuccess = true
			}
		}
		if ranAt != nil {
			if ranAt.Sub(slot) > missedGrace {
				out = append(out, MissedRun{ScheduledAt: slot, State: "caught_up", RanAt: ranAt})
			}
			continue
		}
		state := "missed"
		if laterSuccess {
			state = "expired"
		}
		out = append(out, MissedRun{ScheduledAt: slot, State: state})
	}
	return out, clockDoubt
}

// ReadTimerRuns reads, without locking or writing, the backup run records in
// dir that belong to agent and were started by the timer. Records that cannot
// be parsed are counted, not fatal: the result only feeds a read-only view.
func ReadTimerRuns(dir string, agent identity.AgentRef) ([]BackupRun, int, error) {
	return readRuns(dir, agent, true)
}

// ReadRuns is ReadTimerRuns for runs of any reason.
func ReadRuns(dir string, agent identity.AgentRef) ([]BackupRun, int, error) {
	return readRuns(dir, agent, false)
}

func readRuns(dir string, agent identity.AgentRef, timerOnly bool) ([]BackupRun, int, error) {
	es, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var runs []BackupRun
	unreadable := 0
	for _, e := range es {
		if filepath.Ext(e.Name()) != ".json" || !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			unreadable++
			continue
		}
		var r BackupRun
		if json.Unmarshal(b, &r) != nil || r.Schema != BackupRunSchema || r.AcceptedAt.IsZero() || r.RunID+".json" != e.Name() {
			unreadable++
			continue
		}
		if r.AgentRef != agent || (timerOnly && r.Request.Reason != "timer") {
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].AcceptedAt.Before(runs[j].AcceptedAt) })
	return runs, unreadable, nil
}

// CopyStatus is the latest attempt to copy to one location of the plan
// (contracts 16.G2). State is succeeded, failed or unknown.
type CopyStatus struct {
	TargetRef    LocationRef `json:"target_ref"`
	At           time.Time   `json:"at"`
	RunID        string      `json:"run_id"`
	State        string      `json:"state"`
	FailureCause string      `json:"failure_cause,omitempty"`
}

// ComputeCopyStatus is pure: for each of the plan's copy locations, the
// newest run (runs oldest first) that attempted it. Skipped components are
// not attempts; a location never attempted is left out.
func ComputeCopyStatus(plan ServerPlan, runs []BackupRun) []CopyStatus {
	out := []CopyStatus{}
	for _, c := range plan.ServerCopies {
		var latest *CopyStatus
		for _, r := range runs {
			for _, comp := range r.Components {
				if comp.Kind != "server_copy" || comp.TargetRef == nil || comp.TargetRef.ID != c.TargetRef.ID {
					continue
				}
				if comp.State != "succeeded" && comp.State != "failed" && comp.State != "unknown" {
					continue
				}
				at := r.UpdatedAt
				if comp.CompletedAt != nil {
					at = *comp.CompletedAt
				}
				latest = &CopyStatus{TargetRef: c.TargetRef, At: at, RunID: r.RunID, State: comp.State, FailureCause: comp.FailureCause}
			}
		}
		if latest != nil {
			out = append(out, *latest)
		}
	}
	return out
}
