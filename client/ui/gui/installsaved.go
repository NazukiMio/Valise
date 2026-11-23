package gui

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/ui/service"
)

// Saved custom install presets (docs/INSTALL.md section 7): selecting a saved
// preset only pre-fills the input and runs the full PlanCustomInstall flow
// (local HTTPS download, SHA-256 display, two-tier static check, and risk
// sentence confirmation).

func (a *App) loadSavedInstalls() {
	if a.savedInstallsLoaded {
		return
	}
	a.savedInstallsLoaded = true
	a.refreshSavedInstalls()
}

func (a *App) refreshSavedInstalls() {
	a.run(func() {
		list, err := a.svc.SavedInstalls()
		a.update(func() {
			a.savedInstallsLoaded = true
			if err != nil {
				a.savedInstallsErr = err
				return
			}
			a.savedInstallsErr = nil
			a.savedInstalls = list
		})
	})
}

func (a *App) saveCustomInstallPreset(name, raw, profileID string) {
	a.run(func() {
		_, err := a.svc.SaveCustomInstall(name, raw, profileID)
		if err != nil {
			a.update(func() { a.savedInstallsErr = err })
			return
		}
		list, listErr := a.svc.SavedInstalls()
		a.update(func() {
			a.savedInstallsLoaded = true
			a.savedInstallsErr = listErr
			if listErr == nil {
				a.savedInstalls = list
			}
		})
	})
}

func (a *App) deleteCustomInstallPreset(id string) {
	a.run(func() {
		err := a.svc.DeleteCustomInstall(id)
		if err != nil {
			a.update(func() { a.savedInstallsErr = err })
			return
		}
		list, listErr := a.svc.SavedInstalls()
		a.update(func() {
			a.savedInstallsLoaded = true
			a.savedInstallsErr = listErr
			if listErr == nil {
				a.savedInstalls = list
			}
		})
	})
}

func (a *App) useSavedCustomInstall(host string, item config.SavedInstall) {
	if a.customInstalls == nil {
		a.customInstalls = map[string]*customInstallRun{}
	}
	ci := a.customInstalls[host]
	if ci == nil {
		ci = &customInstallRun{}
		a.customInstalls[host] = ci
	}
	ci.raw = item.Raw
	ci.conn = nil
	ci.plan = nil
	ci.planErr = nil
	ci.sentence = ""
	ci.riskErr = nil
	ci.copied = ""
	a.router.Push("/servers/" + host + "/install-custom")
	a.prepareCustomInstallForProfile(host, ci, item.ProfileID)
}

func (a *App) savedInstallTiles(c *ui.Context, host string) {
	a.loadSavedInstalls()
	if a.savedInstallsErr != nil {
		errorBox(c, a.savedInstallsErr, a.fold("saved-installs-err-"+host))
	}
	if len(a.savedInstalls) == 0 {
		return
	}
	ui.Column(c).Gap(8).Children(func() {
		muted(ui.Text(c, T("install.saved.title"))).FontSize(12)
		for _, item := range a.savedInstalls {
			it := item
			card(ui.Row(c.Key("saved-" + it.ID))).Gap(10).AlignItems(ui.Center).Children(func() {
				left := pressable(ui.Column(c).Grow(1).Gap(4).Padding(4, 6), it.Name, radControl)
				left.OnClick(func() { a.useSavedCustomInstall(host, it) }).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
						ui.Text(c, it.Name).Bold()
						plainTag(c, T("install.saved.badge"))
					})
					mono(muted(ui.Text(c, truncatePresetRaw(it.Raw)))).FontSize(fsCaption).MaxLines(1)
				})
				quietButton(c, T("install.saved.delete")).OnClick(func() { a.deleteCustomInstallPreset(it.ID) })
			})
		}
	})
}

func truncatePresetRaw(raw string) string {
	s := strings.TrimSpace(raw)
	runes := []rune(s)
	if len(runes) <= 72 {
		return s
	}
	return string(runes[:71]) + "…"
}

func defaultCustomPresetName(p *service.CustomInstallPlan) string {
	if p == nil {
		return ""
	}
	if p.URL != "" {
		if u, err := url.Parse(p.URL); err == nil && u.Host != "" {
			if p.ScriptName != "" {
				return u.Host + "/" + p.ScriptName
			}
			return u.Host
		}
	}
	if len(p.Command) >= 2 {
		return strings.Join(p.Command[:2], " ")
	}
	if len(p.Command) == 1 {
		return p.Command[0]
	}
	return truncatePresetRaw(p.Raw)
}
