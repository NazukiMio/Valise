package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

func (a *App) pickProfileLocal() {
	ps := a.ensureProfilesState()
	if a.pickFile == nil || ps.loading || ps.picking || ps.fetchJ.busy || ps.installJ.busy || ps.syncJ.busy {
		return
	}
	ps.picking, ps.doneNote, ps.fetchJ.err, ps.installJ.err, ps.pending, ps.pendingLocal, ps.syncTargets, ps.syncOutcomes = true, "", nil, nil, nil, nil, nil, nil
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
	busy := ps.fetchJ.busy || ps.installJ.busy || ps.syncJ.busy
	card(ui.Column(c)).Gap(10).Children(func() {
		sectionTitle(ui.Text(c, T("profiles.local_preview_title", "name", f.DisplayName, "id", f.ID)))
		mono(muted(ui.Text(c, f.From))).Wrap().Selectable()
		muted(ui.Text(c, T("profiles.local_meta", "id", f.ID, "version", f.Version, "status", f.Status, "pending", f.Pending, "restore", f.RestoreCap))).Wrap().Selectable()
		if f.OverridesOfficial {
			notice(c, "warn", T("profiles.local_overrides_official", "id", f.ID, "version", f.OfficialVersion))
		}
		if f.ReplacesLocal {
			ui.Text(c, T("profiles.local_replaces")).TextColor(palette.Warn).Wrap()
		}
		if f.Diff != nil {
			subPanel(ui.Column(c)).Gap(4).Children(func() {
				ui.Text(c, T("profiles.diff_title")).FontSize(fsSub).Bold()
				if len(f.Diff.Highlights) == 0 {
					muted(ui.Text(c, T("profiles.diff_none"))).FontSize(fsSub).Wrap()
				}
				for i, h := range f.Diff.Highlights {
					ui.Text(c.Key(i), "• "+h).FontSize(fsSub).Wrap().Selectable()
				}
			})
		}
		if len(f.Warnings) > 0 {
			ui.Collapsible(c, T("profiles.local_warnings"), a.fold("profiles-local-warn"), func() {
				subPanel(ui.Column(c)).Gap(4).Children(func() {
					for _, w := range f.Warnings {
						mono(ui.Text(c, w.String())).FontSize(fsCaption).Wrap().Selectable()
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
					mono(ui.Text(c, cmd)).FontSize(fsCaption).Wrap().Selectable()
				}
			})
		})
		refused := len(f.RefusedCmds) > 0
		if refused {
			ui.Column(c).Padding(10, 12).Gap(4).Radius(8).Background(palette.ErrorBg).Border(1, palette.ErrorBorder).Children(func() {
				ui.Text(c, T("profiles.local_refused")).TextColor(palette.Danger).Wrap()
				for _, cmd := range f.RefusedCmds {
					mono(ui.Text(c, cmd)).FontSize(fsCaption).TextColor(palette.Danger).Wrap().Selectable()
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
	if f == nil || len(f.RefusedCmds) > 0 || ps.fetchJ.busy || ps.installJ.busy || ps.syncJ.busy {
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
		ps.syncProfileID = f.ID
		ps.syncTargets = a.outdatedProfileInstances(f.ID, f.Version)
		ps.syncOutcomes, ps.syncJ.err = nil, nil
		a.reloadProfiles()
		a.readProfilesStatus()
	})
}

func (a *App) outdatedProfileInstances(profileID string, version int) []service.ProfileSyncTarget {
	var out []service.ProfileSyncTarget
	for _, it := range a.agents.value.Items {
		if it.MigratedTo != nil || it.Instance.Superseded != nil {
			continue
		}
		if it.Instance.ProfileID == profileID && it.Instance.ProfileVersion < version {
			out = append(out, service.ProfileSyncTarget{
				Host:       it.ConnectionID,
				InstanceID: it.Instance.InstanceID,
				Name:       it.Instance.Name,
			})
		}
	}
	return out
}

func (a *App) profileSyncCard(c *ui.Context, ps *profilesState) {
	card(ui.Column(c)).Gap(8).Children(func() {
		if len(ps.syncTargets) > 0 && !ps.syncJ.busy {
			notice(c, "warn", T("profiles.sync_banner", "n", len(ps.syncTargets)))
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.PrimaryButton(c, T("profiles.sync_now", "n", len(ps.syncTargets))).OnClick(func() { a.runProfileSync(ps) })
			})
		}
		if ps.syncJ.busy {
			a.jobLine(c, &ps.syncJ)
		}
		if ps.syncJ.err != nil {
			errorBox(c, ps.syncJ.err, a.fold("profiles-sync-err"))
		}
		for _, o := range ps.syncOutcomes {
			label := o.Host + " / " + o.InstanceID
			if o.OK {
				statusRow(c, "ok", label, T("profiles.sync_ok"))
			} else {
				statusRow(c, "warn", label, T("profiles.sync_fail", "err", o.Err))
			}
		}
	})
}

func (a *App) runProfileSync(ps *profilesState) {
	if ps.syncJ.busy || len(ps.syncTargets) == 0 || ps.syncProfileID == "" {
		return
	}
	profileID := ps.syncProfileID
	targets := append([]service.ProfileSyncTarget(nil), ps.syncTargets...)
	var outcomes []service.ProfileSyncOutcome
	a.start(&ps.syncJ, 5*time.Minute, func(ctx context.Context) (err error) {
		outcomes, err = a.svc.SyncProfileToInstances(ctx, profileID, targets)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		ps.syncOutcomes = outcomes
		var retry []service.ProfileSyncTarget
		for i, o := range outcomes {
			if !o.OK && i < len(targets) {
				retry = append(retry, targets[i])
			}
		}
		ps.syncTargets = retry
		a.reloadProfiles()
		a.refreshAgents()
	})
}
