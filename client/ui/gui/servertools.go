package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/kittools"
	"valise/ui/service"
)

// Server tools (docs/AGENT_KIT.md 8.4.1): the signed list's tools as they
// are on one server. An enhanced tool is an official binary this client
// downloads and checks; the plan is shown and confirmed before anything is
// written, and always into the user's own ~/.local/bin.

// kitOps is what the tools screens call on the service; tests swap it.
type kitOps struct {
	tools       func(ctx context.Context, host string) ([]service.KitTool, error)
	plan        func(ctx context.Context, host, id string) (*service.KitBinaryPlan, error)
	install     func(ctx context.Context, host, id string) (*service.KitTool, error)
	remove      func(ctx context.Context, host, id string) error
	loginServer func(ctx context.Context, host, name, id string, f service.CloudForm) error
}

func (a *App) kit() kitOps {
	if a.kitHook != nil {
		return *a.kitHook
	}
	return kitOps{
		tools: a.svc.KitTools, plan: a.svc.PlanKitBinary, install: a.svc.InstallKitBinary, remove: a.svc.RemoveKitBinary,
		loginServer: func(ctx context.Context, host, name, id string, f service.CloudForm) error {
			_, err := a.svc.CloudLoginServerForm(ctx, host, name, id, f)
			return err
		},
	}
}

// kitInstall is one tool's install or removal in progress on a host.
type kitInstall struct {
	plan      *service.KitBinaryPlan
	confirm   bool
	askRemove bool
	planJ     job
	installJ  job
	removeJ   job
}

func (ss *settingsState) toolInst(id string) *kitInstall {
	if ss.toolInsts == nil {
		ss.toolInsts = map[string]*kitInstall{}
	}
	if ss.toolInsts[id] == nil {
		ss.toolInsts[id] = &kitInstall{}
	}
	return ss.toolInsts[id]
}

// readKitTools reads the tools on host, independent of the vault read.
func (a *App) readKitTools(host string) {
	ss := a.settingsOf(host)
	if ss.tools.busy {
		return
	}
	ss.tools.started, ss.tools.busy, ss.tools.err = true, true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		list, err := a.kit().tools(ctx, host)
		a.update(func() {
			ss.tools.busy, ss.tools.err, ss.tools.at = false, err, time.Now()
			if err == nil {
				ss.tools.value = list
			}
		})
	})
}

// kitPlan asks what installing id would download; nothing changes.
func (a *App) kitPlan(host, id string, ki *kitInstall) {
	var p *service.KitBinaryPlan
	ki.plan, ki.confirm = nil, false
	ki.installJ.err = nil
	a.start(&ki.planJ, time.Minute, func(ctx context.Context) (err error) {
		p, err = a.kit().plan(ctx, host, id)
		return err
	}, func(err error) {
		if err == nil {
			ki.plan = p
		}
	})
}

// kitInstallRun installs after the confirmation; done runs on success.
func (a *App) kitInstallRun(host, id string, ki *kitInstall, done func(*service.KitTool)) {
	var t *service.KitTool
	a.start(&ki.installJ, 10*time.Minute, func(ctx context.Context) (err error) {
		a.says(&ki.installJ)(T("tools.installing"))
		t, err = a.kit().install(ctx, host, id)
		return err
	}, func(err error) {
		if err == nil {
			ki.plan, ki.confirm = nil, false
			if done != nil {
				done(t)
			}
		}
	})
}

