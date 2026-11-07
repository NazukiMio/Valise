package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// isetTab is the instance settings (docs/UI.md 9.4 "设置").
type isetTab struct {
	cursor  int
	timer   *vaultrpc.TimerStatus
	plan    *service.KitPlan
	removal *service.KitRemovalPlan
}

const (
	isName = iota
	isKit
	isPeriod
	isTimer
	isRules
	isReenroll
	isCount
)

type timerLoaded struct {
	st  *vaultrpc.TimerStatus
	err error
}

type kitPlanned struct {
	install *service.KitPlan
	removal *service.KitRemovalPlan
	force   bool
	err     error
	done    bool
}

var skillStates = map[string]string{agentkit.SkillCurrent: "最新", agentkit.SkillModified: "被改过", agentkit.SkillMissing: "未安装"}

func (t *isetTab) name() string { return "设置" }
func (t *isetTab) help(p *instancePage) string {
	switch t.cursor {
	case isKit:
		return menu("↑↓", "选择", "i", "安装或更新技能", "u", "卸载", "F", "连同被改过的一起卸载")
	case isRules:
		return menu("↑↓", "选择", "enter", "查看全部规则")
	}
	return menu("↑↓", "选择", "enter", "修改")
}

func (t *isetTab) load(m *Model, p *instancePage) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { st, err := svc.TimerStatus(ctx, host, inst); return timerLoaded{st, err} }
}

func (t *isetTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	switch msg := msg.(type) {
	case timerLoaded:
		if msg.err == nil {
			t.timer = msg.st
		}
		return nil, true
	case kitPlanned:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		t.plan, t.removal = msg.install, msg.removal
		if msg.done {
			return func() tea.Msg { k, _ := svc.KitStatus(ctx, host, inst); return kitLoaded{k} }, true
		}
		return t.confirmKit(m, p, msg.force), true
	case tea.KeyPressMsg:
		k := msg.String()
		if c, moved := moveList(t.cursor, isCount, k); moved {
			t.cursor = c
			return nil, true
		}
		switch {
		case k == "i" && t.cursor == isKit:
			return func() tea.Msg {
				pl, err := svc.KitInstall(ctx, host, inst, true)
				return kitPlanned{install: pl, err: err}
			}, true
		case (k == "u" || k == "F") && t.cursor == isKit:
			force := k == "F"
			return func() tea.Msg {
				pl, err := svc.KitUninstall(ctx, host, inst, force, true)
				return kitPlanned{removal: pl, force: force, err: err}
			}, true
		case k == "enter" && t.cursor == isName:
			return askAgentName(m, p), true
		case k == "enter" && t.cursor == isPeriod:
			return t.choosePeriod(m, p), true
		case k == "enter" && t.cursor == isTimer:
			return t.chooseTimer(m, p), true
		case k == "enter" && t.cursor == isRules:
			return m.push(&rulesScreen{page: p}), true
		case k == "enter" && t.cursor == isReenroll:
			return m.push(confirm("重新接入 "+inst+"？", []string{"harness 升级、目录变化或换了 harness 资料版本后使用。会先在终端显示接入报告，你确认后才写入。"},
				func(m *Model) tea.Cmd {
					return m.cliThen(func() tea.Msg {
						return pushMsg{confirm("接受上面的接入报告？", nil, func(m *Model) tea.Cmd {
							return m.cliThen(m.loadServer(host), false, "enroll", host, "--profile", p.inst.ProfileID, "--id", inst, "--accept")
						})}
					}, false, "enroll", host, "--profile", p.inst.ProfileID, "--id", inst)
				})), true
		}
	}
	return nil, false
}

