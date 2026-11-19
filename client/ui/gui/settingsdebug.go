package gui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

var (
	routeUserInfoRe = regexp.MustCompile(`[^/]+:[^/@]+@`)
	routeSecretRe   = regexp.MustCompile(`(?i)(?:sk-[A-Za-z0-9_-]{6,}|ghp_[A-Za-z0-9]{6,}|xox[baprs]-[A-Za-z0-9-]{6,})`)
)

// debugAction records a user-initiated button or action event when debug
// logging is active. Other GUI pages invoke this on key buttons.
func (a *App) debugAction(action, detail string) {
	if a == nil || a.svc == nil {
		return
	}
	a.svc.LogUIEvent(action, detail)
}

// trackRoute records route transitions once per distinct path after
// stripping query parameters, fragments, and any embedded credentials.
func (a *App) trackRoute() {
	if a == nil || a.router == nil {
		return
	}
	cur := sanitizeGUIRoute(a.router.Path())
	if cur == "" || cur == a.lastRoute {
		return
	}
	prev := a.lastRoute
	a.lastRoute = cur
	if prev == cloudAddRoute && cur != cloudAddRoute {
		// Leaving the cloud login page ends the session of the user's own
		// app key; a login still running keeps its own copy.
		a.clearCloudSecret()
		if ca := a.cloudAdding; ca != nil && ca.login == nil {
			a.cloudAdding = nil
		}
	}
	if a.svc == nil {
		return
	}
	if prev == "" {
		a.svc.LogUIEvent("route", cur)
		return
	}
	a.svc.LogUIEvent("route", prev+" -> "+cur)
}

func sanitizeGUIRoute(raw string) string {
	path, _, _ := strings.Cut(raw, "?")
	path, _, _ = strings.Cut(path, "#")
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	for i, seg := range parts {
		if routeUserInfoRe.MatchString(seg) || routeSecretRe.MatchString(seg) || strings.Contains(seg, ".ssh") {
			parts[i] = "<redacted>"
		}
	}
	return strings.Join(parts, "/")
}

func (a *App) debugSettings(c *ui.Context) {
	on := a.svc.DebugLogEnabled()
	envVal := service.DebugLogEnvOverride()
	stateText := T("settings.debug.off")
	if on {
		stateText = T("settings.debug.on")
	}
	note := T("settings.debug.desc")
	if envVal != "" {
		note = T("settings.debug.env_override", "val", envVal) + T("common.dot_sep") + note
	}
	card(ui.Column(c)).Gap(8).Children(func() {
		sectionTitle(ui.Text(c, T("settings.debug.title")))
		settingRow(c, T("settings.debug.switch_title"), stateText, note, func() {
			if ui.Switch(c, &on).Label(T("settings.debug.enable")).Changed() {
				if !on {
					a.debugAction("settings_debug_toggle", "off")
				}
				if err := a.svc.SetDebugLog(on); err != nil {
					a.debugNote = T("settings.debug.save_failed")
					a.debugNoteErr = true
					return
				}
				a.debugNote = ""
				a.debugNoteErr = false
				if on {
					a.debugAction("settings_debug_toggle", "on")
				}
			}
		})
		if p := a.svc.DebugLogPath(); p != "" {
			subPanel(ui.Column(c)).Gap(4).Children(func() {
				mono(muted(ui.Text(c, T("settings.debug.path", "path", p)))).Wrap()
			})
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Button(c, T("settings.debug.open_dir")).OnClick(a.openDebugLogDir)
			ui.Button(c, T("settings.debug.copy_recent")).OnClick(a.copyRecentDebugLogs)
		})
		if a.debugNote != "" {
			tone := palette.Ok
			if a.debugNoteErr {
				tone = palette.Warn
			}
			ui.Text(c, a.debugNote).TextColor(tone).FontSize(12).Wrap()
		}
	})
}

func (a *App) openDebugLogDir() {
	a.debugAction("settings_debug_open_dir", "")
	open := a.openDebugDir
	if open == nil {
		open = a.svc.OpenDebugLogDir
	}
	a.run(func() {
		err := open()
		a.update(func() {
			if err != nil {
				a.debugNote = T("settings.debug.open_failed")
				a.debugNoteErr = true
				return
			}
			a.debugNote = ""
			a.debugNoteErr = false
		})
	})
}

func (a *App) copyRecentDebugLogs() {
	text, err := a.svc.RecentDebugLogs(200)
	if err != nil {
		a.debugNote = T("settings.debug.copy_failed")
		a.debugNoteErr = true
		return
	}
	if strings.TrimSpace(text) == "" {
		a.debugNote = T("settings.debug.empty")
		a.debugNoteErr = false
		return
	}
	if a.copyText == nil || a.copyText(text) != nil {
		a.debugNote = T("settings.debug.copy_failed")
		a.debugNoteErr = true
		return
	}
	n := strings.Count(text, "\n") + 1
	a.debugNote = T("settings.debug.copied", "n", n)
	a.debugNoteErr = false
	a.debugAction("settings_debug_copy", strconv.Itoa(n))
}