// kitInstallBlock is the prepare button, then the plan with its
// confirmation, then the install button that stays off until confirmed.
func (a *App) kitInstallBlock(c *ui.Context, host, id, prepare string, ki *kitInstall, done func(*service.KitTool)) {
	switch {
	case ki.planJ.busy:
		a.jobLine(c, &ki.planJ)
		return
	case ki.installJ.busy:
		a.jobLine(c, &ki.installJ)
		return
	}
	if ki.planJ.err != nil {
		errorBox(c, ki.planJ.err, a.fold("kit-plan-"+host+id))
	}
	if ki.installJ.err != nil {
		errorBox(c, ki.installJ.err, a.fold("kit-install-"+host+id))
	}
	p := ki.plan
	if p == nil {
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, prepare).OnClick(func() { a.kitPlan(host, id, ki) })
			ui.Button(c, T("tools.self_install_shell")).OnClick(func() { a.navigate("/servers/" + host + "/shell") })
		})
		return
	}
	ui.Column(c).Gap(6).Padding(10, 14).Radius(10).Background(palette.Board).Children(func() {
		ui.Text(c, T("tools.plan_version", "version", p.Version, "arch", p.Arch)).Bold()
		mono(muted(ui.Text(c, p.URL))).Wrap()
		mono(muted(ui.Text(c, "SHA-256 "+p.SHA256))).Wrap()
		muted(ui.Text(c, T("tools.plan_path", "path", p.InstallPath))).Wrap()
		ui.Checkbox(c.Key("kc-"+host+id), &ki.confirm, T("tools.confirm_external"))
		ui.Row(c).Gap(8).Children(func() {
			b := ui.PrimaryButton(c, T("tools.install_go")).OnClick(func() { a.kitInstallRun(host, id, ki, done) })
			if !ki.confirm {
				b.Disabled(true)
			}
			ui.Button(c, T("tools.self_install_shell")).OnClick(func() { a.navigate("/servers/" + host + "/shell") })
		})
	})
}

// toolsCard is the settings tab's tools block.
func (a *App) toolsCard(c *ui.Context, host string, ss *settingsState) {
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("tools.title")).FontSize(16).Bold().Grow(1)
			if ss.tools.busy {
				ui.Spinner(c)
			} else {
				ui.Button(c, T("common.refresh")).OnClick(func() { a.readKitTools(host) })
			}
		})
		muted(ui.Text(c, T("tools.hint"))).Wrap()
		if ss.tools.err != nil {
			errorBox(c, ss.tools.err, a.fold("tools-err-"+host))
		}
		if ss.tools.busy && len(ss.tools.value) == 0 {
			muted(ui.Text(c, T("tools.loading")))
		}
		for _, t := range ss.tools.value {
			a.toolRow(c.Key("tool-"+t.ID), host, ss, t)
		}
	})
}

func (a *App) toolRow(c *ui.Context, host string, ss *settingsState, t service.KitTool) {
	ki := ss.toolInst(t.ID)
	enhanced := t.Tier == kittools.TierEnhanced
	if !t.Present && t.Why == "" && !enhanced {
		return
	}
	ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, t.Name).Bold()
				switch {
				case t.Present && t.Ours:
					muted(ui.Text(c, T("tools.installed", "path", t.Path))).Wrap()
				case t.Present:
					muted(ui.Text(c, T("tools.system_present", "path", t.Path))).Wrap()
				case t.Why != "":
					muted(ui.Text(c, T("tools.not_available", "why", t.Why))).Wrap()
				}
			})
			if t.Present && t.Ours && !ki.askRemove && !ki.removeJ.busy {
				ui.Button(c, T("tools.uninstall")).OnClick(func() { ki.askRemove = true })
			}
		})
		switch {
		case t.Present && t.Ours:
			a.toolRemove(c, host, t, ki)
		case !t.Present && t.Why != "":
			ui.Button(c, T("tools.self_install_shell")).OnClick(func() { a.navigate("/servers/" + host + "/shell") })
		case !t.Present:
			a.kitInstallBlock(c, host, t.ID, T("tools.prepare"), ki, func(*service.KitTool) { a.readKitTools(host) })
		}
	})
}

func (a *App) toolRemove(c *ui.Context, host string, t service.KitTool, ki *kitInstall) {
	if ki.removeJ.busy {
		a.jobLine(c, &ki.removeJ)
		return
	}
	if ki.removeJ.err != nil {
		errorBox(c, ki.removeJ.err, a.fold("kit-remove-"+host+t.ID))
	}
	if !ki.askRemove {
		return
	}
	muted(ui.Text(c, T("tools.uninstall_note", "path", t.Path))).Wrap()
	ui.Row(c).Gap(8).Children(func() {
		ui.PrimaryButton(c, T("tools.uninstall_go")).OnClick(func() {
			ki.askRemove = false
			a.start(&ki.removeJ, 5*time.Minute, func(ctx context.Context) error {
				a.says(&ki.removeJ)(T("tools.removing"))
				return a.kit().remove(ctx, host, t.ID)
			}, func(err error) {
				if err == nil {
					a.readKitTools(host)
				}
			})
		})
		ui.Button(c, T("common.cancel")).OnClick(func() { ki.askRemove = false })
	})
}
