package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/agentkit"
	"valise/ui/service"
)

var findingNames = map[string]string{
	agentkit.FindingLongSession:       "会话过长",
	agentkit.FindingCostGrowth:        "token 用量持续上升",
	agentkit.FindingMemoryBloat:       "记忆文件过大",
	agentkit.FindingSessionPileup:     "旧会话堆积",
	agentkit.FindingNoMemoryMechanism: "没有记忆机制",
}

// checkupScreen runs and shows the code-only checkup.
type checkupScreen struct {
	host, inst string
	res        *agentkit.CheckupResult
}

func newCheckup(host, inst string) *checkupScreen { return &checkupScreen{host: host, inst: inst} }

type checkupDone struct {
	res *agentkit.CheckupResult
	err error
}

func (c *checkupScreen) title() string { return "体检" }
func (c *checkupScreen) help() string  { return "r 重新体检" }
func (c *checkupScreen) init(m *Model) tea.Cmd {
	c.res = nil
	return func() tea.Msg {
		r, err := m.svc.Checkup(m.ctx, c.host, c.inst)
		return checkupDone{r, err}
	}
}
func (c *checkupScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case checkupDone:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		c.res = msg.res
		return nil, true
	case tea.KeyPressMsg:
		if msg.String() == "r" {
			return c.init(m), true
		}
	}
	return nil, false
}
func (c *checkupScreen) view(m *Model) string {
	r := c.res
	if r == nil {
		return dimStyle.Render("体检中（只读，由代码计算）…")
	}
	var b strings.Builder
	if r.SessionsDeclared {
		fmt.Fprintf(&b, "会话 %d 个，共 %s；%d 天前的冷会话 %s\n", r.SessionCount, size(r.TotalBytes), agentkit.DefaultColdDays, size(r.ColdBytes))
		if cur := r.Current; cur != nil {
			est := ""
			if cur.Estimated {
				est = "（估算）"
			}
			fmt.Fprintf(&b, "最近的会话 %s：%.1f 天，%d token%s\n", cur.ID, cur.Days, cur.Tokens, est)
		}
		var days []string
		for _, d := range r.Daily {
			days = append(days, fmt.Sprint(d.Tokens))
		}
		fmt.Fprintf(&b, "最近 7 天每日 token：%s\n", strings.Join(days, " "))
	}
	for _, mf := range r.Memory {
		fmt.Fprintf(&b, "记忆文件 %s：%d 行（阈值 %d）\n", mf.Path, mf.Lines, mf.WarnLines)
	}
	for _, n := range r.Notes {
		b.WriteString(dimStyle.Render("· "+n) + "\n")
	}
	b.WriteString("\n")
	if len(r.Findings) == 0 {
		b.WriteString(okStyle.Render("没有发现问题。"))
	}
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "%s %s\n", warnStyle.Render(findingNames[f.ID]+"（"+f.ID+"）"), f.Detail)
	}
	return b.String()
}

// proposalsScreen reviews the agent's classification proposals. Ordinary
// proposals can be accepted together; each demotion needs its own
// confirmation (docs/AGENT_KIT.md 8.2).
type proposalsScreen struct {
	host, inst string
	list       []agentkit.Proposal
	cursor     int
	loaded     bool
}

func newProposals(host, inst string) *proposalsScreen {
	return &proposalsScreen{host: host, inst: inst}
}

type proposalsLoaded struct {
	list []agentkit.Proposal
	err  error
}

