package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/enroll"
	"valise/ui/service"
)

// An installation is one harness install (one dossier) and its agents
// (docs/UI.md 9.4): one card on the server page, and with several agents
// an install page whose agents open as agent subpages.

// installGroup is one installation's agents, the default agent first.
type installGroup struct {
	key    string
	agents []enroll.InstanceSummary
}

func installKey(in enroll.InstanceSummary) string {
	if in.InstallID != "" {
		return in.InstallID
	}
	return in.ProfileID
}

// groupInstalls groups a server's instances by installation, in listing
// order; superseded instances stay cards of their own.
func groupInstalls(ins []enroll.InstanceSummary) []installGroup {
	var out []installGroup
	at := map[string]int{}
	for _, in := range ins {
		if in.Superseded != nil {
			out = append(out, installGroup{key: "superseded:" + in.InstanceID, agents: []enroll.InstanceSummary{in}})
			continue
		}
		k := installKey(in)
		if i, ok := at[k]; ok {
			out[i].agents = append(out[i].agents, in)
			continue
		}
		at[k] = len(out)
		out = append(out, installGroup{key: k, agents: []enroll.InstanceSummary{in}})
	}
	for i := range out {
		a := out[i].agents
		sort.SliceStable(a, func(x, y int) bool { return a[x].Name == "" && a[y].Name != "" })
	}
	return out
}

// groupOf finds the live group with key, from the server's current data.
func groupOf(m *Model, host, key string) installGroup {
	for _, g := range groupInstalls(m.server(host).instances) {
		if g.key == key {
			return g
		}
	}
	return installGroup{key: key}
}

// agentTerm is what the harness calls one agent (harness 资料 terms.agent).
func agentTerm(m *Model, profileID string) string {
	if m.prof != nil && m.prof.ByID[profileID] != nil {
		if t := m.prof.ByID[profileID].Effective.AgentTerm(); t != "" {
			return t
		}
	}
	return "agent"
}

// agentLabel names one agent in its harness's words.
func agentLabel(m *Model, profileID, name string) string {
	if name == "" {
		return "默认 " + agentTerm(m, profileID)
	}
	return agentTerm(m, profileID) + " " + name
}

// installCard is a card for an installation with several agents.
func installCard(m *Model, d *srvData, g installGroup, selected, compact bool) string {
	head := g.agents[0]
	dp := m.display(head.ProfileID)
	title := headStyle.Render(displayName(m, head.ProfileID))
	count := dimStyle.Render(itoa(len(g.agents)) + " 个 " + agentTerm(m, head.ProfileID))
	var rows []string
	for _, in := range g.agents {
		r := "· " + agentLabel(m, in.ProfileID, in.Name)
		if len(attention(m, d, in)) > 0 {
			r += warnStyle.Render("  !")
		}
		rows = append(rows, r)
	}
	if compact {
		info := append([]string{title + " " + count, runStateShort(head)}, rows...)
		body := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(compactIcon(dp), "\n"), "  ", fitLines(info, compactCardW-4-compactIconW-2, compactCardH-2))
		return cardFrame(body, compactCardW, compactCardH, selected, false)
	}
	info := []string{title, count, runState(head), entryState(head), ""}
	info = append(info, rows...)
	body := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(standardIcon(dp), "\n"), "  ", fitLines(info, cardW-4-iconW-2, cardH-2))
	return cardFrame(body, cardW, cardH, selected, false)
}

// newInstallPage: the installation's agents, management, health and
// settings; health and settings are the default agent's, which carries
// the installation's service.
func newInstallPage(host string, g installGroup) *instancePage {
	p := newInstancePage(host, g.agents[0])
	p.install = g.key
	p.tabs = []tab{&agentsTab{}, &manageTab{}, &healthTab{}, &isetTab{}}
	return p
}

// newAgentPage is one agent of a multi-agent installation.
func newAgentPage(host string, in enroll.InstanceSummary) *instancePage {
	p := newInstancePage(host, in)
	p.tabs = []tab{&overviewTab{}, &chatTab{}, &healthTab{}, newPodsTab(), &isetTab{}}
	return p
}

// agentsFound carries the agents detected on the server.
type agentsFound struct {
	found []service.DetectedInstance
	err   error
}

// agentsTab lists the installation's agents, enrolled first, then the
// ones detected but not yet enrolled.
type agentsTab struct {
	cursor  int
	found   []service.DetectedInstance
	err     error
	loading bool
}

// agentRow is an enrolled agent or a detected one.
type agentRow struct {
	in  *enroll.InstanceSummary
	det *service.DetectedInstance
}

func (t *agentsTab) name() string { return "agent" }
func (t *agentsTab) help(p *instancePage) string {
	return menu("↑↓", "选择", "enter", "进入", "n", "接入 Valise", "r", "重新检测")
}

