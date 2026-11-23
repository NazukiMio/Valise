package gui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/profiles"
	"valise/core/kittools"
	"valise/ui/service"
)

type profilesState struct {
	started       bool
	loading       bool
	picking       bool
	status        *service.ProfilesStatus
	statusErr     error
	fetchJ        job
	installJ      job
	pending       *service.PackFile
	pendingLocal  *service.LocalProfileFile
	doneNote      string
	syncProfileID string
	syncTargets   []service.ProfileSyncTarget
	syncJ         job
	syncOutcomes  []service.ProfileSyncOutcome
}

func (a *App) ensureProfilesState() *profilesState {
	if a.profiles == nil {
		a.profiles = &profilesState{}
	}
	return a.profiles
}

func (a *App) readProfilesStatus() {
	ps := a.ensureProfilesState()
	if ps.loading {
		return
	}
	ps.started, ps.loading, ps.statusErr = true, true, nil
	a.run(func() {
		st, err := a.svc.ProfilesStatus()
		a.update(func() { ps.loading, ps.status, ps.statusErr = false, st, err })
	})
}

func (a *App) profilesSettings(c *ui.Context) {
	ps := a.ensureProfilesState()
	if !ps.started {
		a.readProfilesStatus()
	}
	a.profilesStatusCard(c, ps)
	if len(ps.syncTargets) > 0 || len(ps.syncOutcomes) > 0 || ps.syncJ.busy || ps.syncJ.err != nil {
		a.profileSyncCard(c, ps)
	}
	if ps.pending != nil {
		a.profilesPreviewCard(c, ps, ps.pending)
	}
	if ps.pendingLocal != nil {
		a.localProfilePreviewCard(c, ps, ps.pendingLocal)
	}
}

func (a *App) profilesStatusCard(c *ui.Context, ps *profilesState) {
	busy := ps.loading || ps.picking || ps.fetchJ.busy || ps.installJ.busy || ps.syncJ.busy
	card(ui.Column(c)).Gap(8).Children(func() {
		sectionTitle(ui.Text(c, T("profiles.title")))
		if ps.loading && ps.status == nil && ps.statusErr == nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c) })
		}
		if ps.statusErr != nil {
			errorBox(c, ps.statusErr, a.fold("profiles-status-err"))
		}
		if st := ps.status; st != nil {
			switch {
			case st.Pack != nil:
				ui.Text(c, T("profiles.installed", "version", st.Pack.Version)).TextColor(palette.Ok).Bold()
				list := formatPackEntries(st.Pack.Profiles)
				if list != "" || len(st.Clouds) > 0 {
					subPanel(ui.Column(c)).Gap(4).Children(func() {
						if list != "" {
							mono(muted(ui.Text(c, list))).Wrap().Selectable()
						}
						if len(st.Clouds) > 0 {
							muted(ui.Text(c, T("profiles.clouds_list", "list", strings.Join(st.Clouds, T("common.dot_sep"))))).Wrap().Selectable()
						}
					})
				}
			case st.PackProblem != "":
				ui.Text(c, T("profiles.problem", "reason", st.PackProblem)).TextColor(palette.Warn).Wrap().Selectable()
			default:
				ui.Text(c, T("profiles.none")).TextColor(palette.Warn).Wrap()
			}
			if len(st.Local) > 0 {
				muted(ui.Text(c, T("profiles.local_list", "list", strings.Join(st.Local, T("common.list_sep"))))).Wrap().Selectable()
			}
			var overrides []service.ProfileSummary
			for _, sum := range st.Loaded {
				if sum.OverridesOfficial {
					overrides = append(overrides, sum)
				}
			}
			if len(overrides) > 0 {
				subPanel(ui.Column(c)).Gap(6).Children(func() {
					for _, sum := range overrides {
						id := sum.ID
						key := "profiles.override_item"
						if sum.LocalStale {
							key = "profiles.override_stale_item"
						}
						ui.Row(c.Key("ovr-" + id)).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, T(key, "id", id, "local", sum.Version, "official", sum.OfficialVersion)).FontSize(fsSub).TextColor(palette.Warn).Grow(1).Wrap().Selectable()
							ui.Button(c, T("profiles.restore_official")).Disabled(busy).OnClick(func() { a.restoreOfficialProfile(id) })
						})
					}
				})
			}
			if len(st.Rejected) > 0 {
				ui.Text(c, T("profiles.rejected_list", "list", strings.Join(st.Rejected, T("common.list_sep")))).TextColor(palette.Warn).Wrap().Selectable()
			}
		}
		if ps.fetchJ.busy {
			a.jobLine(c, &ps.fetchJ)
		}
		if ps.fetchJ.err != nil {
			errorBox(c, ps.fetchJ.err, a.fold("profiles-fetch-err"))
		}
		if ps.installJ.busy {
			a.jobLine(c, &ps.installJ)
		}
		if ps.installJ.err != nil {
			errorBox(c, ps.installJ.err, a.fold("profiles-install-err"))
		}
		if ps.doneNote != "" {
			ui.Text(c, ps.doneNote).TextColor(palette.Ok).Bold().Wrap()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.PrimaryButton(c, T("profiles.check_online")).Disabled(busy).OnClick(func() { a.fetchPackOnline() })
			if a.pickFile != nil {
				ui.Button(c, T("profiles.pick_file")).Disabled(busy).OnClick(func() { a.pickPackLocal() })
				ui.Button(c, T("profiles.pick_yaml")).Disabled(busy).OnClick(func() { a.pickProfileLocal() })
			}
			ui.Button(c, T("common.refresh")).Disabled(busy).OnClick(func() { ps.doneNote, ps.syncTargets, ps.syncOutcomes = "", nil, nil; a.readProfilesStatus() })
		})
	})
}

