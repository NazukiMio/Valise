package gui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
)

// journey shows the saved points of one agent: the newest on the gold line,
// then all of them along a thin line, newest first, grouped by month; a
// point opens its details. Only successful backups are points (docs/UI.md
// 10.2). An agent that migrated also has its route (docs/UI.md 7.1).
func (a *App) journey(c *ui.Context, row *agentRow, points []journey.TimelinePoint) {
	if a.point != "" {
		for i, p := range points {
			if p.SnapshotRef.SnapshotID == a.point {
				a.pointDetail(c, points, i)
				return
			}
		}
		a.point = ""
	}
	pageHead(c, row.Name+" 走过的路", text.journeySub)
	stops := route(a.agents.value.Items, row.Server, row.Instance)
	if len(stops) > 0 {
		ui.Row(c).Children(func() { ui.Segmented(c, &a.journeyView, "保存的时刻", "走过的服务器") })
		if a.journeyView == 1 {
			routeView(c, stops)
			return
		}
	}
	if a.timeline.err != nil {
		errorBox(c, a.timeline.err, a.fold("timeline-err"))
	}
	if len(points) == 0 {
		if a.timeline.busy {
			ui.Spinner(c)
		} else {
			muted(ui.Text(c, text.journeyEmpty)).FontSize(14)
		}
		return
	}
	a.curveStrip(c, points)
	// The full list stays below, folded by month; only the newest month is
	// open (docs/UI.md 7.2).
	muted(ui.Text(c, fmt.Sprintf("全部 %d 个时刻", len(points)))).FontSize(12).LetterSpacing(0.6).Margin(8, 0, 0, 0)
	var months []string
	byMonth := map[string][]journey.TimelinePoint{}
	for _, p := range points {
		m := fmt.Sprintf("%d 年 %d 月", p.CreatedAt.Local().Year(), p.CreatedAt.Local().Month())
		if byMonth[m] == nil {
			months = append(months, m)
		}
		byMonth[m] = append(byMonth[m], p)
	}
	for mi, m := range months {
		open := a.foldDefault("month-"+row.Server+row.Instance+m, mi == 0)
		ui.Collapsible(c.Key("m"+m), fmt.Sprintf("%s · %d 个", m, len(byMonth[m])), open, func() {
			// One column without gaps, so the line runs unbroken.
			ui.Column(c).Gap(0).Children(func() {
				list := byMonth[m]
				for i, p := range list {
					a.pointLine(c.Key(p.SnapshotRef.SnapshotID), p, i == 0, i == len(list)-1)
				}
			})
		})
	}
	muted(ui.Text(c, text.journeyLocalOnly))
}

// pointLine is a node on the thin line and a one-line card.
func (a *App) pointLine(c *ui.Context, p journey.TimelinePoint, first, last bool) {
	ui.Row(c).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(16).AlignItems(ui.Center).Children(func() {
			top := ui.Box(c).Width(1).Height(13).Background(palette.Line)
			if first {
				top.Background(ui.RGBA(0, 0, 0, 0))
			}
			ui.Box(c).Size(8, 8).Radius(4).Background(palette.Node).Border(1.5, palette.Gold)
			bottom := ui.Box(c).Width(1).Grow(1).Background(palette.Line)
			if last {
				bottom.Background(ui.RGBA(0, 0, 0, 0))
			}
		})
		box := ui.Row(c).Grow(1).Padding(7, 12).Gap(12).Radius(8).Margin(0, 0, 4, 0).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(when(p.CreatedAt, c.Now()))
		if box.Hovered() {
			box.Background(palette.Card).Border(1, palette.CardBorder)
		}
		box.OnClick(func() { a.point = p.SnapshotRef.SnapshotID }).Children(func() {
			ui.Text(c, when(p.CreatedAt, c.Now())).FontSize(13).Bold().Width(120)
			muted(ui.Text(c, tierText(p.Scope.Tier))).Grow(1)
			muted(ui.Text(c, fmt.Sprintf(text.pointFiles, p.FileCount, size(p.PlainBytes))))
		})
	})
}

// pointDetail shows one saved point; earlier and later ones are a step
// away. It reads only what the local copies record: no restore action is
// offered here until the restore segment (docs/UI.md 9 未实现不渲染).
func (a *App) pointDetail(c *ui.Context, points []journey.TimelinePoint, i int) {
	p := points[i]
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "‹ "+text.pointBack).Clicked() {
			a.point = ""
		}
		ui.Spacer(c)
		if i+1 < len(points) && ui.Button(c, "更早").Clicked() {
			a.point = points[i+1].SnapshotRef.SnapshotID
		}
		if i > 0 && ui.Button(c, "更晚").Clicked() {
			a.point = points[i-1].SnapshotRef.SnapshotID
		}
	})
	pageHead(c, when(p.CreatedAt, c.Now()), p.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	card(ui.Column(c)).Children(func() {
		ui.Text(c, "这次带上了什么").Bold()
		fact(c, text.pointScope, tierText(p.Scope.Tier))
		fact(c, "内容", fmt.Sprintf(text.pointFiles, p.FileCount, size(p.PlainBytes)))
	})
	card(ui.Column(c)).Children(func() {
		ui.Text(c, text.pointCopies).Bold()
		for _, cp := range p.Copies {
			ui.Column(c.Key(cp.LocationRef.ID + cp.SourceLocator)).Gap(2).Children(func() {
				ui.Text(c, "仓库 "+cp.LocationRef.ID).FontSize(13)
				muted(ui.Text(c, "只读了备份记录，没有核对数据包，也没有试着解开"))
			})
		}
	})
	if len(p.Warnings) > 0 {
		card(ui.Column(c)).Children(func() {
			ui.Text(c, text.pointWarn).Bold().TextColor(palette.Warn)
			for _, w := range p.Warnings {
				ui.Text(c, w).FontSize(13)
			}
		})
	}
	ui.Collapsible(c, text.techInfo, a.fold("point-tech"), func() {
		muted(ui.Text(c, "备份 "+p.SnapshotRef.SnapshotID+"\n备份库 "+p.SnapshotRef.VaultID)).Font(cur.Fonts.Mono)
	})
}
