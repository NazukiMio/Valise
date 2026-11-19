package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/core/kittools"
	"valise/core/valise"
	"valise/ui/service"
)

// Adding a cloud drive (docs/UI.md 7.9, ARCHITECTURE.md 7.3): the drives
// come from the signed pack. Two logins, said apart: on a server (it then
// copies by itself) and on this computer (copies go through it while
// Valise is open). rclone runs in the window's terminal; Valise passes
// keys through and records nothing typed.

type cloudAdd struct {
	read         bool
	relogin      bool
	clouds       []kittools.Cloud
	err          error
	id           string
	name         string
	bucket       string
	allowPending bool
	local        *service.LocalRclone
	localOK      bool // the search for rclone here finished
	dl           *service.RcloneDownload
	dlJ          job
	login        *cloudLogin
	saved        []string // where it is logged in now: hosts, or “here”
	appID        string   // the user's own app, kept in memory only while this page is open
	appKey       string   // cleared on leaving the page, Done and closing the window

	hostTools map[string]*cloudHostRclone // rclone on each server, by host
}

// cloudAddRoute is the page whose session holds the user's own app key.
const cloudAddRoute = "/locations/add/cloud"

// cloudLogin is one login running in the terminal.
type cloudLogin struct {
	where   string // a host, or "" for this computer
	conn    *installConn
	term    *terminal.Terminal
	text    string
	status  string
	ask     *cloudAsk
	hosts   []string // a batch of servers, one after another
	index   int      // the server being logged in, in hosts
	results []cloudBatchResult
	form    bool          // rclone's questions come as a form
	q       *formQuestion // the question waiting for an answer
	j       job
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
	a.router.Push(cloudAddRoute)
}

func (a *App) openCloudRelogin(name string) {
	id, bucket := "", ""
	if a.svc != nil {
		id = a.svc.CloudIDOf(name)
		bucket = a.svc.CloudBucketOf(name)
	}
	if bucket == "" {
		bucket = a.cloudTargetBucket(name)
	}
	a.cloudAdding = &cloudAdd{name: name, id: id, bucket: bucket, relogin: true}
	a.router.Push(cloudAddRoute)
}

func (a *App) cloudTargetBucket(name string) string {
	prefix := "valise-" + name + ":"
	for _, h := range a.hosts() {
		t := a.target(h, name)
		if t == nil {
			continue
		}
		if rest, ok := strings.CutPrefix(t.Dest, prefix); ok {
			if b, ok := strings.CutSuffix(rest, "/valise-vault/"); ok && b != "" {
				return b
			}
		}
	}
	return ""
}

