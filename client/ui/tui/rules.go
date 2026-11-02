package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/profile"
)

// rulesScreen shows how an agent's files are sorted into the escape pod
// tiers (docs/ARCHITECTURE.md sections 4–5): the user's own rules first,
// then the profile's. Read-only: rules change by accepting the agent's
// proposals (体检) or with valise enroll --assign.
type rulesScreen struct {
	page   *instancePage
	scroll int
}

func (r *rulesScreen) title() string         { return "归类规则" }
func (r *rulesScreen) hostName() string      { return r.page.host }
func (r *rulesScreen) help() string          { return menu("↑↓", "滚动") }
func (r *rulesScreen) init(m *Model) tea.Cmd { return nil }

func (r *rulesScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "down", "j":
		r.scroll++
	case "up", "k":
		r.scroll = max(0, r.scroll-1)
	case "pgdown", "space":
		r.scroll += max(1, m.bodyHeight()/2)
	case "pgup":
		r.scroll = max(0, r.scroll-max(1, m.bodyHeight()/2))
	default:
		return nil, false
	}
	return nil, true
}

// tierReach says which escape pods a category goes into (ARCHITECTURE.md 5).
func tierReach(cat string) string {
	switch cat {
	case profile.CategorySecrets:
		return "标准、完整、原样"
	case profile.CategoryWorkspace:
		return "完整、原样"
	case profile.CategoryBody, profile.CategoryCache, profile.CategoryIgnore:
		return "只进原样"
	}
	return "全部（轻装起）"
}

func (r *rulesScreen) view(m *Model) string {
	p := r.page
	var lines []string
	add := func(s ...string) { lines = append(lines, s...) }
	add(dimStyle.Render("Valise 按这些规则把 agent 的文件分到各类，再决定每一类进哪些逃生舱。"),
		dimStyle.Render("顺序：凭据规则最优先 → 你补充的规则 → 身体 → harness 资料的规则（第一条命中的为准）；都没命中的按凭据对待。"), "")
	if p.target == nil {
		return strings.Join(append(lines, dimStyle.Render("读取中…")), "\n")
	}
	prof := p.target.Profile
	add(headStyle.Render("你补充的规则"))
	if p.target.Record == nil || len(p.target.Record.LocalOverrides) == 0 {
		add("  " + dimStyle.Render("还没有。"))
	} else {
		for _, o := range p.target.Record.LocalOverrides {
			add("  " + padRight(o.Root+":"+o.Glob, 40) + categoryName(o.Category) + dimStyle.Render("  进 "+tierReach(o.Category)))
		}
	}
	add("", headStyle.Render("harness 资料 "+prof.ID+" v"+itoa(prof.ProfileVersion)+" 的规则"))
	byCat := map[string][]profile.PathRule{}
	for _, pr := range prof.Paths {
		byCat[pr.Category] = append(byCat[pr.Category], pr)
	}
	for _, cat := range profile.Categories {
		rules := byCat[cat]
		if len(rules) == 0 {
			continue
		}
		add("  " + accStyle.Render(categoryName(cat)) + dimStyle.Render("　进 "+tierReach(cat)))
		for _, pr := range rules {
			l := "    " + pr.Root + ":" + pr.Glob
			if pr.Verify == "pending" {
				l += warnStyle.Render("  未核实")
			}
			if pr.Note != "" {
				l += dimStyle.Render("  " + pr.Note)
			}
			add(l)
		}
	}
	if prof.Body != nil && len(prof.Body.Paths) > 0 {
		add("  " + accStyle.Render("身体") + dimStyle.Render("　按安装来源重建，只进原样"))
		for _, b := range prof.Body.Paths {
			add("    " + b.Root + ":" + b.Glob)
		}
	}
	if len(prof.ExclusiveSecrets) > 0 {
		add("  " + accStyle.Render("独占凭据") + dimStyle.Render("　同一时间只能在一台服务器上用，着陆到另一台服务器时不放回"))
		for _, e := range prof.ExclusiveSecrets {
			add("    " + e.Root + ":" + e.Glob)
		}
	}
	add("", headStyle.Render("怎么修改"),
		"  · agent 可以通过逃生舱技能提议补充规则，你在「体检」里接受或拒绝。",
		"  · 也可以在终端里重新接入时指定："+accStyle.Render("valise enroll "+p.host+" --profile "+prof.ID+" --id "+p.inst.InstanceID+" --assign <root>:<glob>=<类别>"),
		"  · harness 资料的规则随 harness 资料包更新；Valise 不会自己改动它们。")
	height := m.bodyHeight()
	if len(lines) > height {
		room := height - 1
		r.scroll = min(r.scroll, len(lines)-room)
		end := r.scroll + room
		return strings.Join(lines[r.scroll:end], "\n") + "\n" + faintStyle.Render("  ↑↓ 滚动（"+itoa(end)+" / "+itoa(len(lines))+" 行）")
	}
	r.scroll = 0
	return strings.Join(lines, "\n")
}
