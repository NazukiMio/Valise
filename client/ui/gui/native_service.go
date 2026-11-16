package gui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/core/install"
	"valise/ui/service"
)

func (a *App) nativeOnboardCard(c *ui.Context, server, instance string, n *nativeState) {
	info := n.info
	activeUnits := a.activeNativeUnits(server, instance, info)
	card(ui.Column(c)).Gap(8).Children(func() {
		sectionTitle(ui.Text(c, T("native.onboard_title")))
		muted(ui.Text(c, T("native.onboard_hint"))).Wrap()
		for _, hint := range info.Hints {
			ui.Text(c, hint).Wrap()
		}
		ui.Collapsible(c, T("native.command"), a.fold("native-command"), func() {
			mono(ui.Text(c, strings.Join(info.Argv, " "))).Wrap()
		})
		if len(activeUnits) > 0 {
			ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Border(1, palette.TroubleBorder).Background(palette.Board).Children(func() {
				ui.Text(c, T("native.onboard_service_warn", "units", strings.Join(activeUnits, "、"))).Bold().Wrap()
				muted(ui.Text(c, T("native.stop_and_onboard_note"))).FontSize(12).Wrap()
			})
			ui.Row(c).Gap(8).Children(func() {
				if len(info.SessionArgv) == 0 && !info.Shell {
					ui.PrimaryButton(c, T("native.stop_and_onboard")).OnClick(func() { a.startNative(server, instance, service.NativeCLI, true) })
				} else {
					ui.Button(c, T("native.stop_and_onboard")).OnClick(func() { a.startNative(server, instance, service.NativeCLI, true) })
				}
				ui.Button(c, T("native.connect_direct")).OnClick(func() { a.startNative(server, instance, service.NativeCLI, false) })
			})
			return
		}
		ui.Row(c).Gap(8).Children(func() {
			if len(info.SessionArgv) == 0 && !info.Shell {
				ui.PrimaryButton(c, T("native.connect")).OnClick(func() { a.startNative(server, instance, service.NativeCLI, false) })
			} else {
				ui.Button(c, T("native.connect")).OnClick(func() { a.startNative(server, instance, service.NativeCLI, false) })
			}
		})
	})
}

func (a *App) activeNativeUnits(server, instance string, info *install.NativeInfo) []string {
	var units []enroll.ServiceState
	if info != nil && len(info.Service) > 0 {
		units = info.Service
	} else {
		for _, it := range a.agents.value.Items {
			if it.ConnectionID == server && it.Instance.InstanceID == instance {
				units = it.Instance.Service
				break
			}
		}
	}
	var active []string
	for _, u := range units {
		if (u.LoadState == "" || u.Exists()) && (u.Active == "active" || u.Active == "activating" || u.Active == "reloading") {
			active = append(active, u.Unit)
		}
	}
	return active
}

func (a *App) pauseServiceForNative(ctx context.Context, r *nativeRun) bool {
	if err := a.svc.RequireGate(ctx, r.server, r.instance); err != nil {
		a.update(func() {
			r.stoppingService, r.running, r.err = false, false, err
			if r.exit != nil {
				r.exit()
			}
		})
		return false
	}
	stopped, err := a.svc.PauseInstanceServices(ctx, r.server, r.instance)
	a.update(func() {
		n := a.nativeOf(r.server, r.instance)
		if len(stopped) > 0 {
			r.stoppedUnits = appendUnits(r.stoppedUnits, stopped)
			n.pausedUnits = appendUnits(n.pausedUnits, stopped)
			n.restoreRes, n.restoreErr = nil, nil
			a.setNativeServiceState(r.server, r.instance, stopped, "inactive")
		}
		r.stoppingService = false
		if err != nil {
			r.running = false
			if !r.stopped && !errors.Is(err, context.Canceled) {
				r.err = err
			}
			if r.exit != nil {
				r.exit()
			}
			a.refreshNativeAndAgents(r.server, r.instance)
		}
	})
	return err == nil
}

