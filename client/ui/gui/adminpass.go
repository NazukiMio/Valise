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
	title, why, field := "输入 "+p.host+" 的管理口令", "用来解锁这台服务器的管理员通行证；解锁后一段时间内不再询问。口令不会被保存。", "管理口令"
	cancelled := "已取消，没有解锁管理员通行证"
	switch p.kind {
	case askOwner:
		title, why, field = "输入 Valise 口令", "这一步要确认是你本人（"+p.host+"）；确认后一段时间内不再询问。口令不会被保存。", "Valise 口令"
		cancelled = "已取消，没有确认身份"
	case askServer:
		title, why, field = serverPromptTitle(p.host), "这是 OpenSSH 在问："+p.host+"\n答案直接交给它，只用这一次，Valise 不保存。", "服务器密码"
		cancelled = "已取消，没有登录"
	}
	cancel := func() {
		answer(passReply{err: valise.New("cancelled", cancelled, "")})
	}
	if ui.Modal(c, &p.open, func() {
		ui.Column(c).Width(420).Padding(24).Gap(12).Children(func() {
			ui.Text(c, title).FontSize(18).Bold()
			muted(ui.Text(c, why)).FontSize(12).Wrap()
			in := ui.TextInput(c, &p.value).Password().Placeholder(field).Label(field).AutoFocus()
			if in.Submitted() && p.value != "" {
				answer(passReply{pass: p.value})
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, "取消").OnClick(cancel)
				ui.PrimaryButton(c, "确定").OnClick(func() {
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

// serverPromptTitle turns OpenSSH's “agent@host's password:” into a title.
func serverPromptTitle(prompt string) string {
	if who, ok := strings.CutSuffix(strings.TrimSpace(prompt), "'s password:"); ok {
		return "输入 " + who + " 的服务器密码"
	}
	if strings.Contains(strings.ToLower(prompt), "passphrase") {
		return "输入私钥的口令"
	}
	return "OpenSSH 需要你回答"
}
