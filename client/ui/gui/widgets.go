package gui

import "github.com/egoist/mygo/ui"

// The pieces every page shares (docs/DESIGN.md 2, 3): what can be clicked,
// how a tone reads, and the two buttons mygo has no look for.

// over reports whether the pointer is on e, and whether it presses it, for a
// Draw callback. A copy of an element that is leaving (a transition's Exit,
// a dialog closing) is painted with the callback of its last build, whose
// element mygo no longer answers for; such a copy takes no pointer, so it is
// neither.
func over(e ui.Element) (hover, press bool) {
	defer func() {
		if recover() != nil {
			hover, press = false, false
		}
	}()
	return e.Hovered(), e.Pressed()
}

// pressable makes a row or a card one click target: the pointer, the role
// and its name for assistive technology, and a hover that is only painted,
// so the pointer coming and going does not build the view again. radius is
// the box's own.
func pressable(e ui.Element, label string, radius float32) ui.Element {
	e.Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	return e.Draw(func(p *ui.Painter, r ui.Rect) {
		switch hover, press := over(e); {
		case press:
			p.Fill(r, palette.Hover.Mix(palette.Line, 0.35), radius)
		case hover:
			p.Fill(r, palette.Hover, radius)
			p.Stroke(r, palette.Line, radius, 1)
		}
	})
}

// chevron ends a row that opens another page.
func chevron(c *ui.Context) {
	ui.Icon(c, icons.chevron).FontSize(fsTitle3).TextColor(palette.TextSub)
}

// backLink is a page's way back: one look, one place for the arrow.
func backLink(c *ui.Context, label, path string) ui.Element {
	return ui.Link(c, "‹ "+label, path).FontSize(fsSub)
}

// toneInk and toneGround are a tone's text and surface.
func toneInk(tone string) ui.Color {
	switch tone {
	case "ok":
		return palette.Ok
	case "warn":
		return palette.Warn
	case "danger":
		return palette.Danger
	}
	return palette.TextSub
}

func toneGround(tone string) (bg, border ui.Color) {
	switch tone {
	case "warn":
		return palette.AlertBg, palette.AlertBorder
	case "danger":
		return palette.DangerBg, palette.DangerBorder
	}
	return palette.Board, palette.CardBorder
}

// toneDot is a state's mark. What is known to be off is a hollow ring, so
// it does not read as plain text's gray nor as unknown.
func toneDot(c *ui.Context, tone string) {
	d := ui.Box(c).Size(8, 8).Radius(4)
	if tone == "stop" {
		d.Border(1.5, palette.Stop)
		return
	}
	d.Background(toneColor(tone))
}

// notice is one sentence that must be seen: a warning from a plan, a
// serious finding. Warnings are never drawn as secondary text.
func notice(c *ui.Context, tone, text string) {
	bg, border := toneGround(tone)
	ui.Row(c).Gap(spItem).Padding(spItem, spGroup).Radius(radPanel).Background(bg).Border(1, border).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.alert).FontSize(fsTitle3).TextColor(toneInk(tone))
		ui.Text(c, text).FontSize(fsSub).Grow(1).Wrap()
	})
}

// statusRow is one fact with its own mark: facts are listed apart, never
// merged into one badge (AGENTS.md 7.5).
func statusRow(c *ui.Context, tone, label, value string) {
	ui.Row(c.Key("st-" + label)).Gap(spItem).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Padding(6, 0, 0, 0).Children(func() { toneDot(c, tone) })
		ui.Text(c, label).FontSize(fsSub).TextColor(palette.TextSub).Width(cur.Sizes.LabelWidth + 24).Wrap()
		v := ui.Text(c, value).FontSize(fsSub).Grow(1).Wrap()
		if tone == "warn" || tone == "danger" {
			v.TextColor(toneInk(tone))
		}
	})
}

// dangerButton confirms what cannot be undone. It is drawn only inside a
// confirmation, and its words say what happens.
func dangerButton(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c).Padding(7, 14).Radius(cur.Sizes.Radius).Background(palette.Danger).Label(label)
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		if hover, press := over(b); hover || press {
			p.Fill(r, palette.Shadow.Alpha(0.14), cur.Sizes.Radius)
		}
	})
	return b.Children(func() { ui.Text(c, label).TextColor(palette.Card.Alpha(1)).Bold() })
}

// quietButton is an action that stays out of the way: close, got it.
func quietButton(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c).Padding(7, 12).Radius(cur.Sizes.Radius).Label(label)
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		if hover, press := over(b); hover || press {
			p.Fill(r, palette.SurfaceHover, cur.Sizes.Radius)
		}
	})
	return b.Children(func() { ui.Text(c, label).TextColor(palette.TextSub) })
}

// pressableCard is pressable for a whole card: the hover is the accent on
// its edge, as the card already has its own ground.
func pressableCard(e ui.Element, label string) ui.Element {
	e.Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	return e.Draw(func(p *ui.Painter, r ui.Rect) {
		if hover, press := over(e); hover || press {
			p.Stroke(r, palette.Brand, cur.Sizes.CardRadius, 1.5)
		}
	})
}
