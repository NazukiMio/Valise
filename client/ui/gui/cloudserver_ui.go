package gui

import (
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/kittools"
	"valise/core/valise"
	"valise/ui/service"
)

// cloudHostRclone is what is known of rclone on one server, and whether
// the server is picked for this login.
type cloudHostRclone struct {
	loading, present, ours bool
	path                   string
	err                    error
	autoPicked             bool
	pick                   bool // picked for the login; only a ready server can be
	inst                   kitInstall
}

func (st *cloudHostRclone) ready() bool {
	return st != nil && !st.loading && st.err == nil && st.present
}

// probeRclone looks for rclone on host, off the main thread.
func (a *App) probeRclone(ca *cloudAdd, host string) {
	if ca.hostTools == nil {
		ca.hostTools = map[string]*cloudHostRclone{}
	}
	st := ca.hostTools[host]
	if st == nil {
		st = &cloudHostRclone{}
		ca.hostTools[host] = st
	}
	if st.loading {
		return
	}
	st.loading, st.err = true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		list, err := a.kit().tools(ctx, host)
		a.update(func() {
			st.loading, st.err = false, err
			if err != nil {
				return
			}
			st.present, st.ours, st.path = false, false, ""
			for _, t := range list {
				if t.ID == "rclone" {
					st.present, st.ours, st.path = t.Present, t.Ours, t.Path
				}
			}
			a.autoPick(ca, host, st)
		})
	})
}

// autoPick picks a ready server once: the only server, or on a re-login
// the servers that already have this drive. Later unticking stays.
func (a *App) autoPick(ca *cloudAdd, host string, st *cloudHostRclone) {
	if st.autoPicked || !st.ready() {
		return
	}
	st.autoPicked = true
	if len(a.hosts()) == 1 || (ca.relogin && a.target(host, ca.name) != nil) {
		st.pick = true
	}
}

func rcloneTag(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(12).TextColor(palette.TextSub)
}

func (a *App) serverLoginRow(c *ui.Context, ca *cloudAdd, cl *kittools.Cloud) {
	hosts := a.hosts()
	settingRow(c, T("loc.cloud_on_server"), T("loc.cloud_on_server_what"), "", nil)
	for _, h := range hosts {
		a.hostRcloneRow(c.Key("hr-"+h), ca, h)
	}
	var sel []string
	for _, h := range hosts {
		if st := ca.hostTools[h]; st.ready() && st.pick {
			sel = append(sel, h)
		}
	}
	var b ui.Element
	switch len(sel) {
	case 0:
		b = ui.Button(c, T("loc.cloud_pick_servers")).Disabled(true)
	case 1:
		b = ui.PrimaryButton(c, T("loc.cloud_login_on", "host", sel[0]))
	default:
		b = ui.PrimaryButton(c, T("loc.cloud_login_batch", "n", len(sel)))
	}
	if len(sel) > 0 {
		b.OnClick(func() { a.startCloudBatchLogin(ca, cl, sel) })
		if !validName(ca.name) {
			b.Disabled(true)
		}
	}
}

func (a *App) hostRcloneRow(c *ui.Context, ca *cloudAdd, h string) {
	st := ca.hostTools[h]
	if st == nil {
		st = &cloudHostRclone{loading: true}
	}
	ui.Column(c).Gap(6).Padding(8, 12).Radius(10).Background(palette.Board).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
			cb := ui.Checkbox(c, &st.pick, h)
			if !st.ready() {
				st.pick = false
				cb.Disabled(true)
			}
			switch {
			case st.loading:
				rcloneTag(c, T("loc.rc_checking"))
			case st.err != nil:
				rcloneTag(c, T("loc.rc_probe_err", "reason", reasonOf(st.err)))
			case !st.present:
				rcloneTag(c, T("loc.rc_missing"))
			case st.ours:
				rcloneTag(c, T("loc.rc_present"))
			default:
				rcloneTag(c, T("loc.rc_system"))
			}
			if a.target(h, ca.name) != nil {
				rcloneTag(c, T("loc.rc_loggedin"))
			}
			if slices.Contains(ca.saved, h) {
				rcloneTag(c, T("loc.rc_done"))
			}
			if !st.loading && st.err != nil {
				ui.Button(c, T("common.refresh")).OnClick(func() { a.probeRclone(ca, h) })
			}
		})
		if !st.loading && st.err == nil && !st.present {
			a.kitInstallBlock(c, h, "rclone", T("loc.rc_prepare"), &st.inst, func(t *service.KitTool) {
				st.present, st.ours, st.pick = true, true, true
				if t != nil {
					st.path = t.Path
				}
			})
		}
	})
}

func reasonOf(err error) string {
	if e, ok := valise.As(err); ok {
		return e.Reason
	}
	return err.Error()
}
