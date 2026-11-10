package gui

import (
	"context"
	"io"
	"strings"
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
}

// cloudLogin is one login running in the terminal.
type cloudLogin struct {
	where  string // a host, or "" for this computer
	conn   *installConn
	term   *terminal.Terminal
	text   string
	status string
	ask    *cloudAsk
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
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				muted(ui.Text(c, T("loc.cloud_own_app"))).Wrap().Grow(1)
				if cl.AppHelp != "" {
					ui.Link(c, T("loc.cloud_app_help"), cl.AppHelp)
				}
			})
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

// startCloudLogin runs rclone's login in the terminal: on a server, or on
// this computer when where is "".
func (a *App) startCloudLogin(ca *cloudAdd, cl *kittools.Cloud, where string) {
	lg := &cloudLogin{where: where, conn: newInstallConn()}
	ca.login = lg
	if a.newTerminal != nil {
		if t, err := a.newTerminal(lg.conn); err == nil {
			lg.term = t
		}
	}
	if lg.term == nil {
		a.run(func() { a.pipeText(lg.conn.out, func(s string) { lg.text += s }) })
	}
	name, id, opts := strings.TrimSpace(ca.name), cl.ID, service.CloudOptions{}
	a.start(&lg.j, 15*time.Minute, func(ctx context.Context) error {
		ask := func(text, yes string) bool {
			q := &cloudAsk{text: text, yes: yes, answer: make(chan bool, 1)}
			a.update(func() { lg.ask = q })
			defer a.update(func() { lg.ask = nil })
			select {
			case ok := <-q.answer:
				return ok
			case <-ctx.Done():
				return false
			}
		}
		defer lg.conn.outW.Close()
		if where == "" {
			return a.svc.CloudLoginHere(ctx, name, id, opts, lg.conn.in, lg.conn.outW)
		}
		_, err := a.svc.CloudLoginServer(ctx, where, name, id, opts, service.CloudLoginIO{
			LoginIO: service.LoginIO{Stdin: lg.conn.in, Stdout: lg.conn.outW, Cols: 100, Rows: 30,
				Status:      func(s service.LoginStatus) { a.update(func() { lg.status = loginStatusText(s) }) },
				ConfirmOpen: func(link, domain string) bool { return ask(T("loc.open_ask", "domain", domain), T("loc.open_go")) },
				OpenBrowser: a.openURL},
			ConfirmAuthorize: func(line string) bool { return ask(T("loc.authorize_ask", "line", line), T("loc.authorize_go")) },
		})
		return err
	}, func(err error) {
		if err == nil {
			ca.saved = append(ca.saved, orText(where, T("loc.here")))
			a.locs().read = false
		}
	})
}

func (a *App) pipeText(r io.Reader, add func(string)) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s := string(buf[:n])
			a.update(func() { add(s) })
		}
		if err != nil {
			return
		}
	}
}

// loginStatusText says each step of the relay apart.
func loginStatusText(s service.LoginStatus) string {
	switch s.State {
	case service.LoginRelayReady:
		return T("loc.relay_ready", "port", s.Port)
	case service.LoginBrowserOpened:
		return T("loc.browser_opened")
	case service.LoginCallbackReceived:
		return T("loc.callback")
	case service.LoginExited:
		return T("loc.exited", "code", s.ExitCode)
	}
	return ""
}

func (a *App) cloudLoginView(c *ui.Context, ca *cloudAdd) {
	lg := ca.login
	card(ui.Column(c)).Gap(10).Children(func() {
		where := orText(lg.where, T("loc.here"))
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("loc.cloud_logging", "where", where)).Bold().Grow(1)
			if lg.j.busy {
				ui.Spinner(c)
			}
		})
		if lg.status != "" {
			muted(ui.Text(c, lg.status))
		}
		muted(ui.Text(c, T("loc.cloud_terminal")))
		if lg.term != nil {
			terminal.View(c, lg.term).Height(320).AutoFocus()
		} else {
			mono(ui.Text(c, orText(lg.text, T("install.no_output")))).FontSize(12).Wrap().Padding(10).Radius(8).Background(palette.Board)
		}
		if q := lg.ask; q != nil {
			ui.Row(c).Gap(10).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
				ui.Text(c, q.text).Wrap().Grow(1)
				ui.Button(c, T("loc.skip")).OnClick(func() { q.reply(false) })
				ui.PrimaryButton(c, q.yes).OnClick(func() { q.reply(true) })
			})
		}
		if lg.j.busy {
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.stop")).OnClick(func() { a.stopJob(&lg.j) })
			})
			return
		}
		if lg.j.err != nil {
			errorBox(c, lg.j.err, a.fold("cloudlogin-err"))
		} else {
			ui.Text(c, T("loc.cloud_saved", "where", where, "name", ca.name)).TextColor(palette.Ok).Bold()
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.PrimaryButton(c, T("common.back")).OnClick(func() { ca.login = nil })
		})
	})
}
