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
	}
	cancel := func() {
		answer(passReply{err: valise.New("cancelled", cancelled, "")})
	}
	if ui.Modal(c, &p.open, func() {
		dialog(ui.Column(c), 420).Children(func() {
			dialogTitle(ui.Text(c, title))
			muted(ui.Text(c, why)).FontSize(12).Wrap()
			in := ui.TextInput(c, &p.value).Password().Placeholder(field).Label(field).AutoFocus()
			if in.Submitted() && p.value != "" {
				answer(passReply{pass: p.value})
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(cancel)
				ui.PrimaryButton(c, T("common.ok")).OnClick(func() {
					if p.value != "" {
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
