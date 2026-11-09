package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/valise"
	"valise/ui/service"
)

// Removing a server (docs/ARCHITECTURE.md 2.3): only from this computer,
// or also cleaning Valise's things on the server, item by item. The
// backup library always stays.

type leaveState struct {
	open    bool
	mode    string // "here" or "clean"
	readJ   job
	left    *service.ServerLeftovers
	offline error // the read failed: only removing here is possible
	items   map[string]bool
	steps   []service.LeaveStep
	j       job
}

func (a *App) openLeave(name string) {
	ss := a.settingsOf(name)
	ls := &leaveState{open: true, mode: "here", items: map[string]bool{
		service.LeaveTimers: true, service.LeaveTools: true, service.LeaveClouds: true, service.LeavePorter: true, service.LeavePasses: true}}
	ss.leave = ls
	var l *service.ServerLeftovers
	a.start(&ls.readJ, time.Minute, func(ctx context.Context) (err error) {
		a.says(&ls.readJ)(T("leave.reading", "name", name))
		l, err = a.svc.ServerLeftovers(ctx, name)
		return err
	}, func(err error) {
		ls.left, ls.offline = l, err
		if err != nil {
			ls.left = a.svc.LastLeftovers(name)
		}
	})
}

func (a *App) leaveDialog(c *ui.Context, name string, ss *settingsState) {
	ls := ss.leave
	if ui.Modal(c, &ls.open, func() {
		ui.Column(c).Width(560).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("leave.dialog_title", "name", name)).FontSize(18).Bold()
			switch {
			case ls.readJ.busy:
				a.jobLine(c, &ls.readJ)
			case ls.j.busy || len(ls.steps) > 0 || ls.j.err != nil:
				a.leaveProgress(c, name, ls)
				return
			case ls.offline != nil:
				a.leaveOffline(c, name, ls)
			default:
				a.leaveChoice(c, ls)
			}
			a.leaveButtons(c, name, ls)
		})
	}).Dismissed() && !ls.j.busy {
		a.stopJob(&ls.readJ)
		ss.leave = nil
	}
}

func (a *App) leaveChoice(c *ui.Context, ls *leaveState) {
	ui.RadioGroup(c, func() {
		ui.Radio(c, &ls.mode, "here", T("leave.here"))
		muted(ui.Text(c, T("leave.here_what"))).Wrap().Padding(0, 0, 4, 26)
		ui.Radio(c, &ls.mode, "clean", T("leave.clean"))
		muted(ui.Text(c, T("leave.clean_what"))).Wrap().Padding(0, 0, 0, 26)
	}).Label(T("leave.title"))
	if ls.mode == "clean" {
		l := ls.left
		ui.Column(c).Gap(6).Padding(0, 0, 0, 26).Children(func() {
			for _, item := range []string{service.LeaveTimers, service.LeaveTools, service.LeaveClouds, service.LeavePorter, service.LeavePasses} {
				if !l.Has(item) {
					continue
				}
				on := ls.items[item]
				if ui.Checkbox(c.Key("li-"+item), &on, leaveItemLabel(item, l)).Changed() {
					ls.items[item] = on
				}
				if note := leaveItemNote(item, l); note != "" {
					muted(ui.Text(c, note)).Wrap().Padding(0, 0, 0, 26)
				}
			}
			if l.Has(service.LeaveSkills) {
				ui.Column(c).Gap(6).Padding(10, 12).Radius(10).Background(palette.TroubleBg).Children(func() {
					on := ls.items[service.LeaveSkills]
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						if ui.Checkbox(c, &on, T("leave.item.skills")).Changed() {
							ls.items[service.LeaveSkills] = on
						}
						ui.Text(c, T("leave.skills_not_advised")).FontSize(12).Bold().TextColor(palette.Warn)
					})
					muted(ui.Text(c, T("leave.skills_why"))).Wrap()
					for _, sk := range l.Skills {
						muted(ui.Text(c, T("leave.skills_of", "agent", orText(sk.Label, sk.Instance), "n", len(sk.Skills)))).Wrap()
					}
				})
			}
		})
	}
	ui.Column(c).Gap(4).Padding(10, 12).Radius(10).Background(palette.Board).Children(func() {
		ui.Text(c, T("leave.vault_kept")).Bold()
		muted(ui.Text(c, T("leave.vault_kept_what"))).Wrap()
	})
}

func (a *App) leaveOffline(c *ui.Context, name string, ls *leaveState) {
	ls.mode = "here"
	reason := a.serverState(name, time.Now()).Reason
	if ue, ok := valise.As(ls.offline); ok && ue.Reason != "" {
		reason = ue.Reason
	}
	if reason == "" {
		ui.Text(c, T("leave.offline_plain", "name", name)).Wrap()
	} else {
		ui.Text(c, T("leave.offline", "name", name, "reason", reason)).Wrap()
	}
	ui.Column(c).Gap(4).Padding(10, 12).Radius(10).Background(palette.Board).Children(func() {
		l := ls.left
		if l == nil || l.ReadAt.IsZero() {
			ui.Text(c, T("leave.left_unknown")).Bold()
			return
		}
		ui.Text(c, T("leave.left_title", "when", when(l.ReadAt, time.Now()))).Bold()
		muted(ui.Text(c, strings.Join(leftoverWhere(l), T("common.dot_sep")))).Wrap()
	})
}

