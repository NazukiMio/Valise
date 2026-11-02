package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"valise/core/agentkit"
	"valise/core/profile"
	"valise/ui/service"
)

// healthTab answers two questions (docs/UI.md 9.5): do the escape pods
// really work, and is the agent all right. One line per item, problems
// first, one key each; the selected item explains itself below. Checkup
// findings join the agent group; the agent's classification proposals
// stay at the bottom.
type healthTab struct {
	proposals []agentkit.Proposal
	loadedP   bool
	cursor    int // over rows(), then proposals
	nrows     int // len(rows()) at the last view, for help()

	testing bool
	prog    progressBox
}

type proposalsLoaded struct {
	list []agentkit.Proposal
	err  error
}

type healthLoaded struct {
	r   *service.HealthReport
	err error
}

type restoreTested struct {
	r   *service.RestoreTest
	err error
}

type testProgress struct{}

func (c *healthTab) name() string { return "健康" }
func (c *healthTab) help(p *instancePage) string {
	items := []string{"↑↓", "选择", "t", "测试恢复", "u", "同步", "i", "忽略", "y", "复制给 agent 的话", "r", "刷新"}
	if len(c.proposals) > 0 && c.cursor >= c.nrows {
		items = append(items, "a", "接受", "x", "拒绝")
	}
	return menu(items...)
}

func (c *healthTab) load(m *Model, p *instancePage) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return tea.Batch(
		func() tea.Msg { l, err := svc.Proposals(ctx, host, inst); return proposalsLoaded{l, err} },
		p.loadHealth(m),
	)
}

// rows are the health items with the checkup findings added to the agent
// group, problems first.
func (c *healthTab) rows(m *Model, p *instancePage) []service.HealthItem {
	if p.health == nil {
		return nil
	}
	r := service.HealthReport{Items: append([]service.HealthItem{}, p.health.Items...)}
	if ck := p.check; ck != nil {
		for _, f := range ck.Findings {
			info := findingInfo[f.ID]
			r.Items = append(r.Items, service.HealthItem{ID: "finding:" + f.ID, Group: service.HealthAgent, Level: service.HealthWarn,
				Title: "会话与记忆", Text: info.title + detailNumber(f), Key: "y"})
		}
	}
	m.svc.MarkDismissed(p.host, p.inst.InstanceID, r.Items)
	r.Sort()
	return r.Items
}

func detailNumber(f agentkit.Finding) string {
	if f.Detail == "" {
		return ""
	}
	return "：" + f.Detail
}

func (c *healthTab) onProposal(m *Model, p *instancePage) bool {
	return len(c.proposals) > 0 && c.cursor >= len(c.rows(m, p))
}

func (c *healthTab) decide(m *Model, p *instancePage, d agentkit.DecideParams) tea.Cmd {
	d.InstanceID = p.inst.InstanceID
	svc, ctx, host, send := m.svc, m.ctx, p.host, m.send
	return func() tea.Msg {
		res, err := svc.DecideProposals(ctx, host, d)
		if err != nil {
			return failed(err)
		}
		send(flashMsg(fmt.Sprintf("已接受 %d 条、拒绝 %d 条；接受的规则从下一次建立逃生舱起生效", len(res.Accepted), len(res.Rejected))))
		l, err := svc.Proposals(ctx, host, d.InstanceID)
		return proposalsLoaded{l, err}
	}
}

// runTest starts a restore test of the newest escape pod.
func (c *healthTab) runTest(m *Model, p *instancePage) tea.Cmd {
	if c.testing {
		return nil
	}
	c.testing = true
	c.prog = progressBox{}
	svc, host, inst, send := m.svc, p.host, p.inst.InstanceID, m.send
	ctx := service.WithProgress(m.ctx, func(pr service.Progress) { c.prog.set(pr); send(testProgress{}) })
	return func() tea.Msg { r, err := svc.RunRestoreTest(ctx, host, inst, ""); return restoreTested{r, err} }
}

