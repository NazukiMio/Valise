package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/identity"
	"valise/ui/service"
)

// station is one stop on an agent's route: a server it lived on, or a stop
// whose server is not known.
type station struct {
	Label   string
	Note    string
	Current bool // the agent being looked at
	Unknown bool
}

// route walks the recorded migrations through one agent: back along its
// migration relations to their sources, and on along MigratedTo. Only
// recorded relations link stops; nothing is joined by name (docs/UI.md
// 7.1, 10.2). It returns nil when the agent never migrated.
func route(items []service.AgentSummary, server, instance string) []station {
	var cur *service.AgentSummary
	for i := range items {
		if items[i].ConnectionID == server && items[i].Instance.InstanceID == instance {
			cur = &items[i]
		}
	}
	if cur == nil {
		return nil
	}
	byRef := func(ref identity.AgentRef) *service.AgentSummary {
		for i := range items {
			if items[i].AgentRef != nil && *items[i].AgentRef == ref {
				return &items[i]
			}
		}
		return nil
	}
	stops := []station{{Label: cur.ConnectionID, Current: true}}
	seen := map[*service.AgentSummary]bool{cur: true}
	// Backwards: where it came from.
	for at := cur; at != nil; {
		var from *identity.RelationSummary
		for i, rel := range at.Relations {
			if rel.Kind == identity.MigrationCompleted && (at.AgentRef == nil || rel.TargetAgent == *at.AgentRef) {
				from = &at.Relations[i]
			}
		}
		if from == nil {
			break
		}
		next := (*service.AgentSummary)(nil)
		stop := station{Label: "来源未知", Note: "迁移时连不上原服务器", Unknown: true}
		if from.SourceAgent != nil {
			if next = byRef(*from.SourceAgent); next != nil && !seen[next] {
				stop = station{Label: next.ConnectionID, Note: "迁出"}
				seen[next] = true
			} else {
				next = nil
				stop = station{Label: "另一台服务器", Note: "这台电脑没有读到它", Unknown: true}
			}
		}
		stops = append([]station{stop}, stops...)
		at = next
	}
	// Forwards: where it went.
	for at := cur; at.MigratedTo != nil; {
		var next *service.AgentSummary
		for i := range items {
			if items[i].ConnectionID == at.MigratedTo.ConnectionID && items[i].AgentRef != nil && *items[i].AgentRef == at.MigratedTo.AgentRef {
				next = &items[i]
			}
		}
		stops = append(stops, station{Label: at.MigratedTo.ConnectionID, Note: "迁入"})
		if next == nil || seen[next] {
			break
		}
		seen[next] = true
		at = next
	}
	if len(stops) < 2 {
		return nil
	}
	return stops
}

// routeView draws the route as stops on a gentle line, oldest on the left.
// An unknown stop is drawn hollow and dashed, never as a guessed server.
func routeView(c *ui.Context, stops []station) {
	n := len(stops)
	box := ui.Box(c).Height(190).Radius(16).Background(ui.Hex("#fbf9f4")).Border(1, palette.CardBorder).Clip()
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		y := func(i int) float32 { return r.Y + r.H*0.42 + float32(i%2)*18 - 9 }
		x := func(i int) float32 { return r.X + r.W*(float32(i)+0.5)/float32(n) }
		for i := 1; i < n; i++ {
			seg := (&ui.Path{}).MoveTo(x(i-1), y(i-1)).CubeTo((x(i-1)+x(i))/2, y(i-1)-26, (x(i-1)+x(i))/2, y(i)+26, x(i), y(i))
			if stops[i-1].Unknown || stops[i].Unknown {
				seg.Dash(0, 5, 5)
			}
			p.StrokePath(seg, 1.8, palette.Gold)
		}
		for i, st := range stops {
			dot := (&ui.Path{}).Circle(x(i), y(i), 7)
			switch {
			case st.Current:
				for j, rad := range []float32{22, 15} {
					p.FillPath((&ui.Path{}).Circle(x(i), y(i), rad), ui.RGBA(212, 192, 163, 0.22*float32(j+1)))
				}
				p.FillPath(dot, ui.Hex("#8c7e6c"))
			case st.Unknown:
				p.FillPath(dot, ui.Hex("#fbf9f4"))
				p.StrokePath(dot.Dash(0, 2, 2), 1.4, palette.TextSub)
			default:
				p.FillPath(dot, ui.Hex("#ffffff"))
				p.StrokePath(dot, 1.6, palette.Gold)
			}
		}
	})
	box.Children(func() {
		ui.Row(c).Fill().Children(func() {
			for i, st := range stops {
				ui.Column(c.Key(i)).Grow(1).Basis(0).FillHeight().Justify(ui.End).AlignItems(ui.Center).Padding(0, 6, 18, 6).Gap(2).Children(func() {
					t := ui.Text(c, st.Label).FontSize(13).MaxLines(1)
					if st.Current {
						t.Bold()
					}
					if st.Unknown {
						t.TextColor(palette.TextSub)
					}
					note := st.Note
					if st.Current {
						note = "现在在这里"
					}
					muted(ui.Text(c, note)).FontSize(11).MaxLines(1)
				})
			}
		})
	})
	muted(ui.Text(c, "只按记录下来的迁移关系连线；连不上原服务器时做的迁移，来源画成虚线的“来源未知”。"))
}
