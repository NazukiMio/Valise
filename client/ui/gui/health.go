package gui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"slices"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

// healthState is one agent's health page while this window lives.
type healthState struct {
	busy           bool
	report         *service.HealthReport
	err            error
	at             time.Time
	fix            *fixState
	sys            *sysFixState // a fix built into Valise (healthsysfix.go)
	canTest        bool
	testReadFailed bool
}

// fixState is one repair action from its confirmation to its result: the
// command's exit and the service afterwards are kept apart (AGENTS.md 7.5).
type fixState struct {
	item     service.HealthItem
	loading  bool // reading the action's declaration
	action   *profile.Action
	access   string
	confirm  bool // the confirmation is open
	running  bool
	progress []string
	result   *service.ActionResult
	err      error
}

func (a *App) readHealth(server, instance string) {
	key := server + "/" + instance
	st := a.health[key]
	if st == nil {
		st = &healthState{}
		a.health[key] = st
	}
	if st.busy {
		return
	}
	st.busy = true
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		r, err := a.svc.Health(ctx, server, instance)
		caps, capErr := a.svc.BackupCapabilities(ctx, server)
		last, hasLast := a.svc.LastRestoreTest(server, instance)
		a.update(func() {
			st.busy, st.report, st.err, st.at = false, r, err, time.Now()
			st.canTest = slices.Contains(caps, revive.ReviewCapability)
			st.testReadFailed = capErr != nil
			if last.AgentRef != nil && last.State != "running" && last.State != "needs_cleanup" {
				matched := r != nil && r.AgentRef != nil && *r.AgentRef == *last.AgentRef
				hasLast = hasLast && matched
				if !matched && a.restoreTest != nil && !a.restoreTest.j.busy && a.restoreTest.server == server && a.restoreTest.instance == instance {
					a.restoreTest = nil
				}
			}
			if hasLast && (a.restoreTest == nil || !a.restoreTest.j.busy && (a.restoreTest.server != server || a.restoreTest.instance != instance)) {
				a.restoreTest = &restoreTestFlow{server: server, instance: instance, result: &last}
			}
		})
	})
}

// healthTab lists the health checks, bad first; an item whose harness
// declares a repair action offers it (docs/UI.md 7.3).
func (a *App) healthTab(c *ui.Context, row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.health[key]
	if st == nil {
		a.readHealth(row.Server, row.Instance)
		st = a.health[key]
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		line := T("health.reading")
		if st.report != nil {
			line = T("health.read_at", "when", when(st.at, c.Now()))
		}
		muted(ui.Text(c, line)).FontSize(13).Grow(1)
		if st.busy {
			ui.Spinner(c).Size(14, 14)
		}
		ui.Button(c, T("health.recheck")).Disabled(st.busy).OnClick(func() {
			a.readHealth(row.Server, row.Instance)
			if !row.Retired() {
				a.readProposals(row.Server, row.Instance)
			}
		})
	})
	if st.err != nil {
		errorBox(c, st.err, a.fold("health-err-"+key))
	}
	if t := a.restoreTest; t != nil && t.server == row.Server && t.instance == row.Instance {
		a.restoreTestReport(c, t)
	}
	if !row.Retired() {
		a.proposalsCard(c, row)
	}
	if st.report == nil {
		a.looseReports(c, row, st, false, false)
		return
	}
	items := append([]service.HealthItem(nil), st.report.Items...)
	if t := a.restoreTest; t != nil && t.server == row.Server && t.instance == row.Instance && (t.j.busy || t.result != nil || t.j.err != nil) {
		items = slices.DeleteFunc(items, func(it service.HealthItem) bool { return it.ID == "restore_test" })
	}
	sort.SliceStable(items, func(i, j int) bool { return service.Rank(items[i].Level) < service.Rank(items[j].Level) })
	fixPlaced, sysPlaced := a.healthItems(c, row, st, items)
	a.looseReports(c, row, st, fixPlaced, sysPlaced)
	a.actualRestoreEvidence(c, row)
	muted(ui.Text(c, T("health.scope_note")))
}

// looseReports draws a repair's confirmation, and its progress when its
// finding is not on the page any more.
func (a *App) looseReports(c *ui.Context, row *agentRow, st *healthState, fixPlaced, sysPlaced bool) {
	if st.fix != nil && !fixPlaced {
		a.fixReport(c, row, st)
	}
	if st.sys != nil && !sysPlaced {
		a.sysFixReport(c, row, st)
	}
}

// fixButton is a finding's repair: only the first one on the page is the
// primary button (docs/DESIGN.md 2).
func fixButton(c *ui.Context, label string, primary bool) ui.Element {
	if primary {
		return ui.PrimaryButton(c, label)
	}
	return ui.Button(c, label)
}

