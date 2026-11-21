package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

func (a *App) pickProfileLocal() {
	ps := a.ensureProfilesState()
	if a.pickFile == nil || ps.loading || ps.picking || ps.fetchJ.busy || ps.installJ.busy {
		return
	}
	ps.picking, ps.doneNote, ps.fetchJ.err, ps.installJ.err, ps.pending, ps.pendingLocal = true, "", nil, nil, nil, nil
	a.run(func() {
		path, err := a.pickFile(T("profiles.pick_yaml_title"), T("profiles.pick_yaml_msg"))
		var lf *service.LocalProfileFile
		if err == nil && path != "" {
			lf, err = a.svc.ReadLocalProfileFile(path)
		}
		a.update(func() {
			ps.picking = false
			if err != nil {
				ps.fetchJ.err = err
				return
			}
			if path != "" {
				ps.pendingLocal = lf
			}
		})
	})
}

func (a *App) localProfilePreviewCard(c *ui.Context, ps *profilesState, f *service.LocalProfileFile) {
	busy := ps.fetchJ.busy || ps.installJ.busy
	card(ui.Column(c)).Gap(10).Children(func() {
		sectionTitle(ui.Text(c, T("profiles.local_preview_title", "name", f.DisplayName, "id", f.ID)))
		mono(muted(ui.Text(c, f.From))).Wrap()
		muted(ui.Text(c, T("profiles.local_meta", "id", f.ID, "version", f.Version, "status", f.Status, "pending", f.Pending, "restore", f.RestoreCap))).Wrap()
		if f.ReplacesLocal {
			ui.Text(c, T("profiles.local_replaces")).TextColor(palette.Warn).Wrap()
		}
		if len(f.Warnings) > 0 {
			ui.Collapsible(c, T("profiles.local_warnings"), a.fold("profiles-local-warn"), func() {
				subPanel(ui.Column(c)).Gap(4).Children(func() {
					for _, w := range f.Warnings {
						mono(ui.Text(c, w.String())).FontSize(fsCaption).Wrap()
					}
				})
			})
		}
		muted(ui.Text(c, T("profiles.local_unverified_note"))).Wrap()
		ui.Collapsible(c, T("profiles.unverified_cmds"), a.fold("profiles-local-cmds"), func() {
			subPanel(ui.Column(c)).Gap(4).Children(func() {
				if len(f.Commands) == 0 {
					muted(ui.Text(c, T("profiles.local_no_cmds"))).Wrap()
				}
				for _, cmd := range f.Commands {
					mono(ui.Text(c, cmd)).FontSize(fsCaption).Wrap()
				}
			})
		})
		refused := len(f.RefusedCmds) > 0
		if refused {
			ui.Column(c).Padding(10, 12).Gap(4).Radius(8).Background(palette.ErrorBg).Border(1, palette.ErrorBorder).Children(func() {
				ui.Text(c, T("profiles.local_refused")).TextColor(palette.Danger).Wrap()
				for _, cmd := range f.RefusedCmds {
					mono(ui.Text(c, cmd)).FontSize(fsCaption).TextColor(palette.Danger).Wrap()
				}
			})
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).Disabled(busy).OnClick(func() { ps.pendingLocal = nil })
			if !refused {
				ui.PrimaryButton(c, T("profiles.local_import_go")).Disabled(busy).OnClick(func() { a.installPendingLocalProfile() })
			}
		})
	})
}

func (a *App) installPendingLocalProfile() {
	ps := a.ensureProfilesState()
	f := ps.pendingLocal
	if f == nil || len(f.RefusedCmds) > 0 || ps.fetchJ.busy || ps.installJ.busy {
		return
	}
	ps.doneNote = ""
	a.start(&ps.installJ, time.Minute, func(context.Context) error {
		a.says(&ps.installJ)(T("profiles.installing"))
		_, err := a.svc.InstallLocalProfile(f)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		ps.pendingLocal, ps.doneNote = nil, T("profiles.local_import_done", "id", f.ID)
		a.reloadProfiles()
		a.readProfilesStatus()
	})
}
