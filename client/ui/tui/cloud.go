package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

// cloudScreen is a server's cloud backup (docs/UI.md 9.3 "云备份").
type cloudScreen struct {
	host   string
	target int
	cursor int
	lists  map[string]*vaultrpc.TargetListResult
	errs   map[string]error
}

type targetListed struct {
	name string
	list *vaultrpc.TargetListResult
	err  error
}

func newCloud(host string) *cloudScreen {
	return &cloudScreen{host: host, lists: map[string]*vaultrpc.TargetListResult{}, errs: map[string]error{}}
}

func (c *cloudScreen) title() string { return "云备份" }
func (c *cloudScreen) help() string {
	if len(c.targets(nil)) == 0 {
		return menu("+", "添加云备份位置")
	}
	return menu("s", "立即同步", "a", "自动同步", "d", "删除这个周期（云端）", "+", "添加位置", "tab", "换位置", "r", "刷新")
}

func (c *cloudScreen) targets(m *Model) []vaultrpc.TargetState {
	if m == nil {
		return nil
	}
	if st := m.server(c.host).status; st != nil {
		return st.Targets
	}
	return nil
}

func (c *cloudScreen) init(m *Model) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range c.targets(m) {
		cmds = append(cmds, c.list(m, t.Name))
	}
	return tea.Batch(cmds...)
}

func (c *cloudScreen) list(m *Model, name string) tea.Cmd {
	svc, ctx, host := m.svc, m.ctx, c.host
	return func() tea.Msg { l, err := svc.TargetList(ctx, host, name); return targetListed{name, l, err} }
}

// row is one period in the table: on the server, on the target, or both.
type cloudRow struct {
	leaf     string
	server   *vaultrpc.LeafState
	remote   *vaultrpc.RemoteLeaf
	deleting bool
}

func (c *cloudScreen) rows(m *Model, target string) []cloudRow {
	byLeaf := map[string]*cloudRow{}
	if st := m.server(c.host).status; st != nil {
		for i := range st.Leaves {
			l := &st.Leaves[i]
			byLeaf[l.Leaf] = &cloudRow{leaf: l.Leaf, server: l, deleting: l.Deleting}
		}
	}
	if lst := c.lists[target]; lst != nil {
		for i := range lst.Leaves {
			l := &lst.Leaves[i]
			r := byLeaf[l.Leaf]
			if r == nil {
				r = &cloudRow{leaf: l.Leaf}
				byLeaf[l.Leaf] = r
			}
			r.remote = l
		}
	}
	var out []cloudRow
	for _, r := range byLeaf {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].leaf > out[j].leaf })
	return out
}

func (c *cloudScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case targetListed:
		c.lists[msg.name], c.errs[msg.name] = msg.list, msg.err
		return nil, true
	case srvLoaded:
		return c.init(m), false
	case tea.KeyPressMsg:
		ts := c.targets(m)
		k := msg.String()
		if k == "+" {
			return c.addTarget(m), true
		}
		if len(ts) == 0 {
			return nil, false
		}
		t := ts[min(c.target, len(ts)-1)]
		rows := c.rows(m, t.Name)
		if cur, moved := moveList(c.cursor, len(rows), k); moved {
			c.cursor = cur
			return nil, true
		}
		switch k {
		case "tab":
			c.target, c.cursor = (c.target+1)%len(ts), 0
		case "r":
			return tea.Batch(m.loadServer(c.host), c.list(m, t.Name)), true
		case "s":
			return m.push(newSync(c.host, t.Name)), true
		case "a":
			return c.chooseAuto(m, t.Target), true
		case "d":
			if c.cursor < len(rows) && rows[c.cursor].remote != nil {
				return confirmLeafDelete(m, c.host, rows[c.cursor].leaf, t.Name, func(m *Model) tea.Cmd {
					return tea.Batch(m.loadServer(c.host), c.list(m, t.Name))
				}), true
			}
			return func() tea.Msg { return flashMsg("这个周期不在 " + t.Name + " 上") }, true
		default:
			return nil, false
		}
		return nil, true
	}
	return nil, false
}

func (c *cloudScreen) chooseAuto(m *Model, t vaultrpc.Target) tea.Cmd {
	return m.push(pick("自动同步到 "+t.Name+"（每次建立逃生舱后，距上次同步超过这个间隔就顺带同步；不常驻）", []choice{
		{label: "关", value: "off"}, {label: "每 6 小时最多一次", value: "6h"}, {label: "每天最多一次", value: "1d"}, {label: "每周最多一次", value: "7d"},
	}, func(m *Model, every string) tea.Cmd {
		t.Auto = nil
		if every != "off" {
			t.Auto = &vaultrpc.AutoSync{Every: every}
		}
		svc, ctx, host := m.svc, m.ctx, c.host
		return func() tea.Msg {
			if err := svc.TargetSet(ctx, host, t); err != nil {
				return failed(err)
			}
			return srvRefresh(host, "已保存自动同步设置")
		}
	}))
}