func (c *healthTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case proposalsLoaded:
		c.loadedP = true
		if msg.err == nil {
			c.proposals = msg.list
		}
		return nil, true
	case testProgress:
		return nil, true
	case restoreTested:
		c.testing = false
		switch {
		case msg.err != nil:
			m.flash, m.flashErr = errText(msg.err), true
		case msg.r.OK:
			m.flash, m.flashErr = "恢复测试通过：两边都解开并核对了 "+itoa(msg.r.Files)+" 个文件，没有切换，临时文件已删除", false
		default:
			m.flash, m.flashErr = "恢复测试没有通过："+msg.r.Problem, true
		}
		return c.load(m, p), true
	case tea.KeyPressMsg:
		k := msg.String()
		rows := c.rows(m, p)
		if cur, moved := moveList(c.cursor, len(rows)+len(c.proposals), k); moved {
			c.cursor = cur
			return nil, true
		}
		switch k {
		case "t":
			return c.runTest(m, p), true
		case "u":
			return startSync(m, p.host), true
		case "k":
			return m.push(newKitTools(p.host)), true
		case "y":
			m.flash, m.flashErr = "已复制到剪贴板（终端支持时）："+agentAsk, false
			return tea.SetClipboard(agentAsk), true
		case "i":
			if c.cursor >= len(rows) {
				return nil, true
			}
			it := rows[c.cursor]
			on, err := m.svc.ToggleDismiss(p.host, p.inst.InstanceID, it)
			for i, r := range c.rows(m, p) { // the cursor follows the item
				if r.ID == it.ID {
					c.cursor = i
				}
			}
			switch {
			case err != nil:
				m.flash, m.flashErr = errText(err), true
			case it.Level != service.HealthWarn && it.Level != service.HealthBad:
				m.flash, m.flashErr = "只有要留意的项可以忽略", false
			case on && it.Level == service.HealthBad:
				m.flash, m.flashErr = "主页不再提醒「"+it.Title+"」，"+itoa(m.svc.RedDismissDays())+" 天后恢复；红色的项在健康页里照常显示（时长可在 设置 › 高级 里改）", false
			case on:
				m.flash, m.flashErr = "已忽略「"+it.Title+"」；再按 i 恢复提醒", false
			default:
				m.flash, m.flashErr = "已恢复提醒「"+it.Title+"」", false
			}
			return nil, true
		case "r":
			m.flash, m.flashErr = "正在刷新…", false
			return tea.Batch(c.load(m, p), p.loadCheckup(m)), true
		}
		if !c.onProposal(m, p) {
			return nil, false
		}
		pr := c.proposals[c.cursor-len(rows)]
		switch k {
		case "a":
			if pr.Demotion {
				return m.push(confirm("接受这条降级建议？", []string{
					pr.Rule.Root + ":" + pr.Rule.Glob + " → " + categoryName(pr.Rule.Category),
					"接受后这些文件不再装进轻装、标准、完整逃生舱（原样逃生舱仍包含它们）。",
				}, func(m *Model) tea.Cmd { return c.decide(m, p, agentkit.DecideParams{AcceptDemotions: []string{pr.ID}}) })), true
			}
			return c.decide(m, p, agentkit.DecideParams{Accept: []string{pr.ID}}), true
		case "x":
			return c.decide(m, p, agentkit.DecideParams{Reject: []string{pr.ID}}), true
		}
	}
	return nil, false
}

// topHealth is the n most important problems, bad first.
func topHealth(m *Model, p *instancePage, n int) []service.HealthItem {
	var out []service.HealthItem
	for _, it := range (&healthTab{}).rows(m, p) {
		if (it.Level == service.HealthBad || it.Level == service.HealthWarn) && !it.Quiet() {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return service.Rank(out[i].Level) < service.Rank(out[j].Level) })
	return out[:min(n, len(out))]
}

func levelMarkPlain(level string) string {
	if level == service.HealthBad {
		return "✗"
	}
	return "!"
}

func levelMark(level string) string {
	switch level {
	case service.HealthBad:
		return errStyle.Render("✗")
	case service.HealthWarn:
		return warnStyle.Render("!")
	case service.HealthOK:
		return okStyle.Render("✓")
	}
	return dimStyle.Render("·")
}