func (a *App) leaveButtons(c *ui.Context, name string, ls *leaveState) {
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		ui.Button(c, T("common.cancel")).OnClick(func() { ls.open = false })
		if ls.readJ.busy {
			return
		}
		if ls.mode == "here" {
			ui.PrimaryButton(c, T("leave.go_here")).OnClick(func() { a.removeHere(name, ls) })
			return
		}
		ui.PrimaryButton(c, T("leave.go_clean")).OnClick(func() { a.runLeave(name, ls) })
	})
}

func (a *App) removeHere(name string, ls *leaveState) {
	if err := a.svc.RemoveServerHere(name, ls.left); err != nil {
		ls.j.err = err
		return
	}
	a.leaveDone(name)
}

func (a *App) leaveDone(name string) {
	delete(a.settings, name)
	delete(a.passes, name)
	a.refreshServers()
	a.refreshAgents()
	a.router.Push("/servers")
}

func (a *App) runLeave(name string, ls *leaveState) {
	ls.steps = nil
	opts := service.LeaveOptions{Items: ls.items, Left: ls.left,
		Doing: func(_, what string) { a.says(&ls.j)(what) },
		Step:  func(st service.LeaveStep) { a.update(func() { ls.steps = append(ls.steps, st) }) }}
	a.start(&ls.j, 20*time.Minute, func(ctx context.Context) error { return a.svc.LeaveServer(ctx, name, opts) },
		func(err error) {
			if err == nil {
				a.leaveDone(name)
			}
		})
}

// leaveProgress shows each chosen item: done, running, failed or not run.
func (a *App) leaveProgress(c *ui.Context, name string, ls *leaveState) {
	done := map[string]service.LeaveStep{}
	for _, s := range ls.steps {
		done[s.Item] = s
	}
	next := true
	var left []string
	for _, item := range service.LeaveOrder {
		if !(item == service.LeaveMark || ls.items[item]) || !ls.left.Has(item) {
			continue
		}
		s, ok := done[item]
		mark, ink, state := "○", palette.TextSub, T("leave.state.not_run")
		switch {
		case ok:
			mark, ink, state = "✓", palette.Ok, orText(s.Detail, T("leave.state.done"))
		case next && ls.j.busy:
			mark, ink, state, next = "▸", palette.Text, a.doingText(ls.j.doing, ls.j.doingAt, c.Now()), false
		case next && ls.j.err != nil:
			mark, ink, state, next = "✗", palette.Warn, T("leave.state.failed"), false
		}
		if !ok && item != service.LeaveMark {
			left = append(left, leaveItemLabel(item, ls.left))
		}
		ui.Row(c.Key("lp-"+item)).Gap(12).Padding(8, 12).Radius(8).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Text(c, mark).Bold().TextColor(ink).Width(16)
			ui.Text(c, leaveItemLabel(item, ls.left)).Bold().Width(200)
			muted(ui.Text(c, state)).Wrap().Grow(1)
			if mark == "▸" {
				ui.Spinner(c)
			}
		})
	}
	if ls.j.busy {
		ui.Row(c).Justify(ui.End).Children(func() { ui.Button(c, T("common.stop")).OnClick(func() { a.stopJob(&ls.j) }) })
		return
	}
	if ls.j.err == nil {
		return
	}
	errorBox(c, ls.j.err, a.fold("leave-err-"+name))
	ui.Text(c, T("leave.stopped", "name", name, "left", strings.Join(left, T("common.list_sep")))).Wrap()
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		ui.Button(c, T("leave.go_here")).OnClick(func() { a.removeHere(name, ls) })
		ui.PrimaryButton(c, T("common.retry")).OnClick(func() { a.retryLeave(name, ls) })
	})
}

// retryLeave reads again (what is left changed) and runs the rest.
func (a *App) retryLeave(name string, ls *leaveState) {
	var l *service.ServerLeftovers
	ls.steps = nil
	a.start(&ls.j, time.Minute, func(ctx context.Context) (err error) {
		a.says(&ls.j)(T("leave.reading", "name", name))
		l, err = a.svc.ServerLeftovers(ctx, name)
		return err
	}, func(err error) {
		if err == nil {
			ls.left = l
			a.runLeave(name, ls)
		}
	})
}

func leaveItemLabel(item string, l *service.ServerLeftovers) string {
	switch item {
	case service.LeaveTools:
		var names []string
		for _, t := range l.Tools {
			names = append(names, t.Name)
		}
		return T("leave.item.tools", "names", strings.Join(names, T("common.list_sep")))
	case service.LeavePasses:
		if len(l.Passes) > 1 {
			return T("leave.item.passes_both")
		}
		return T("leave.item.passes_user")
	}
	return T("leave.item." + item)
}

func leaveItemNote(item string, l *service.ServerLeftovers) string {
	switch item {
	case service.LeaveTimers:
		return T("leave.note.timers")
	case service.LeaveClouds:
		return T("leave.note.clouds", "names", strings.Join(l.Clouds, T("common.list_sep")))
	case service.LeavePasses:
		return T("leave.note.passes")
	}
	return ""
}

// leftoverWhere says where each thing stays on the server, for the user to
// clean it by hand; no commands.
func leftoverWhere(l *service.ServerLeftovers) []string {
	var out []string
	if l.Porter {
		out = append(out, T("leave.where.porter"))
	}
	if len(l.Passes) > 0 {
		out = append(out, T("leave.where.passes"))
	}
	if len(l.Timers) > 0 {
		out = append(out, T("leave.where.timers"))
	}
	if len(l.Clouds) > 0 {
		out = append(out, T("leave.where.clouds"))
	}
	for _, t := range l.Tools {
		out = append(out, T("leave.where.tool", "name", t.Name))
	}
	return append(out, T("leave.where.vault"))
}
