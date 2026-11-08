package gui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/profile"
	"valise/core/valise"
	"valise/ui/service"
)

// healthState is one agent's health page while this window lives.
type healthState struct {
	busy   bool
	report *service.HealthReport
	err    error
	at     time.Time
	fix    *fixState
}

// fixState is one repair action from its confirmation to its result: the
// command's exit and the service afterwards are kept apart (AGENTS.md 7.5).
type fixState struct {
	item     service.HealthItem
	loading  bool // reading the action's declaration
	action   *profile.Action
	access   string
	confirm  bool // the confirmation is open
	running  bool
	progress []string
	result   *service.ActionResult
	err      error
}

func (a *App) readHealth(server, instance string) {
	key := server + "/" + instance
	st := a.health[key]
	if st == nil {
		st = &healthState{}
		a.health[key] = st
	}
	if st.busy {
		return
	}
	st.busy = true
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		r, err := a.svc.Health(ctx, server, instance)
		a.update(func() { st.busy, st.report, st.err, st.at = false, r, err, time.Now() })
	})
}

// healthTab lists the health checks, bad first; an item whose harness
// declares a repair action offers it (docs/UI.md 7.3).
func (a *App) healthTab(c *ui.Context, row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.health[key]
	if st == nil {
		a.readHealth(row.Server, row.Instance)
		st = a.health[key]
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		line := "正在读取健康检查…"
		if !st.busy && st.report != nil {
			line = "上次读取：" + when(st.at, c.Now())
		}
		muted(ui.Text(c, line)).FontSize(13).Grow(1)
		if st.busy {
			ui.Spinner(c)
		} else {
			ui.Button(c, "重新检查").OnClick(func() { a.readHealth(row.Server, row.Instance) })
		}
	})
	if st.err != nil {
		errorBox(c, st.err, a.fold("health-err-"+key))
	}
	if st.fix != nil {
		a.fixReport(c, row, st)
	}
	if st.report == nil {
		return
	}
	items := append([]service.HealthItem(nil), st.report.Items...)
	sort.SliceStable(items, func(i, j int) bool { return service.Rank(items[i].Level) < service.Rank(items[j].Level) })
	card(ui.Column(c)).Padding(4, 0).Gap(0).Children(func() {
		for i, it := range items {
			if i > 0 {
				ui.Divider(c)
			}
			a.healthLine(c.Key(it.ID), row, st, it)
		}
	})
	muted(ui.Text(c, "这里只说明已声明检查项的结果，不代表 Agent 不会出错。")).FontSize(11)
}

func (a *App) healthLine(c *ui.Context, row *agentRow, st *healthState, it service.HealthItem) {
	tone := map[string]string{service.HealthOK: "ok", service.HealthWarn: "warn", service.HealthBad: "warn"}[it.Level]
	ui.Row(c).Padding(14, 18).Gap(14).AlignItems(ui.Center).Children(func() {
		dot := ui.Box(c).Size(8, 8).Radius(4).Background(palette.Line)
		if tone != "" {
			dot.Background(toneColor(tone))
		}
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, it.Title).FontSize(14).Bold().Wrap()
			if it.Text != "" {
				muted(ui.Text(c, it.Text)).FontSize(13).Wrap()
			}
		})
		// Only a declared repair action is offered here; built-in system
		// fixes are not in the desktop yet, so they are not drawn.
		if it.ActionID != "" && (st.fix == nil || !st.fix.running) {
			ui.PrimaryButton(c, it.ActionName).OnClick(func() { a.prepareFix(row, st, it) })
		}
	})
}

// prepareFix reads the action's declaration, then asks for confirmation.
func (a *App) prepareFix(row *agentRow, st *healthState, it service.HealthItem) {
	f := &fixState{item: it, loading: true}
	st.fix = f
	server, instance := row.Server, row.Instance
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		list, err := a.svc.Actions(ctx, server, instance)
		a.update(func() {
			f.loading = false
			if err != nil {
				f.err = err
				return
			}
			for i := range list.Actions {
				if list.Actions[i].ID == it.ActionID {
					f.action = &list.Actions[i]
				}
			}
			switch {
			case list.Stale:
				f.err = valise.New("action_profile_stale", "本机与服务器的 harness 资料版本不同，动作没有执行", "先重新接入这个 Agent，再回来执行")
			case f.action == nil:
				f.err = valise.New("action_unknown", "harness 资料里没有动作 "+it.ActionID, "检查资料包版本")
			default:
				f.access, f.confirm = a.svc.ActionAccess(server, *f.action), true
			}
		})
	})
}