func (c *healthTab) view(m *Model, p *instancePage, height int) string {
	w := min(m.width-2, 100)
	if p.health == nil {
		if p.healthErr != nil {
			return errStyle.Render(errText(p.healthErr))
		}
		return dimStyle.Render("读取中…（只读，不会改动 agent 的任何文件）")
	}
	rows := c.rows(m, p)
	c.nrows = len(rows)
	c.cursor = min(c.cursor, max(0, len(rows)+len(c.proposals)-1))
	problems := 0
	for _, it := range rows {
		if it.Counts() {
			problems++
		}
	}
	var b strings.Builder
	if problems == 0 {
		b.WriteString(okStyle.Render("✓ 一切正常") + "\n")
	} else {
		b.WriteString(warnStyle.Render("! "+itoa(problems)+" 件事要留意") + "\n")
	}
	group := ""
	for i, it := range rows {
		if it.Group != group {
			group = it.Group
			title := "逃生舱状态"
			if group == service.HealthAgent {
				title = "agent 健康"
			}
			b.WriteString("\n" + dimStyle.Render(title) + "\n")
		}
		text := it.Text
		if it.ID == "restore_test" && c.testing {
			pr := c.prog.get()
			text = spin(m.now()) + " 正在测试：" + testStage(pr.Stage)
			if pr.Total > 0 {
				text += "  " + size(min(pr.Done, pr.Total)) + " / " + size(pr.Total)
			}
		}
		mk := levelMark(it.Level)
		switch {
		case it.Dismissed == nil:
		case it.Dismissed.IsZero():
			mk, text = dimStyle.Render("!"), dimStyle.Render(text+"（已忽略；按 i 恢复提醒）")
		default:
			days := itoa(max(1, int(time.Until(*it.Dismissed).Hours()/24+0.5)))
			text += dimStyle.Render("（主页不再提醒，" + days + " 天后恢复）")
		}
		line := mk + " " + padRight(it.Title, 14) + text
		if it.Key != "" && i == c.cursor {
			line += "  " + keyStyle.Render(it.Key)
		}
		if i == c.cursor {
			p.setFocus(strings.Count(b.String(), "\n"), strings.Count(b.String(), "\n"))
		}
		b.WriteString(cursorLine(i == c.cursor, line) + "\n")
	}
	if c.cursor < len(rows) {
		if d := c.detail(p, rows[c.cursor]); d != "" {
			b.WriteString("\n" + box(rows[c.cursor].Title, d, w) + "\n")
		}
	}
	if c.loadedP && len(c.proposals) > 0 {
		at := strings.Count(b.String(), "\n") + 1
		pv, from, to := c.proposalsView(w, c.cursor-len(rows))
		if from >= 0 {
			p.setFocus(at+from, at+to)
		}
		b.WriteString("\n" + pv)
	}
	return b.String()
}

func testStage(stage string) string {
	switch stage {
	case service.TestLocal:
		return "在这台电脑上解开并核对"
	case service.StageStage:
		return "在服务器上解开"
	case service.StageVerify:
		return "服务器核对文件"
	}
	return "准备中"
}

// detail explains the selected item: its own text, or a finding's why and
// what to do.
func (c *healthTab) detail(p *instancePage, it service.HealthItem) string {
	id, ok := strings.CutPrefix(it.ID, "finding:")
	if !ok {
		return it.Detail
	}
	info := findingInfo[id]
	var th *profile.Thresholds
	harness := info.generic
	if p.check != nil {
		if bars := findingBars(p.check, id, th); bars != "" {
			return info.why + "\n" + bars + "\n" + dimStyle.Render("处理：") + harness + "；或按 y 复制给 agent 的话。"
		}
	}
	return info.why + "\n" + dimStyle.Render("处理：") + harness + "；或按 y 复制给 agent 的话。"
}

// proposalsView lists the agent's classification proposals; sel is the
// selected one (negative for none). It also returns the selected lines.
func (c *healthTab) proposalsView(w, sel int) (string, int, int) {
	var b strings.Builder
	from, to := -1, -1
	b.WriteString(dimStyle.Render("agent 认为这些文件也该装进逃生舱，或应该换一类：") + "\n")
	for i, pr := range c.proposals {
		if i == sel {
			from = strings.Count(b.String(), "\n") + 1
		}
		line := pr.Rule.Root + ":" + pr.Rule.Glob + " → " + categoryName(pr.Rule.Category)
		if pr.Demotion {
			line += warnStyle.Render("  降级：不再进轻装逃生舱，需要单独确认")
		}
		b.WriteString(cursorLine(i == sel, line) + "\n")
		b.WriteString(dimStyle.Render("    理由（agent 提供）："+pr.Reason) + "\n")
		if pr.Unclassified > 0 {
			b.WriteString(warnStyle.Render(fmt.Sprintf("    其中 %d 个文件现在没有归类（按凭据对待），接受后会进入轻装逃生舱；请确认里面没有密钥", pr.Unclassified)) + "\n")
		}
		if i == sel {
			to = strings.Count(b.String(), "\n")
		}
	}
	return box("归档建议", strings.TrimRight(b.String(), "\n"), w), from, to
}
