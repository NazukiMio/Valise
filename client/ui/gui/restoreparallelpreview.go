package gui

import (
	"context"
	"github.com/egoist/mygo/ui"
	"time"
	"valise/core/revive"
	"valise/ui/service"
)

func (a *App) previewParallelRestore(r *restoreFlow) {
	if r.j.busy || r.source < 0 || r.source >= len(r.sources) {
		return
	}
	req := service.RestoreRequest{Options: service.ReviveOptions{Target: r.origin, From: r.origin, Mode: revive.ModeRollback, SnapshotID: r.point.SnapshotRef.SnapshotID}, Snapshot: r.point.SnapshotRef, Source: r.sources[r.source]}
	var prepared *service.PreparedParallelRestore
	a.start(&r.j, 15*time.Minute, func(ctx context.Context) (err error) {
		prepared, err = a.svc.PrepareParallelRestore(ctx, req)
		return err
	}, func(err error) {
		if err == nil {
			if r.parallelPrepared != nil {
				r.parallelPrepared.Close()
			}
			r.parallelPrepared = prepared
			r.parallelPlan, r.step = &prepared.View, "parallel_preview"
			r.credential, r.draft, r.parallelBaseline = false, false, false
		}
	})
}

func (a *App) parallelPreview(c *ui.Context, r *restoreFlow) {
	p := r.parallelPlan
	if p == nil {
		return
	}
	if r.j.busy {
		a.jobLine(c, &r.j)
		return
	}
	ui.Row(c).Gap(10).Children(func() {
		ui.Button(c, T("restore.edit")).OnClick(func() { r.step = "setup" })
		ui.Button(c, T("common.refresh")).OnClick(func() { a.previewParallelRestore(r) })
	})
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Text(c, T("environment.preview_title")).Bold()
		fact(c, T("environment.saved_mode"), T("environment.mode."+string(p.Mode)))
		fact(c, T("environment.preview_location"), journeyLocation(r.sources[r.source].Copy.LocationRef))
		fact(c, T("environment.preview_files"), T("environment.preview_file_count", "n", p.Files, "bytes", size(p.Bytes)))
		fact(c, T("environment.preview_program"), T("environment.preview_file_count", "n", p.ProgramFiles, "bytes", size(p.ProgramBytes)))
		fact(c, T("environment.preview_version"), p.ProgramVersion)
		fact(c, T("environment.preview_roots"), T("environment.preview_root_count", "logical", p.LogicalRoots, "physical", p.PhysicalRoots))
		if p.ManagedSource && p.Original != nil && p.Candidate != nil {
			ui.Text(c, T("environment.preview_managed")).Wrap()
			fact(c, T("environment.preview_from_slot"), T("environment.old_cleanup_slot", "slot", p.Original.Slot))
			fact(c, T("environment.preview_to_slot"), T("environment.old_cleanup_slot", "slot", p.Candidate.Slot))
		} else {
			ui.Text(c, T("environment.preview_native")).Wrap()
		}
		ui.Text(c, T("environment.preview_source")).Wrap()
		muted(ui.Text(c, T("environment.preview_health"))).Wrap()
		if p.Baseline != nil {
			fact(c, T("environment.preview_baseline"), p.Baseline.Time.Local().Format("2006-01-02 15:04:05"))
		}
	})
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Text(c, T("environment.preview_space")).Bold()
		for i, b := range p.Space {
			label := T("environment.preview_disk", "n", i+1)
			fact(c, label, T("environment.preview_space_value", "need", size(int64(b.NeedBytes)), "free", size(int64(b.FreeBytes))))
			if b.NeedBytes > b.FreeBytes {
				ui.Text(c, T("environment.preview_space_low")).TextColor(palette.Warn).Wrap()
			}
		}
		muted(ui.Text(c, T("environment.preview_space_hint"))).Wrap()
	})
	if p.ProfileUntested {
		ui.Text(c, T("environment.preview_draft")).TextColor(palette.Warn).Wrap()
	}
	if u := p.Reuse; u != nil {
		card(ui.Column(c)).Gap(8).Children(func() {
			ui.Text(c, T("environment.reuse_title", "slot", u.Target.Slot)).Bold()
			muted(ui.Text(c, T("environment.reuse_effect"))).Wrap()
			for _, item := range u.Items {
				label := T("environment.old_cleanup_temporary")
				if item.Kind == "data" {
					label = T("environment.old_cleanup_data", "root", item.Root)
				} else if item.Kind == "program" {
					label = T("environment.old_cleanup_program")
				}
				value := T("environment.old_cleanup_removed")
				if item.Present {
					value = T("restoretest.file_count", "n", item.Files, "size", size(item.Bytes))
				}
				fact(c, label, value)
			}
		})
	}
	if r.parallelPrepared != nil && r.restoreMode != nil && r.restoreMode.prepareSupported {
		a.parallelPrepareChoices(c, r)
	} else {
		muted(ui.Text(c, T("environment.preview_only"))).Wrap()
	}
	ui.Collapsible(c, T("restore.technical"), a.fold("parallel-preview-details"), func() {
		fact(c, T("restore.services"), p.NativeUnit)
		bootKey := "environment.preview_boot_off"
		if p.EnableOnBoot {
			bootKey = "environment.preview_boot_on"
		}
		fact(c, T("environment.preview_boot"), T(bootKey))
		mono(muted(ui.Text(c, p.Digest))).Wrap()
	})
}