func (t *agentsTab) load(m *Model, p *instancePage) tea.Cmd {
	if t.found != nil || t.loading {
		return nil
	}
	return t.detect(m, p)
}

func (t *agentsTab) detect(m *Model, p *instancePage) tea.Cmd {
	t.loading = true
	svc, ctx, host := m.svc, m.ctx, p.host
	return func() tea.Msg { f, err := svc.DetectInstances(ctx, host); return agentsFound{f, err} }
}

func (t *agentsTab) set(msg agentsFound) {
	t.loading, t.err = false, msg.err
	t.found = []service.DetectedInstance{}
	if msg.err == nil {
		t.found = msg.found
	}
}

func (t *agentsTab) rows(m *Model, p *instancePage) []agentRow {
	g := groupOf(m, p.host, p.install)
	var out []agentRow
	for i := range g.agents {
		out = append(out, agentRow{in: &g.agents[i]})
	}
	for i := range t.found {
		f := &t.found[i]
		if f.Enrolled == "" && f.ProfileID == p.inst.ProfileID {
			out = append(out, agentRow{det: f})
		}
	}
	return out
}

// missing: detection finished without finding enrolled agent id.
func (t *agentsTab) missing(id string) bool {
	if t.found == nil || t.loading || t.err != nil {
		return false
	}
	for _, f := range t.found {
		if f.Enrolled == id {
			return false
		}
	}
	return true
}

func (t *agentsTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	rows := t.rows(m, p)
	if c, moved := moveList(t.cursor, len(rows), k.String()); moved {
		t.cursor = c
		return nil, true
	}
	if t.cursor >= len(rows) {
		return nil, false
	}
	r := rows[t.cursor]
	switch k.String() {
	case "enter":
		if r.in != nil {
			return m.push(newAgentPage(p.host, *r.in)), true
		}
		return t.connect(m, p, r.det), true
	case "n":
		if r.det != nil {
			return t.connect(m, p, r.det), true
		}
		return func() tea.Msg { return flashMsg("这个 " + agentTerm(m, r.in.ProfileID) + " 已经接入 Valise") }, true
	case "r":
		t.found = nil
		return tea.Batch(t.detect(m, p), m.loadServer(p.host)), true
	}
	return nil, false
}

func (t *agentsTab) connect(m *Model, p *instancePage, d *service.DetectedInstance) tea.Cmd {
	t.found = nil
	return connectOne(m, p.host, d.ProfileID, d.Name, t.detect(m, p))
}

func (t *agentsTab) view(m *Model, p *instancePage, height int) string {
	d := m.server(p.host)
	var b strings.Builder
	rows := t.rows(m, p)
	t.cursor = min(t.cursor, max(0, len(rows)-1))
	for i, r := range rows {
		if i == t.cursor {
			n := strings.Count(b.String(), "\n")
			p.setFocus(n, n)
		}
		if r.in != nil {
			line := agentLabel(m, r.in.ProfileID, r.in.Name) + dimStyle.Render("  ·  ") + backupLine(m, d, *r.in, true)
			if a := attention(m, d, *r.in); len(a) > 0 && !strings.Contains(line, a[0]) {
				line += warnStyle.Render("  ! " + a[0])
			}
			if t.missing(r.in.InstanceID) {
				line += warnStyle.Render("  ! 重新检测时没有找到它的目录")
			}
			b.WriteString("  " + cursorLine(i == t.cursor, line) + "\n")
			continue
		}
		line := agentLabel(m, r.det.ProfileID, r.det.Name) + faintStyle.Render("  ·  还没接入 Valise（按 n 接入）")
		b.WriteString("  " + cursorLine(i == t.cursor, faintStyle.Render(line)) + "\n")
	}
	switch {
	case t.loading:
		b.WriteString("\n" + dimStyle.Render("正在检测服务器上的其他 "+agentTerm(m, p.inst.ProfileID)+"…") + "\n")
	case t.err != nil:
		b.WriteString("\n" + warnStyle.Render("检测失败：") + errText(t.err) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("管理页、服务和管理动作属于整个安装，在 2 管理；每个 "+agentTerm(m, p.inst.ProfileID)+" 的备份和逃生舱在它自己的页面里。") + "\n")
	return b.String()
}

// heading names the page: the installation and its agent count on an
// install page, the instance otherwise.
func (p *instancePage) heading(m *Model) string {
	if p.install == "" {
		return p.inst.InstanceID
	}
	return p.install + "（" + itoa(len(groupOf(m, p.host, p.install).agents)) + " 个 " + agentTerm(m, p.inst.ProfileID) + "）"
}