func (t *isetTab) confirmKit(m *Model, p *instancePage, force bool) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	switch {
	case t.plan != nil && t.plan.Result != nil && len(t.plan.Result.Written) > 0:
		detail := []string{"写到：" + t.plan.Result.Dir}
		for _, f := range t.plan.Result.Written {
			detail = append(detail, "  "+f)
		}
		for _, f := range t.plan.Result.Backups {
			detail = append(detail, dimStyle.Render("  （已有不同版本，先备份为 "+f+"）"))
		}
		return m.push(confirm("写入这些技能文件？", detail, func(m *Model) tea.Cmd {
			return func() tea.Msg {
				pl, err := svc.KitInstall(ctx, host, inst, false)
				return kitPlanned{install: pl, err: err, done: true}
			}
		}))
	case t.plan != nil && t.plan.NoSkills != "":
		return func() tea.Msg { return flashMsg(t.plan.NoSkills) }
	case t.removal != nil && len(t.removal.Result.Removed) > 0:
		detail := []string{"将删除："}
		for _, f := range t.removal.Result.Removed {
			detail = append(detail, "  "+f)
		}
		for _, f := range t.removal.Result.Kept {
			detail = append(detail, dimStyle.Render("  保留（你或 agent 改过它；需要时按 F）："+f))
		}
		return m.push(confirm("删除这些技能文件？", detail, func(m *Model) tea.Cmd {
			return func() tea.Msg {
				pl, err := svc.KitUninstall(ctx, host, inst, force, false)
				return kitPlanned{removal: pl, err: err, done: true}
			}
		}))
	}
	return func() tea.Msg { return flashMsg("技能已经是最新，没有需要写入或删除的文件") }
}

func (t *isetTab) choosePeriod(m *Model, p *instancePage) tea.Cmd {
	return m.push(pick("备份分组 · 修改从下一组生效", []choice{
		{label: "每周", value: vault.UnitWeek}, {label: "每月", value: vault.UnitMonth, desc: "默认"}, {label: "每季度", value: vault.UnitQuarter},
	}, func(m *Model, unit string) tea.Cmd {
		svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
		return func() tea.Msg {
			if err := svc.PeriodSet(ctx, host, inst, vault.PeriodSetting{Unit: unit, TZ: service.LocalZone()}); err != nil {
				return failed(err)
			}
			return flashMsg("已保存，备份分组从下一组开始生效（时区 " + service.LocalZone() + "）")
		}
	}))
}

func (t *isetTab) chooseTimer(m *Model, p *instancePage) tea.Cmd {
	return m.push(pick("定时备份（轻装）", []choice{
		{label: "关", value: "off"}, {label: "每 6 小时", value: "6h"}, {label: "每天", value: "1d"}, {label: "每周", value: "7d"},
	}, func(m *Model, every string) tea.Cmd {
		svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
		return func() tea.Msg {
			st, err := svc.TimerSet(ctx, host, vaultrpc.TimerSetParams{InstanceID: inst, Enabled: every != "off", Every: every})
			if err != nil {
				return failed(err)
			}
			return timerLoaded{st, nil}
		}
	}))
}

func (t *isetTab) view(m *Model, p *instancePage, height int) string {
	w := min(m.width-2, 100)
	sections := []struct{ title, body string }{
		{"名字", nameText(p.inst)},
		{"Agent 保护技能", t.kitText(p)},
		{"备份分组", t.periodText(m, p)},
		{"定时备份", t.timerText()},
		{"归类规则", t.rulesText(p)},
		{"重新接入", dimStyle.Render("harness 升级、目录变化后使用；会先显示接入报告。")},
	}
	var b strings.Builder
	for i, s := range sections {
		title := s.title
		if i == t.cursor {
			title = selStyle.Render("▸ " + title)
		}
		st := cardStyle(i == t.cursor, false, w)
		card := st.Render(title + "\n" + s.body)
		if i == t.cursor {
			n := strings.Count(b.String(), "\n")
			p.setFocus(n, n+lipgloss.Height(card)-1)
		}
		b.WriteString(card + "\n")
	}
	return b.String()
}

func (t *isetTab) kitText(p *instancePage) string {
	var b strings.Builder
	b.WriteString(dimStyle.Render("让 agent 自己知道什么时候该建立备份、怎么交接、怎么检查会话与记忆。") + "\n")
	switch k := p.kit; {
	case k == nil:
		b.WriteString(dimStyle.Render("读取中…"))
	case k.Dir == "":
		b.WriteString("这个 agent 的 harness 资料没有声明技能目录。")
	default:
		var parts []string
		for _, s := range k.Skills {
			parts = append(parts, s.Skill+" "+skillStates[s.State])
		}
		b.WriteString(strings.Join(parts, "  ·  "))
		if k.LastMCP != nil {
			b.WriteString("\n" + dimStyle.Render("agent 最近一次通过它联系 Valise："+k.LastMCP.Local().Format("2006-01-02 15:04")))
		}
	}
	if pl := t.plan; pl != nil && (len(pl.MCPCommand) > 0 || pl.MCPSnippet != "") {
		b.WriteString("\n\n接入方法（Valise 不改 harness 的配置，请你执行或转告 agent）：\n")
		if len(pl.MCPCommand) > 0 {
			b.WriteString("  " + accStyle.Render(strings.Join(pl.MCPCommand, " ")) + "\n")
		}
		for _, l := range strings.Split(strings.TrimRight(pl.MCPSnippet, "\n"), "\n") {
			if l != "" {
				b.WriteString("  " + l + "\n")
			}
		}
		b.WriteString(dimStyle.Render("  或转告 agent：" + pl.AgentLine))
	}
	if r := t.removal; r != nil && len(r.MCPCommand) > 0 {
		b.WriteString("\n\n撤除接入（请你执行）：" + accStyle.Render(strings.Join(r.MCPCommand, " ")))
	}
	return b.String()
}

