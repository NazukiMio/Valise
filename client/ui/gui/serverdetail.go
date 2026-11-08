package gui

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// The server detail (docs/UI.md 7.5): its state, porter and source each as
// a tag; the two passes; the agents on it; and why it is offline or needs
// attention.

// passState is what the detail page reads and does for one server.
type passState struct {
	doc         *service.DoctorReport
	docErr      error
	docBusy     bool
	busy        bool // issuing or revoking the admin pass
	err         error
	issuing     bool // the issue form is open
	pass, pass2 string
	revoke      bool // the revoke confirmation is open
}

func (a *App) passOf(host string) *passState {
	ps := a.passes[host]
	if ps == nil {
		ps = &passState{}
		a.passes[host] = ps
		a.readDoctor(host)
	}
	return ps
}

// readDoctor asks porter its version over the user pass.
func (a *App) readDoctor(host string) {
	ps := a.passes[host]
	ps.docBusy, ps.docErr = true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		doc, err := a.svc.Doctor(ctx, host)
		a.update(func() { ps.docBusy, ps.doc, ps.docErr = false, doc, err })
	})
}

func (a *App) serverPage(c *ui.Context, name string) {
	if !a.servers.started {
		a.refreshServers()
	}
	var row *serverRow
	for i := range a.servers.value {
		if a.servers.value[i].Name == name {
			row = &a.servers.value[i]
		}
	}
	ps := a.passOf(name)
	st := a.serverState(name, c.Now())
	ui.Link(c, "‹ "+text.navServers, "/servers")
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Children(func() { pageHead(c, name, "") })
		if a.agents.busy {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); muted(ui.Text(c, "正在连接 "+name+"…")) })
		} else {
			ui.Button(c, "重新连接").OnClick(func() { a.refreshAgents(); a.readDoctor(name) })
		}
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		stateTag(c, st)
		if obs := a.observation(name); obs != nil && obs.ObservedAt != nil {
			muted(ui.Text(c, when(*obs.ObservedAt, c.Now())+" 读取"))
		}
		switch {
		case ps.doc != nil && ps.doc.PorterVersion != "":
			plainTag(c, "porter "+ps.doc.PorterVersion)
		case ps.doc != nil && ps.doc.Problem != nil:
			plainTag(c, "porter 不可用")
		}
		if row != nil && row.Valise {
			plainTag(c, "Valise 接入")
		} else if row != nil {
			plainTag(c, "来自 SSH 配置")
		}
	})
	if st.Word == "离线" || st.Word == "需要处理" {
		a.serverTrouble(c, name, st)
	}
	if row != nil {
		a.passBlock(c, name, row.Valise, ps)
	}
	a.agentsHere(c, name)
}

// plainTag is a neutral fact as a small tag.
func plainTag(c *ui.Context, s string) {
	ui.Box(c.Key(s)).Padding(2, 10).Radius(999).Border(1, palette.Line).Children(func() {
		ui.Text(c, s).FontSize(12).TextColor(palette.TextSub)
	})
}

func (a *App) serverTrouble(c *ui.Context, name string, st serverState) {
	obs := a.observation(name)
	card(ui.Column(c)).Gap(8).Background(palette.TroubleBg).Border(1, palette.TroubleBorder).Children(func() {
		ui.Text(c, name+" "+st.Word).FontSize(15).Bold().TextColor(palette.Warn)
		fact(c, "原因", st.Reason)
		if st.Suggestion != "" {
			fact(c, "建议", st.Suggestion)
		}
		if obs != nil && !obs.LastAttemptAt.IsZero() {
			fact(c, "上次尝试", when(obs.LastAttemptAt, c.Now()))
		}
		if obs != nil && obs.ObservedAt != nil {
			fact(c, "上次读到", when(*obs.ObservedAt, c.Now()))
		}
		if obs != nil && obs.Error != nil && obs.Error.Detail != "" {
			ui.Collapsible(c, text.techInfo, a.fold("srv-detail-"+name), func() {
				mono(muted(ui.Text(c, obs.Error.Code+"\n"+obs.Error.Detail))).Wrap()
			})
		}
	})
}

