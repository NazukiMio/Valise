package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/vault"
)

// podScreen is one escape pod: plain words first, technical details folded.
type podScreen struct {
	page *instancePage
	snap vault.Meta
	tech bool
}

func newPodScreen(p *instancePage, s vault.Meta) *podScreen { return &podScreen{page: p, snap: s} }

func (s *podScreen) title() string {
	return s.snap.CreatedAt.Local().Format("01-02 15:04") + " 的备份"
}
func (s *podScreen) help() string {
	return menu("l", "恢复", "u", "同步", "t", "技术信息")
}
func (s *podScreen) init(m *Model) tea.Cmd { return nil }

func (s *podScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "l":
		return m.push(newRevive(s.page.host, s.snap)), true
	case "u":
		return startSync(m, s.page.host), true
	case "t":
		s.tech = !s.tech
		return nil, true
	}
	return nil, false
}

var tierContents = map[string]string{
	vault.TierSoul:        "记忆、人格、技能、配置、会话",
	vault.TierSoulSecrets: "记忆、人格、技能、配置、会话、登录凭据、未归类的文件",
	vault.TierFull:        "记忆、人格、技能、配置、会话、登录凭据、未归类的文件、工作区",
	vault.TierRaw:         "实例目录下的全部文件（整体按凭据对待）",
}

func (s *podScreen) view(m *Model) string {
	w := min(m.width-2, 100)
	d := m.server(s.page.host)
	e := s.page.log[s.snap.SnapshotID]
	where := "服务器 " + okStyle.Render("✓")
	if d.status != nil {
		for _, r := range m.svc.RepoStatuses(s.page.host, d.status.ServerName) {
			mark := warnStyle.Render("还没有")
			if r.Has(s.snap.SnapshotID) {
				mark = okStyle.Render("✓")
			}
			where += "　仓库 " + r.Name + " " + mark
		}
		for _, t := range d.status.Targets {
			mark := warnStyle.Render("还没有")
			for _, id := range t.LastSynced {
				if id == s.snap.SnapshotID {
					mark = okStyle.Render("✓")
				}
			}
			where += "　备份位置 " + t.Name + " " + mark
		}
	}
	reason := reasonText(e)
	if reason == "" {
		reason = dimStyle.Render("（没有填写）")
	}
	main := box(dayLabel(s.snap.CreatedAt, m.now())+" "+s.snap.CreatedAt.Local().Format("15:04")+" 的备份", kv(
		"谁建立的", whoText(e),
		"原因", reason,
		"装了什么", tierNames[s.snap.Tier]+"："+tierContents[s.snap.Tier],
		"大小", size(s.snap.PlainBytes)+dimStyle.Render("（"+itoa(s.snap.FileCount)+" 个文件；这次新写入 "+size(s.snap.AddedBytes)+"，其余和以前的共用）"),
		"在哪里", where,
	), w)
	var b strings.Builder
	b.WriteString(main + "\n")
	b.WriteString(dimStyle.Render("想让 agent 回到这个时刻、搬到另一台服务器，或复制一个新的：按 ") + keyStyle.Render("l") + dimStyle.Render(" 恢复。") + "\n\n")
	if s.tech {
		leaf := ""
		if d.status != nil {
			leaf = d.status.LeafOf(s.snap.SnapshotID)
		}
		b.WriteString(box("技术信息", kv("快照 id", s.snap.SnapshotID, "周期", leaf, "epoch", itoa(s.snap.Epoch), "档位", s.snap.Tier, "harness 资料", s.snap.ProfileID), w))
	} else {
		b.WriteString(dimStyle.Render("▸ 技术信息（按 t 展开）"))
	}
	return b.String()
}
