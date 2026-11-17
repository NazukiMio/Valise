package gui

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/ui/service"
)

// restoreStages is where a restore stands, as five stops the person can
// see ahead of them; the words come from the catalog in order.
func restoreStages() []string {
	return []string{T("restore.stage.choose"), T("restore.stage.plan"), T("restore.stage.prepare"),
		T("restore.stage.confirm"), T("restore.stage.result")}
}

// restoreStageAt is the stop a flow step belongs to.
func restoreStageAt(step string) int {
	switch step {
	case "setup":
		return 0
	case "plan":
		return 1
	case "prepare":
		return 2
	case "confirm", "swap":
		return 3
	}
	return 4
}

// restoreStepper draws the stops: the ones behind are checked, the one at
// hand is filled, the ones ahead are outlines. It only shows where the flow
// is; it is not a way to jump between steps.
func restoreStepper(c *ui.Context, step string) {
	at := restoreStageAt(step)
	names := restoreStages()
	ui.Row(c).Gap(spInside).AlignItems(ui.Center).Padding(spHair, 0).Role(ui.RoleGroup).Label(names[at]).Children(func() {
		for i, name := range names {
			ic := c.Key("stage-" + strconv.Itoa(i))
			if i > 0 {
				line := palette.Line
				if i <= at {
					line = palette.Brand.Alpha(0.6)
				}
				ui.Box(c.Key("line-" + strconv.Itoa(i))).Height(1.5).Grow(1).MinWidth(16).Background(line)
			}
			ui.Row(ic).Gap(spInside).AlignItems(ui.Center).Shrink(0).Children(func() {
				mark := ui.Box(ic).Size(22, 22).Radius(11).Justify(ui.Center).AlignItems(ui.Center).
					Transition(ui.ElementTransition{Duration: motionBase, Colors: true})
				ink := palette.TextSub
				switch {
				case i < at:
					mark.Background(palette.Brand.Alpha(0.14)).Children(func() {
						ui.Icon(ic, icons.check).FontSize(13).TextColor(palette.Brand)
					})
				case i == at:
					ink = palette.Text
					mark.Background(palette.Brand).Children(func() {
						ui.Text(ic, strconv.Itoa(i+1)).FontSize(fsCaption).Bold().TextColor(palette.Card)
					})
				default:
					mark.Border(1.5, palette.Line).Children(func() {
						muted(ui.Text(ic, strconv.Itoa(i+1))).FontSize(fsCaption)
					})
				}
				t := ui.Text(ic, name).FontSize(fsSub).TextColor(ink)
				if i == at {
					t.Bold()
				}
			})
		}
	})
}

// restoreOutcome is one line of a restore's result with its own mark: the
// files, the program, the service and the health are told apart
// (AGENTS.md 7.5).
type restoreOutcome struct{ tone, label, value string }

func restoreOutcomes(t service.RestoreTask) []restoreOutcome {
	unknown := T("restore.unknown")
	var out []restoreOutcome
	if t.PreBackupSnapshot != "" {
		out = append(out, restoreOutcome{"ok", T("restore.backup_current_result"), T("restore.checked")})
	}
	if t.Verified {
		out = append(out, restoreOutcome{"ok", T("restore.stage_verified"), T("restore.checked")})
	} else {
		out = append(out, restoreOutcome{"", T("restore.stage_verified"), unknown})
	}
	dirTone := ""
	switch t.SwapState {
	case revive.StateHealthy, revive.StateRestored:
		dirTone = "ok"
	case revive.StateRolledBack:
		dirTone = "warn"
	}
	directory := T("restore.journal." + strings.ToLower(orText(t.SwapState, "unknown")))
	if t.Rollback != nil {
		directory, dirTone = T("restore.rollback.directory."+t.Rollback.Directories), "warn"
	}
	out = append(out, restoreOutcome{dirTone, T("restore.directory"), directory})
	runtime, health := restoreOutcome{"", T("restore.service_result"), unknown}, restoreOutcome{"", T("restore.health_result"), unknown}
	switch {
	case t.Rollback != nil:
		out = append(out, restoreOutcome{"", T("restore.body"), T("restore.rollback.program." + t.Rollback.Program)})
		runtime.value = T("restore.rollback.service." + t.Rollback.Service)
		health.value = T("restore.rollback.health." + t.Rollback.Health)
	case t.State == "succeeded" && t.SwapState == revive.StateHealthy:
		runtime.tone, runtime.value = "ok", T("restore.service_checked")
		health.tone, health.value = "ok", T("restore.health_checked")
	case t.SwapState == revive.StateRestored:
		runtime.tone, runtime.value = "stop", T("restore.no_start")
		health.value = T("restore.not_checked")
	}
	out = append(out, runtime, health)
	if t.Relation {
		out = append(out, restoreOutcome{"ok", T("restore.relation_result"), T("restore.relation_checked")})
	} else {
		out = append(out, restoreOutcome{"", T("restore.relation_result"), T("restore.no_relation")})
	}
	return out
}

// restoreResultTone is the result title's tone: only a finished restore
// whose service was checked is calm; what is unknown is said as unknown.
func restoreResultTone(t service.RestoreTask) string {
	switch t.State {
	case "succeeded":
		if t.SwapState == revive.StateHealthy {
			return "ok"
		}
		if t.SwapState == revive.StateRestored {
			return "" // files only, as asked: not a warning, not a success
		}
		return "warn"
	case "needs_recovery", "failed":
		return "danger"
	case "cancelled":
		return ""
	}
	return "warn"
}

// pointFact is a small label over its value, for the facts of the chosen
// time point at the top of the restore page.
func pointFact(c *ui.Context, label, value string) {
	ui.Column(c.Key("pf-" + label)).Gap(2).Children(func() {
		muted(ui.Text(c, label))
		ui.Text(c, value).FontSize(fsBody).Bold()
	})
}
