package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/core/kittools"
	"valise/ui/service"
)

// Adding a cloud drive (docs/UI.md 7.9, ARCHITECTURE.md 7.3): the drives
// come from the signed pack. Two logins, said apart: on a server (it then
// copies by itself) and on this computer (copies go through it while
// Valise is open). rclone runs in the window's terminal; Valise passes
// keys through and records nothing typed.

type cloudAdd struct {
	read    bool
	clouds  []kittools.Cloud
	err     error
	id      string
	name    string
	host    string
	local   *service.LocalRclone
	localOK bool // the search for rclone here finished
	dl      *service.RcloneDownload
	dlJ     job
	login   *cloudLogin
	saved   []string // where it is logged in now: hosts, or “here”
	appID   string   // the user's own app, passed to rclone once
	appKey  string
}

// cloudLogin is one login running in the terminal.
type cloudLogin struct {
	where  string // a host, or "" for this computer
	conn   *installConn
	term   *terminal.Terminal
	text   string
	status string
	ask    *cloudAsk
	form   bool          // rclone's questions come as a form
	q      *formQuestion // the question waiting for an answer
	j      job
}

// cloudAsk is a question the login waits on.
type cloudAsk struct {
	text   string
	yes    string
	answer chan bool
}

// reply answers once; later clicks are ignored.
func (q *cloudAsk) reply(ok bool) {
	select {
	case q.answer <- ok:
	default:
	}
}

func (a *App) openCloudAdd() {
	a.cloudAdding = &cloudAdd{name: "gdrive"}
	a.router.Push("/locations/add/cloud")
}

func (a *App) cloudAddPage(c *ui.Context) {
	if a.cloudAdding == nil {
		a.cloudAdding = &cloudAdd{name: "gdrive"}
	}
	ca := a.cloudAdding
	if !ca.read {
		a.readClouds(ca)
	}
	ui.Link(c, "‹ "+T("loc.add_title"), "/locations/add")
	pageHead(c, T("loc.cloud_title"), "")
	if ca.err != nil {
		errorBox(c, ca.err, a.fold("clouds-err"))
	}
	if ca.login != nil {
		a.cloudLoginView(c, ca)
		return
	}
	card(ui.Column(c)).Gap(8).Children(func() {
		for _, cl := range ca.clouds {
			a.cloudChoice(c.Key("cl-"+cl.ID), ca, cl)
		}
	})
	cl := ca.chosen()
	if cl == nil {
		return
	}
	card(ui.Column(c)).Gap(12).Children(func() {
		labeled(c, T("loc.name"), func() { ui.TextInput(c, &ca.name).Label(T("loc.name")).Placeholder("gdrive") })
		if cl.AskApp {
			a.ownApp(c, ca, cl)
		}
		a.serverLoginRow(c, ca, cl)
		a.hereLoginRow(c, ca, cl)
		ui.Text(c, T("loc.cloud_server_risk")).FontSize(12).TextColor(palette.Warn).Wrap()
		muted(ui.Text(c, T("loc.cloud_visible"))).Wrap()
		ui.Collapsible(c, T("common.details"), a.fold("cloud-details"), func() {
			muted(ui.Text(c, T("loc.cloud_details"))).Wrap()
		})
	})
	if len(ca.saved) > 0 {
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.PrimaryButton(c, T("common.done")).OnClick(func() { a.cloudAdding, a.locAdding = nil, nil; a.router.Push("/locations") })
		})
	}
}

func (a *App) readClouds(ca *cloudAdd) {
	ca.read = true
	a.run(func() {
		list, err := a.svc.Clouds()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		local := a.svc.FindLocalRclone(ctx)
		a.update(func() {
			ca.clouds, ca.err, ca.local, ca.localOK = list, err, local, true
			for _, cl := range list {
				if ca.id == "" && !cl.Pending() {
					ca.id = cl.ID
				}
			}
		})
	})
}

func (ca *cloudAdd) chosen() *kittools.Cloud {
	for i := range ca.clouds {
		if ca.clouds[i].ID == ca.id {
			return &ca.clouds[i]
		}
	}
	return nil
}

