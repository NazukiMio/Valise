package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/install"
)

// WP5: bounded scrollable sub-windows for script security check findings and
// full script previews across installpage.go, installcustom.go, and
// restore_install.go.

func (a *App) renderScriptFindings(c *ui.Context, fs []install.Finding) {
	if len(fs) == 0 {
		ui.Text(c, T("install.check_none")).FontSize(12).TextColor(palette.Ok).Wrap()
		return
	}
	hasRefuse := false
	for _, f := range fs {
		if f.Refuse {
			hasRefuse = true
			break
		}
	}
	summaryKey := "install.check_hits_count"
	if hasRefuse {
		summaryKey = "install.check_refused_count"
	}
	ui.Text(c, T(summaryKey, "n", len(fs))).FontSize(12).Bold().TextColor(palette.Warn).Wrap()
	ui.Column(c).Radius(8).Border(1, palette.CardBorder).Background(palette.Card).Padding(8, 10).Children(func() {
		ui.Scroll(c).MaxHeight(200).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				for i, f := range fs {
					key := "install.finding_hit"
					if f.Refuse {
						key = "install.finding_refuse"
					}
					ui.Column(c.Key(i)).Gap(2).Children(func() {
						ui.Text(c, "· "+T(key, "reason", f.Reason)).FontSize(12).TextColor(palette.Warn).Wrap().Selectable()
						switch {
						case f.Line > 0 && f.Snippet != "":
							mono(muted(ui.Text(c, T("install.finding_where", "line", f.Line, "snippet", f.Snippet)))).FontSize(fsCaption).Wrap().Selectable()
						case f.Line > 0:
							mono(muted(ui.Text(c, T("install.finding_line", "line", f.Line)))).FontSize(fsCaption).Wrap().Selectable()
						case f.Snippet != "":
							mono(muted(ui.Text(c, f.Snippet))).FontSize(fsCaption).Wrap().Selectable()
						}
					})
				}
			})
		})
	})
}

func (a *App) scriptPreview(c *ui.Context, foldKey string, script []byte, openDefault bool) {
	text := string(script)
	lines := strings.Count(text, "\n") + 1
	var fold *bool
	if openDefault {
		fold = a.foldDefault(foldKey, true)
	} else {
		fold = a.fold(foldKey)
	}
	ui.Collapsible(c, T("install.script", "n", lines), fold, func() {
		ui.Column(c).Gap(6).Children(func() {
			if a.copyText != nil {
				ui.Row(c).Justify(ui.End).Children(func() {
					quietButton(c, T("install.copy_script")).OnClick(func() { _ = a.copyText(text) })
				})
			}
			ui.Scroll(c).MaxHeight(260).Children(func() {
				codeBox(mono(ui.Text(c, text)).FontSize(fsCaption).Wrap().Selectable())
			})
		})
	})
}

// Legacy locale key "install.check_hits" remains for imported catalogs.
