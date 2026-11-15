package gui

import (
	"slices"

	"github.com/egoist/mygo/ui"
	"valise/core/journey"
	"valise/ui/service"
)

func (a *App) testSources(p journey.TimelinePoint) []service.RestoreSource {
	return slices.DeleteFunc(a.svc.RestoreSources(p), func(s service.RestoreSource) bool {
		return s.Copy.HeaderDigest == "" || s.Copy.Metadata.SnapshotID != p.SnapshotRef.SnapshotID || s.Copy.Metadata.InstanceID != p.RecordedInstanceID
	})
}

func (a *App) chooseTestPoint(t *restoreTestFlow, point journey.TimelinePoint) {
	point.Copies = slices.Clone(point.Copies)
	t.selected = &point
	t.sources, t.source, t.request = a.testSources(point), 0, nil
}

// Defaults apply once. Incremental scans must not replace a human selection
// with a newer point or switch its selected copy behind the confirmation.
func (a *App) defaultTestPoint(t *restoreTestFlow) {
	if t.selected != nil {
		return
	}
	for _, p := range a.pointsOf(t.server, t.instance) {
		if len(a.testSources(p)) > 0 {
			a.chooseTestPoint(t, p)
			return
		}
	}
}

func (a *App) confirmTestSelection(t *restoreTestFlow) {
	if t.selected == nil || t.source < 0 || t.source >= len(t.sources) {
		return
	}
	req := service.SelectedRestoreTest{Depth: "files", Target: t.server, Instance: t.instance, Snapshot: t.selected.SnapshotRef, Source: t.sources[t.source]}
	t.request, t.confirm = &req, false
	a.needKey(func() { a.startRestoreTest(t, false) })
}

func (a *App) openPointTest(p journey.TimelinePoint, server, instance string) {
	old := a.restoreTest
	a.openRestoreTest(&agentRow{Server: server, Instance: instance})
	if t := a.restoreTest; t != nil && t != old && t.confirm {
		a.chooseTestPoint(t, p)
		a.showRestoreTest(t)
	}
}

func (a *App) restoreTestSelection(c *ui.Context, t *restoreTestFlow) {
	a.defaultTestPoint(t)
	open := true
	_, windowHeight := c.Size()
	if ui.Modal(c, &open, func() {
		ui.Column(c).Width(560).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("restoretest.choose_title")).FontSize(18).Bold()
			ui.Scroll(c).MaxHeight(max(200, windowHeight-160)).Children(func() {
				ui.Column(c).Gap(12).Children(func() {
					fact(c, T("restore.target"), t.server+" / "+t.instance)
					ui.Text(c, T("restoretest.point_select")).Bold()
					points := a.pointsOf(t.server, t.instance)
					if len(points) > 0 {
						ui.Scroll(c).Height(float32(min(len(points), 6)) * 28).Children(func() {
							ui.RadioGroup(c, func() {
								key := ""
								if t.selected != nil {
									key = pointKey(*t.selected)
								}
								for _, p := range points {
									label := T("restoretest.point_label", "time", p.CreatedAt.Local().Format("2006-01-02 15:04:05"), "scope", tierText(p.Scope.Tier))
									if ui.Radio(c.Key(pointKey(p)), &key, pointKey(p), label).Changed() {
										a.chooseTestPoint(t, p)
									}
								}
							}).Label(T("restoretest.point_select"))
						})
					}
					if a.timeline.busy {
						ui.Row(c).Gap(8).Children(func() { ui.Spinner(c); muted(ui.Text(c, T("restoretest.scanning"))).Wrap() })
					}
					if a.timeline.err != nil {
						errorBox(c, a.timeline.err, a.fold("test-point-read-error"))
					}
					if t.selected != nil {
						fact(c, T("restore.snapshot_time"), t.selected.CreatedAt.Local().Format("2006-01-02 15:04:05"))
						fact(c, T("point.scope"), tierText(t.selected.Scope.Tier))
					}
					if len(t.sources) > 0 {
						ui.Text(c, T("restore.read_from")).Bold()
						ui.RadioGroup(c, func() {
							for i, source := range t.sources {
								if ui.Radio(c.Key(source.Copy.SourceLocator+source.Copy.LocationRef.ID), &t.source, i, journeyLocation(source.Copy.LocationRef)).Changed() {
									t.source = i
								}
							}
						}).Label(T("restore.read_from"))
					} else {
						muted(ui.Text(c, T("restoretest.no_readable_copy"))).Wrap()
						ui.Link(c, T("nav.locations"), "/locations")
					}
					muted(ui.Text(c, T("restoretest.confirm_hint"))).Wrap()
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { t.confirm = false })
				if t.selected != nil && len(t.sources) > 0 {
					ui.PrimaryButton(c, T("restoretest.start")).OnClick(func() { a.confirmTestSelection(t) })
				}
			})
		})
	}).Dismissed() {
		t.confirm = false
	}
}
