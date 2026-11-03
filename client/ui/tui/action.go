package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/profile"
	"valise/ui/service"
)

// Managed actions (docs/INSTALL.md section 5): read actions run at once;
// the others are confirmed first, config and danger take an escape pod
// unless unticked, danger needs its name typed. Terminal actions run in
// the terminal while the TUI is paused.

// startAction runs or confirms one action.
func startAction(m *Model, p *instancePage, a profile.Action) tea.Cmd {
	if a.Risk == profile.RiskRead && !a.Terminal {
		return m.push(newActionResult(m, p.host, p.inst.InstanceID, a, false))
	}
	if a.Risk == profile.RiskConfig || a.Risk == profile.RiskDanger {
		host, inst := p.host, p.inst.InstanceID
		return gateThen(m, host, inst, func(m *Model) tea.Cmd { return m.push(newActionConfirm(host, inst, a)) })
	}
	return m.push(newActionConfirm(p.host, p.inst.InstanceID, a))
}

// actionConfirm is the confirmation of a manage, config or danger action.
type actionConfirm struct {
	host, inst string
	a          profile.Action
	seal       bool
	name       textinput.Model
}

func newActionConfirm(host, inst string, a profile.Action) *actionConfirm {
	c := &actionConfirm{host: host, inst: inst, a: a, seal: service.SealsByDefault(a)}
	if a.Risk == profile.RiskDanger {
		c.name = textinput.New()
		c.name.Placeholder = a.Name
		c.name.Focus()
	}
	return c
}

func (c *actionConfirm) title() string    { return c.a.Name }
func (c *actionConfirm) hostName() string { return c.host }
func (c *actionConfirm) typing() bool     { return c.a.Risk == profile.RiskDanger }
func (c *actionConfirm) help() string {
	if service.SealsByDefault(c.a) && c.a.Risk != profile.RiskDanger {
		return menu("enter", "执行", "space", "逃生舱 开/关")
	}
	return menu("enter", "执行")
}
func (c *actionConfirm) init(m *Model) tea.Cmd { return nil }

func (c *actionConfirm) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "esc":
		return nil, false
	case "space":
		if service.SealsByDefault(c.a) && c.a.Risk != profile.RiskDanger {
			c.seal = !c.seal
			return nil, true
		}
	case "enter":
		if c.a.Risk == profile.RiskDanger && strings.TrimSpace(c.name.Value()) != c.a.Name {
			return func() tea.Msg { return flashMsg("名称不一致；输入「" + c.a.Name + "」才能执行") }, true
		}
		m.pop()
		if c.a.Terminal {
			args := []string{"action", c.host, c.inst, c.a.ID, "--yes"}
			if !c.seal {
				args = append(args, "--no-seal")
			}
			return m.runCLI(args...), true
		}
		return m.push(newActionResult(m, c.host, c.inst, c.a, c.seal)), true
	}
	if c.a.Risk == profile.RiskDanger {
		var cmd tea.Cmd
		c.name, cmd = c.name.Update(msg)
		return cmd, true
	}
	return nil, true
}

func (c *actionConfirm) view(m *Model) string {
	w := min(m.width-2, 76)
	rows := []string{headStyle.Render(c.a.Name) + "    " + riskStyle(c.a.Risk).Render(service.RiskLabels[c.a.Risk]), "",
		"会发生什么：" + c.a.Explain}
	if c.a.Undo != "" {
		rows = append(rows, "能否撤销："+c.a.Undo)
	}
	rows = append(rows, "", dimStyle.Render("实际命令："+strings.Join(c.a.Argv, " ")), dimStyle.Render(m.svc.ActionAccess(c.host, c.a)))
	if c.a.Terminal {
		rows = append(rows, dimStyle.Render("会在终端里运行；终端里的内容 Valise 不读取。"))
	}
	if service.SealsByDefault(c.a) {
		box := "[x] 执行前先建立逃生舱（推荐）"
		if !c.seal {
			box = warnStyle.Render("[ ] 不建逃生舱：出了问题时没有改动之前的状态可以着陆")
		}
		rows = append(rows, "", box)
	}
	if c.a.Risk == profile.RiskDanger {
		rows = append(rows, "", warnStyle.Render("这是高风险动作。输入动作名称「"+c.a.Name+"」后按回车执行："), c.name.View())
	}
	return box("", strings.Join(rows, "\n"), w)
}

func riskStyle(risk string) lipgloss.Style {
	switch risk {
	case profile.RiskConfig, profile.RiskDanger:
		return warnStyle
	case profile.RiskManage:
		return infoStyle
	}
	return dimStyle
}

// actionResult runs an action and shows the outcome: exit code and the
// instance's state apart, text output, or a secret on the clipboard.
type actionResult struct {
	host, inst string
	a          profile.Action
	seal       bool
	running    bool
	res        *service.ActionResult
	err        error
	clip       *service.SecretClip
	copiedAt   time.Time
	cleared    bool
	scroll     int
}

type actionDone struct {
	res *service.ActionResult
	err error
}

type clipTick struct{}

func newActionResult(m *Model, host, inst string, a profile.Action, seal bool) *actionResult {
	return &actionResult{host: host, inst: inst, a: a, seal: seal, running: true}
}

