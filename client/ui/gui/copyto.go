package gui

import (
	"context"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/protection"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// 备份后复制到 (docs/UI.md 7.9, ARCHITECTURE.md 7.4): one dialog, two
// halves. Above, where the server copies by itself: the plan's copies,
// changed through the plan (prepare, confirm, the server checks). Below,
// the folders and cloud drives this computer fills: its own binding, kept here only.

type copyEdit struct {
	read    job
	targets []vaultrpc.TargetState
	server  map[string]*serverCopyEdit
	local   *service.LocalCopiesView
	repos   map[string]*[2]bool // after a backup, when Valise opens
	clouds  map[string]*[2]bool // after a backup, when Valise opens
	saveJ   job
}

type serverCopyEdit struct {
	on    bool
	every string
}

// Intervals a server copy can have.
var copyEvery = []string{"1h", "24h", "168h"}

func (a *App) openCopyTo(row *agentRow, ps *protState) {
	e := &copyEdit{
		server: map[string]*serverCopyEdit{},
		repos:  map[string]*[2]bool{},
		clouds: map[string]*[2]bool{},
	}
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
		ps.localCopies = lc
		e.targets, e.local = st.Targets, lc
		for _, t := range st.Targets {
			e.server[t.Name] = &serverCopyEdit{every: "24h"}
		}
		for _, cp := range ps.desired().ServerCopies {
			if s := e.server[cp.TargetRef.ID]; s != nil {
				s.on, s.every = cp.Enabled != nil && *cp.Enabled, cp.MinimumInterval
			}
		}
		for _, l := range lc.Repos {
			e.repos[l.RepoID] = &[2]bool{l.AfterBackup != nil && *l.AfterBackup, l.OnClientOpen != nil && *l.OnClientOpen}
		}
		for _, r := range a.svc.Repos() {
			if e.repos[r.Name] == nil {
				e.repos[r.Name] = &[2]bool{true, true}
			}
		}
		for _, cl := range lc.Clouds {
			e.clouds[cl.RepoID] = &[2]bool{cl.AfterBackup != nil && *cl.AfterBackup, cl.OnClientOpen != nil && *cl.OnClientOpen}
		}
		for _, cl := range a.svc.Config.Clouds {
			if e.clouds[cl.Name] == nil {
				e.clouds[cl.Name] = &[2]bool{true, true}
			}
		}
	})
}

func (a *App) copyToDialog(c *ui.Context, row *agentRow, ps *protState) {
	e := ps.copyTo
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 520).Children(func() {
			dialogTitle(ui.Text(c, T("copyto.title", "name", row.Name)))
			if e.read.busy {
				a.jobLine(c, &e.read)
				return
			}
			if e.read.err != nil {
				errorBox(c, e.read.err, a.fold("copyto-err"))
			}
			a.serverCopies(c, e)
			a.localCopies(c, row, ps, e)
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

func (a *App) localCopies(c *ui.Context, row *agentRow, ps *protState, e *copyEdit) {
	ui.Text(c, T("copyto.local")).Bold()
	muted(ui.Text(c, T("copyto.local_what"))).Wrap()
	if e.local != nil && !e.local.Planned {
		muted(ui.Text(c, T("copyto.local_need_plan"))).Wrap()
		if !e.saveJ.busy {
			ui.Row(c).Children(func() {
				ui.Button(c, T("copyto.enable_default_plan")).OnClick(func() { a.enableDefaultPlanForCopyTo(row, ps, e) })
			})
		}
		return
	}
	repoNames := orderedCopyNames(repoConfigNames(a.svc.Repos()), e.repos)
	cloudNames := orderedCopyNames(cloudConfigNames(a.svc.Config.Clouds), e.clouds)
	if len(repoNames) == 0 && len(cloudNames) == 0 {
		muted(ui.Text(c, T("copyto.local_none"))).Wrap()
		return
	}
	for _, name := range repoNames {
		b := e.repos[name]
		ui.Row(c.Key("lc-"+name)).Gap(14).Padding(8, 12).Radius(8).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Text(c, name).Bold().Grow(1)
			ui.Checkbox(c, &b[0], T("copyto.after_backup"))
			ui.Checkbox(c, &b[1], T("copyto.on_open"))
		})
	}
	for _, name := range cloudNames {
		b := e.clouds[name]
		ui.Row(c.Key("cc-"+name)).Gap(14).Padding(8, 12).Radius(8).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Grow(1).Children(func() {
				ui.Text(c, name).Bold()
				plainTag(c, T("loc.kind.cloud"))
			})
			ui.Checkbox(c, &b[0], T("copyto.after_backup"))
			ui.Checkbox(c, &b[1], T("copyto.on_open"))
		})
	}
}

