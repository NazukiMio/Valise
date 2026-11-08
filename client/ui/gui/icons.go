package gui

import "github.com/egoist/mygo/ui"

// Icons are simple line drawings made for Valise (24×24, stroke only).
var icons = struct {
	valise, agent, server *ui.SVG
}{
	valise: svg(`<rect x="3" y="7" width="18" height="13" rx="3"/><path d="M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/><path d="M3 12h18"/>`),
	agent:  svg(`<rect x="5" y="8" width="14" height="11" rx="3"/><path d="M12 4v4"/><circle cx="9.5" cy="13" r="1"/><circle cx="14.5" cy="13" r="1"/>`),
	server: svg(`<rect x="4" y="4" width="16" height="7" rx="2"/><rect x="4" y="13" width="16" height="7" rx="2"/><path d="M8 7.5h.01M8 16.5h.01"/>`),
}

func svg(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}