func (r *actionResult) title() string    { return r.a.Name }
func (r *actionResult) hostName() string { return r.host }
func (r *actionResult) busy() string {
	if r.running {
		return "动作正在执行，等它结束"
	}
	return ""
}
func (r *actionResult) help() string {
	if r.clip != nil && !r.cleared {
		return menu("c", "立即清除剪贴板")
	}
	return menu("↑↓", "滚动输出")
}

func (r *actionResult) init(m *Model) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	host, inst, id, seal := r.host, r.inst, r.a.ID, r.seal
	return func() tea.Msg {
		res, err := svc.RunAction(ctx, host, inst, id, service.ActionOptions{Seal: seal})
		return actionDone{res, err}
	}
}

func (r *actionResult) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case actionDone:
		r.running, r.res, r.err = false, msg.res, msg.err
		if r.err == nil && r.a.Output == profile.OutputSecret && r.res.Exit == 0 {
			clip, err := service.CopySecret(m.ctx, r.res.Secret)
			if err != nil {
				r.err = err
				return nil, true
			}
			r.clip, r.copiedAt = clip, m.now()
			r.res.Secret = ""
			return tickClip(), true
		}
		return m.loadServer(r.host), true
	case clipTick:
		if r.clip == nil || r.cleared {
			return nil, true
		}
		select {
		case <-r.clip.Done():
			r.cleared = true
			return nil, true
		default:
		}
		return tickClip(), true
	case tea.KeyPressMsg:
		switch msg.String() {
		case "c":
			if r.clip != nil && !r.cleared {
				r.clip.Clear()
				r.cleared = true
				return nil, true
			}
		case "esc":
			if r.running {
				return func() tea.Msg { return flashMsg("动作正在执行，等它结束") }, true
			}
			if r.clip != nil && !r.cleared {
				r.clip.Clear()
			}
			return nil, false
		case "up", "k":
			r.scroll = max(0, r.scroll-1)
			return nil, true
		case "down", "j":
			r.scroll++
			return nil, true
		}
	}
	return nil, false
}

func tickClip() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return clipTick{} })
}

func (r *actionResult) view(m *Model) string {
	head := headStyle.Render(r.a.Name) + "  " + riskStyle(r.a.Risk).Render(service.RiskLabels[r.a.Risk])
	if r.running {
		step := "正在执行…"
		if r.seal {
			step = "正在建立逃生舱，然后执行…"
		}
		return head + "\n\n" + dimStyle.Render(step)
	}
	if r.err != nil {
		return head + "\n\n" + errStyle.Render(errText(r.err))
	}
	lines := []string{head, ""}
	if r.res.Snapshot != "" {
		lines = append(lines, "  "+okStyle.Render("✓")+" 逃生舱：已建立")
	}
	lines = append(lines, "  "+mark(r.res.Exit == 0, true)+" 命令：退出码 "+itoa(r.res.Exit))
	if r.res.Checked && len(r.res.Service) == 0 && len(r.res.Entries) == 0 {
		lines = append(lines, "  "+dimStyle.Render("· 这个 agent 没有声明服务与管理页，无法检查运行状态"))
	}
	if r.res.Checked {
		for _, sv := range r.res.Service {
			lines = append(lines, "  "+mark(sv.Active == "active", true)+" 服务："+sv.Unit+" "+map[bool]string{true: "运行中", false: sv.Active}[sv.Active == "active"])
		}
		for _, e := range r.res.Entries {
			lines = append(lines, "  "+mark(e.Listening, true)+" 管理页：端口 "+itoa(e.Port)+" "+map[bool]string{true: "在监听", false: "没有监听"}[e.Listening])
		}
	}
	if r.clip != nil {
		lines = append(lines, "", "  "+okStyle.Render("✓")+" 已复制到剪贴板（没有显示，也没有保存）")
		if r.cleared {
			lines = append(lines, "  剪贴板已清除。")
		} else {
			left := max(0, int((service.SecretWindow-m.now().Sub(r.copiedAt)).Seconds()+0.5))
			total := int(service.SecretWindow.Seconds())
			bar := okStyle.Render(strings.Repeat("█", left)) + faintStyle.Render(strings.Repeat("░", total-left))
			lines = append(lines, "  剪贴板将在 "+headStyle.Render(itoa(left)+" 秒")+" 后清除   "+bar)
		}
		lines = append(lines, "", dimStyle.Render("  注意：剪贴板管理工具可能会保留历史记录。"))
	}
	if r.a.Output == profile.OutputText && r.res.Output != "" {
		out := strings.Split(r.res.Output, "\n")
		note := "命令输出"
		if r.res.Truncated {
			note += "（只保留最后 200 行）"
		}
		lines = append(lines, "", dimStyle.Render(note))
		room := max(3, m.bodyHeight()-len(lines)-1)
		r.scroll = min(r.scroll, max(0, len(out)-room))
		for _, l := range out[r.scroll:min(len(out), r.scroll+room)] {
			lines = append(lines, "  "+l)
		}
	}
	return strings.Join(lines, "\n")
}
