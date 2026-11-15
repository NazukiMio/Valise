package gui

import (
	"context"

	"github.com/egoist/mygo/ui"
	"valise/ui/service"
)

func (a *App) reviewTestMaterials(ctx context.Context, t *restoreTestFlow, review service.TestMaterialReview) bool {
	reply := make(chan bool, 1)
	a.update(func() { t.materialReview = &review; t.reviewReply = reply })
	defer a.update(func() {
		if t.reviewReply == reply {
			t.materialReview = nil
			t.reviewReply = nil
		}
	})
	select {
	case approved := <-reply:
		return approved
	case <-ctx.Done():
		return false
	}
}

func (a *App) restoreTestMaterialReview(c *ui.Context, t *restoreTestFlow) {
	r := t.materialReview
	if r == nil {
		return
	}
	reply := func(approve bool) {
		if t.reviewReply != nil {
			select {
			case t.reviewReply <- approve:
			default:
			}
		}
	}
	open := true
	_, height := c.Size()
	if ui.Modal(c, &open, func() {
		ui.Column(c).Width(560).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("restoretest.materials.title")).FontSize(18).Bold()
			ui.Scroll(c).MaxHeight(max(200, height-160)).Children(func() {
				ui.Column(c).Gap(12).Children(func() {
					fact(c, T("restore.snapshot_time"), r.TargetTime.Local().Format("2006-01-02 15:04:05"))
					fact(c, T("point.scope"), tierText(r.TargetTier))
					if r.Baseline != nil {
						fact(c, T("restoretest.materials.baseline_time"), r.Baseline.Time.Local().Format("2006-01-02 15:04:05"))
					}
					if r.BaselineLocation != nil {
						fact(c, T("restoretest.materials.baseline_location"), journeyLocation(*r.BaselineLocation))
					}
					fact(c, T("restoretest.file_scope"), T("restoretest.file_count", "n", r.Files, "size", size(r.Bytes)))
					muted(ui.Text(c, T("restoretest.materials.explain"))).Wrap()
					if r.BaselineCredentials {
						ui.Text(c, T("restoretest.materials.credentials")).Wrap()
					}
					if r.Partial {
						ui.Text(c, T("restoretest.materials.partial")).TextColor(palette.Warn).Wrap()
					}
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { reply(false) })
				ui.PrimaryButton(c, T("restoretest.materials.continue")).OnClick(func() { reply(true) })
			})
		})
	}).Dismissed() {
		reply(false)
	}
}