func (a *App) enableDefaultPlanForCopyTo(row *agentRow, ps *protState, e *copyEdit) {
	if ps.ref == nil || e.saveJ.busy {
		return
	}
	var rev protection.Revision
	switch {
	case ps.view != nil && ps.view.ServerPlan != nil:
		rev = ps.view.ServerPlan.Revision
	case ps.view != nil && ps.view.SupersededPlan != nil:
		rev = ps.view.SupersededPlan.Revision
	}
	d := ensureScheduleDefaults(ps.desired())
	host, ref := row.Server, *ps.ref
	a.start(&e.saveJ, 5*time.Minute, func(ctx context.Context) error {
		a.says(&e.saveJ)(T("protect.preparing"))
		fc, err := a.svc.PrepareProtectionChange(ctx, host, protection.PrepareParams{AgentRef: ref, ExpectedRevision: rev, Desired: d})
		if err != nil {
			return err
		}
		a.confirm(fc.PlanID)
		a.says(&e.saveJ)(T("protect.applying"))
		t, err := a.svc.StartProtectionChange(ctx, fc.PlanID, "desktop-"+fc.PlanID)
		for err == nil && t.State != "succeeded" && t.State != "failed" && t.State != "cancelled" && ctx.Err() == nil {
			time.Sleep(time.Second)
			t, err = a.svc.GetProtectionTask(ctx, t.TaskRef)
		}
		if err == nil && t.Error != nil {
			err = taskErr(t.Error)
		}
		return err
	}, func(err error) {
		if err == nil {
			a.readProt(host, ps)
			a.openCopyTo(row, ps)
		}
	})
}

// saveCopyTo changes the binding for folders/clouds that changed (or creates
// the initial binding when none exists yet), then the server plan if changed.
func (a *App) saveCopyTo(row *agentRow, ps *protState) {
	e := ps.copyTo
	d := ensureScheduleDefaults(ps.desired())
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
		wasRepo, wasCloud := map[string][2]bool{}, map[string][2]bool{}
		hasCloudBinding := false
		if e.local.Binding != nil {
			for _, l := range e.local.Binding.LocalCopies {
				wasRepo[l.RepoID] = [2]bool{l.AfterBackup != nil && *l.AfterBackup, l.OnClientOpen != nil && *l.OnClientOpen}
			}
			if e.local.Binding.CloudCopies != nil {
				hasCloudBinding = true
				for _, l := range e.local.Binding.CloudCopies {
					wasCloud[l.RepoID] = [2]bool{l.AfterBackup != nil && *l.AfterBackup, l.OnClientOpen != nil && *l.OnClientOpen}
				}
			}
		}
		for _, name := range orderedCopyNames(repoConfigNames(a.svc.Repos()), e.repos) {
			b := e.repos[name]
			before, saved := wasRepo[name]
			if e.local.Binding == nil || !saved || *b != before {
				after, opened := b[0], b[1]
				changes = append(changes, service.LocalCopyChange{Kind: "repo", Repo: name, AfterBackup: &after, OnClientOpen: &opened})
			}
		}
		for _, name := range orderedCopyNames(cloudConfigNames(a.svc.Config.Clouds), e.clouds) {
			b := e.clouds[name]
			before, saved := wasCloud[name]
			if e.local.Binding == nil || !hasCloudBinding || !saved || *b != before {
				after, opened := b[0], b[1]
				changes = append(changes, service.LocalCopyChange{Kind: "cloud", Repo: name, AfterBackup: &after, OnClientOpen: &opened})
			}
		}
	}
	plan := func() {
		ps.copyTo = nil
		if planChanged {
			d.ServerCopies = copies
			a.applyDesired(row, ps, d)
		} else if len(changes) > 0 {
			a.readProt(row.Server, ps)
		}
	}
	if len(changes) == 0 {
		plan()
		return
	}
	host, inst := row.Server, row.Instance
	var refreshed *service.LocalCopiesView
	a.start(&e.saveJ, time.Minute, func(ctx context.Context) error {
		if _, err := a.svc.SetLocalCopies(ctx, host, inst, changes); err != nil {
			return err
		}
		refreshed, _ = a.svc.LocalCopies(ctx, host, inst)
		return nil
	}, func(err error) {
		if err == nil {
			if refreshed != nil {
				ps.localCopies = refreshed
			}
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

func repoConfigNames(repos []config.Repo) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Name)
	}
	return out
}

func cloudConfigNames(clouds []config.Cloud) []string {
	out := make([]string, 0, len(clouds))
	for _, c := range clouds {
		out = append(out, c.Name)
	}
	return out
}

func orderedCopyNames(preferred []string, m map[string]*[2]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range preferred {
		if m[name] != nil && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	var extra []string
	for name, b := range m {
		if b != nil && !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}
