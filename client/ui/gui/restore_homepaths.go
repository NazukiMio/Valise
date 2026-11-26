package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/ui/service"
)

// WP7: cross-$HOME migration controls and sanitized config-path preview inside
// restorePlan (restoresetup.go).

func (a *App) restoreHomePathsSection(c *ui.Context, r *restoreFlow, p *service.RevivePlan) {
	if len(p.ExternalSymlinks) > 0 {
		notice(c, "warn", T("restore.ext_symlinks.warn", "n", len(p.ExternalSymlinks)))
		ui.Collapsible(c, T("restore.ext_symlinks.list", "n", len(p.ExternalSymlinks)), a.fold("restore-ext-symlinks"), func() {
			subPanel(ui.Column(c)).Gap(4).Children(func() {
				for i, s := range p.ExternalSymlinks {
					line := s.Root + "/" + s.Path + " -> " + s.Target
					mono(muted(ui.Text(c.Key("es-"+itoa(i)), line))).FontSize(fsCaption).Wrap().Selectable()
				}
			})
		})
	}
	if r.mode != revive.ModeMigrate {
		return
	}
	if p.HomeChanged {
		ui.Checkbox(c, &r.remapHomeSymlinks, T("restore.home.remap_symlinks", "old", p.SourceHome, "new", p.TargetHome))
	}
	hasMCP := p.Plan.Profile.AgentKit != nil && len(p.Plan.Profile.AgentKit.MCPAddArgv) > 0
	if hasMCP {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			needTag(c, T("need.admin"))
			ui.Checkbox(c, &r.carryMCP, T("restore.home.carry_mcp"))
		})
	}
	if p.HomeChanged && len(p.ConfigHomeHits) > 0 {
		subPanel(ui.Column(c)).Gap(6).Children(func() {
			ui.Checkbox(c, &r.rewriteConfigHomePaths, T("restore.home.rewrite_config", "n", len(p.ConfigHomeHits)))
			ui.Scroll(c).MaxHeight(160).Children(func() {
				codeBox(ui.Column(c)).Gap(3).Children(func() {
					for i, h := range p.ConfigHomeHits {
						line := h.Root + "/" + h.Path + ":L" + itoa(h.Line) + ": " + h.OldPath + " -> " + h.NewPath
						mono(muted(ui.Text(c.Key("ch-"+itoa(i)), line))).FontSize(fsCaption).Wrap().Selectable()
					}
				})
			})
		})
	}
}