// runFix runs the confirmed action, then reads health again.
func (a *App) runFix(row *agentRow, st *healthState) {
	f := st.fix
	f.confirm, f.running = false, true
	server, instance, id := row.Server, row.Instance, f.action.ID
	seal := service.SealsByDefault(*f.action)
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := a.svc.RunAction(ctx, server, instance, id, service.ActionOptions{Seal: seal,
			Progress: func(m string) { a.update(func() { f.progress = append(f.progress, m) }) }})
		a.update(func() {
			f.running, f.result, f.err = false, res, err
			a.readHealth(server, instance)
			a.refreshAgents()
		})
	})
}

// fixReport is the confirmation, the progress and what came of the action.
func (a *App) fixReport(c *ui.Context, row *agentRow, st *healthState) {
	f := st.fix
	if f.confirm {
		ui.Modal(c, &f.confirm, func() {
			ui.Column(c).Width(460).Padding(24).Gap(12).Children(func() {
				ui.Text(c, "执行「"+f.action.Name+"」？").FontSize(18).Bold()
				muted(ui.Text(c, service.RiskLabels[f.action.Risk]+" · "+f.access)).FontSize(12)
				ui.Text(c, f.action.Explain).FontSize(13).Wrap()
				ui.Column(c).Gap(4).Padding(10, 12).Radius(8).Background(palette.Board).Children(func() {
					muted(ui.Text(c, "在 "+row.Server+" 上执行的命令（来自 harness 资料）"))
					ui.Text(c, strings.Join(f.action.Argv, " ")).FontSize(12).Font(cur.Fonts.Mono).Wrap()
				})
				muted(ui.Text(c, "需要管理登录：有管理钥匙时会请你输入管理口令。")).FontSize(12).Wrap()
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					ui.Button(c, "取消").OnClick(func() { st.fix = nil })
					ui.PrimaryButton(c, "执行").OnClick(func() { a.runFix(row, st) })
				})
			})
		})
		return
	}
	ui.Column(c).Gap(6).Padding(12, 16).Radius(10).Background(palette.Board).Border(1, palette.CardBorder).Children(func() {
		switch {
		case f.loading:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); muted(ui.Text(c, "正在读取动作的说明…")) })
		case f.running:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); ui.Text(c, "正在执行「"+f.item.ActionName+"」…").FontSize(13) })
			for i, p := range f.progress {
				muted(ui.Text(c.Key(i), p))
			}
		case f.err != nil:
			a.fixError(c, row, f)
		case f.result != nil:
			fixResult(c, f)
		}
	})
}

// fixResult tells the command's exit and the service afterwards apart.
func fixResult(c *ui.Context, f *fixState) {
	r := f.result
	if r.Operation != nil {
		ui.Text(c, "动作已开始，尚未结束；可在命令行用 valise action status 查看").FontSize(13).Wrap()
		return
	}
	exit := fmt.Sprintf("命令已退出（退出码 %d）", r.Exit)
	ui.Text(c, exit).FontSize(13).Bold()
	if r.Checked {
		s, tone := serviceText(r.Service)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(tone))
			ui.Text(c, "之后读到："+s).FontSize(13)
		})
	}
	if v := r.Verification; v != nil {
		muted(ui.Text(c, v.Text)).FontSize(12).Wrap()
	}
}

// fixError explains a failure; a server-password admin login cannot be
// passed through the desktop yet (askpass, docs/UI.md 7.3).
func (a *App) fixError(c *ui.Context, row *agentRow, f *fixState) {
	if ue, ok := valise.As(f.err); ok && ue.Code == "admin_needs_password" {
		ui.Text(c, "桌面版暂时不能代为输入服务器密码").Bold().TextColor(palette.Warn)
		ui.Text(c, "这台服务器没有管理钥匙，管理登录要用服务器密码，而 OpenSSH 的密码提示还不能转交到这个窗口。请在终端里执行同一步：").FontSize(13).Wrap()
		ui.Text(c, "valise action "+row.Server+" "+row.Instance+" "+f.item.ActionID).FontSize(12).Font(cur.Fonts.Mono).Selectable()
		muted(ui.Text(c, "也可以先创建管理钥匙（valise server admin-key "+row.Server+"），之后在这里输入管理口令即可。")).Wrap()
		return
	}
	errorBox(c, f.err, a.fold("fix-err-"+row.Server+row.Instance))
}