func (a *App) restoreOfficialProfile(id string) {
	ps := a.ensureProfilesState()
	if ps.loading || ps.picking || ps.fetchJ.busy || ps.installJ.busy || ps.syncJ.busy {
		return
	}
	ps.doneNote, ps.syncTargets, ps.syncOutcomes = "", nil, nil
	a.start(&ps.installJ, time.Minute, func(context.Context) error {
		return a.svc.RemoveLocalProfile(id)
	}, func(err error) {
		if err != nil {
			return
		}
		ps.doneNote = T("profiles.restore_official_done", "id", id)
		a.reloadProfiles()
		a.readProfilesStatus()
	})
}

func (a *App) profilesPreviewCard(c *ui.Context, ps *profilesState, pf *service.PackFile) {
	busy := ps.fetchJ.busy || ps.installJ.busy
	card(ui.Column(c)).Gap(10).Children(func() {
		sectionTitle(ui.Text(c, T("profiles.preview_title", "version", pf.Pack.Manifest.Version)))
		mono(muted(ui.Text(c, pf.From))).Wrap().Selectable()
		list := formatPackEntries(pf.Pack.Manifest.Profiles)
		clouds := formatPackClouds(pf.Pack.Kit)
		if list != "" || clouds != "" {
			subPanel(ui.Column(c)).Gap(4).Children(func() {
				if list != "" {
					mono(ui.Text(c, list)).Wrap().Selectable()
				}
				if clouds != "" {
					muted(ui.Text(c, T("profiles.clouds_list", "list", clouds))).Wrap().Selectable()
				}
			})
		}
		if pf.SignatureErr == nil {
			ui.Text(c, T("profiles.sig_ok")).TextColor(palette.Ok).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).Disabled(busy).OnClick(func() { ps.pending = nil })
				ui.PrimaryButton(c, T("profiles.install_go")).Disabled(busy).OnClick(func() { a.installPendingPack(false) })
			})
			return
		}
		errorBox(c, pf.SignatureErr, a.fold("profiles-sig-err"))
		muted(ui.Text(c, T("profiles.unverified_note"))).Wrap()
		cmds := packCommands(pf.Pack)
		if len(cmds) > 0 {
			ui.Collapsible(c, T("profiles.unverified_cmds"), a.fold("profiles-cmds"), func() {
				subPanel(ui.Column(c)).Gap(4).Children(func() {
					for _, cmd := range cmds {
						mono(ui.Text(c, cmd)).FontSize(fsCaption).Wrap().Selectable()
					}
				})
			})
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).Disabled(busy).OnClick(func() { ps.pending = nil })
			ui.Button(c, T("profiles.install_local")).Disabled(busy).OnClick(func() { a.installPendingPack(true) })
		})
	})
}

