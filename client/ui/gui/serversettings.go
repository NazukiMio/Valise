package gui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/vaultrpc"
	"valise/ui/service"
)

// The server's settings tab (docs/UI.md 9.4): each row says how things
// are now; a row that needs doing has a button with a tag saying what it
// will ask for. Removing the server is its own block at the bottom.

// settingsState is what the settings tab read and is doing for a server.
type settingsState struct {
	vault     *vaultrpc.StatusResult
	vaultErr  error
	vaultBusy bool
	read      bool

	naming bool // the first-name field is open
	name   string
	nameJ  job

	newName string // rename
	plan    *service.RenamePlan
	planJ   job
	renJ    job
	renamed bool

	migAdmin          string // "yes" to get an admin pass too
	migPass, migPass2 string
	migJ              job
	migrated          bool
	porterMissing     bool

	porterW  *wizard // porter update: where it comes from; kept across frames
	deployJ  job
	deployed *service.DeployReport

	regJ job // registering the key

	leave *leaveState
}

func (a *App) settingsOf(host string) *settingsState {
	if a.settings[host] == nil {
		a.settings[host] = &settingsState{migAdmin: a.defaultAdminChoice()}
	}
	return a.settings[host]
}

// readSettings reads the server's vault (its name and the registered
// keys) over the user pass.
func (a *App) readSettings(host string) {
	ss := a.settingsOf(host)
	ss.vaultBusy, ss.vaultErr, ss.read = true, nil, true
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		st, err := a.svc.VaultStatus(ctx, host)
		a.update(func() { ss.vaultBusy, ss.vault, ss.vaultErr = false, st, err })
	})
}

// settingRow is one row: what it is, how it is now, and maybe an action
// with the tag of what it needs.
func settingRow(c *ui.Context, title, now, note string, action func()) {
	ui.Row(c.Key("set-"+title)).Gap(14).Padding(12, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).Bold().Width(150)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, now).Wrap()
			if note != "" {
				muted(ui.Text(c, note)).Wrap()
			}
		})
		if action != nil {
			action()
		}
	})
}

// actionWith is a button with the tag of what it needs.
func actionWith(c *ui.Context, label, need string, onClick func()) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if need != "" {
			needTag(c, need)
		}
		ui.Button(c, label).OnClick(onClick)
	})
}

func (a *App) settingsTab(c *ui.Context, name string, row *serverRow, ps *passState) {
	ss := a.settingsOf(name)
	if !ss.read {
		a.readSettings(name)
	}
	tiered := row != nil && row.Valise
	card(ui.Column(c)).Gap(8).Children(func() {
		a.nameRow(c, name, ss)
		a.loginRow(c, name, tiered, ps)
		a.porterRow(c, name, ps)
		if tiered {
			if a.svc.HasDailyKey(name) {
				settingRow(c, T("set.user_pass"), T("pass.usable"), "", nil)
			} else {
				settingRow(c, T("set.user_pass"), T("set.user_pass_none"), "", func() {
					actionWith(c, T("set.reinstall"), T("need.server_password"), func() { a.rekeyServer(name) })
				})
			}
		}
		a.keyRow(c, name, ss)
		if ss.vaultErr != nil {
			errorBox(c, ss.vaultErr, a.fold("set-err-"+name))
		}
	})
	card(ui.Column(c)).Gap(8).Border(1, palette.TroubleBorder).Children(func() {
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, T("leave.title")).FontSize(16).Bold().TextColor(palette.Warn)
				muted(ui.Text(c, T("leave.what"))).FontSize(13).Wrap()
			})
			ui.Button(c, T("leave.open")).OnClick(func() { a.openLeave(name) })
		})
	})
	if ss.leave != nil {
		a.leaveDialog(c, name, ss)
	}
}

func (a *App) nameRow(c *ui.Context, name string, ss *settingsState) {
	switch {
	case ss.vault == nil && ss.vaultBusy:
		settingRow(c, T("set.name"), T("set.reading"), "", func() { ui.Spinner(c) })
	case ss.vault == nil:
		settingRow(c, T("set.name"), T("set.unknown"), "", nil)
	case ss.vault.ServerName == "":
		settingRow(c, T("set.name"), T("set.name_none"), T("set.name_none_why"), func() {
			if !ss.naming {
				ui.Button(c, T("set.name_first")).OnClick(func() { ss.naming, ss.name = true, defaultName(name) })
			}
		})
		if ss.naming {
			a.firstName(c, name, ss)
		}
	default:
		settingRow(c, T("set.name"), ss.vault.ServerName, T("set.name_folder"), func() {
			actionWith(c, T("set.rename"), T("need.valise"), func() {
				ss.newName, ss.plan, ss.renamed, ss.renJ.err, ss.planJ.err = "", nil, false, nil, nil
				a.router.Push("/servers/" + name + "/rename")
			})
		})
	}
}

