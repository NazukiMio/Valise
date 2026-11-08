package gui

import "github.com/egoist/mygo/ui"

// Icons are the line set of the approved 2026-11-05 sketch (docs/UI.md
// 7.3): the small suitcase, the robot and their companions, 24×24, stroke
// only, in the color of the text around them.
var icons = struct {
	valise, agent, server, folder, settings, plus, back, clock, open, search, cards, list, check, image *ui.SVG
}{
	valise:   svg(`<rect x="4" y="7" width="16" height="14" rx="3"/><path d="M9 7V4h6v3M9 11v6m6-6v6"/>`),
	agent:    svg(`<rect x="5" y="6" width="14" height="13" rx="4"/><path d="M12 3v3M8 12h.01M16 12h.01M9 16h6"/>`),
	server:   svg(`<rect x="4" y="4" width="16" height="6" rx="2"/><rect x="4" y="14" width="16" height="6" rx="2"/><path d="M8 7h.01M8 17h.01M13 7h3m-3 10h3"/>`),
	folder:   svg(`<path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3Z"/>`),
	settings: svg(`<circle cx="12" cy="12" r="4"/><path d="M12 2v3m0 14v3M2 12h3m14 0h3M5 5l2 2m10 10 2 2M5 19l2-2M17 7l2-2"/>`),
	plus:     svg(`<path d="M12 5v14M5 12h14"/>`),
	back:     svg(`<path d="m14 6-6 6 6 6"/>`),
	clock:    svg(`<circle cx="12" cy="12" r="9"/><path d="M12 7v6l4 2"/>`),
	open:     svg(`<path d="M14 4h6v6m0-6L10 14M10 5H4v15h15v-6"/>`),
	search:   svg(`<circle cx="11" cy="11" r="6"/><path d="m20 20-4.5-4.5"/>`),
	cards:    svg(`<rect x="4" y="4" width="7" height="7" rx="2"/><rect x="13" y="4" width="7" height="7" rx="2"/><rect x="4" y="13" width="7" height="7" rx="2"/><rect x="13" y="13" width="7" height="7" rx="2"/>`),
	list:     svg(`<path d="M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01"/>`),
	check:    svg(`<path d="m5 12 4 4L19 6"/>`),
	image:    svg(`<rect x="3" y="5" width="18" height="14" rx="3"/><circle cx="9" cy="10" r="2"/><path d="m21 16-5-5-8 8"/>`),
}

func svg(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}
