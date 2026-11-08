package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/valise"
)

// passPrompt is the admin passphrase being asked for by work running off
// the main thread; the answer goes back on reply and is never kept.
type passPrompt struct {
	host  string
	owner bool // the Valise passphrase (proving it is you), else the admin passphrase
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
func (a *App) AdminPassphrase(host string) (string, error) { return a.askPass(host, false) }

// OwnerPassphrase is service.OwnerAuth.Passphrase for the desktop: the
// Valise passphrase, asked when a step must confirm it is you.
func (a *App) OwnerPassphrase(host string) (string, error) { return a.askPass(host, true) }

func (a *App) askPass(host string, owner bool) (string, error) {
	reply := make(chan passReply, 1)
	a.update(func() { a.asking = &passPrompt{host: host, owner: owner, open: true, reply: reply} })
	r := <-reply
	return r.pass, r.err
}

// passDialog draws the question while one is open.
func (a *App) passDialog(c *ui.Context) {
	p := a.asking
	if p == nil {
		return
	}
	answer := func(r passReply) {
		p.reply <- r
		p.value = ""
		a.asking = nil
	}
	title, why, field := "输入 "+p.host+" 的管理口令", "用来解锁这台服务器的管理钥匙；解锁后一段时间内不再询问。口令不会被保存。", "管理口令"
	cancelled := "已取消，没有解锁管理钥匙"
	if p.owner {
		title, why, field = "输入 Valise 口令", "这一步要确认是你本人（"+p.host+"）；确认后一段时间内不再询问。口令不会被保存。", "Valise 口令"
		cancelled = "已取消，没有确认身份"
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
	}).Dismissed() && a.asking == p {
		cancel()
	}
}
