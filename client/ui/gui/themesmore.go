package gui

import "github.com/egoist/mygo/ui"

// The heavier built-in themes (docs/UI.md 7.4.4): each is a palette, a
// material and a few sizes over the classic layout. Their warnings, errors
// and dialogs keep the same contrast rules as the classic themes.

// journalTheme is 手账: a paper notebook with ink-blue writing, a dot grid
// and notes stuck in with a short hard shadow.
func journalTheme() *Theme {
	t := classicLight()
	t.ID, t.Name, t.Timeline = ThemeJournal, "Journal", TimelineGold
	t.Fonts.Display = "Kaiti SC, STKaiti, LXGW WenKai, KaiTi, Noto Serif CJK SC, Songti SC"
	t.Sizes.Radius, t.Sizes.CardRadius, t.Sizes.PanelRadius = 6, 8, 6
	t.Material = Material{Kind: MaterialJournal, Grain: 1, Grid: 22}
	t.Pal = Palette{
		Paper: ui.Hex("#f3eee3"), Card: ui.Hex("#fffdf8"), CardBorder: ui.RGBA(120, 95, 60, 0.20),
		Text: ui.Hex("#2c2925"), TextSub: ui.Hex("#665d50"), Line: ui.Hex("#e2d8c5"), Board: ui.Hex("#faf6ec"), Hover: ui.Hex("#f6efe0"),
		Ok: ui.Hex("#466b40"), Warn: ui.Hex("#94591a"), Stop: ui.Hex("#83796a"),
		Danger: ui.Hex("#a3362a"), DangerBg: ui.Hex("#f8e8e3"), DangerBorder: ui.RGBA(163, 54, 42, 0.45),
		Side: ui.Hex("#e6d9c1"), SideText: ui.Hex("#574c3e"), Brand: ui.Hex("#34548a"),
		Accent: ui.Hex("#34548a"), AccentHover: ui.Hex("#2a4675"), AccentText: ui.Hex("#ffffff"),
		Surface: ui.Hex("#fffdf8"), SurfaceHover: ui.Hex("#f4ecdc"), Border: ui.Hex("#d6c9b1"), Focus: ui.Hex("#93a9cf"),
		Gold: ui.Hex("#e0bc72"), GoldSoft: ui.RGBA(240, 222, 180, 0.9), Canvas: ui.Hex("#fbf7ee"),
		Strand: ui.Hex("#7d8fb3"), Spine: ui.Hex("#34548a"), Node: ui.Hex("#fffdf8"), NodeCore: ui.Hex("#34548a"),
		AlertBg: ui.Hex("#f8ead2"), AlertBorder: ui.RGBA(196, 146, 74, 0.5), AlertText: ui.Hex("#634a2b"),
		ErrorBg: ui.Hex("#f8e8e3"), ErrorBorder: ui.RGBA(163, 54, 42, 0.35),
		TroubleBg: ui.Hex("#fcf4e6"), TroubleBorder: ui.RGBA(196, 140, 74, 0.65),
		LogoBg: ui.Hex("#fffdf8"), Shadow: ui.Hex("#4a3a22"),
	}
	return t
}

// nightTheme is 夜航: a night sky with a lantern's gold, panes lit from
// above, the timeline a line of light.
func nightTheme() *Theme {
	t := classicDark()
	t.ID, t.Name, t.Timeline = ThemeNight, "Night voyage", TimelineStrands
	t.Fonts.Display = ""
	t.Sizes.CardRadius, t.Sizes.PanelRadius = 16, 12
	t.Material = Material{Kind: MaterialNight, Stars: 9}
	t.Pal = Palette{
		Paper: ui.Hex("#0c1020"), Card: ui.RGBA(24, 30, 52, 0.86), CardBorder: ui.RGBA(170, 186, 255, 0.13),
		Text: ui.Hex("#e7eaf3"), TextSub: ui.Hex("#a1a9bf"), Line: ui.Hex("#2b3352"), Board: ui.RGBA(19, 25, 44, 0.9), Hover: ui.Hex("#1d2440"),
		Ok: ui.Hex("#83c99d"), Warn: ui.Hex("#eab468"), Stop: ui.Hex("#8e96ab"),
		Danger: ui.Hex("#ff8f80"), DangerBg: ui.Hex("#3b1f2a"), DangerBorder: ui.RGBA(255, 143, 128, 0.45),
		Side: ui.RGBA(8, 11, 22, 0.72), SideText: ui.Hex("#a1a9bf"), Brand: ui.Hex("#e6c47e"),
		Accent: ui.Hex("#e6c47e"), AccentHover: ui.Hex("#f0d49d"), AccentText: ui.Hex("#141a2e"),
		Surface: ui.Hex("#1b2238"), SurfaceHover: ui.Hex("#242c46"), Border: ui.Hex("#323b5c"), Focus: ui.Hex("#7486c4"),
		Gold: ui.Hex("#e6c47e"), GoldSoft: ui.Hex("#2b2a33"), Canvas: ui.RGBA(14, 19, 36, 0.7),
		Strand: ui.Hex("#94aaf0"), Spine: ui.Hex("#c3cff5"), Node: ui.Hex("#1b2238"), NodeCore: ui.Hex("#e6c47e"),
		AlertBg: ui.Hex("#33291d"), AlertBorder: ui.RGBA(234, 180, 104, 0.38), AlertText: ui.Hex("#eed6aa"),
		ErrorBg: ui.Hex("#3b1f2a"), ErrorBorder: ui.RGBA(255, 143, 128, 0.42),
		TroubleBg: ui.Hex("#2a2534"), TroubleBorder: ui.RGBA(234, 180, 104, 0.5),
		LogoBg: ui.Hex("#e7eaf3"), Shadow: ui.Hex("#000000"),
	}
	return t
}
