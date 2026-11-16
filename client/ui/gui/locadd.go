package gui

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/valise"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// Adding a location (docs/UI.md 7.9): the kind first. A folder is added
// and filled at once; another machine goes through the same fingerprint
// check and one login as adding a server; a cloud has its own page. A host
// in a server's own SSH configuration is under 更多选项.

// locAdd is the add page's state.
type locAdd struct {
	kind     string
	more     bool
	name     string
	path     string // folder, or the machine's folder
	addr     string
	port     string
	user     string
	host     string // the server whose SSH host is used
	dest     string // <host>:<path> in that server's SSH configuration
	anySrc   bool
	keys     []service.HostKey
	mismatch bool
	j        job
	warnings []string
	done     string // the added location
	cause    string // the server could not reach the SSH host: why
}

func (a *App) openLocAdd() {
	a.locAdding = &locAdd{kind: locRepo, port: "22"}
	a.router.Push("/locations/add")
}

func (a *App) locAddPage(c *ui.Context) {
	if a.locAdding == nil {
		a.locAdding = &locAdd{kind: locRepo, port: "22"}
	}
	f := a.locAdding
	ui.Link(c, "‹ "+T("nav.locations"), "/locations")
	pageHead(c, T("loc.add_title"), "")
	if f.done != "" {
		a.locAdded(c, f)
		return
	}
	if len(f.keys) > 0 {
		a.stationFingerprint(c, f)
		return
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		for _, k := range []string{locRepo, locStation, locCloud} {
			a.kindChoice(c.Key("kind-"+k), f, k)
		}
		ui.Collapsible(c, T("common.more"), &f.more, func() { a.kindChoice(c, f, locSSH) })
	})
	card(ui.Column(c)).Gap(12).Children(func() {
		switch f.kind {
		case locRepo:
			a.repoForm(c, f)
		case locStation:
			a.stationForm(c, f)
		case locCloud:
			muted(ui.Text(c, T("loc.cloud_next"))).Wrap()
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.PrimaryButton(c, T("common.next")).OnClick(func() { a.openCloudAdd() })
			})
		case locSSH:
			a.sshForm(c, f)
		}
	})
}

// kindChoice is one kind, picked by a click.
func (a *App) kindChoice(c *ui.Context, f *locAdd, kind string) {
	row := ui.Row(c).Gap(12).Padding(12, 14).Radius(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).
		Label(T("loc.kind."+kind)).Border(1, palette.Line)
	if f.kind == kind {
		row.Border(2, palette.Brand)
	}
	row.OnClick(func() { f.kind, f.j.err, f.cause = kind, nil, "" }).Children(func() {
		ui.Icon(c, locIcon(kind)).FontSize(20).TextColor(palette.Brand)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, T("loc.kind."+kind)).Bold()
			muted(ui.Text(c, T("loc.kind_what."+kind))).Wrap()
		})
	})
}

// nameField says the rule under the field, in the warning color once
// what is typed breaks it, so a disabled 添加 is never unexplained.
func (a *App) nameField(c *ui.Context, f *locAdd, placeholder string) {
	labeled(c, T("loc.name"), func() {
		ui.TextInput(c, &f.name).Label(T("loc.name")).Placeholder(placeholder)
	})
	rule := muted(ui.Text(c, T("set.name_rule")))
	if f.name != "" && !validName(f.name) {
		rule.TextColor(palette.Warn)
	}
}

// nameFrom suggests a name from a folder: its last part, in lowercase,
// other characters as hyphens.
func nameFrom(path string) string {
	base := strings.ToLower(filepath.Base(path))
	var b strings.Builder
	for _, r := range base {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	n := strings.TrimRight(b.String(), "-")
	if len(n) > 32 {
		n = strings.TrimRight(n[:32], "-")
	}
	return n
}

func (a *App) addButtons(c *ui.Context, f *locAdd, label string, ready bool, run func()) {
	if f.j.busy {
		a.jobLine(c, &f.j)
		return
	}
	if f.j.err != nil {
		errorBox(c, f.j.err, a.fold("locadd-err"))
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		ui.Button(c, T("common.cancel")).OnClick(func() { a.closeAdding(); a.router.Push("/locations") })
		b := ui.PrimaryButton(c, label).OnClick(run)
		if !ready {
			b.Disabled(true)
		}
	})
}

