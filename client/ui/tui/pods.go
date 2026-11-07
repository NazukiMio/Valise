package tui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// Tier names in the UI (docs/PRODUCT.md 8.1).
var tierNames = map[string]string{vault.TierSoul: "轻装", vault.TierSoulSecrets: "标准", vault.TierFull: "完整", vault.TierRaw: "原样"}

var tierChoices = []choice{
	{label: "轻装", value: vault.TierSoul, desc: "记忆、人格、技能、配置、会话；不含凭据和工作区"},
	{label: "标准", value: vault.TierSoulSecrets, desc: "轻装内容，加登录凭据和未归类文件；不含工作区"},
	{label: "完整", value: vault.TierFull, desc: "标准内容，再加 agent 的工作区文件"},
	{label: "原样", value: vault.TierRaw, desc: "实例目录下的全部文件，含凭据、身体与缓存；体积大"},
}

// whoText says who started a snapshot, from the seal log.
func whoText(e vaultrpc.SealLogEntry) string {
	switch {
	case e.SnapshotID == "":
		return "来源未知"
	case e.Initiator == vaultrpc.InitiatorTimer:
		return "定时"
	case e.Initiator == vaultrpc.InitiatorAgentMCP && strings.HasPrefix(e.Reason, "auto:"):
		return "安全网自动"
	case e.Initiator == vaultrpc.InitiatorAgentMCP && e.Reason == "handoff":
		return "交接前 agent"
	case e.Initiator == vaultrpc.InitiatorAgentMCP:
		return "agent 自己建立"
	}
	return "你手动建立"
}

func reasonText(e vaultrpc.SealLogEntry) string {
	switch {
	case strings.HasPrefix(e.Reason, "auto:"):
		return "距上次超过 10 分钟，agent 用工具前先建一个"
	case e.Reason == "handoff":
		return "开新会话前的交接"
	}
	return service.PlainWording(e.Reason)
}

// podsTab lists the instance's escape pods (docs/UI.md 9.4 "备份").
type podsTab struct {
	cursor    int
	search    textinput.Model
	searching bool
}

func newPodsTab() *podsTab {
	ti := textinput.New()
	ti.Prompt = "搜索 "
	ti.Placeholder = "原因、日期（10-30）、谁建立的、轻装/标准…"
	return &podsTab{search: ti}
}

func (t *podsTab) name() string { return "备份" }
func (t *podsTab) typing() bool { return t.searching }
func (t *podsTab) help(p *instancePage) string {
	if t.searching {
		return menu("enter", "完成", "esc", "清空")
	}
	return menu("/", "搜索", "enter", "查看", "b", "建立备份", "u", "同步")
}
func (t *podsTab) load(m *Model, p *instancePage) tea.Cmd { return nil }