// cloudChoice is one drive; one not yet tested is shown and cannot be
// picked.
func (a *App) cloudChoice(c *ui.Context, ca *cloudAdd, cl kittools.Cloud) {
	row := ui.Row(c).Gap(12).Padding(10, 14).Radius(10).AlignItems(ui.Center).Border(1, palette.Line)
	if ca.id == cl.ID {
		row.Border(2, palette.Brand)
	}
	if !cl.Pending() {
		row.Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(cl.Name).OnClick(func() { ca.id = cl.ID })
	}
	row.Children(func() {
		ui.Icon(c, icons.cloud).FontSize(18).TextColor(palette.Brand)
		ui.Text(c, cl.Name).Bold().Width(140)
		note := cl.AccessNote
		if cl.Access == kittools.AccessAccount {
			note = T("loc.cloud_whole")
		}
		if cl.Pending() {
			note = T("loc.cloud_pending")
		}
		muted(ui.Text(c, note)).Wrap().Grow(1)
	})
}

func (a *App) serverLoginRow(c *ui.Context, ca *cloudAdd, cl *kittools.Cloud) {
	hosts := a.hosts()
	if ca.host == "" && len(hosts) > 0 {
		ca.host = hosts[0]
	}
	settingRow(c, T("loc.cloud_on_server"), T("loc.cloud_on_server_what"), "", func() {
		if len(hosts) > 1 {
			ui.RadioGroup(c, func() {
				for _, h := range hosts {
					ui.Radio(c.Key("ch-"+h), &ca.host, h, h)
				}
			}).Row().Gap(10).Label(T("loc.cloud_on_server"))
		}
		b := ui.Button(c, T("loc.cloud_login_on", "host", ca.host))
		b.OnClick(func() { a.startCloudLogin(ca, cl, ca.host) })
		if ca.host == "" || !validName(ca.name) {
			b.Disabled(true)
		}
	})
}

func (a *App) hereLoginRow(c *ui.Context, ca *cloudAdd, cl *kittools.Cloud) {
	now := T("loc.cloud_here_what")
	if ca.local != nil {
		now += T("common.dot_sep") + T("loc.rclone_found", "version", ca.local.Version)
	}
	settingRow(c, T("loc.cloud_here"), now, "", func() {
		switch {
		case !ca.localOK || ca.dlJ.busy:
			ui.Spinner(c)
		case ca.local == nil && ca.dl == nil:
			ui.Button(c, T("loc.rclone_get")).OnClick(func() { a.planRclone(ca) })
		case ca.local == nil:
		default:
			b := ui.Button(c, T("loc.cloud_login_here")).OnClick(func() { a.startCloudLogin(ca, cl, "") })
			if !validName(ca.name) {
				b.Disabled(true)
			}
		}
	})
	if ca.dlJ.err != nil {
		errorBox(c, ca.dlJ.err, a.fold("rclone-err"))
	}
	if d := ca.dl; d != nil && ca.local == nil && !ca.dlJ.busy {
		ui.Column(c).Gap(4).Padding(10, 14).Radius(10).Background(palette.Board).Children(func() {
			ui.Text(c, T("loc.rclone_download", "version", d.Version, "size", size(d.Bytes))).Bold()
			mono(muted(ui.Text(c, d.URL))).Wrap()
			mono(muted(ui.Text(c, "SHA-256 "+d.SHA256))).Wrap()
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.PrimaryButton(c, T("loc.rclone_download_go")).OnClick(func() { a.installRclone(ca) })
			})
		})
	}
}

func (a *App) planRclone(ca *cloudAdd) {
	var d *service.RcloneDownload
	a.start(&ca.dlJ, time.Minute, func(ctx context.Context) (err error) { d, err = a.svc.PlanLocalRclone(ctx); return err },
		func(err error) { ca.dl = d })
}

func (a *App) installRclone(ca *cloudAdd) {
	d := ca.dl
	var local *service.LocalRclone
	a.start(&ca.dlJ, 10*time.Minute, func(ctx context.Context) (err error) {
		a.says(&ca.dlJ)(T("loc.rclone_downloading"))
		local, err = a.svc.InstallLocalRclone(ctx, d)
		return err
	}, func(err error) {
		if err == nil {
			ca.local = local
		}
	})
}
