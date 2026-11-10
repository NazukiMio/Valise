package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/protection"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// 备份后复制到 (docs/UI.md 7.9, ARCHITECTURE.md 7.4): one dialog, two
// halves. Above, where the server copies by itself: the plan's copies,
// changed through the plan (prepare, confirm, the server checks). Below,
// the folders this computer fills: its own binding, kept here only.

type copyEdit struct {
	read    job
	targets []vaultrpc.TargetState
	server  map[string]*serverCopyEdit
	local   *service.LocalCopiesView
	repos   map[string]*[2]bool // after a backup, when Valise opens
	saveJ   job
}

type serverCopyEdit struct {
	on    bool
	every string
}

// Intervals a server copy can have.
var copyEvery = []string{"1h", "24h", "168h"}

func (a *App) openCopyTo(row *agentRow, ps *protState) {
	e := &copyEdit{server: map[string]*serverCopyEdit{}, repos: map[string]*[2]bool{}}
	ps.copyTo = e
	host, inst := row.Server, row.Instance
	var st *vaultrpc.StatusResult
	var lc *service.LocalCopiesView
	a.start(&e.read, time.Minute, func(ctx context.Context) (err error) {
		if st, err = a.svc.VaultStatus(ctx, host); err != nil {
			return err
		}
		lc, err = a.svc.LocalCopies(ctx, host, inst)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		e.targets, e.local = st.Targets, lc
		for _, t := range st.Targets {
			e.server[t.Name] = &serverCopyEdit{every: "24h"}
		}
		for _, cp := range ps.desired().ServerCopies {
			if s := e.server[cp.TargetRef.ID]; s != nil {
				s.on, s.every = cp.Enabled != nil && *cp.Enabled, cp.MinimumInterval
			}
		}
		for _, r := range a.svc.Repos() {
			e.repos[r.Name] = &[2]bool{true, true}
		}
		if lc.Binding != nil {
			for _, l := range lc.Binding.LocalCopies {
				if b := e.repos[l.RepoID]; b != nil {
					b[0], b[1] = l.AfterBackup != nil && *l.AfterBackup, l.OnClientOpen != nil && *l.OnClientOpen
				}
			}
		}
	})
}

func (a *App) copyToDialog(c *ui.Context, row *agentRow, ps *protState) {
	e := ps.copyTo
	open := true
	if ui.Modal(c, &open, func() {
		ui.Column(c).Width(520).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("copyto.title", "name", row.Name)).FontSize(18).Bold()
			if e.read.busy {
				a.jobLine(c, &e.read)
				return
			}
			if e.read.err != nil {
				errorBox(c, e.read.err, a.fold("copyto-err"))
			}
			a.serverCopies(c, e)
			a.localCopies(c, e)
			if e.saveJ.busy {
				a.jobLine(c, &e.saveJ)
				return
			}
			if e.saveJ.err != nil {
				errorBox(c, e.saveJ.err, a.fold("copyto-save-err"))
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { ps.copyTo = nil })
				b := ui.PrimaryButton(c, T("protect.save")).OnClick(func() { a.saveCopyTo(row, ps) })
				if e.read.err != nil || e.local == nil {
					b.Disabled(true)
				}
			})
		})
	}).Dismissed() && !e.saveJ.busy {
		ps.copyTo = nil
	}
}

func (a *App) serverCopies(c *ui.Context, e *copyEdit) {
	ui.Text(c, T("copyto.server")).Bold()
	muted(ui.Text(c, T("copyto.server_what"))).Wrap()
	if len(e.targets) == 0 {
		muted(ui.Text(c, T("copyto.server_none"))).Wrap()
	}
	for _, t := range e.targets {
		s := e.server[t.Name]
		ui.Row(c.Key("sc-"+t.Name)).Gap(10).Padding(8, 12).Radius(8).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c, &s.on, t.Name).Grow(1)
			if !s.on {
				return
			}
			ui.RadioGroup(c, func() {
				for _, v := range copyEvery {
					ui.Radio(c.Key("ev-"+t.Name+v), &s.every, v, T("copyto.every."+v))
				}
			}).Row().Gap(10).Label(T("copyto.every_label"))
		})
	}
}

func (a *App) localCopies(c *ui.Context, e *copyEdit) {
	ui.Text(c, T("copyto.local")).Bold()
	muted(ui.Text(c, T("copyto.local_what"))).Wrap()
	if e.local != nil && !e.local.Planned {
		muted(ui.Text(c, T("copyto.local_need_plan"))).Wrap()
		return
	}
	if len(e.repos) == 0 {
		muted(ui.Text(c, T("copyto.local_none"))).Wrap()
	}
	for _, r := range a.svc.Repos() {
		b := e.repos[r.Name]
		if b == nil {
			continue
		}
		ui.Row(c.Key("lc-"+r.Name)).Gap(14).Padding(8, 12).Radius(8).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Text(c, r.Name).Bold().Grow(1)
			ui.Checkbox(c, &b[0], T("copyto.after_backup"))
			ui.Checkbox(c, &b[1], T("copyto.on_open"))
		})
	}
}

// saveCopyTo changes the binding only for folders that changed, then the
// plan only when the server half changed.
func (a *App) saveCopyTo(row *agentRow, ps *protState) {
	e := ps.copyTo
	d := ps.desired()
	var copies []protection.ServerCopy
	for _, t := range e.targets {
		s := e.server[t.Name]
		if s.on {
			on := true
			copies = append(copies, protection.ServerCopy{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: t.Name, AuthorityID: ps.ref.ServerUID},
				Enabled: &on, MinimumInterval: s.every})
		}
	}
	if copies == nil {
		copies = []protection.ServerCopy{}
	}
	planChanged := !sameCopies(d.ServerCopies, copies)
	var changes []service.LocalCopyChange
	if e.local.Planned {
		was := map[string][2]bool{}
		if e.local.Binding != nil {
			for _, l := range e.local.Binding.LocalCopies {
				was[l.RepoID] = [2]bool{l.AfterBackup != nil && *l.AfterBackup, l.OnClientOpen != nil && *l.OnClientOpen}
			}
		}
		for _, r := range a.svc.Repos() {
			b, ok := e.repos[r.Name]
			before, saved := was[r.Name]
			if !saved {
				before = [2]bool{true, true}
			}
			if ok && *b != before {
				after, opened := b[0], b[1]
				changes = append(changes, service.LocalCopyChange{Repo: r.Name, AfterBackup: &after, OnClientOpen: &opened})
			}
		}
	}
	plan := func() {
		ps.copyTo = nil
		if planChanged {
			d.ServerCopies = copies
			a.applyDesired(row, ps, d)
		}
	}
	if len(changes) == 0 {
		plan()
		return
	}
	host, inst := row.Server, row.Instance
	a.start(&e.saveJ, time.Minute, func(ctx context.Context) error {
		_, err := a.svc.SetLocalCopies(ctx, host, inst, changes)
		return err
	}, func(err error) {
		if err == nil {
			plan()
		}
	})
}

func sameCopies(x, y []protection.ServerCopy) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i].TargetRef != y[i].TargetRef || x[i].MinimumInterval != y[i].MinimumInterval || (x[i].Enabled != nil && *x[i].Enabled) != (y[i].Enabled != nil && *y[i].Enabled) {
			return false
		}
	}
	return true
}