// pods returns the instance's snapshots, newest first, filtered.
func (t *podsTab) pods(m *Model, p *instancePage) []vault.Meta {
	d := m.server(p.host)
	if d.status == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(t.search.Value()))
	var out []vault.Meta
	for _, s := range d.status.Snapshots {
		if s.InstanceID != p.inst.InstanceID {
			continue
		}
		if q != "" {
			lt := s.CreatedAt.Local()
			e := p.log[s.SnapshotID]
			hay := strings.ToLower(strings.Join([]string{lt.Format("2006-01-02 01-02 1月2日 15:04"), dayLabel(s.CreatedAt, m.now()),
				whoText(e), reasonText(e), tierNames[s.Tier], d.status.LeafOf(s.SnapshotID), s.SnapshotID}, " "))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (t *podsTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	if t.searching {
		switch k.String() {
		case "enter", "down":
			t.searching = false
			t.search.Blur()
		case "esc":
			t.searching = false
			t.search.Blur()
			t.search.SetValue("")
		default:
			var cmd tea.Cmd
			t.search, cmd = t.search.Update(msg)
			t.cursor = 0
			return cmd, true
		}
		return nil, true
	}
	list := t.pods(m, p)
	if c, moved := moveList(t.cursor, len(list), k.String()); moved {
		t.cursor = c
		return nil, true
	}
	switch k.String() {
	case "/":
		t.searching = true
		t.search.Focus()
		return nil, true
	case "enter":
		if t.cursor < len(list) {
			return m.push(newPodScreen(p, list[t.cursor])), true
		}
		return nil, true
	case "b":
		return startSeal(m, p), true
	case "u":
		return startSync(m, p.host), true
	case "esc":
		if t.search.Value() != "" {
			t.search.SetValue("")
			return nil, true
		}
	}
	return nil, false
}

func (t *podsTab) view(m *Model, p *instancePage, height int) string {
	d := m.server(p.host)
	all := 0
	if d.status != nil {
		for _, s := range d.status.Snapshots {
			if s.InstanceID == p.inst.InstanceID {
				all++
			}
		}
	}
	list := t.pods(m, p)
	t.cursor = min(t.cursor, max(0, len(list)-1))
	searchLine := t.search.View() + "  " + dimStyle.Render(itoa(len(list))+" / "+itoa(all))
	if !t.searching && t.search.Value() == "" {
		searchLine = dimStyle.Render("按 / 搜索 · 共 " + itoa(all) + " 个备份")
	}
	if len(list) == 0 {
		msg := "还没有备份。按 " + keyStyle.Render("b") + " 建立第一个。"
		if all > 0 {
			msg = "没有匹配的备份。"
		}
		return searchLine + "\n\n" + msg
	}
	now := m.now()
	var lines []string
	cursorLine := 0
	day := ""
	for i, s := range list {
		if l := dayLabel(s.CreatedAt, now); l != day {
			day = l
			lines = append(lines, dimStyle.Render(l))
		}
		if i == t.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, podRow(d, p, s, i == t.cursor, m.width))
	}
	return searchLine + "\n" + scrollLinesHint(lines, cursorLine, height-1, "第 "+itoa(t.cursor+1)+" / "+itoa(len(list))+" 个")
}

func podRow(d *srvData, p *instancePage, s vault.Meta, selected bool, width int) string {
	e := p.log[s.SnapshotID]
	who := whoText(e)
	whoSt := dimStyle
	switch who {
	case "你手动建立":
		whoSt = accStyle
	case "agent 自己建立", "交接前 agent":
		whoSt = infoStyle
	}
	cloud := warnStyle.Render("☁ 未同步")
	if d.synced(s.SnapshotID) {
		cloud = okStyle.Render("☁ 已同步")
	}
	reasonW := max(10, min(width, 120)-62)
	reason := reasonText(e)
	if lipgloss.Width(reason) > reasonW {
		reason = truncate(reason, reasonW-1) + "…"
	}
	cols := []string{
		s.CreatedAt.Local().Format("15:04"),
		whoSt.Render(padRight(who, 12)),
		padRight(reason, reasonW),
		padRight(tierNames[s.Tier], 4),
		dimStyle.Render(padRight(size(s.PlainBytes), 9)),
		cloud,
	}
	return cursorLine(selected, strings.Join(cols, "  "))
}

func padRight(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func truncate(s string, w int) string {
	var b strings.Builder
	for _, r := range s {
		if lipgloss.Width(b.String()+string(r)) > w {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sealDone reports a finished seal.
type sealDone struct {
	rep *vaultrpc.SealReport
	err error
}

// startSeal asks what to pack and why, then builds the escape pod.
func startSeal(m *Model, p *instancePage) tea.Cmd {
	return ensureKey(m, p.host, func(m *Model) tea.Cmd { return pickSeal(m, p) })
}

func pickSeal(m *Model, p *instancePage) tea.Cmd {
	return m.push(pick("建立备份 · 选择内容", tierChoices, func(m *Model, tier string) tea.Cmd {
		return m.push(newInput("原因（可选，以后在列表里能看到，例如\"升级前\"）", "", false, func(m *Model, reason string) tea.Cmd {
			sc := newSealScreen(m, p.host, p.inst.InstanceID, tier)
			return tea.Batch(m.push(sc), sc.run(m, reason))
		}))
	}))
}

// sealResult turns a seal into a status line and refreshes what changed.
func sealResult(m *Model, p *instancePage, msg sealDone) tea.Cmd {
	if msg.err != nil {
		return func() tea.Msg { return failed(msg.err) }
	}
	text := "备份已建立（" + tierNames[msg.rep.Tier] + "，" + size(msg.rep.PlainBytes) + "，这次新写入 " + size(msg.rep.AddedBytes) + "），已写入服务器"
	if len(msg.rep.AutoSync) > 0 {
		text += "；正在后台顺带同步到 " + strings.Join(msg.rep.AutoSync, "、")
	}
	if len(m.svc.Repos()) > 0 {
		text += "；正在后台补到仓库"
	}
	m.flash, m.flashErr = text, false
	return tea.Batch(m.loadServer(p.host), p.loadLog(m), repoSyncAll(m, p.host))
}