func (a *App) cloudAddPage(c *ui.Context) {
	if a.cloudAdding == nil {
		if a.router.Path() != cloudAddRoute {
			return // leaving: the page is only still on screen for its exit
		}
		a.cloudAdding = &cloudAdd{name: "gdrive"}
	}
	ca := a.cloudAdding
	if !ca.read {
		a.readClouds(ca)
	}
	backLabel, backTarget := T("loc.add_title"), "/locations/add"
	title := T("loc.cloud_title")
	doneTarget := "/locations"
	if ca.relogin {
		backLabel, backTarget = ca.name, "/locations/"+ca.name
		title = T("loc.cloud_relogin_title", "name", ca.name)
		doneTarget = backTarget
	}
	backLink(c, backLabel, backTarget)
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Children(func() { pageHead(c, title, "") })
		if len(ca.saved) > 0 && ca.login == nil {
			ui.PrimaryButton(c, T("common.done")).OnClick(func() { a.closeAdding(); a.router.Push(doneTarget) })
		}
	})
	if ca.err != nil {
		errorBox(c, ca.err, a.fold("clouds-err"))
		if isPackError(ca.err) {
			ui.Row(c).Children(func() {
				ui.PrimaryButton(c, T("loc.cloud_open_profiles")).OnClick(func() {
					a.closeAdding()
					a.setTab = 3
					a.readProfilesStatus()
					a.navigate("/settings")
				})
			})
		}
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
		if ca.relogin {
			labeled(c, T("loc.name"), func() { ui.Text(c, ca.name).Bold() })
		} else {
			labeled(c, T("loc.name"), func() { ui.TextInput(c, &ca.name).Label(T("loc.name")).Placeholder("gdrive") })
		}
		if cl.NeedsBucket {
			labeled(c, T("loc.cloud_bucket"), func() {
				if ca.relogin && ca.bucket != "" {
					ui.Text(c, ca.bucket).Bold()
				} else {
					ui.TextInput(c, &ca.bucket).Label(T("loc.cloud_bucket")).Placeholder(T("loc.cloud_bucket_placeholder"))
				}
			})
			muted(ui.Text(c, T("loc.cloud_bucket_hint"))).Wrap()
		}
		if cl.AskApp {
			a.ownApp(c, ca, cl)
		}
		if cl.Pending() {
			notice(c, "warn", T("loc.cloud_pending_warn"))
			ui.Checkbox(c.Key("cp-"+cl.ID), &ca.allowPending, T("loc.cloud_pending_confirm"))
		}
		a.serverLoginRow(c, ca, cl)
		a.hereLoginRow(c, ca, cl)
		ui.Text(c, T("loc.cloud_server_risk")).FontSize(12).TextColor(palette.Warn).Wrap()
		ui.Collapsible(c, T("common.details"), a.fold("cloud-details"), func() {
			muted(ui.Text(c, T("loc.cloud_details"))).Wrap()
		})
	})
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
			for _, h := range a.hosts() {
				a.probeRclone(ca, h)
			}
			for _, cl := range list {
				if ca.id == "" && !cl.Pending() {
					ca.id = cl.ID
				}
			}
			if ca.id == "" && len(list) > 0 {
				ca.id = list[0].ID
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

func isPackError(err error) bool {
	ue, ok := valise.As(err)
	if !ok {
		return false
	}
	switch ue.Code {
	case "kit_tools_none", "pack_unusable", "no_profiles", "profiles_broken":
		return true
	}
	return false
}

// cloudChoice is one drive; pending drives stay selectable so the user can opt
// in with explicit risk confirmation.
func (a *App) cloudChoice(c *ui.Context, ca *cloudAdd, cl kittools.Cloud) {
	row := ui.Row(c).Gap(12).Padding(10, 14).Radius(10).AlignItems(ui.Center).Border(1, palette.Line)
	if ca.id == cl.ID {
		row.Border(2, palette.Brand)
	}
	row.Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(cl.Name).OnClick(func() {
		if ca.id != cl.ID {
			oldID := ca.id
			ca.id = cl.ID
			ca.allowPending = false
			if !ca.relogin && (ca.name == "" || ca.name == oldID) {
				ca.name = cl.ID
			}
		}
	})
	row.Children(func() {
		ui.Icon(c, icons.cloud).FontSize(18).TextColor(palette.Brand)
		ui.Text(c, cl.Name).Bold().Width(200)
		note := cl.AccessNote
		if cl.Access == kittools.AccessAccount && !cl.Pending() {
			note = T("loc.cloud_whole")
		}
		if cl.Pending() {
			note = T("loc.cloud_pending") + T("common.dot_sep") + note
		}
		muted(ui.Text(c, note)).Wrap().Grow(1)
	})
}

func (ca *cloudAdd) canLogin(cl *kittools.Cloud) bool {
	if cl == nil || !validName(ca.name) {
		return false
	}
	if cl.Pending() && !ca.allowPending {
		return false
	}
	if cl.NeedsBucket && !kittools.ValidCloudBucket(strings.TrimSpace(ca.bucket)) {
		return false
	}
	return true
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
			if !ca.canLogin(cl) {
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
