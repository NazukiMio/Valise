package gui

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/enroll"
	"valise/ui/service"
)

// The add-server wizard (docs/UI.md 7.5): connect → confirm identity → log
// in → prepare the server → the agents on it.
const (
	wzConnect = iota
	wzIdentity
	wzLogin
	wzPrepare
)

// wizardSteps are the wizard's five steps in the current language.
func wizardSteps() []string {
	return []string{T("wizard.step.connect"), T("wizard.step.identity"), T("wizard.step.login"), T("wizard.step.prepare"), T("wizard.step.agents")}
}

// wizard is one run of the wizard. Secrets typed here (the admin
// passphrase) live only until the server is added or the wizard closes.
type wizard struct {
	fromHome bool
	step     int

	addr, user, port, name string
	login, keyPath         string // config.LoginPassword or LoginKey
	importing              bool   // choosing from ~/.ssh/config instead
	hosts                  []string
	hostsErr               error

	busy     bool
	err      error
	keys     []service.HostKey
	mismatch bool // the user said the fingerprints differ

	admin, adminPass, adminPass2 string // admin: "yes" when asked for
	more                         bool
	localPorter                  string
	steps                        []service.SetupStep
	host                         string // the server's name once it is added
	doing                        string // what is happening now, in plain words
	doingAt                      time.Time
	cancel                       context.CancelFunc // stops the running step
}

// startWizard opens the wizard; from the home page it goes on to the
// agents once the server is ready.
func (a *App) startWizard(fromHome bool) {
	a.wiz = &wizard{fromHome: fromHome, login: config.LoginPassword}
	a.router.Push("/servers/add")
}

// splitAddress reads “user@host:port”, “host:port”, “[v6]:port” or a bare
// host.
func splitAddress(s string) (user, host, port string) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "@"); i >= 0 {
		user, s = s[:i], s[i+1:]
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		return user, h, p
	}
	return user, strings.Trim(s, "[]"), ""
}

// server is the wizard's server as typed so far.
func (w *wizard) server() config.Server {
	user, host, port := splitAddress(w.addr)
	if w.user != "" {
		user = w.user
	}
	if w.port != "" {
		port = w.port
	}
	n, _ := strconv.Atoi(port)
	srv := config.Server{Name: w.name, Source: config.SourceValise, HostName: host, User: user, Port: n, Login: w.login}
	if w.login == config.LoginKey {
		srv.LoginKey = w.keyPath
	}
	if srv.Name == "" {
		srv.Name = defaultName(host)
	}
	return srv
}

// defaultName makes an address into a server name: lowercase letters,
// digits and hyphens.
func defaultName(host string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(host) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// problem is what keeps the connect step from going on, "" when nothing.
func (w *wizard) problem() string {
	srv := w.server()
	switch {
	case srv.HostName == "":
		return T("wizard.err.no_host")
	case w.port != "" && (srv.Port <= 0 || srv.Port > 65535):
		return T("wizard.err.port")
	case enroll.ValidInstanceID(srv.Name) != nil:
		return T("wizard.err.name")
	case srv.Login == config.LoginKey && srv.LoginKey == "":
		return T("wizard.err.no_key")
	}
	if err := srv.Validate(); err != nil {
		return err.Error()
	}
	return ""
}

func (a *App) wizardPage(c *ui.Context) {
	w := a.wiz
	if w == nil {
		a.startWizard(false)
		w = a.wiz
	}
	ui.Link(c, "‹ "+T("nav.servers"), "/servers")
	stepper(c, w.step)
	switch w.step {
	case wzConnect:
		if w.importing {
			a.importStep(c, w)
		} else {
			a.connectStep(c, w)
		}
	case wzIdentity:
		a.identityStep(c, w)
	case wzLogin:
		a.loginStep(c, w)
	case wzPrepare:
		a.prepareStep(c, w)
	}
}

// stepper is the wizard's five steps, the done ones ticked.
func stepper(c *ui.Context, at int) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
		for i, s := range wizardSteps() {
			if i > 0 {
				ui.Box(c.Key(i)).Size(18, 1).Background(palette.Line)
			}
			t := s
			ink := palette.TextSub
			switch {
			case i < at:
				t, ink = "✓ "+s, palette.Ok
			case i == at:
				ink = palette.Text
			}
			e := ui.Text(c.Key("s"+s), t).FontSize(12).TextColor(ink)
			if i == at {
				e.Bold()
			}
		}
	})
}

