package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
	"valise/core/valise"
	"valise/core/vault"
)

type pointPromotion struct {
	ref       journey.SnapshotRef
	note      string
	open      bool
	published bool
	job       job
}

func filterJourney(points []journey.TimelinePoint, filter int) []journey.TimelinePoint {
	if filter != 1 {
		return points
	}
	var kept []journey.TimelinePoint
	for _, p := range points {
		if p.Kind == vault.KindSavepoint {
			kept = append(kept, p)
		}
	}
	return kept
}

func pointKind(c *ui.Context, p journey.TimelinePoint) {
	pointKindLabel(c, p.Kind)
	if p.Kind == vault.KindSavepoint && p.Note != "" {
		muted(ui.Text(c, p.Note)).FontSize(fsCaption).MaxLines(1)
	}
}

func pointKindLabel(c *ui.Context, kind string) {
	switch kind {
	case vault.KindSavepoint:
		ui.Row(c).Gap(spHair).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "★").TextColor(palette.Gold)
			ui.Text(c, T("savepoint.kind.manual")).FontSize(fsCaption).Bold().TextColor(palette.Brand).MaxLines(1)
		})
	case vault.KindAuto:
		muted(ui.Text(c, T("savepoint.kind.auto"))).FontSize(fsCaption)
	default:
		muted(ui.Text(c, T("savepoint.kind.history"))).FontSize(fsCaption)
	}
}

func promotionHost(p journey.TimelinePoint) string {
	if p.Kind != vault.KindAuto || p.MetadataState == "conflict" {
		return ""
	}
	for _, cp := range p.Copies {
		if cp.LocationRef.Kind == "server_vault" && cp.Observation.Status == "fresh" {
			return cp.LocationRef.AuthorityID
		}
	}
	return ""
}

func (a *App) promotePoint(c *ui.Context, p journey.TimelinePoint) {
	host := promotionHost(p)
	st := a.pointPromotion
	if st != nil && st.ref == p.SnapshotRef && st.job.busy {
		a.jobLine(c, &st.job)
		return
	}
	if host == "" {
		return
	}
	ui.Button(c, T("savepoint.promote")).OnClick(func() {
		a.pointPromotion = &pointPromotion{ref: p.SnapshotRef, open: true}
	})
	if st == nil || st.ref != p.SnapshotRef {
		return
	}
	if st.job.err != nil {
		errorBox(c, st.job.err, a.fold("promotion-error"))
	}
	if !st.open {
		return
	}
	if ui.Modal(c, &st.open, func() {
		dialog(ui.Column(c), 460).Children(func() {
			dialogTitle(ui.Text(c, T("savepoint.promote.title")))
			muted(ui.Text(c, T("savepoint.promote.why"))).Wrap()
			note, ok := savepointNoteInput(c, &st.note)
			ui.Row(c).Gap(spInside).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { a.pointPromotion = nil })
				ui.PrimaryButton(c, T("savepoint.promote.go")).Disabled(!ok).OnClick(func() {
					a.startPromotion(st, host, p.RecordedInstanceID, note)
				})
			})
		})
	}).Dismissed() {
		st.open = false
	}
}

func (a *App) startPromotion(st *pointPromotion, host, instance, note string) {
	st.open = false
	a.start(&st.job, 30*time.Minute, func(ctx context.Context) error {
		return a.svc.PromoteSnapshot(ctx, host, instance, st.ref.SnapshotID, note)
	}, func(err error) {
		if ue, ok := valise.As(err); err != nil && (!ok || ue.Code != "snapshot_promoted_copy_incomplete") {
			return
		}
		st.published = true
		a.closePoint()
		a.readTimeline(nil)
	})
}

func (a *App) promotionResult(c *ui.Context) {
	st := a.pointPromotion
	if st == nil || !st.published {
		return
	}
	if st.job.err != nil {
		errorBox(c, st.job.err, a.fold("promotion-copy-error"))
	} else {
		ui.Text(c, T("savepoint.promote.done")).TextColor(palette.Ok).Wrap()
	}
	ui.Button(c, T("common.close")).OnClick(func() { a.pointPromotion = nil })
}