func (a *App) restoreNativeServices(server, instance string, units []string) {
	n := a.nativeOf(server, instance)
	if n.restoreBusy || len(units) == 0 {
		return
	}
	n.restoreBusy, n.restoreErr, n.restoreRes = true, nil, nil
	list := append([]string(nil), units...)
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := a.svc.RestoreInstanceServices(ctx, server, instance, list)
		a.update(func() {
			n.restoreBusy, n.restoreRes, n.restoreErr = false, res, err
			if err == nil {
				n.pausedUnits = nil
				if a.native != nil && a.native.server == server && a.native.instance == instance {
					a.native.stoppedUnits = nil
				}
			} else if res != nil && len(res.StartedUnits) > 0 {
				n.pausedUnits = removeUnits(n.pausedUnits, res.StartedUnits)
			}
			if res != nil && len(res.Service) > 0 && n.info != nil {
				n.info.Service = res.Service
			}
			a.refreshNativeAndAgents(server, instance)
		})
	})
}

func (a *App) nativePausedBanner(c *ui.Context, server, instance string, n *nativeState) {
	if len(n.pausedUnits) > 0 {
		reportPanel(ui.Row(c)).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("native.paused_pending", "units", strings.Join(n.pausedUnits, "、"))).Grow(1).Wrap()
			if n.restoreBusy {
				ui.Spinner(c)
				muted(ui.Text(c, T("native.restoring_service")))
			} else {
				ui.Button(c, T("native.restore_service")).OnClick(func() { a.restoreNativeServices(server, instance, n.pausedUnits) })
			}
		})
	}
	a.nativeRestoreReport(c, n)
}

func (a *App) nativeRestoreReport(c *ui.Context, n *nativeState) {
	if !n.restoreBusy && n.restoreErr == nil && n.restoreRes == nil {
		return
	}
	reportPanel(ui.Column(c)).Gap(6).Children(func() {
		if n.restoreBusy {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, T("native.restoring_service")).FontSize(13)
			})
			return
		}
		if res := n.restoreRes; res != nil && len(res.StartedUnits) > 0 {
			ui.Text(c, T("native.service_started", "units", strings.Join(res.StartedUnits, "、"))).FontSize(13).Bold()
		}
		if n.restoreErr != nil {
			errorBox(c, n.restoreErr, a.fold("native-restore-err"))
		}
		res := n.restoreRes
		if res == nil {
			return
		}
		if res.Checked {
			s, tone := serviceText(res.Service)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(tone))
				ui.Text(c, T("health.after", "state", s)).FontSize(13)
			})
			if len(res.Entries) > 0 {
				entryMsg, entryTone := T("entry.closed"), "warn"
				for _, e := range res.Entries {
					if e.Listening {
						entryMsg, entryTone = T("entry.listening"), "ok"
					}
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(entryTone))
					ui.Text(c, entryMsg).FontSize(13)
				})
			}
		}
		if v := res.Verification; v != nil {
			tone := "warn"
			if v.State == "resolved" {
				tone = "ok"
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(tone))
				muted(ui.Text(c, v.Text)).FontSize(12).Wrap()
			})
		}
	})
}

func (a *App) setNativeServiceState(server, instance string, units []string, active string) {
	if n := a.natives[server+"/"+instance]; n != nil && n.info != nil {
		for i := range n.info.Service {
			if slices.Contains(units, n.info.Service[i].Unit) {
				n.info.Service[i].Active = active
			}
		}
	}
	for i := range a.agents.value.Items {
		it := &a.agents.value.Items[i]
		if it.ConnectionID != server || it.Instance.InstanceID != instance {
			continue
		}
		for j := range it.Instance.Service {
			if slices.Contains(units, it.Instance.Service[j].Unit) {
				it.Instance.Service[j].Active = active
			}
		}
	}
}

func (a *App) refreshNativeAndAgents(server, instance string) {
	key := server + "/" + instance
	n := a.natives[key]
	if n != nil {
		a.run(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			info, err := a.svc.InstanceNativeInfo(ctx, server, instance)
			if err == nil && info != nil {
				a.update(func() { n.info = info })
			}
		})
	}
	if a.health[key] != nil {
		a.readHealth(server, instance)
	}
	if list, err := a.svc.ServerList(); err == nil && len(list) > 0 {
		a.refreshAgents()
	}
}

func appendUnits(dst, more []string) []string {
	for _, u := range more {
		if !slices.Contains(dst, u) {
			dst = append(dst, u)
		}
	}
	return dst
}

func removeUnits(dst, done []string) []string {
	var out []string
	for _, u := range dst {
		if !slices.Contains(done, u) {
			out = append(out, u)
		}
	}
	return out
}
