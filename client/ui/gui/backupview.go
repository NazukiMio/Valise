package gui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/protection"
)

func backupUnresolved(state string) bool {
	switch state {
	case "running", "cancelling", "reconciling", "needs_recovery":
		return true
	}
	return false
}

func backupIsRunning(br *backupRun) bool {
	return br.j.busy && br.task != nil && (br.task.State == "running" || br.task.State == "cancelling")
}

func backupDuration(d time.Duration) string {
	seconds := max(0, int(d.Seconds()))
	if seconds < 60 {
		return T("backup.duration.seconds", "n", seconds)
	}
	return T("backup.duration.minutes", "m", seconds/60, "s", seconds%60)
}

// Unknown results freeze at the last server observation; they are not a
// live clock or evidence that the executor has stopped.
func backupClock(br *backupRun, now time.Time) string {
	start, end, key := br.started, br.ended, "backup.clock.finished"
	if br.j.busy {
		end, key = now, "backup.clock.running"
	}
	if br.task != nil && br.task.Run != nil {
		run := br.task.Run
		start = run.AcceptedAt
		switch {
		case br.task.State == "reconciling" || br.task.State == "needs_recovery":
			end, key = run.UpdatedAt, "backup.clock.observed"
		case !backupUnresolved(br.task.State):
			end, key = run.UpdatedAt, "backup.clock.finished"
		case !br.j.busy:
			end, key = run.UpdatedAt, "backup.clock.observed"
		}
	}
	if br.task != nil && br.task.Run == nil && backupUnresolved(br.task.State) && !br.j.busy {
		return T("backup.clock.unknown", "at", br.task.ObservedAt.Local().Format("15:04:05"))
	}
	if start.IsZero() || end.IsZero() {
		return ""
	}
	return T(key, "at", start.Local().Format("15:04:05"), "duration", backupDuration(end.Sub(start)))
}

func backupComponentName(comp protection.BackupComponent) string {
	if comp.Kind == "server_copy" && comp.TargetRef != nil {
		return T("backup.part.copy", "name", comp.TargetRef.ID)
	}
	return T("backup.part.server")
}

func backupComponentLine(c *ui.Context, comp protection.BackupComponent, now time.Time, live bool) {
	mark, ink, state := "○", palette.TextSub, T("backup.state.waiting")
	switch comp.State {
	case "succeeded":
		mark, ink, state = "✓", palette.Ok, T("backup.state.done")
	case "failed":
		mark, ink, state = "✗", palette.Warn, T("backup.state.failed")
	case "unknown":
		mark, ink, state = "?", palette.Warn, T("backup.state.unknown")
	case "skipped":
		state = T("backup.state.skipped")
	case "running":
		mark, ink, state = "▸", palette.Text, T("backup.state.running")
		if !live {
			mark, ink, state = "?", palette.Warn, T("backup.state.unknown")
		}
	}
	if comp.StartedAt != nil {
		end := comp.CompletedAt
		if comp.State == "running" && live {
			end = &now
		}
		if end != nil {
			state += T("common.dot_sep") + backupDuration(end.Sub(*comp.StartedAt))
		}
	}
	ui.Column(c).Gap(4).Padding(8, 12).Radius(8).Background(palette.Board).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, mark).Bold().TextColor(ink).Width(16)
			ui.Text(c, backupComponentName(comp)).Bold().Grow(1).Wrap()
			muted(ui.Text(c, state)).Wrap()
			if mark == "▸" {
				ui.Spinner(c)
			}
		})
		if comp.State == "failed" || comp.State == "unknown" {
			muted(ui.Text(c, backupFailure(comp))).Wrap()
		}
	})
}

func backupFailure(comp protection.BackupComponent) string {
	key := "backup.failure.other"
	if comp.Kind == "server_copy" {
		keys := map[string]string{
			"host_unknown": "backup.failure.host_unknown", "connect_refused": "backup.failure.connect_refused",
			"connect_timeout": "backup.failure.connect_timeout", "auth_denied": "backup.failure.auth_denied",
			"host_key_unverified": "backup.failure.host_key_unverified", "not_writable": "backup.failure.not_writable",
			"tool_missing": "backup.failure.tool_missing",
		}
		if k := keys[comp.FailureCause]; k != "" {
			key = k
		}
	}
	return T(key)
}