func (a *App) repoForm(c *ui.Context, f *locAdd) {
	a.nameField(c, f, "usb")
	labeled(c, T("loc.folder"), func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			mono(ui.Text(c, orText(f.path, T("loc.folder_none")))).Grow(1).Wrap()
			ui.Button(c, T("loc.folder_pick")).OnClick(func() { a.pickLocFolder(f) })
		})
	})
	a.addButtons(c, f, T("loc.add_go"), validName(f.name) && len(f.name) <= 32 && f.path != "", func() {
		r, err := a.svc.RepoAdd(f.name, f.path)
		if err != nil {
			f.j.err = err
			return
		}
		f.done = r.Name
		a.locs().read = false
		a.syncLocation(location{r.Name, locRepo})
	})
}

func (a *App) pickLocFolder(f *locAdd) {
	if a.pickFolder == nil {
		return
	}
	a.run(func() {
		p, err := a.pickFolder(T("picker.repo.title"), T("picker.repo.message"))
		a.update(func() {
			if err == nil && p != "" {
				f.path = p
				if f.name == "" {
					f.name = nameFrom(p)
				}
			}
		})
	})
}

func (f *locAdd) station() config.Station {
	port, _ := strconv.Atoi(strings.TrimSpace(f.port))
	return config.Station{Name: strings.TrimSpace(f.name), Address: strings.TrimSpace(f.addr), Port: port,
		User: strings.TrimSpace(f.user), Path: strings.TrimSpace(f.path), AnySource: f.anySrc}
}

func (a *App) stationForm(c *ui.Context, f *locAdd) {
	a.nameField(c, f, "nas")
	ui.Row(c).Gap(10).Children(func() {
		labeled(c, T("loc.addr"), func() { ui.TextInput(c, &f.addr).Label(T("loc.addr")).Placeholder("192.168.1.20") }).Grow(1)
		labeled(c, T("loc.port"), func() { ui.TextInput(c, &f.port).Label(T("loc.port")).Placeholder("22") }).Width(90)
	})
	ui.Row(c).Gap(10).Children(func() {
		labeled(c, T("loc.user"), func() { ui.TextInput(c, &f.user).Label(T("loc.user")).Placeholder("valise") }).Grow(1)
		labeled(c, T("loc.station_path"), func() { ui.TextInput(c, &f.path).Label(T("loc.station_path")).Placeholder("/volume1/valise") }).Grow(1)
	})
	ui.Checkbox(c, &f.anySrc, T("loc.any_source"))
	ui.Collapsible(c, T("common.details"), a.fold("station-details"), func() {
		muted(ui.Text(c, T("loc.station_details"))).Wrap()
	})
	st := f.station()
	a.addButtons(c, f, T("common.next"), st.Validate() == nil, func() {
		var keys []service.HostKey
		a.start(&f.j, time.Minute, func(ctx context.Context) (err error) {
			a.says(&f.j)(T("loc.scanning", "addr", st.Address))
			keys, err = a.svc.StationScan(ctx, st)
			return err
		}, func(err error) {
			if err == nil {
				f.keys, f.mismatch = keys, false
			}
		})
	})
}

