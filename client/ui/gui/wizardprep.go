package gui

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// prepLabels name the steps of preparing a server, in order: adding by
// hand has the passes and the last check; importing from ~/.ssh/config
// records the server instead.
func prepLabels(importing bool) [][2]string {
	if importing {
		return [][2]string{{service.SetupCheck, "环境检查"}, {service.SetupDeploy, "porter"},
			{service.SetupRegister, "登记这台电脑"}, {service.SetupName, "服务器名"}, {service.SetupRecord, "记录到这台电脑"}}
	}
	return [][2]string{{service.SetupKeys, "办通行证"}, {service.SetupCheck, "环境检查"}, {service.SetupDeploy, "porter"},
		{service.SetupRegister, "登记这台电脑"}, {service.SetupName, "服务器名"}, {service.SetupDaily, "用用户通行证连上 porter"}}
}

// stepDetail says a finished step's result in the interface's words.
func stepDetail(s service.SetupStep) string {
	if s.Step == service.SetupKeys {
		out := "用户通行证（只能查看和备份）"
		if strings.Contains(s.Detail, "admin") {
			out += "、管理员通行证"
		}
		return out
	}
	return s.Detail
}

// prepareStep shows each step on its own line: done, running, failed or
// not reached, said apart (docs/UI.md 7.5).
func (a *App) prepareStep(c *ui.Context, w *wizard) {
	card(ui.Column(c)).Gap(12).Padding(22).Children(func() {
		ui.Text(c, "正在准备「"+w.host+"」").FontSize(20).Bold().Font(cur.Fonts.Display)
		done := map[string]service.SetupStep{}
		for _, s := range w.steps {
			done[s.Step] = s
		}
		next := true
		for _, l := range prepLabels(w.importing) {
			s, ok := done[l[0]]
			mark, ink, detail := "○", palette.TextSub, ""
			switch {
			case ok:
				mark, ink, detail = "✓", palette.Ok, stepDetail(s)
			case next && w.busy:
				mark, ink, detail, next = "▸", palette.Text, a.doingNow(w, c.Now()), false
			case next && w.err != nil:
				mark, ink, detail, next = "✗", palette.Warn, "没有完成，见下方原因；之后的步骤没有执行", false
			}
			ui.Row(c.Key(l[0])).Gap(12).Padding(8, 12).Radius(8).Background(palette.Board).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mark).Bold().TextColor(ink).Width(16)
				ui.Text(c, l[1]).Bold().Width(170)
				muted(ui.Text(c, detail)).Wrap().Grow(1)
				if mark == "▸" {
					ui.Spinner(c)
				}
			})
		}
		if w.err != nil {
			errorBox(c, w.err, a.fold("wiz-err"))
			a.porterChoice(c, w)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			switch {
			case w.busy:
				muted(ui.Text(c, "可以随时停止；已经做完的步骤会保留。")).Grow(1)
				ui.Button(c, "停止").OnClick(func() { a.stopPrepare(w) })
			case w.err != nil:
				ui.Button(c, "取消").OnClick(func() { a.closeWizard() })
				ui.PrimaryButton(c, "重试").OnClick(func() {
					if w.importing {
						a.runImport(w, w.host)
					} else {
						a.runAdd(w)
					}
				})
			default:
				ui.Button(c, "完成").OnClick(func() { a.closeWizard() })
				ui.PrimaryButton(c, "看看上面有哪些 Agent").OnClick(func() { a.router.Push("/servers/" + w.host + "/agents") })
			}
		})
	})
}

// doingNow is the step's current sub-step with how long it has waited, or
// that OpenSSH is waiting for the user's answer in the dialog.
func (a *App) doingNow(w *wizard, now time.Time) string {
	for _, p := range a.asks {
		if p.kind == askServer {
			return "等你在弹框里回答 OpenSSH 的提问"
		}
	}
	text := orText(w.doing, "进行中")
	if secs := int(now.Sub(w.doingAt).Seconds()); !w.doingAt.IsZero() && secs >= 3 {
		text += "…（已等 " + itoa(secs) + " 秒）"
	} else {
		text += "…"
	}
	return text
}

// stopPrepare ends the running work and the questions it asked.
func (a *App) stopPrepare(w *wizard) {
	if w.cancel != nil {
		w.cancel()
	}
	a.dropServerPrompts()
}

// dropServerPrompts answers OpenSSH's open questions with “cancelled” when
// the user stops: the ssh asking is being ended anyway.
func (a *App) dropServerPrompts() {
	keep := a.asks[:0]
	for _, p := range a.asks {
		if p.kind == askServer {
			p.reply <- passReply{err: errStopped}
			continue
		}
		keep = append(keep, p)
	}
	a.asks = keep
}