func (a *App) fetchPackOnline() {
	ps := a.ensureProfilesState()
	if ps.loading || ps.picking || ps.fetchJ.busy || ps.installJ.busy || ps.syncJ.busy {
		return
	}
	ps.doneNote, ps.installJ.err, ps.pending, ps.pendingLocal, ps.syncTargets, ps.syncOutcomes = "", nil, nil, nil, nil, nil
	var pf *service.PackFile
	a.start(&ps.fetchJ, 2*time.Minute, func(ctx context.Context) (err error) {
		a.says(&ps.fetchJ)(T("profiles.fetching"))
		pf, err = a.svc.FetchPack(ctx, "")
		return err
	}, func(err error) {
		if err == nil {
			ps.pending = pf
		}
	})
}

func (a *App) pickPackLocal() {
	ps := a.ensureProfilesState()
	if a.pickFile == nil || ps.loading || ps.picking || ps.fetchJ.busy || ps.installJ.busy || ps.syncJ.busy {
		return
	}
	ps.picking, ps.doneNote, ps.fetchJ.err, ps.installJ.err, ps.pending, ps.pendingLocal, ps.syncTargets, ps.syncOutcomes = true, "", nil, nil, nil, nil, nil, nil
	a.run(func() {
		path, err := a.pickFile(T("profiles.pick_title"), T("profiles.pick_msg"))
		var pf *service.PackFile
		if err == nil && path != "" {
			pf, err = a.svc.ReadPackFile(path)
		}
		a.update(func() {
			ps.picking = false
			if err != nil {
				ps.fetchJ.err = err
				return
			}
			if path != "" {
				ps.pending = pf
			}
		})
	})
}

func (a *App) installPendingPack(local bool) {
	ps := a.ensureProfilesState()
	pf := ps.pending
	if pf == nil || ps.fetchJ.busy || ps.installJ.busy || (!local && pf.SignatureErr != nil) {
		return
	}
	ps.doneNote = ""
	var note string
	a.start(&ps.installJ, time.Minute, func(context.Context) error {
		a.says(&ps.installJ)(T("profiles.installing"))
		if local {
			paths, err := a.svc.InstallPackAsLocal(pf)
			if err != nil {
				return err
			}
			note = T("profiles.local_done", "n", len(paths))
			return nil
		}
		if err := a.svc.InstallPack(pf); err != nil {
			return err
		}
		note = T("profiles.install_done", "version", pf.Pack.Manifest.Version)
		return nil
	}, func(err error) {
		if err != nil {
			return
		}
		ps.pending, ps.doneNote = nil, note
		a.reloadProfiles()
		a.readProfilesStatus()
	})
}

func formatPackEntries(entries []profiles.ManifestEntry) string {
	var parts []string
	for _, p := range entries {
		parts = append(parts, fmt.Sprintf("%s v%d", p.ID, p.ProfileVersion))
	}
	return strings.Join(parts, T("common.dot_sep"))
}

func formatPackClouds(kit *kittools.List) string {
	if kit == nil {
		return ""
	}
	var names []string
	for _, t := range kit.Tools {
		for _, cl := range t.Clouds {
			names = append(names, cl.Name)
		}
	}
	return strings.Join(names, T("common.dot_sep"))
}

func packCommands(p *profiles.Pack) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, d := range p.Docs {
		for _, cmd := range profiles.Commands(d.Profile) {
			out = append(out, fmt.Sprintf("[%s] %s", d.Profile.ID, cmd))
		}
	}
	return out
}
