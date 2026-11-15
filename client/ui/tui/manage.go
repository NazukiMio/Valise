package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/profile"
	"valise/ui/service"
)

// manageTab: the management page, local pages, login, managed actions
// grouped by risk, and the native CLI (docs/UI.md 9.5 "管理",
// docs/INSTALL.md section 5).
type manageTab struct {
	cursor  int
	showCmd bool
	actions *service.ActionList
	loadErr error
}

// manageRow is one line of the tab; rows without do are group headers.
type manageRow struct {
	group, label, note string
	cmd                []string // shown with v
	do                 func(m *Model, p *instancePage) tea.Cmd
}

type actionsLoaded struct {
	list *service.ActionList
	err  error
}

func (t *manageTab) name() string { return "管理" }
func (t *manageTab) help(p *instancePage) string {
	return menu("↑↓", "选择", "enter", "执行", "v", "查看实际命令")
}

func (t *manageTab) load(m *Model, p *instancePage) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { l, err := svc.Actions(ctx, host, inst); return actionsLoaded{l, err} }
}

func (t *manageTab) rows(m *Model, p *instancePage) []manageRow {
	var rows []manageRow
	host, inst := p.host, p.inst.InstanceID
	var prof *profile.Profile
	if p.target != nil {
		prof = &p.target.Profile
	}
	if prof != nil && len(prof.Entries) > 0 {
		rows = append(rows, manageRow{group: "管理页", label: "打开管理页", note: entryState(p.inst),
			do: func(m *Model, p *instancePage) tea.Cmd { return m.runCLI("open", host, inst) }})
	}
	rows = append(rows, manageRow{group: grp(rows, "管理页"), label: "打开服务器上的本地页面…", note: "粘贴地址或填端口",
		do: func(m *Model, p *instancePage) tea.Cmd {
			return m.push(newInput("服务器上的页面地址或端口（例如 http://127.0.0.1:8080/ 或 8080）", "", false,
				func(m *Model, v string) tea.Cmd { m.pop(); return m.runCLI("forward", host, strings.TrimSpace(v)) }))
		}})
	if prof != nil && len(prof.Login) > 0 {
		rows = append(rows, manageRow{group: "登录授权", label: "选择登录方式…", note: itoa(len(prof.Login)) + " 种",
			do: func(m *Model, p *instancePage) tea.Cmd { return loginPicker(m, p) }})
	}
	if t.actions != nil {
		for _, risk := range []string{profile.RiskRead, profile.RiskManage, profile.RiskConfig, profile.RiskDanger} {
			first := true
			for _, a := range t.actions.Actions {
				if a.Risk != risk {
					continue
				}
				a := a
				g := ""
				if first {
					g, first = "动作 · "+service.RiskLabels[risk], false
				}
				rows = append(rows, manageRow{group: g, label: a.Name, note: m.svc.ActionAccess(p.host, a), cmd: a.Argv,
					do: func(m *Model, p *instancePage) tea.Cmd { return startAction(m, p, a) }})
			}
		}
	}
	if prof != nil && len(prof.NativeCLI) > 0 {
		pid := prof.ID
		if p.inst.NativeCLI {
			rows = append(rows, manageRow{group: "原生", label: "原生界面（" + strings.Join(prof.NativeCLI, " ") + "）", note: "独立的管理连接",
				do: func(m *Model, p *instancePage) tea.Cmd {
					return gatedCLI(m, host, "native", host, "--instance", p.inst.InstanceID)
				}})
		}
		rows = append(rows, manageRow{label: "打开这台服务器的终端", note: pid + " 的命令可以直接用",
			do: func(m *Model, p *instancePage) tea.Cmd { return gatedCLI(m, host, "shell", host, "--profile", pid) }})
	} else {
		rows = append(rows, manageRow{group: "原生", label: "打开这台服务器的终端", note: "独立的管理连接",
			do: func(m *Model, p *instancePage) tea.Cmd { return gatedCLI(m, host, "shell", host) }})
	}
	return rows
}

// grp returns g unless the previous row already belongs to that group.
func grp(rows []manageRow, g string) string {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].group != "" {
			if rows[i].group == g {
				return ""
			}
			break
		}
	}
	return g
}

func (t *manageTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case actionsLoaded:
		t.actions, t.loadErr = msg.list, msg.err
		return nil, true
	case tea.KeyPressMsg:
		rows := t.rows(m, p)
		if c, ok := moveList(t.cursor, len(rows), msg.String()); ok {
			t.cursor = c
			return nil, true
		}
		switch msg.String() {
		case "v":
			t.showCmd = !t.showCmd
			return nil, true
		case "enter":
			if t.cursor < len(rows) && rows[t.cursor].do != nil {
				return rows[t.cursor].do(m, p), true
			}
		}
	}
	return nil, false
}

func (t *manageTab) view(m *Model, p *instancePage, height int) string {
	if p.target == nil {
		return dimStyle.Render("读取中…")
	}
	rows := t.rows(m, p)
	t.cursor = min(t.cursor, len(rows)-1)
	var lines []string
	cur := 0
	for i, r := range rows {
		if r.group != "" {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, dimStyle.Render(r.group))
		}
		if i == t.cursor {
			cur = len(lines)
		}
		label := r.label
		pad := max(2, 32-lipgloss.Width(label))
		lines = append(lines, cursorLine(i == t.cursor, label+strings.Repeat(" ", pad)+dimStyle.Render(r.note)))
		if t.showCmd && len(r.cmd) > 0 {
			lines = append(lines, faintStyle.Render("    实际命令："+strings.Join(r.cmd, " ")))
		}
	}
	if t.loadErr != nil {
		lines = append(lines, "", warnStyle.Render("管理动作读不出来："+firstLine(errText(t.loadErr))))
	} else if t.actions != nil && len(t.actions.Actions) == 0 {
		lines = append(lines, "", dimStyle.Render("这个 harness 的 harness 资料没有声明管理动作。"))
	} else if t.actions != nil && t.actions.Stale {
		lines = append(lines, "", warnStyle.Render("服务器上保存的 harness 资料是旧版本；新增的动作要重新接入后才能用（设置 › 重新接入）。"))
	}
	return scrollLinesHint(lines, cur, height, "还有更多")
}