func (t *isetTab) periodText(m *Model, p *instancePage) string {
	cur := "每月（默认）"
	if st := m.server(p.host).status; st != nil {
		for _, l := range st.Leaves {
			if l.Current && l.Period != nil && l.Period.InstanceID == p.inst.InstanceID {
				names := map[string]string{vault.UnitWeek: "每周", vault.UnitMonth: "每月", vault.UnitQuarter: "每季度"}
				cur = names[l.Period.Unit] + "　当前分组 " + l.Period.Start + " ~ " + l.Period.End
			}
		}
	}
	return cur + "\n" + dimStyle.Render("按月/周/季度分组保存；清理时整组删除。修改从下一组生效。")
}

func (t *isetTab) timerText() string {
	if t.timer == nil {
		return dimStyle.Render("读取中…")
	}
	if !t.timer.Enabled {
		return "关" + dimStyle.Render("　开启后会在服务器上写两个 systemd 用户单元，关闭时删除。")
	}
	s := okStyle.Render("每 "+t.timer.Every) + dimStyle.Render("　服务器上的单元："+strings.Join(t.timer.Units, "、"))
	if !t.timer.Linger {
		s += "\n" + warnStyle.Render("这台服务器没有为你的用户开启 linger：你没登录时定时器不会运行（管理员可执行 loginctl enable-linger）。")
	}
	return s
}

func (t *isetTab) rulesText(p *instancePage) string {
	s := "profile " + p.inst.ProfileID + " v" + itoa(p.inst.ProfileVersion)
	if p.inst.ProfileStatus != "verified" {
		s += warnStyle.Render("　未核实（可以建立备份；恢复时需要你确认）")
	}
	if p.target != nil && p.target.Record != nil {
		n := len(p.target.Record.LocalOverrides)
		s += "\n你补充的归类规则：" + itoa(n) + " 条"
		for _, o := range p.target.Record.LocalOverrides {
			s += "\n  " + o.Root + ":" + o.Glob + " → " + categoryName(o.Category)
		}
	}
	return s + "\n" + dimStyle.Render("按 enter 查看每一类文件进哪些备份，以及怎么修改。")
}

// nameText is the name section: the agent's name and where it comes from.
func nameText(in enroll.InstanceSummary) string {
	var b strings.Builder
	switch {
	case in.Label == "":
		b.WriteString(dimStyle.Render("没有名字，只显示实例名 " + in.InstanceID + "；按 enter 起一个"))
	default:
		b.WriteString(in.Label + dimStyle.Render("（实例名 "+in.InstanceID+" 不变；按 enter 修改）"))
	}
	if len(in.HarnessAgents) > 1 {
		b.WriteString("\n" + dimStyle.Render("这个目录里有 "+itoa(len(in.HarnessAgents))+" 个 agent："+strings.Join(in.HarnessAgents, "、")+"（一起备份与恢复）"))
	}
	return b.String()
}

// askAgentName sets the agent's name in Valise (an owner operation).
func askAgentName(m *Model, p *instancePage) tea.Cmd {
	host, inst := p.host, p.inst.InstanceID
	return m.push(newInput("在 Valise 里显示为（留空：跟随 harness 里的名字；只改显示，不改任何文件）", p.inst.Label, false, func(m *Model, v string) tea.Cmd {
		svc, ctx := m.svc, m.ctx
		v = strings.TrimSpace(v)
		return func() tea.Msg {
			if err := svc.SetAgentLabel(ctx, host, inst, v); err != nil {
				return failed(err)
			}
			return refreshed{host, "名字已保存"}
		}
	}))
}