// passBlock is the user pass and the admin pass, or for a server from
// ~/.ssh/config, that the user's own SSH setup logs in.
func (a *App) passBlock(c *ui.Context, name string, tiered bool, ps *passState) {
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, "通行证").FontSize(16).Bold()
		if !tiered {
			muted(ui.Text(c, "用你自己的 SSH 设置登录。这台服务器没有用户通行证与管理员通行证之分。")).FontSize(13).Wrap()
			return
		}
		user := "能用"
		if !a.svc.HasDailyKey(name) {
			user = "这台电脑上没有"
		}
		passLine(c, "用户通行证 · "+user, "Valise 用它自动查看和备份，只能做这些。")
		has := a.svc.HasAdminKey(name)
		left, open := a.svc.AdminRemaining(name)
		ui.Row(c).Gap(10).Padding(10, 12).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				switch {
				case !has:
					ui.Text(c, "管理员通行证 · 没办").Bold()
					muted(ui.Text(c, "改动服务器时用服务器密码临时登录。")).Wrap()
				case open:
					ui.Text(c, fmt.Sprintf("管理员通行证 · 已解锁 · 剩余 %d 分钟", int(left.Minutes())+1)).Bold()
					muted(ui.Text(c, "到时自动锁上。")).Wrap()
				default:
					ui.Text(c, "管理员通行证 · 已锁上").Bold()
					muted(ui.Text(c, fmt.Sprintf("改动服务器时输管理口令解锁，%d 分钟后自动锁上。", int(a.svc.AdminWindow().Minutes())))).Wrap()
				}
			})
			switch {
			case ps.busy:
				ui.Spinner(c)
			case !has && !ps.issuing:
				ui.Button(c, "办一张管理员通行证").OnClick(func() { ps.issuing, ps.err = true, nil })
			case has:
				ui.Button(c, "注销").OnClick(func() { ps.revoke, ps.err = true, nil })
			}
		})
		if ps.issuing && !ps.busy {
			a.issueForm(c, name, ps)
		}
		if ps.err != nil {
			errorBox(c, ps.err, a.fold("pass-err-"+name))
		}
	})
	if ps.revoke {
		a.revokeDialog(c, name, ps)
	}
}

func (a *App) issueForm(c *ui.Context, name string, ps *passState) {
	ui.Column(c).Gap(8).Children(func() {
		labeled(c, "管理口令", func() {
			ui.TextInput(c, &ps.pass).Password().Label("管理口令").Placeholder("至少 12 个字符，和 Valise 口令不同").AutoFocus()
		})
		labeled(c, "再输一次", func() {
			ui.TextInput(c, &ps.pass2).Password().Label("再输一次").Placeholder("再输一次管理口令")
		})
		muted(ui.Text(c, "办理要登录服务器一次：OpenSSH 会问服务器密码（或用你的私钥）。")).Wrap()
		why := ""
		switch {
		case len([]rune(ps.pass)) < 12:
			why = "管理口令至少 12 个字符"
		case ps.pass != ps.pass2:
			why = "两次输入的管理口令不一样"
		}
		if why != "" && ps.pass != "" {
			ui.Text(c, why).FontSize(12).TextColor(palette.Warn)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "取消").OnClick(func() { ps.issuing, ps.pass, ps.pass2 = false, "", "" })
			b := ui.PrimaryButton(c, "办理").OnClick(func() { a.issuePass(name, ps) })
			if why != "" {
				b.Disabled(true)
			}
		})
	})
}

func (a *App) issuePass(name string, ps *passState) {
	pass := ps.pass
	ps.busy, ps.err, ps.pass, ps.pass2 = true, nil, "", ""
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		err := a.svc.CreateAdminKey(ctx, name, pass)
		a.update(func() {
			ps.busy, ps.err = false, err
			if err == nil {
				ps.issuing = false
			}
		})
	})
}

func (a *App) revokeDialog(c *ui.Context, name string, ps *passState) {
	if ui.Modal(c, &ps.revoke, func() {
		ui.Column(c).Width(420).Padding(24).Gap(12).Children(func() {
			ui.Text(c, "注销 "+name+" 的管理员通行证？").FontSize(18).Bold()
			muted(ui.Text(c, "会从服务器上移除它，并删掉这台电脑上的那一份。之后改动服务器要用服务器密码临时登录；备份不受影响。注销前要用它（或服务器密码）登录一次。")).FontSize(12).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, "取消").OnClick(func() { ps.revoke = false })
				ui.PrimaryButton(c, "注销").OnClick(func() {
					ps.revoke, ps.busy, ps.err = false, true, nil
					a.run(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
						defer cancel()
						err := a.svc.RemoveAdminKey(ctx, name)
						a.update(func() { ps.busy, ps.err = false, err })
					})
				})
			})
		})
	}).Dismissed() {
		ps.revoke = false
	}
}

// agentsHere lists the agents on the server, each leading to its page.
func (a *App) agentsHere(c *ui.Context, name string) {
	var here []agentRow
	for _, r := range a.agentRows(a.agents.value) {
		if r.Server == name && !r.Retired() {
			here = append(here, r)
		}
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("这台服务器上的 Agent · %d", len(here))).FontSize(16).Bold().Grow(1)
			ui.Button(c, "查找、接入或安装").OnClick(func() { a.router.Push("/servers/" + name + "/agents") })
		})
		if len(here) == 0 {
			muted(ui.Text(c, "还没有接入的 Agent。")).FontSize(13)
		}
		for _, r := range here {
			line := ui.Row(c.Key(r.Instance)).Gap(10).Padding(6, 4).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Role(ui.RoleButton).Label(r.Name)
			if line.Hovered() {
				line.Background(palette.Hover)
			}
			line.OnClick(func() { a.router.Push("/agents/" + r.Server + "/" + r.Instance) }).Children(func() {
				a.avatar(c, r, 24)
				ui.Text(c, r.Name).Bold()
				muted(ui.Text(c, r.Harness)).Grow(1)
				ui.Text(c, "›").TextColor(palette.Line)
			})
		}
	})
}