func (p *proposalsScreen) title() string { return "归类提议" }
func (p *proposalsScreen) help() string {
	return "↑↓ 选择  ·  a 接受所选  ·  A 接受全部普通提议  ·  x 拒绝所选  ·  X 拒绝全部"
}
func (p *proposalsScreen) init(m *Model) tea.Cmd {
	return func() tea.Msg {
		l, err := m.svc.Proposals(m.ctx, p.host, p.inst)
		return proposalsLoaded{l, err}
	}
}
func (p *proposalsScreen) decide(m *Model, d agentkit.DecideParams) tea.Cmd {
	d.InstanceID = p.inst
	return func() tea.Msg {
		res, err := m.svc.DecideProposals(m.ctx, p.host, d)
		if err != nil {
			return failed(err)
		}
		m.send(flashMsg(fmt.Sprintf("已接受 %d 条、拒绝 %d 条；接受的规则从下一次封存起生效", len(res.Accepted), len(res.Rejected))))
		l, err := m.svc.Proposals(m.ctx, p.host, p.inst)
		return proposalsLoaded{l, err}
	}
}
func (p *proposalsScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case proposalsLoaded:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		p.list, p.loaded = msg.list, true
		p.cursor = min(p.cursor, max(0, len(p.list)-1))
		return nil, true
	case tea.KeyPressMsg:
		if len(p.list) == 0 && msg.String() != "esc" {
			return nil, msg.String() != "q"
		}
		cur := p.list[min(p.cursor, len(p.list)-1)]
		switch msg.String() {
		case "up", "k":
			p.cursor = max(0, p.cursor-1)
		case "down", "j":
			p.cursor = min(len(p.list)-1, p.cursor+1)
		case "a":
			if cur.Demotion {
				return m.push(confirm("接受降级提议 "+cur.ID+"？", []string{
					cur.Rule.Root + ":" + cur.Rule.Glob + " → " + cur.Rule.Category,
					"接受后这些文件将不再进入 soul 档（raw 档仍包含它们）。",
				}, func(m *Model) tea.Cmd { return p.decide(m, agentkit.DecideParams{AcceptDemotions: []string{cur.ID}}) })), true
			}
			return p.decide(m, agentkit.DecideParams{Accept: []string{cur.ID}}), true
		case "A":
			return m.push(confirm("接受全部普通提议？（降级提议不包括在内，需要逐个确认）", nil,
				func(m *Model) tea.Cmd { return p.decide(m, agentkit.DecideParams{Accept: []string{"all"}}) })), true
		case "x":
			return p.decide(m, agentkit.DecideParams{Reject: []string{cur.ID}}), true
		case "X":
			return m.push(confirm("拒绝全部提议？", nil,
				func(m *Model) tea.Cmd { return p.decide(m, agentkit.DecideParams{Reject: []string{"all"}}) })), true
		default:
			return nil, false
		}
		return nil, true
	}
	return nil, false
}
func (p *proposalsScreen) view(m *Model) string {
	if !p.loaded {
		return dimStyle.Render("读取中…")
	}
	if len(p.list) == 0 {
		return "没有待确认的归类提议。"
	}
	var b strings.Builder
	for i, pr := range p.list {
		tag := ""
		if pr.Demotion {
			tag = warnStyle.Render(" [降级：离开 soul 档]")
		}
		b.WriteString(cursorLine(i == p.cursor, fmt.Sprintf("%s  %s:%s → %s%s", pr.ID, pr.Rule.Root, pr.Rule.Glob, pr.Rule.Category, tag)) + "\n")
		b.WriteString(dimStyle.Render("      理由（agent 提供）："+pr.Reason) + "\n")
		if pr.Unclassified > 0 {
			b.WriteString(warnStyle.Render(fmt.Sprintf("      其中 %d 个文件目前未分类（按凭据层封存），接受后将进入 soul 档；请确认里面没有凭据", pr.Unclassified)) + "\n")
		}
	}
	return b.String()
}

// kitScreen shows and changes the agent-kit installation.
type kitScreen struct {
	host, inst string
	st         *agentkit.KitStatusResult
	plan       *service.KitPlan
	removal    *service.KitRemovalPlan
	force      bool // the pending uninstall also removes modified skills
}

func newKit(host, inst string) *kitScreen { return &kitScreen{host: host, inst: inst} }

type kitStatusLoaded struct {
	st  *agentkit.KitStatusResult
	err error
}

type kitPlanned struct {
	install *service.KitPlan
	removal *service.KitRemovalPlan
	err     error
	done    bool
}

func (k *kitScreen) title() string { return "逃生舱技能" }
func (k *kitScreen) help() string {
	return "i 安装或更新  ·  u 卸载  ·  F 卸载（含被修改过的）"
}
func (k *kitScreen) init(m *Model) tea.Cmd {
	return func() tea.Msg {
		st, err := m.svc.KitStatus(m.ctx, k.host, k.inst)
		return kitStatusLoaded{st, err}
	}
}