// stationFingerprint is the same check as adding a server: the person
// compares what the machine offers with what it says about itself.
func (a *App) stationFingerprint(c *ui.Context, f *locAdd) {
	st := f.station()
	card(ui.Column(c)).Gap(12).Children(func() {
		dialogTitle(ui.Text(c, T("loc.fp_title", "name", st.Name)))
		ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
			muted(ui.Text(c, T("wizard.identity.offered", "addr", knownHostsName(st.Address, st.PortOr22()))))
			for _, k := range f.keys {
				ui.Row(c.Key(k.Fingerprint)).Gap(12).Children(func() {
					ui.Text(c, strings.ToUpper(strings.TrimPrefix(k.Type, "ssh-"))).FontSize(12).Bold().Width(90)
					mono(ui.Text(c, k.Fingerprint)).FontSize(12).Wrap().Grow(1)
				})
			}
		})
		for i, s := range []string{T("loc.step_login", "addr", st.Address), T("loc.step_key", "path", st.Path), T("loc.step_cert")} {
			passLine(c, itoa(i+1), s)
		}
		if f.mismatch {
			errorBox(c, valise.New("host_key_rejected", T("wizard.identity.stopped"), T("wizard.identity.stopped_hint")), a.fold("loc-mm"))
		}
		if f.j.busy {
			a.jobLine(c, &f.j)
			return
		}
		if f.j.err != nil {
			errorBox(c, f.j.err, a.fold("locadd-err"))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.back")).OnClick(func() { f.keys, f.mismatch, f.j.err = nil, false, nil })
			if f.mismatch {
				return
			}
			ui.Button(c, T("wizard.identity.mismatch")).OnClick(func() { f.mismatch = true })
			ui.PrimaryButton(c, T("wizard.identity.match")).OnClick(func() {
				keys := f.keys
				var res *service.StationAddResult
				a.start(&f.j, 10*time.Minute, func(ctx context.Context) (err error) {
					a.says(&f.j)(T("loc.station_adding", "addr", st.Address))
					res, err = a.svc.StationAdd(ctx, st, keys)
					return err
				}, func(err error) {
					if err == nil {
						f.done, f.warnings, f.keys = res.Station.Name, res.Warnings, nil
						a.locs().read = false
					}
				})
			})
		})
	})
}

// sshForm saves a host the chosen server already reaches through its own
// SSH configuration; the server tries it first (contracts 16.G3).
func (a *App) sshForm(c *ui.Context, f *locAdd) {
	hosts := a.hosts()
	if f.host == "" && len(hosts) > 0 {
		f.host = hosts[0]
	}
	labeled(c, T("loc.ssh_server"), func() {
		ui.RadioGroup(c, func() {
			for _, h := range hosts {
				ui.Radio(c.Key("sh-"+h), &f.host, h, h)
			}
		}).Row().Gap(16).Label(T("loc.ssh_server"))
	})
	a.nameField(c, f, "nas")
	labeled(c, T("loc.ssh_dest"), func() {
		ui.TextInput(c, &f.dest).Label(T("loc.ssh_dest")).Placeholder("nas:/volume1/valise-vault/")
	})
	muted(ui.Text(c, T("loc.ssh_what"))).Wrap()
	t := vaultrpc.Target{Name: strings.TrimSpace(f.name), Kind: "rsync", Dest: strings.TrimSpace(f.dest)}
	if f.cause != "" {
		ui.Text(c, f.cause).TextColor(palette.Warn).Wrap()
		a.addButtons(c, f, T("loc.ssh_save_anyway"), true, func() { a.saveSSH(f, t) })
		return
	}
	a.addButtons(c, f, T("loc.add_go"), f.host != "" && validName(t.Name) && strings.Contains(t.Dest, ":"), func() {
		host := f.host
		var res *vaultrpc.TargetCheckResult
		a.start(&f.j, 2*time.Minute, func(ctx context.Context) (err error) {
			a.says(&f.j)(T("loc.ssh_testing", "host", host))
			res, err = a.svc.CheckTarget(ctx, host, vaultrpc.TargetCheckParams{Target: &t})
			if errors.Is(err, service.ErrCheckUnsupported) {
				res, err = &vaultrpc.TargetCheckResult{Reachable: true}, nil
			}
			return err
		}, func(err error) {
			switch {
			case err != nil:
			case res.Reachable:
				a.saveSSH(f, t)
			default:
				what, remedy := service.CopyCause(res.FailureCause, t.Name)
				f.cause = T("loc.ssh_unreachable", "host", host, "what", what, "remedy", remedy)
			}
		})
	})
}

func (a *App) saveSSH(f *locAdd, t vaultrpc.Target) {
	host := f.host
	a.start(&f.j, time.Minute, func(ctx context.Context) error { return a.svc.TargetSet(ctx, host, t) }, func(err error) {
		if err == nil {
			f.done = t.Name
			a.locs().read = false
		}
	})
}

// locAdded says what was added and what to know, one line each.
func (a *App) locAdded(c *ui.Context, f *locAdd) {
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("loc.added", "name", f.done)).FontSize(16).Bold().TextColor(palette.Ok)
		for _, w := range f.warnings {
			ui.Text(c, w).FontSize(13).TextColor(palette.Warn).Wrap()
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.PrimaryButton(c, T("common.done")).OnClick(func() { a.closeAdding(); a.router.Push("/locations") })
		})
	})
}
