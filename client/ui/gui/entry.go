package gui

import (
	"context"
	"errors"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// entryState is one agent's admin page tunnel while this window lives.
// The page itself opens only in the system browser (docs/UI.md 6).
type entryState struct {
	busy    bool
	open    bool
	display string // token-free address, safe to show
	steps   []service.EntryStatus
	err     error
	cancel  context.CancelFunc
}

var entrySteps = map[string]string{service.EntryTunnel: "隧道已建立", service.EntryRemote: "服务器端口在监听",
	service.EntryHealth: "页面能打开", service.EntryAuth: "管理页令牌"}

// openEntry starts the tunnel off the main thread; it stays up until it is
// closed, the window closes, or it has been idle for DefaultEntryIdle.
func (a *App) openEntry(server, instance string) {
	key := server + "/" + instance
	st := &entryState{busy: true}
	a.entries[key] = st
	ctx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	a.run(func() {
		err := a.entryOpener(ctx, server, instance, service.OpenOptions{Idle: service.DefaultEntryIdle,
			Status: func(s service.EntryStatus) { a.update(func() { st.steps = append(st.steps, s) }) },
			Ready: func(open, display string) {
				a.update(func() { st.busy, st.open, st.display = false, true, display })
				if err := a.openURL(open); err != nil {
					a.update(func() { st.err = errors.New("无法打开浏览器，请手动打开 " + display) })
				}
			}})
		a.update(func() {
			st.busy, st.open = false, false
			if err != nil && ctx.Err() == nil {
				st.err = err
			}
		})
	})
}

func (a *App) closeEntry(key string) {
	if st := a.entries[key]; st != nil && st.cancel != nil {
		st.cancel()
	}
	delete(a.entries, key)
}

// entryButton is the agent page's one primary button and what came of it.
func (a *App) entryButton(c *ui.Context, row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.entries[key]
	switch {
	case st == nil:
		ui.PrimaryButton(c, "打开管理页").OnClick(func() { a.openEntry(row.Server, row.Instance) })
	case st.busy:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			muted(ui.Text(c, "正在连接…"))
		})
	case st.open:
		ui.Button(c, "关闭管理页").OnClick(func() { a.closeEntry(key) })
	default:
		ui.PrimaryButton(c, "重新打开管理页").OnClick(func() { a.closeEntry(key); a.openEntry(row.Server, row.Instance) })
	}
}

// entryReport lists each step on its own line: the tunnel, the server's
// port and the page are separate facts (AGENTS.md 7.5).
func (a *App) entryReport(c *ui.Context, row *agentRow) {
	st := a.entries[row.Server+"/"+row.Instance]
	if st == nil {
		return
	}
	ui.Column(c).Gap(4).Padding(12, 16).Radius(10).Background(palette.Board).Border(1, palette.CardBorder).Children(func() {
		if st.open {
			ui.Text(c, "管理页已在浏览器中打开："+st.display).FontSize(13).Bold()
		}
		for i, s := range st.steps {
			if s.Step == service.EntryAuth && s.OK {
				continue // the token itself is never shown
			}
			tone := "ok"
			if !s.OK {
				tone = "warn"
			}
			ui.Row(c.Key(i)).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(tone))
				ui.Text(c, entrySteps[s.Step]).FontSize(12)
				muted(ui.Text(c, s.Detail)).MaxLines(2)
			})
		}
		if st.err != nil {
			errorBox(c, st.err, a.fold("entry-err-"+row.Server+row.Instance))
		}
	})
}
