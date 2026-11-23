package gui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/profile"
	"valise/core/revive"
	"valise/ui/service"
)

// Structured target directory selection for migrate/clone (no raw root=path syntax).

func (a *App) restoreProfileID(r *restoreFlow) string {
	if r == nil {
		return ""
	}
	if r.source >= 0 && r.source < len(r.sources) {
		if id := strings.TrimSpace(r.sources[r.source].Copy.Metadata.ProfileID); id != "" {
			return id
		}
	}
	for _, it := range a.agents.value.Items {
		if it.Instance.InstanceID == r.point.RecordedInstanceID && (r.origin == "" || it.ConnectionID == r.origin) {
			return it.Instance.ProfileID
		}
	}
	return ""
}

func (a *App) ensurePlacement(r *restoreFlow) {
	if r == nil || r.mode == revive.ModeRollback || strings.TrimSpace(r.target) == "" {
		return
	}
	profileID := a.restoreProfileID(r)
	if profileID == "" {
		return
	}
	key := r.origin + "|" + r.target + "|" + r.mode + "|" + r.point.SnapshotRef.SnapshotID + "|" + r.id
	if r.placeKey == key {
		return
	}
	r.placeKey = key
	r.placeLoading = true
	r.placeErr = nil
	r.placement = nil
	r.placeMode = ""
	r.customStateDir = ""
	r.extraRoots = nil

	req := service.TargetPlacementRequest{
		From:               r.origin,
		Target:             r.target,
		Mode:               r.mode,
		ProfileID:          profileID,
		RecordedInstanceID: r.point.RecordedInstanceID,
		DesiredInstanceID:  r.id,
	}
	fn := a.inspectPlacement
	if fn == nil {
		fn = a.svc.InspectTargetPlacement
	}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pl, err := fn(ctx, req)
		a.update(func() {
			if r.placeKey != key {
				return
			}
			r.placeLoading = false
			r.placement = pl
			r.placeErr = err
			if err == nil && pl != nil {
				r.placeMode = "recommended"
				r.customStateDir = pl.RecommendedStateDir
				r.extraRoots = map[string]string{}
				r.roots = r.resolvedRoots()
			}
		})
	})
}

func (r *restoreFlow) resolvedRoots() string {
	if r == nil {
		return ""
	}
	if r.placeMode == "" && strings.TrimSpace(r.customStateDir) == "" && r.placement == nil && len(r.extraRoots) == 0 {
		return r.roots
	}
	var lines []string
	stateDir := strings.TrimSpace(r.customStateDir)
	if r.placeMode == "replace" && r.placement != nil {
		stateDir = strings.TrimSpace(r.placement.DefaultStateDir)
	}
	if stateDir != "" {
		lines = append(lines, profile.StateRoot+"="+stateDir)
	}
	if len(r.extraRoots) > 0 {
		ids := make([]string, 0, len(r.extraRoots))
		for id := range r.extraRoots {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if v := strings.TrimSpace(r.extraRoots[id]); v != "" {
				lines = append(lines, id+"="+v)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) restorePlacementUI(c *ui.Context, r *restoreFlow) {
	if r.mode == revive.ModeRollback {
		return
	}
	a.ensurePlacement(r)
	ui.Text(c, T("restore.path_mapping")).Bold()
	if r.mode == revive.ModeClone && r.target == r.origin {
		muted(ui.Text(c, T("restore.same_server_hint"))).Wrap()
	} else {
		muted(ui.Text(c, T("restore.path_hint"))).Wrap()
	}
	if r.placeLoading {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			muted(ui.Text(c, T("restore.place.loading")))
		})
	}
	if r.placeErr != nil && r.target != "" {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			muted(ui.Text(c, T("restore.place.manual_fallback"))).FontSize(fsCaption).Grow(1).Wrap()
			quietButton(c, T("common.refresh")).OnClick(func() {
				r.placeKey = ""
				a.ensurePlacement(r)
			})
		})
	}
	pl := r.placement
	if r.placeMode == "" {
		r.placeMode = "recommended"
	}
	ui.RadioGroup(c, func() {
		ui.Radio(c.Key("pl-rec"), &r.placeMode, "recommended", T("restore.place.recommended"))
		if r.placeMode == "recommended" || pl == nil || !pl.CanReplaceDefault {
			labeled(c, T("restore.place.state_dir"), func() {
				ui.TextInput(c, &r.customStateDir).Label(T("restore.place.state_dir")).Placeholder(T("restore.place.state_placeholder"))
			})
		}
		if pl != nil && pl.SourceRoots[profile.StateRoot] != "" {
			muted(ui.Text(c, T("restore.place.source_dir", "dir", pl.SourceRoots[profile.StateRoot]))).FontSize(fsCaption).Wrap().Selectable()
		}
		if pl != nil && pl.CanReplaceDefault {
			ui.Radio(c.Key("pl-rep"), &r.placeMode, "replace", T("restore.place.replace_default", "dir", pl.DefaultStateDir))
			if r.placeMode == "replace" && len(pl.DefaultOccupiedBy) > 0 {
				notice(c, "warn", T("restore.place.occupied_warn", "instances", strings.Join(pl.DefaultOccupiedBy, T("common.list_sep"))))
			}
		}
	}).Label(T("restore.path_mapping"))

	if pl != nil && len(pl.ExtraRootIDs) > 0 {
		if r.extraRoots == nil {
			r.extraRoots = map[string]string{}
		}
		ui.Collapsible(c, T("restore.place.extra_roots"), a.fold("restore-extra-roots"), func() {
			for _, id := range pl.ExtraRootIDs {
				id := id
				val := r.extraRoots[id]
				labeled(c, id, func() {
					if ui.TextInput(c.Key("extra-"+id), &val).Label(id).Placeholder(T("restore.place.state_placeholder")).Changed() {
						r.extraRoots[id] = val
					}
				})
			}
		})
	}
}
