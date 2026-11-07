package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// Fixes built into Valise for linger and the server clock follow the B02
// repair flow: f 修复 · 动作名 → confirmation → result. The confirmation
// lists every command and the system setting it changes; the result keeps
// each exit code apart from the re-check. A sudo fix runs through the CLI
// in the terminal, where the user turns advanced mode on and types sudo's
// password (docs/INSTALL.md 4.1); Valise never turns it on by itself.

func (c *healthTab) planSystemFix(m *Model, p *instancePage, id string) tea.Cmd {
	c.repairSeq++
	seq := c.repairSeq
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	m.flash, m.flashErr = "正在核对服务器状态…", false
	return func() tea.Msg {
		f, err := svc.PlanSystemFix(ctx, host, inst, id, service.SystemFixOptions{})
		return repairReady{page: p, tab: c, seq: seq, actionID: id, fix: f, err: err}
	}
}

func (m *Model) readySystemFix(p *instancePage, f *service.SystemFix) tea.Cmd {
	if f.Mode == service.FixManual {
		m.flash, m.flashErr = "Valise 不能代为执行："+f.Why+"。请自己在服务器上执行："+commandList(f.Manual), true
		return nil
	}
	return m.push(&systemFixConfirm{host: p.host, inst: p.inst.InstanceID, fix: f})
}

func commandList(cmds [][]string) string {
	var out []string
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return strings.Join(out, "；")
}

// systemFixConfirm shows the commands and changes; enter is the second,
// explicit consent.
type systemFixConfirm struct {
	host, inst string
	fix        *service.SystemFix
}

func (c *systemFixConfirm) title() string    { return c.fix.Name }
func (c *systemFixConfirm) hostName() string { return c.host }
func (c *systemFixConfirm) help() string     { return menu("enter", "执行", "esc", "取消") }
func (c *systemFixConfirm) init(m *Model) tea.Cmd {
	return nil
}

// viaTerminal: sudo needs advanced mode, which this process never has on.
func (c *systemFixConfirm) viaTerminal() bool {
	return c.fix.Mode == service.FixAdvanced
}

func (c *systemFixConfirm) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "esc":
		return nil, false
	case "enter":
		m.pop()
		f := c.fix
		if c.viaTerminal() {
			return m.cliThen(m.refreshInstance(c.host, c.inst), false,
				"health", "fix", c.host, c.inst, f.ID, "--advanced", "--yes", "--digest", f.Digest), true
		}
		return m.push(&systemFixResult{host: c.host, inst: c.inst, fix: f, running: true}), true
	}
	return nil, true
}

func (c *systemFixConfirm) view(m *Model) string {
	w := min(m.width-2, 76)
	f := c.fix
	rows := []string{headStyle.Render(f.Name) + "    " + warnStyle.Render("系统设置"), "",
		"会发生什么：" + f.Goal, "影响：" + f.Impact, "", "将执行的命令与改变的系统设置："}
	for i, st := range f.Steps {
		rows = append(rows, "  "+itoa(i+1)+". "+strings.Join(st.Command, " "))
		if st.When != "" {
			rows = append(rows, "     "+dimStyle.Render(st.When))
		}
		rows = append(rows, "     改变："+st.Change, "     撤销："+st.Undo)
	}
	rows = append(rows, "", dimStyle.Render(f.Access))
	if c.viaTerminal() {
		rows = append(rows, warnStyle.Render("需要管理员权限：按 enter 后在终端里由你开启高级模式（Valise 口令、管理口令与确认句），sudo 密码也在终端里输入；Valise 不读取、不保存。"))
	}
	rows = append(rows, "", "确认上面的命令与改动后按 enter 执行；esc 取消，什么都不会改。")
	return box("", strings.Join(rows, "\n"), w)
}

// systemFixResult runs a confirmed direct fix and shows the outcome.
type systemFixResult struct {
	host, inst string
	fix        *service.SystemFix
	running    bool
	res        *service.SystemFixResult
	err        error
}

type systemFixDone struct {
	page *systemFixResult
	res  *service.SystemFixResult
	err  error
}

func (r *systemFixResult) title() string    { return r.fix.Name }
func (r *systemFixResult) hostName() string { return r.host }
func (r *systemFixResult) help() string     { return "" }
func (r *systemFixResult) busy() string {
	if r.running {
		return "修复正在执行，等它结束"
	}
	return ""
}

func (r *systemFixResult) init(m *Model) tea.Cmd {
	svc, ctx, f := m.svc, m.ctx, r.fix
	return func() tea.Msg {
		res, err := svc.RunSystemFix(ctx, f.Host, f.Instance, f.ID, f.Digest, service.SystemFixOptions{}, service.InstallIO{})
		return systemFixDone{r, res, err}
	}
}

func (r *systemFixResult) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case systemFixDone:
		if msg.page != r {
			return nil, true
		}
		r.running, r.res, r.err = false, msg.res, msg.err
		return m.refreshInstance(r.host, r.inst), true
	case tea.KeyPressMsg:
		if msg.String() == "esc" && r.running {
			return func() tea.Msg { return flashMsg("修复正在执行，等它结束") }, true
		}
	}
	return nil, false
}

func (r *systemFixResult) view(m *Model) string {
	head := headStyle.Render(r.fix.Name) + "  " + warnStyle.Render("系统设置")
	if r.running {
		return head + "\n\n" + dimStyle.Render("正在执行…")
	}
	lines := []string{head, ""}
	if r.err != nil {
		lines = append(lines, errStyle.Render(errText(r.err)))
	}
	if r.res == nil {
		return strings.Join(lines, "\n")
	}
	lines = append(lines, systemFixLines(r.res)...)
	return strings.Join(lines, "\n")
}

// systemFixLines: each command's exit code, then the re-check.
func systemFixLines(res *service.SystemFixResult) []string {
	var lines []string
	for _, st := range res.Steps {
		cmd := strings.Join(st.Command, " ")
		if st.Ran {
			lines = append(lines, "  "+mark(st.Exit == 0, true)+" 命令："+cmd+" · 退出码 "+itoa(st.Exit))
		} else {
			lines = append(lines, "  "+mark(false, false)+" 未执行："+cmd+"（"+st.Note+"）")
		}
	}
	if v := res.Verification; v != nil {
		lines = append(lines, "  "+mark(v.State == "resolved", v.State != "unknown")+" 该问题复核结果："+v.Text)
	}
	if res.Next != "" {
		lines = append(lines, "", "下一步："+res.Next)
	}
	if !res.Audited {
		lines = append(lines, "", warnStyle.Render("审计记录没有写入服务器（连接问题）；结果以上面为准。"))
	}
	return lines
}
