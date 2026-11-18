package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/valise"
)

// What a prompt asks for.
const (
	askAdmin  = iota // the admin passphrase (unlocks the admin pass)
	askOwner         // the Valise passphrase (proving it is you)
	askServer        // OpenSSH's own prompt: a server password or a key's passphrase
	askPIN           // the local six-digit PIN
)

// passPrompt is a secret being asked for by work running off the main
// thread; the answer goes back on reply and is never kept. Prompts wait in
// line: one dialog at a time.
type passPrompt struct {
	host  string // for askServer, OpenSSH's prompt
	kind  int
	value string
	open  bool
	reply chan passReply
}

// errStopped answers a question whose work the user stopped.
var errStopped = valise.New("cancelled", T("service.short.stopped"), "")

type passReply struct {
	pass string
	err  error
}

// AdminPassphrase is service.AdminAuth.Passphrase for the desktop: it asks
// in the window and waits. It is called off the main thread only.
func (a *App) AdminPassphrase(host string) (string, error) { return a.askPass(host, askAdmin) }

// OwnerPassphrase is service.OwnerAuth.Passphrase for the desktop: the
// Valise passphrase, asked when a step must confirm it is you.
func (a *App) OwnerPassphrase(host string) (string, error) { return a.askPass(host, askOwner) }

func (a *App) OwnerPIN(host string) (string, error) { return a.askPass(host, askPIN) }

// ServerPassword is service.PasswordAuth.Prompt for the desktop: OpenSSH's
// own question, relayed (docs/ARCHITECTURE.md 2.1).
func (a *App) ServerPassword(prompt string) (string, error) { return a.askPass(prompt, askServer) }

func (a *App) askPass(host string, kind int) (string, error) {
	reply := make(chan passReply, 1)
	a.update(func() { a.asks = append(a.asks, &passPrompt{host: host, kind: kind, open: true, reply: reply}) })
	r := <-reply
	return r.pass, r.err
}

// passDialog draws the question while one is open.
func (a *App) passDialog(c *ui.Context) {
	if len(a.asks) == 0 {
		return
	}
	p := a.asks[0]
	answer := func(r passReply) {
		p.reply <- r
		p.value = ""
		a.asks = a.asks[1:]
	}
	title, why, field := T("ask.admin.title", "host", p.host), T("ask.admin.why"), T("pass.admin_passphrase")
	cancelled := T("ask.cancel.admin")
	switch p.kind {
	case askOwner:
		title, why, field = T("ask.owner.title"), T("ask.owner.why", "what", p.host), T("ask.owner.field")
		cancelled = T("ask.cancel.owner")
	case askServer:
		title, why, field, cancelled = serverPromptMeta(p.host)
	case askPIN:
		title, why, field = T("pin.verify"), T("pin.each_operation"), T("pin.field")
		cancelled = T("ask.cancel.owner")
	}
	cancel := func() {
		answer(passReply{err: valise.New("cancelled", cancelled, "")})
	}
	if ui.Modal(c, &p.open, func() {
		dialog(ui.Column(c), 420).Children(func() {
			askKind(c, field, askGoesTo(p))
			dialogTitle(ui.Text(c, title))
			muted(ui.Text(c, why)).Wrap()
			in := ui.TextInput(c, &p.value).Password().Placeholder(field).Label(field).AutoFocus()
			allowEmpty := p.kind == askServer
			if in.Submitted() && (p.value != "" || allowEmpty) {
				answer(passReply{pass: p.value})
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(cancel)
				ui.PrimaryButton(c, T("common.ok")).OnClick(func() {
					if p.value != "" || allowEmpty {
						answer(passReply{pass: p.value})
					}
				})
			})
		})
	}).Dismissed() && len(a.asks) > 0 && a.asks[0] == p {
		cancel()
	}
}

// serverPromptMeta maps OpenSSH and sudo prompts to the dialog's copy.
func serverPromptMeta(prompt string) (title, why, field, cancelled string) {
	trimmed := strings.TrimSpace(prompt)
	if strings.Contains(strings.ToLower(trimmed), "sudo") {
		who := strings.TrimSuffix(strings.TrimPrefix(trimmed, "[sudo] password for "), ":")
		title = T("ask.sudo.generic")
		if who != trimmed && who != "" {
			title = T("ask.sudo.title", "who", who)
		}
		return title, T("ask.sudo.why", "prompt", prompt), T("ask.sudo.field"), T("ask.cancel.sudo")
	}
	if who, ok := strings.CutSuffix(trimmed, "'s password:"); ok {
		return T("ask.server.title", "who", who), T("ask.server.why", "prompt", prompt), T("wizard.login.password"), T("ask.cancel.server")
	}
	if strings.Contains(strings.ToLower(prompt), "passphrase") {
		return T("ask.key_passphrase"), T("ask.server.why", "prompt", prompt), T("ask.key_passphrase_field"), T("ask.cancel.server")
	}
	return T("ask.generic"), T("ask.server.why", "prompt", prompt), T("wizard.login.password"), T("ask.cancel.server")
}

// askGoesTo says where the secret is checked: on this computer, or by the
// server. Either way Valise keeps nothing.
func askGoesTo(p *passPrompt) string {
	if p.kind != askServer {
		return T("ask.dest.local")
	}
	low := strings.ToLower(p.host)
	switch {
	case strings.Contains(low, "sudo"):
		return T("ask.dest.sudo")
	case strings.Contains(low, "passphrase"):
		return T("ask.dest.local")
	}
	return T("ask.dest.server")
}

// askKind is the strip over a secret's dialog: which secret it is and where
// it goes, so a server password is never typed where a passphrase is asked.
func askKind(c *ui.Context, kind, dest string) {
	ui.Row(c).Gap(spInside).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(spHair+2).Padding(3, 10).Radius(radPill).Background(palette.Brand.Alpha(0.12)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.key).FontSize(fsSub).TextColor(palette.Brand)
			ui.Text(c, kind).FontSize(fsCaption).Bold().TextColor(palette.Brand)
		})
		muted(ui.Text(c, dest)).Wrap()
	})
}