// firstName names the server once; it needs no passphrase.
func (a *App) firstName(c *ui.Context, name string, ss *settingsState) {
	ui.Column(c).Gap(8).Padding(4, 14).Children(func() {
		labeled(c, T("set.name_new"), func() {
			ui.TextInput(c, &ss.name).Label(T("set.name_new")).Placeholder(T("set.name_rule")).AutoFocus()
		})
		muted(ui.Text(c, T("set.name_rule"))).Wrap()
		if ss.nameJ.busy {
			a.jobLine(c, &ss.nameJ)
			return
		}
		if ss.nameJ.err != nil {
			errorBox(c, ss.nameJ.err, a.fold("name-err-"+name))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() { ss.naming = false })
			b := ui.PrimaryButton(c, T("set.name_save")).OnClick(func() {
				n := ss.name
				a.start(&ss.nameJ, time.Minute, func(ctx context.Context) error { return a.svc.ServerNameSet(ctx, name, n) },
					func(err error) {
						if err == nil {
							ss.naming = false
							a.readSettings(name)
						}
					})
			})
			if !validName(ss.name) {
				b.Disabled(true)
			}
		})
	})
}

func (a *App) loginRow(c *ui.Context, name string, tiered bool, ps *passState) {
	if tiered {
		now := T("set.login_valise_user")
		if a.svc.HasAdminKey(name) {
			now = T("set.login_valise_both")
		}
		settingRow(c, T("set.login"), now, "", nil)
		return
	}
	note := T("set.login_own_note")
	jump := ps.doc != nil && ps.doc.SSH.ProxyJump != ""
	if jump {
		note = T("set.login_jump")
	}
	settingRow(c, T("set.login"), T("set.login_own", "name", name), note, func() {
		if !jump {
			actionWith(c, T("set.migrate"), T("need.own_ssh"), func() {
				ss := a.settingsOf(name)
				ss.migrated, ss.migJ.err, ss.porterMissing = false, nil, false
				a.router.Push("/servers/" + name + "/migrate")
			})
		}
	})
}

func (a *App) porterRow(c *ui.Context, name string, ps *passState) {
	mine := strings.TrimPrefix(a.svc.Version, "v")
	open := func() {
		ss := a.settingsOf(name)
		ss.deployed, ss.deployJ.err, ss.porterW = nil, nil, nil
		a.router.Push("/servers/" + name + "/porter")
	}
	switch {
	case ps.doc == nil && ps.docBusy:
		settingRow(c, T("prepare.porter"), T("set.reading"), "", func() { ui.Spinner(c) })
	case ps.doc == nil:
		settingRow(c, T("prepare.porter"), T("set.unknown"), "", nil)
	case !ps.doc.PorterFound || ps.doc.PorterVersion == "":
		settingRow(c, T("prepare.porter"), T("set.porter_none"), "", func() { actionWith(c, T("set.porter_deploy"), T("need.admin"), open) })
	case a.releaseTag() == "":
		settingRow(c, T("prepare.porter"), ps.doc.PorterVersion, T("set.porter_dev"), func() { actionWith(c, T("set.porter_update"), T("need.admin"), open) })
	case versionLess(ps.doc.PorterVersion, mine):
		settingRow(c, T("prepare.porter"), T("set.porter_older", "version", ps.doc.PorterVersion, "mine", mine), "", func() {
			actionWith(c, T("set.porter_update_to", "version", mine), T("need.admin"), open)
		})
	case versionLess(mine, ps.doc.PorterVersion):
		settingRow(c, T("prepare.porter"), T("set.porter_newer", "version", ps.doc.PorterVersion, "mine", mine), T("set.porter_no_downgrade"), nil)
	default:
		settingRow(c, T("prepare.porter"), T("set.porter_same", "version", ps.doc.PorterVersion), "", nil)
	}
}

func (a *App) keyRow(c *ui.Context, name string, ss *settingsState) {
	key, err := a.svc.KeyShow()
	switch {
	case ss.vault == nil:
		settingRow(c, T("set.key"), T("set.unknown"), "", nil)
	case err == nil && slices.Contains(ss.vault.Recipients, key.Fingerprint):
		settingRow(c, T("set.key"), T("set.key_registered"), "", nil)
	case ss.regJ.busy:
		settingRow(c, T("set.key"), T("set.key_not_yet"), "", func() { ui.Spinner(c) })
	default:
		need := ""
		if len(ss.vault.Recipients) > 0 {
			need = T("need.valise")
		}
		settingRow(c, T("set.key"), T("set.key_not_yet"), T("set.key_auto"), func() {
			actionWith(c, T("set.key_register"), need, func() { a.needKey(func() { a.registerKey(name, ss) }) })
		})
	}
	if ss.regJ.err != nil {
		errorBox(c, ss.regJ.err, a.fold("reg-err-"+name))
	}
	if a.svc.RecoveryEnabled() {
		for _, l := range a.svc.ServerLocks() {
			if l.Server == name {
				settingRow(c, T("lock.title"), T("lock.state."+l.State), "", nil)
			}
		}
	}
}

func (a *App) registerKey(name string, ss *settingsState) {
	a.start(&ss.regJ, 2*time.Minute, func(ctx context.Context) error { return a.ensureRegistered(ctx, name) },
		func(err error) {
			if err == nil {
				a.readSettings(name)
			}
		})
}

// versionLess compares dotted versions (“0.9.0” < “0.9.2”); anything after
// a “-” or “+” is ignored.
func versionLess(x, y string) bool {
	parse := func(v string) []int {
		v = strings.TrimPrefix(v, "v")
		if i := strings.IndexAny(v, "-+ "); i >= 0 {
			v = v[:i]
		}
		var out []int
		for _, p := range strings.Split(v, ".") {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		return out
	}
	return slices.Compare(parse(x), parse(y)) < 0
}

// validName is the rule for server names: lowercase letters, digits and
// hyphens, at most 64.
func validName(s string) bool {
	if s == "" || len(s) > 64 || s[0] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
