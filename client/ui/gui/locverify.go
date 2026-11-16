package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/vault"
	"valise/ui/service"
)

// Checking a period's copies, and deleting one period at a location
// (ARCHITECTURE.md 7.1.1, VAULT_FORMAT.md 10.4): before deleting, every
// other place is checked on the spot; places whose packs must be
// downloaded wait for the person, and skipped ones count as unchecked.
// The server decides what may be deleted; the check only helps decide.

// verifyRun is one check, maybe on the way to deleting.
type verifyRun struct {
	host, leaf, where string
	del               bool
	j                 job
	rep               *service.CopyReport
	pending           []string // places that need pack downloads
	pendingBytes      int64
	sure              bool // with no other complete copy: “I know these cannot be restored”
	delJ              job
	deleted           bool
}

func (a *App) startVerify(host, leaf, where string, del bool) {
	ls := a.locs()
	ls.verify = &verifyRun{host: host, leaf: leaf, where: where, del: del}
	a.runVerify(false)
}

// runVerify checks; without download it only notes the places that need
// pack downloads.
func (a *App) runVerify(download bool) {
	v := a.locs().verify
	o := service.VerifyOptions{Packs: true}
	if v.del {
		o.Skip = map[string]bool{v.where: true}
	} else {
		o.Only = map[string]bool{v.where: true}
	}
	var pending []string
	var total int64
	if !download {
		o.Confirm = func(place string, bytes int64) bool {
			pending, total = append(pending, place), total+bytes
			return false
		}
	}
	var rep *service.CopyReport
	a.start(&v.j, 2*time.Hour, func(ctx context.Context) (err error) {
		a.says(&v.j)(T("loc.verifying"))
		rep, err = a.svc.VerifyCopies(ctx, v.host, v.leaf, o)
		return err
	}, func(err error) {
		if err == nil {
			v.rep, v.pending, v.pendingBytes = rep, pending, total
		}
	})
}

func (a *App) verifyDialog(c *ui.Context) {
	ls := a.locs()
	v := ls.verify
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 520).Children(func() {
			title := T("loc.verify_title", "period", periodText(v.leaf))
			if v.del {
				title = T("loc.delete_title", "where", v.where, "who", a.agentOfLeaf(v.host, v.leaf), "period", periodText(v.leaf))
			}
			dialogTitle(ui.Text(c, title)).Wrap()
			if v.deleted {
				ui.Text(c, T("loc.deleted")).TextColor(palette.Ok).Bold()
				ui.Row(c).Justify(ui.End).Children(func() {
					ui.PrimaryButton(c, T("common.done")).OnClick(func() { ls.verify = nil })
				})
				return
			}
			if v.j.busy {
				a.jobLine(c, &v.j)
				return
			}
			if v.j.err != nil {
				errorBox(c, v.j.err, a.fold("verify-err"))
			}
			if v.rep != nil {
				a.reportLines(c, v.rep)
			}
			if len(v.pending) > 0 {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					muted(ui.Text(c, T("loc.need_download", "places", joinList(v.pending), "size", size(v.pendingBytes)))).Wrap().Grow(1)
					ui.Button(c, T("loc.download_check")).OnClick(func() { a.runVerify(true) })
				})
			}
			if v.del && v.rep != nil {
				a.deleteButtons(c, v)
				return
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.close")).OnClick(func() { ls.verify = nil })
			})
		})
	}).Dismissed() && !v.j.busy && !v.delJ.busy {
		ls.verify = nil
	}
}

// reportLines are the places of a check, each with what it holds.
func (a *App) reportLines(c *ui.Context, rep *service.CopyReport) {
	for _, ch := range rep.Checks {
		if ch.State == service.CopyAbsent {
			continue
		}
		ink := palette.TextSub
		switch ch.State {
		case service.CopyComplete:
			ink = palette.Ok
		case service.CopyIncomplete, service.CopyUnknown:
			ink = palette.Warn
		}
		ui.Row(c.Key("rl-"+ch.Where)).Gap(10).Padding(8, 12).Radius(8).Background(palette.Board).Children(func() {
			ui.Text(c, service.CopyPlace(ch)).Bold().Width(150)
			ui.Text(c, service.CopyStateText(ch)).FontSize(12).TextColor(ink).Wrap().Grow(1)
		})
	}
}

// deleteButtons: one confirmation when another place holds a complete
// copy, else the person first says they know it cannot be restored.
func (a *App) deleteButtons(c *ui.Context, v *verifyRun) {
	ls := a.locs()
	safe := v.rep.CompleteElsewhere(v.where)
	if safe {
		muted(ui.Text(c, T("loc.delete_safe", "where", v.where))).Wrap()
	} else {
		ui.Text(c, T("loc.delete_unsafe")).TextColor(palette.Warn).Wrap()
		ui.Checkbox(c, &v.sure, T("loc.delete_sure"))
	}
	if v.delJ.busy {
		a.jobLine(c, &v.delJ)
		return
	}
	if v.delJ.err != nil {
		errorBox(c, v.delJ.err, a.fold("delete-err"))
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		ui.Button(c, T("common.cancel")).OnClick(func() { ls.verify = nil })
		b := ui.PrimaryButton(c, T("loc.delete_go")).OnClick(func() {
			a.start(&v.delJ, 10*time.Minute, func(ctx context.Context) error {
				return a.svc.TargetLeafDelete(ctx, v.host, v.where, v.leaf)
			}, func(err error) {
				if err == nil {
					v.deleted = true
					ls.read = false
				}
			})
		})
		if !safe && !v.sure {
			b.Disabled(true)
		}
	})
}

func (a *App) agentOfLeaf(host, leaf string) string {
	who, _, _ := vault.SplitLeaf(leaf)
	return a.agentName(host, who)
}
