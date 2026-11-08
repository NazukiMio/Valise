package gui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
)

// journey lists the saved points of one agent along a thin line, newest
// first, grouped by month; a point opens its details. Only successful
// backups are points (docs/UI.md 10.2). The flowing line, glow and page
// turns of 10.1 follow once the design is chosen (docs/UI.md 7.1).
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
	// One column without gaps, so the line runs unbroken from node to node.
	ui.Column(c).Gap(0).Children(func() {
		month := ""
		for i, p := range points {
			m := fmt.Sprintf("%d 年 %d 月", p.CreatedAt.Local().Year(), p.CreatedAt.Local().Month())
			if m != month {
				month = m
				ui.Row(c.Key("m"+m)).Padding(10, 0, 12, 0).Children(func() {
					ui.Text(c, m).Bold()
				})
			}
			a.pointLine(c.Key(p.SnapshotRef.SnapshotID), p, i == 0, i == len(points)-1)
		}
	})
	muted(ui.Text(c, text.journeyLocalOnly))
}

// pointLine is a node on the line and its card.
func (a *App) pointLine(c *ui.Context, p journey.TimelinePoint, first, last bool) {
	ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(18).AlignItems(ui.Center).Children(func() {
			lineColor := palette.Line
			top := ui.Box(c).Width(1).Height(18).Background(lineColor)
			if first {
				top.Background(ui.RGBA(0, 0, 0, 0))
			}
			ui.Box(c).Size(11, 11).Radius(6).Background(ui.Hex("#ffffff")).Border(2, palette.Gold).
				Shadow(0, 0, 8, 1, palette.GoldSoft)
			bottom := ui.Box(c).Width(1).Grow(1).Background(lineColor)
			if last {
				bottom.Background(ui.RGBA(0, 0, 0, 0))
			}
		})
		box := card(ui.Column(c)).Grow(1).Padding(12, 16).Gap(3).Margin(0, 0, 12, 0).Cursor(ui.CursorPointer).
			Label(when(p.CreatedAt, c.Now()))
		if box.Hovered() {
			box.Border(1, palette.Gold)
		}
		box.OnClick(func() { a.point = p.SnapshotRef.SnapshotID }).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.End).Children(func() {
				ui.Text(c, when(p.CreatedAt, c.Now())).Bold().FontSize(14)
				muted(ui.Text(c, tierText(p.Scope.Tier)))
			})
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
		muted(ui.Text(c, "备份 "+p.SnapshotRef.SnapshotID+"\n金库 "+p.SnapshotRef.VaultID)).Font("monospace")
	})
}