func (a *App) connectStep(c *ui.Context, w *wizard) {
	ui.Row(c).Gap(24).AlignItems(ui.Start).Children(func() {
		card(ui.Column(c)).Grow(1).Basis(0).Gap(14).Padding(22).Children(func() {
			ui.Text(c, T("wizard.connect.title")).FontSize(20).Bold().Font(cur.Fonts.Display)
			labeled(c, T("wizard.field.address"), func() {
				ui.TextInput(c, &w.addr).Label(T("wizard.field.address")).Placeholder("agent@192.0.2.24:22").AutoFocus()
			})
			muted(ui.Text(c, T("wizard.connect.paste_hint"))).Wrap()
			user, _, port := splitAddress(w.addr)
			ui.Row(c).Gap(10).Children(func() {
				labeled(c, T("wizard.field.user"), func() { ui.TextInput(c, &w.user).Label(T("wizard.field.user")).Placeholder(orText(user, "root")) }).Grow(1)
				labeled(c, T("wizard.field.port"), func() { ui.TextInput(c, &w.port).Label(T("wizard.field.port")).Placeholder(orText(port, "22")) }).Width(110)
			})
			ui.Text(c, T("wizard.connect.how")).Bold()
			ui.Row(c).Gap(10).Children(func() {
				loginTile(c, w, config.LoginPassword, T("wizard.login.password"), T("wizard.login.password_sub"))
				loginTile(c, w, config.LoginKey, T("wizard.login.key"), T("wizard.login.key_sub"))
			})
			if w.login == config.LoginKey {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					mono(muted(ui.Text(c, orText(w.keyPath, T("wizard.key.none"))))).Grow(1).MaxLines(1)
					ui.Button(c, T("wizard.key.pick")).OnClick(func() { a.pickKeyFile(w) })
				})
			}
			labeled(c, T("wizard.field.name"), func() {
				ui.TextInput(c, &w.name).Label(T("wizard.field.name")).Placeholder(orText(defaultName(w.server().HostName), "home-vps"))
			})
			muted(ui.Text(c, T("wizard.name.hint"))).Wrap()
			textLink(c, T("wizard.import.link"), func() { a.openImport(w) })
			why := w.problem()
			if why != "" && (w.addr != "" || w.name != "") {
				ui.Text(c, why).FontSize(12).TextColor(palette.Warn)
			}
			if w.err != nil {
				errorBox(c, w.err, a.fold("wiz-err"))
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { a.wiz = nil; a.router.Push("/servers") })
				if w.busy {
					srv := w.server()
					muted(ui.Text(c, T("wizard.scanning", "addr", knownHostsName(srv.HostName, srv.Port))))
					ui.Spinner(c)
				} else {
					b := ui.PrimaryButton(c, T("wizard.connect.next")).OnClick(func() { a.scanKeys(w) })
					if why != "" {
						b.Disabled(true)
					}
				}
			})
		})
		ui.Column(c).Width(300).Gap(14).Children(func() {
			a.liveCard(c, w)
			card(ui.Column(c)).Gap(10).Children(func() {
				muted(ui.Text(c, T("wizard.next.title"))).FontSize(12)
				how := T("wizard.next.password")
				if w.login == config.LoginKey {
					how = T("wizard.next.key")
				}
				for i, s := range []string{T("wizard.next.fingerprint"), how, T("wizard.next.passes"), T("wizard.next.agents")} {
					ui.Row(c.Key(i)).Gap(10).Children(func() {
						ui.Text(c, itoa(i+1)).FontSize(12).Bold().TextColor(palette.Brand)
						ui.Text(c, s).FontSize(13).Wrap()
					})
				}
				muted(ui.Text(c, T("wizard.next.offline"))).FontSize(11)
			})
		})
	})
}

// loginTile is one of the two ways to log in, chosen by clicking.
func loginTile(c *ui.Context, w *wizard, kind, title, sub string) {
	on := w.login == kind
	t := ui.Column(c.Key(kind)).Grow(1).Basis(0).Gap(4).Padding(12, 14).Radius(cur.Sizes.Radius).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(title).Border(1, palette.Border)
	if on {
		t.Border(2, palette.Brand).Background(palette.Board)
	} else if t.Hovered() {
		t.Background(palette.Hover)
	}
	t.OnClick(func() { w.login = kind }).Children(func() {
		ui.Text(c, title).Bold().TextColor(palette.Text)
		muted(ui.Text(c, sub)).Wrap()
	})
}

// labeled puts a visible label over a field.
func labeled(c *ui.Context, label string, field func()) ui.Element {
	return ui.Column(c.Key("f-" + label)).Gap(5).Children(func() {
		ui.Text(c, label).FontSize(12).Bold().TextColor(palette.TextSub)
		field()
	})
}

// liveCard is how the server will look in the list, as it is typed.
func (a *App) liveCard(c *ui.Context, w *wizard) {
	srv := w.server()
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.server).FontSize(22).TextColor(palette.Brand)
			ui.Text(c, orText(srv.Name, T("wizard.card.new"))).FontSize(16).Bold().Grow(1)
			stateTag(c, serverState{Key: "unknown", Word: T("server.state.unknown"), Tone: "stop"})
		})
		addr := srv.HostName
		if srv.User != "" {
			addr = srv.User + "@" + addr
		}
		if srv.Port != 0 {
			addr += ":" + strconv.Itoa(srv.Port)
		}
		mono(muted(ui.Text(c, orText(addr, T("wizard.card.no_addr"))))).FontSize(13)
		how := T("wizard.card.password")
		if w.login == config.LoginKey {
			how = T("wizard.card.key")
		}
		muted(ui.Text(c, how+T("common.dot_sep")+T("servers.no_agents")))
	})
}

func orText(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func (a *App) pickKeyFile(w *wizard) {
	if a.pickFile == nil {
		return
	}
	a.run(func() {
		p, err := a.pickFile(T("wizard.key.pick_title"), T("wizard.key.pick_msg"))
		a.update(func() {
			if err == nil && p != "" {
				w.keyPath = p
			}
		})
	})
}

func (a *App) scanKeys(w *wizard) {
	srv := w.server()
	w.busy, w.err, w.mismatch = true, nil, false
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		keys, err := a.svc.ScanHostKeys(ctx, srv)
		a.update(func() {
			w.busy, w.err, w.keys = false, err, keys
			if err == nil {
				w.step = wzIdentity
			}
		})
	})
}
