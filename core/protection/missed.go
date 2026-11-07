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

// ScheduledTimes lists the daily schedule's trigger times in (from, to]. A
// local time skipped by a DST change does not occur, as with systemd's
// OnCalendar; a repeated local time occurs once, at its first instant.
func ScheduledTimes(s Schedule, from, to time.Time) ([]time.Time, error) {
	if !dailyRE.MatchString(s.DailyAt) || !ValidTimezone(s.Timezone) {
		return nil, errors.New("schedule invalid")
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, err
	}
	hh, _ := strconv.Atoi(s.DailyAt[:2])
	mm, _ := strconv.Atoi(s.DailyAt[3:])
	start := from.In(loc)
	end := to.In(loc)
	// Iterate calendar days at local noon, which no DST change crosses.
	day := time.Date(start.Year(), start.Month(), start.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, -1)
	last := time.Date(end.Year(), end.Month(), end.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, 1)
	var out []time.Time
	for ; !day.After(last); day = day.AddDate(0, 0, 1) {
		t := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc)
		if t.Hour() != hh || t.Minute() != mm || t.Day() != day.Day() {
			continue // nonexistent local time
		}
		if t.After(from) && !t.After(to) {
			out = append(out, t.UTC())
		}
	}
	return out, nil
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
	// Two extra days give the last reported slot its following slot.
	slots, err := ScheduledTimes(plan.Schedule, from, now.Add(48*time.Hour))
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
		if r.AgentRef != agent || r.Request.Reason != "timer" {
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].AcceptedAt.Before(runs[j].AcceptedAt) })
	return runs, unreadable, nil
}