func (a *App) healthLine(c *ui.Context, row *agentRow, st *healthState, it service.HealthItem, first bool) {
	tone := map[string]string{service.HealthOK: "ok", service.HealthWarn: "warn", service.HealthBad: "danger"}[it.Level]
	ui.Row(c).Padding(14, 18).Gap(14).AlignItems(ui.Center).Children(func() {
		if tone != "" {
			toneDot(c, tone)
		} else {
			ui.Box(c).Size(8, 8).Radius(4).Background(palette.Line)
		}
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			title := ui.Text(c, it.Title).FontSize(fsBody).Bold().Wrap().Selectable()
			if tone == "danger" {
				title.TextColor(palette.Danger)
			}
			if it.Text != "" {
				muted(ui.Text(c, it.Text)).FontSize(13).Wrap().Selectable()
			}
		})
		// A declared repair action, or the built-in linger fix; the clock
		// fix is not in the desktop yet, so it is not drawn (docs/UI.md 9.5).
		if it.ID == "restore_test" {
			if st.canTest {
				ui.Button(c, T("restoretest.open")).OnClick(func() { a.openRestoreTest(row) })
			} else {
				ui.Column(c).Gap(6).MaxWidth(240).Children(func() {
					if st.testReadFailed {
						muted(ui.Text(c, T("restoretest.connection_unread"))).Wrap()
						ui.Button(c, T("health.recheck")).OnClick(func() { a.readHealth(row.Server, row.Instance) })
					} else {
						muted(ui.Text(c, T("restoretest.needs_update"))).Wrap()
						ui.Button(c, T("restoretest.server_settings")).OnClick(func() { a.navigate("/servers/" + row.Server) })
					}
				})
			}
		} else if it.ActionID != "" && (st.fix == nil || !st.fix.running) {
			fixButton(c, it.ActionName, first).OnClick(func() { a.prepareFix(row, st, it) })
		} else if it.SystemFix == service.FixLinger && (st.sys == nil || !st.sys.busy()) {
			fixButton(c, it.ActionName, first).OnClick(func() { a.prepareSysFix(row, st, it) })
		}
	})
}

// prepareFix reads the action's declaration, then asks for confirmation.
func (a *App) prepareFix(row *agentRow, st *healthState, it service.HealthItem) {
	f := &fixState{item: it, loading: true}
	st.fix = f
	server, instance := row.Server, row.Instance
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		list, err := a.svc.Actions(ctx, server, instance)
		a.update(func() {
			f.loading = false
			if err != nil {
				f.err = err
				return
			}
			for i := range list.Actions {
				if list.Actions[i].ID == it.ActionID {
					f.action = &list.Actions[i]
				}
			}
			switch {
			case list.Stale:
				f.err = valise.New("action_profile_stale", T("health.err.stale"), T("health.err.stale_hint"))
			case f.action == nil:
				f.err = valise.New("action_unknown", T("health.err.unknown", "id", it.ActionID), T("health.err.unknown_hint"))
			default:
				f.access, f.confirm = a.svc.ActionAccess(server, *f.action), true
			}
		})
	})
}

// runFix runs the confirmed action, then reads health again.
func (a *App) runFix(row *agentRow, st *healthState) {
	f := st.fix
	f.confirm, f.running = false, true
	server, instance, id := row.Server, row.Instance, f.action.ID
	seal := service.SealsByDefault(*f.action)
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := a.svc.RunAction(ctx, server, instance, id, service.ActionOptions{Seal: seal,
			Progress: func(m string) { a.update(func() { f.progress = append(f.progress, m) }) }})
		a.update(func() {
			f.running, f.result, f.err = false, res, err
			a.readHealth(server, instance)
			a.refreshAgents()
		})
	})
}

// fixReport is the confirmation, the progress and what came of the action.
func (a *App) fixReport(c *ui.Context, row *agentRow, st *healthState) {
	f := st.fix
	if f.confirm {
		ui.Modal(c, &f.confirm, func() {
			dialog(ui.Column(c), 460).Children(func() {
				dialogTitle(ui.Text(c, T("health.run_title", "name", f.action.Name)))
				muted(ui.Text(c, service.RiskLabels[f.action.Risk]+" · "+f.access)).FontSize(12)
				ui.Text(c, f.action.Explain).FontSize(13).Wrap()
				codeBox(ui.Column(c)).Gap(4).Children(func() {
					muted(ui.Text(c, T("health.run_cmd", "server", row.Server)))
					ui.Text(c, strings.Join(f.action.Argv, " ")).FontSize(12).Font(cur.Fonts.Mono).Wrap().Selectable()
				})
				muted(ui.Text(c, T("health.admin_login"))).FontSize(12).Wrap()
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					ui.Button(c, T("common.cancel")).OnClick(func() { st.fix = nil })
					ui.PrimaryButton(c, T("health.run")).OnClick(func() { a.runFix(row, st) })
				})
			})
		})
		return
	}
	reportPanel(ui.Column(c)).Gap(6).Children(func() {
		switch {
		case f.loading:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); muted(ui.Text(c, T("health.reading_action"))) })
		case f.running:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); ui.Text(c, T("health.running", "name", f.item.ActionName)).FontSize(13) })
			for i, p := range f.progress {
				muted(ui.Text(c.Key(i), p))
			}
		case f.err != nil:
			a.fixError(c, row, f)
		case f.result != nil:
			fixResult(c, f)
		}
	})
}

// fixResult tells the command's exit and the service afterwards apart.
func fixResult(c *ui.Context, f *fixState) {
	r := f.result
	if r.Operation != nil {
		ui.Text(c, T("health.pending")).FontSize(13).Wrap()
		return
	}
	exit := T("health.exited", "code", r.Exit)
	ui.Text(c, exit).FontSize(13).Bold().Selectable()
	if r.Checked {
		s, tone := serviceText(r.Service)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(tone))
			ui.Text(c, T("health.after", "state", s)).FontSize(13).Selectable()
		})
	}
	if v := r.Verification; v != nil {
		muted(ui.Text(c, v.Text)).FontSize(12).Wrap().Selectable()
	}
}

// fixError explains a failure. A login with the server password is asked
// for in this window (askpass, docs/UI.md 9.5), so nothing sends the person
// to a terminal.
func (a *App) fixError(c *ui.Context, row *agentRow, f *fixState) {
	errorBox(c, f.err, a.fold("fix-err-"+row.Server+row.Instance))
}
