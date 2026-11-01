package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/agentkit"
	"valise/core/profile"
)

// Finding titles and plain explanations (docs/AGENT_KIT.md 5, 8.6).
var findingInfo = map[string]struct{ title, why, generic string }{
	agentkit.FindingLongSession: {"有一个会话太长了",
		"会话越长，agent 每次回复要读的上下文越多：回复变慢、花费变高，也更容易忘记早期的约定。",
		"开一个新会话；想保留上下文就先让 agent 做一次交接（逃生舱技能 valise-handoff）"},
	agentkit.FindingCostGrowth: {"花费连续几天在涨",
		"最近几天每天用掉的 token 越来越多，通常是会话越积越长，或 agent 在反复读大文件。",
		"看看最近的会话是否过长；需要时开新会话，并让 agent 交接"},
	agentkit.FindingMemoryBloat: {"记忆文件太长了",
		"记忆文件每次都会被读进上下文，太长会挤占对话空间，也会让重要的约定被淹没。",
		"让 agent 把记忆整理成要点，过时的内容归档到单独的文件"},
	agentkit.FindingSessionPileup: {"旧会话堆积",
		"很久没碰过的会话占了大部分空间；它们也会进入逃生舱，让每次建立更慢、更大。",
		"用 harness 自己的功能归档或清理很久不用的会话"},
	agentkit.FindingNoMemoryMechanism: {"没有长期记忆",
		"这个 agent 没有任何长期记忆文件或记忆插件，换会话后它会忘记你说过的偏好。",
		"建立一个记忆文件，或启用 harness 自带的记忆功能（见 harness 文档）"},
}

func findingTitle(id string) string {
	if f, ok := findingInfo[id]; ok {
		return f.title
	}
	return id
}

const agentAsk = "请用 valise-checkup 技能看一下体检结果，提出整理方案，等我确认后再做。"

// checkupTab renders the checkup and the archive suggestions (docs/UI.md 9.4).
type checkupTab struct {
	proposals []agentkit.Proposal
	loadedP   bool
	cursor    int
}

type proposalsLoaded struct {
	list []agentkit.Proposal
	err  error
}

func (c *checkupTab) name() string { return "体检" }
func (c *checkupTab) help(p *instancePage) string {
	items := []string{"c", "重新体检", "y", "复制给 agent 的话"}
	if len(c.proposals) > 0 {
		items = append(items, "↑↓", "选建议", "a", "接受", "x", "拒绝")
	}
	return menu(items...)
}

func (c *checkupTab) load(m *Model, p *instancePage) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { l, err := svc.Proposals(ctx, host, inst); return proposalsLoaded{l, err} }
}