func (c *cloudScreen) addTarget(m *Model) tea.Cmd {
	return m.push(newInput("云备份位置的名字（小写字母、数字、连字符，例如 nas）", "nas", false, func(m *Model, name string) tea.Cmd {
		name = strings.TrimSpace(name)
		return m.push(newInput("位置：服务器能用 SSH 登录的 <主机>:<绝对路径>（目前支持 rsync；登录凭据在服务器上由你配置）", "nas:/volume1/valise-vault/", false,
			func(m *Model, dest string) tea.Cmd {
				svc, ctx, host := m.svc, m.ctx, c.host
				t := vaultrpc.Target{Name: name, Kind: "rsync", Dest: strings.TrimSpace(dest)}
				return func() tea.Msg {
					if err := svc.TargetSet(ctx, host, t); err != nil {
						return failed(err)
					}
					return srvRefresh(host, "已添加云备份位置 "+name)
				}
			}))
	}))
}

// refreshed reports a finished change; the root reloads the server.
type refreshed struct{ host, text string }

func srvRefresh(host, text string) tea.Msg { return refreshed{host: host, text: text} }

func (c *cloudScreen) view(m *Model) string {
	w := min(m.width-2, 110)
	ts := c.targets(m)
	if len(ts) == 0 {
		return box("", "还没有云备份位置。\n\n逃生舱先写在服务器上；再同步到 NAS、网盘等另一处，服务器坏了也不怕。\n按 "+keyStyle.Render("+")+" 添加（目前支持经 SSH 的 rsync）。", w)
	}
	t := ts[min(c.target, len(ts)-1)]
	auto := "关"
	if t.Auto != nil {
		auto = okStyle.Render("开") + " · 每 " + t.Auto.Every + " 最多一次"
	}
	last := dimStyle.Render("从未成功")
	if t.LastOK != nil {
		last = ago(*t.LastOK, m.now()) + " " + okStyle.Render("成功")
	}
	if t.LastError != "" {
		last += "　" + errStyle.Render("上次失败")
	}
	dest := t.Dest
	if name := m.server(c.host).status.ServerName; name != "" {
		dest = strings.TrimRight(t.Dest, "/") + "/" + name + "/"
	}
	head := box(t.Name, kv("位置", dest, "自动同步", auto, "上次同步", last), w)
	var b strings.Builder
	b.WriteString(head + "\n")
	if e := c.errs[t.Name]; e != nil {
		b.WriteString(errStyle.Render("无法列出 "+t.Name+" 上的内容："+firstLine(errText(e))) + "\n")
	}
	rows := c.rows(m, t.Name)
	b.WriteString(dimStyle.Render("按 agent 和周期浏览（\"两处都有\"才算真正安全）：") + "\n")
	var lines []string
	for i, r := range rows {
		lines = append(lines, cursorLine(i == c.cursor, cloudLine(r)))
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("  还没有逃生舱"))
	}
	b.WriteString(scrollLines(lines, c.cursor, m.bodyHeight()-lipgloss.Height(head)-3))
	return b.String()
}

func cloudLine(r cloudRow) string {
	inst, period, _ := vault.SplitLeaf(r.leaf)
	label := inst + "  " + strings.Replace(period, "_", " ~ ", 1)
	if r.leaf == vault.LegacyLeaf {
		label = "分档之前的旧金库"
	}
	n, bytes := 0, int64(0)
	if r.server != nil {
		n, bytes = len(r.server.Snapshots), r.server.Bytes
	} else if r.remote != nil {
		n, bytes = len(r.remote.Snapshots), r.remote.Bytes
	}
	where := ""
	switch {
	case r.deleting || (r.remote != nil && r.remote.Deleting):
		where = errStyle.Render("删除未完成")
	case r.server != nil && r.remote != nil && len(r.remote.Snapshots) >= len(r.server.Snapshots):
		where = okStyle.Render("两处都有")
	case r.server != nil && r.remote != nil:
		where = warnStyle.Render("云端还缺 " + itoa(len(r.server.Snapshots)-len(r.remote.Snapshots)) + " 个")
	case r.server != nil:
		where = warnStyle.Render("只在服务器")
	default:
		where = warnStyle.Render("只在云端")
	}
	cur := ""
	if r.server != nil && r.server.Current {
		cur = dimStyle.Render("  当前周期")
	}
	return padRight(label, 44) + padRight(itoa(n)+" 个", 7) + padRight(size(bytes), 10) + where + cur
}