func (c *checkupTab) decide(m *Model, p *instancePage, d agentkit.DecideParams) tea.Cmd {
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

func (c *checkupTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case proposalsLoaded:
		c.loadedP = true
		if msg.err == nil {
			c.proposals = msg.list
		}
		c.cursor = max(0, min(c.cursor, len(c.proposals)-1))
		return nil, true
	case tea.KeyPressMsg:
		k := msg.String()
		switch k {
		case "c":
			m.flash, m.flashErr = "正在体检…", false
			return p.loadCheckup(m), true
		case "y":
			m.flash, m.flashErr = "已复制到剪贴板（终端支持时）："+agentAsk, false
			return tea.SetClipboard(agentAsk), true
		}
		if len(c.proposals) == 0 {
			return nil, false
		}
		if cur, moved := moveList(c.cursor, len(c.proposals), k); moved {
			c.cursor = cur
			return nil, true
		}
		pr := c.proposals[c.cursor]
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

func (c *checkupTab) view(m *Model, p *instancePage, height int) string {
	w := min(m.width-2, 100)
	var b strings.Builder
	ck := p.check
	switch {
	case ck == nil:
		b.WriteString(dimStyle.Render("体检中…（只读，不会改动 agent 的任何文件）") + "\n")
	case len(ck.Findings) == 0:
		b.WriteString(box("", okStyle.Render("✓ 健康")+dimStyle.Render("　体检只读，不会改动 agent 的任何文件。上次体检："+ago(ck.At, m.now())), w) + "\n")
	default:
		b.WriteString(box("", warnStyle.Render("▲ "+itoa(len(ck.Findings))+" 个提醒")+dimStyle.Render("　体检只读，不会改动 agent 的任何文件。上次体检："+ago(ck.At, m.now())), w) + "\n")
		var th *profile.Thresholds
		var remedies map[string][]string
		if m.prof != nil && m.prof.ByID[p.inst.ProfileID] != nil {
			if cp := m.prof.ByID[p.inst.ProfileID].Effective.Checkup; cp != nil {
				th, remedies = cp.Thresholds, cp.Remedies
			}
		}
		for _, f := range ck.Findings {
			b.WriteString(findingCard(ck, f, th, remedies, p, w) + "\n")
		}
	}
	if ck != nil {
		b.WriteString(box("", okNotes(ck), w) + "\n")
	}
	at := strings.Count(b.String(), "\n")
	pv, from, to := c.proposalsView(w)
	if from >= 0 {
		p.setFocus(at+from, at+to)
	}
	b.WriteString(pv)
	return b.String()
}

func harnessName(p *instancePage) string {
	if p.target != nil && p.target.Profile.DisplayName != "" {
		return p.target.Profile.DisplayName
	}
	return p.inst.ProfileID
}

func findingCard(ck *agentkit.CheckupResult, f agentkit.Finding, th *profile.Thresholds, remedies map[string][]string, p *instancePage, w int) string {
	info := findingInfo[f.ID]
	var b strings.Builder
	b.WriteString(info.why + "\n")
	if bars := findingBars(ck, f.ID, th); bars != "" {
		b.WriteString(bars + "\n")
	}
	if f.Detail != "" {
		b.WriteString(dimStyle.Render(f.Detail) + "\n")
	}
	b.WriteString("\n" + headStyle.Render("处理建议") + "\n")
	harness := info.generic
	if rs := remedies[f.ID]; len(rs) > 0 {
		harness = strings.Join(rs, "；")
	}
	b.WriteString("1. " + dimStyle.Render(harnessName(p)+" 自己的做法：") + harness + "\n")
	b.WriteString("2. " + dimStyle.Render("交给 agent：") + "\"" + agentAsk + "\"" + dimStyle.Render("（按 y 复制）") + "\n")
	b.WriteString("3. " + dimStyle.Render("记忆整理技能：Valise 计划提供自己的 valise-memory 技能，帮 agent 整理、压缩、归档记忆，目前在规划中。"))
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cWarn).Padding(0, 1).Width(w)
	return st.Render(warnStyle.Render("▲ "+info.title) + "\n" + b.String())
}

// findingBars draws the measured value against its threshold.
func findingBars(ck *agentkit.CheckupResult, id string, th *profile.Thresholds) string {
	days, tokens, pile := float64(agentkit.DefaultLongSessionDays), float64(agentkit.DefaultLongSessionTokens), float64(agentkit.DefaultPileupBytes)
	if th != nil {
		if th.LongSessionDays > 0 {
			days = float64(th.LongSessionDays)
		}
		if th.LongSessionTokens > 0 {
			tokens = float64(th.LongSessionTokens)
		}
		if th.PileupBytes > 0 {
			pile = float64(th.PileupBytes)
		}
	}
	switch id {
	case agentkit.FindingLongSession:
		s := ck.Current
		if s == nil && len(ck.Sessions) > 0 {
			s = &ck.Sessions[0]
		}
		if s == nil {
			return ""
		}
		est := ""
		if s.Estimated {
			est = "（估算）"
		}
		return bar("会话持续", s.Days, days, fmt.Sprintf("%.0f 天", s.Days), fmt.Sprintf("%.0f 天开始提醒", days)) + "\n" +
			bar("token 用量", float64(s.Tokens), tokens, fmt.Sprintf("%d 万%s", s.Tokens/10000, est), fmt.Sprintf("%.0f 万开始提醒", tokens/10000))
	case agentkit.FindingMemoryBloat:
		var rows []string
		for _, mf := range ck.Memory {
			if mf.WarnLines > 0 {
				rows = append(rows, bar(truncate(mf.Path, 14), float64(mf.Lines), float64(mf.WarnLines), itoa(mf.Lines)+" 行", itoa(mf.WarnLines)+" 行开始提醒"))
			}
		}
		return strings.Join(rows, "\n")
	case agentkit.FindingSessionPileup:
		return bar("会话总量", float64(ck.TotalBytes), pile, size(ck.TotalBytes), size(int64(pile))+" 开始提醒") + "\n" +
			bar("很久没碰过", float64(ck.ColdBytes), float64(max(ck.TotalBytes, 1)), size(ck.ColdBytes), "占全部的比例")
	case agentkit.FindingCostGrowth:
		var peak int64 = 1
		for _, d := range ck.Daily {
			peak = max(peak, d.Tokens)
		}
		var rows []string
		for _, d := range ck.Daily {
			rows = append(rows, bar(d.Day[5:], float64(d.Tokens), float64(peak), itoa(int(d.Tokens/1000))+"k", ""))
		}
		return strings.Join(rows, "\n")
	}
	return ""
}

// bar: label, a 20-cell bar scaled so the threshold sits at 80%, value.
func bar(label string, v, limit float64, value, note string) string {
	const cells = 20
	pos := int(v / limit * cells * 0.8)
	pos = max(0, min(cells, pos))
	mark := int(cells * 0.8)
	var b strings.Builder
	for i := 0; i < cells; i++ {
		switch {
		case i == mark:
			b.WriteString(dimStyle.Render("│"))
		case i < pos && v >= limit:
			b.WriteString(warnStyle.Render("█"))
		case i < pos:
			b.WriteString(okStyle.Render("█"))
		default:
			b.WriteString(faintStyle.Render("░"))
		}
	}
	out := padRight(label, 14) + b.String() + "  " + value
	if note != "" {
		out += dimStyle.Render("（" + note + "）")
	}
	return out
}

func okNotes(ck *agentkit.CheckupResult) string {
	var parts []string
	if ck.SessionsDeclared {
		parts = append(parts, itoa(ck.SessionCount)+" 个会话，共 "+size(ck.TotalBytes))
	}
	for _, mf := range ck.Memory {
		parts = append(parts, mf.Path+" "+itoa(mf.Lines)+" 行")
	}
	if ck.MemoryMechanism {
		parts = append(parts, "有长期记忆")
	}
	s := dimStyle.Render("测到的：" + strings.Join(parts, " · "))
	for _, n := range ck.Notes {
		s += "\n" + dimStyle.Render("· "+n)
	}
	return s
}

var categoryNames = map[string]string{"identity": "人格", "memory": "记忆", "skills": "技能", "config": "配置", "sessions": "会话",
	"state": "状态库", "body_patch": "身体补丁", "secrets": "凭据", "workspace": "工作区", "cache": "缓存", "ignore": "不备份", "body": "身体"}

func categoryName(c string) string {
	if n, ok := categoryNames[c]; ok {
		return n
	}
	return c
}

// proposalsView also returns the selected proposal's lines within it
// (-1 when there is none).
func (c *checkupTab) proposalsView(w int) (string, int, int) {
	if !c.loadedP {
		return "", -1, -1
	}
	if len(c.proposals) == 0 {
		return box("归档建议", dimStyle.Render("没有待处理的建议。agent 认为有文件应该装进逃生舱（或换一类）时，会出现在这里。"), w), -1, -1
	}
	var b strings.Builder
	from, to := -1, -1
	b.WriteString(dimStyle.Render("agent 认为这些文件也该装进逃生舱，或应该换一类：") + "\n")
	for i, pr := range c.proposals {
		if i == c.cursor {
			from = 1 + lipgloss.Height(lipgloss.NewStyle().Width(w-4).Render(b.String())) - 1
		}
		line := pr.Rule.Root + ":" + pr.Rule.Glob + " → " + categoryName(pr.Rule.Category)
		if pr.Demotion {
			line += warnStyle.Render("  降级：不再进轻装逃生舱，需要单独确认")
		}
		b.WriteString(cursorLine(i == c.cursor, line) + "\n")
		b.WriteString(dimStyle.Render("    理由（agent 提供）："+pr.Reason) + "\n")
		if pr.Unclassified > 0 {
			b.WriteString(warnStyle.Render(fmt.Sprintf("    其中 %d 个文件现在没有归类（按凭据对待），接受后会进入轻装逃生舱；请确认里面没有密钥", pr.Unclassified)) + "\n")
		}
		if i == c.cursor {
			to = lipgloss.Height(lipgloss.NewStyle().Width(w - 4).Render(b.String()))
		}
	}
	return box("归档建议", strings.TrimRight(b.String(), "\n"), w), from, to
}
