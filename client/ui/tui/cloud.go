package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// cloudScreen is a server's repositories and stations (docs/UI.md 9.4
// "仓库与空间站"): repositories (local folders) first, then stations.
type cloudScreen struct {
	host   string
	target int // index into places
	cursor int
	lists  map[string]*vaultrpc.TargetListResult
	errs   map[string]error
	repos  []service.RepoStatus
	m      *Model // for help(), which has no model
}

type targetListed struct {
	name string
	list *vaultrpc.TargetListResult
	err  error
}

func newCloud(host string) *cloudScreen {
	return &cloudScreen{host: host, lists: map[string]*vaultrpc.TargetListResult{}, errs: map[string]error{}}
}

func (c *cloudScreen) title() string { return "仓库与空间站" }
func (c *cloudScreen) help() string {
	if c.m == nil || len(places(c.m, c.host)) == 0 {
		return menu("+", "添加仓库或空间站")
	}
	if c.current(c.m).repo {
		return menu("s", "立即同步", "x", "从列表移除仓库", "+", "添加", "tab", "换位置", "r", "刷新")
	}
	return menu("s", "立即同步", "a", "自动同步", "d", "删除这个周期（空间站）", "+", "添加", "tab", "换位置", "r", "刷新")
}

// current is the place shown.
func (c *cloudScreen) current(m *Model) place {
	ps := places(m, c.host)
	if len(ps) == 0 {
		return place{}
	}
	return ps[min(c.target, len(ps)-1)]
}

func (c *cloudScreen) repoStatus(name string) *service.RepoStatus {
	for i := range c.repos {
		if c.repos[i].Name == name {
			return &c.repos[i]
		}
	}
	return nil
}

func (c *cloudScreen) station(m *Model, name string) *vaultrpc.TargetState {
	for _, t := range c.targets(m) {
		if t.Name == name {
			return &t
		}
	}
	return nil
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
	c.m = m
	name := ""
	if st := m.server(c.host).status; st != nil {
		name = st.ServerName
	}
	c.repos = m.svc.RepoStatuses(c.host, name)
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

func (c *cloudScreen) rows(m *Model, p place) []cloudRow {
	byLeaf := map[string]*cloudRow{}
	if st := m.server(c.host).status; st != nil {
		for i := range st.Leaves {
			l := &st.Leaves[i]
			byLeaf[l.Leaf] = &cloudRow{leaf: l.Leaf, server: l, deleting: l.Deleting}
		}
	}
	lst := c.lists[p.name]
	if p.repo {
		lst = &vaultrpc.TargetListResult{}
		if rs := c.repoStatus(p.name); rs != nil {
			for leaf, ids := range rs.Leaves {
				lst.Leaves = append(lst.Leaves, vaultrpc.RemoteLeaf{Leaf: leaf, Snapshots: ids})
			}
		}
	}
	if lst != nil {
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
		ps := places(m, c.host)
		k := msg.String()
		if k == "+" {
			return c.addPlace(m), true
		}
		if len(ps) == 0 {
			return nil, false
		}
		p := c.current(m)
		rows := c.rows(m, p)
		if cur, moved := moveList(c.cursor, len(rows), k); moved {
			c.cursor = cur
			return nil, true
		}
		switch k {
		case "tab":
			c.target, c.cursor = (c.target+1)%len(ps), 0
		case "r":
			cmds := []tea.Cmd{m.loadServer(c.host)}
			if !p.repo {
				cmds = append(cmds, c.list(m, p.name))
			}
			return tea.Batch(cmds...), true
		case "s":
			sync := func(m *Model) tea.Cmd { return m.push(newSync(c.host, p)) }
			if st := m.server(c.host).status; st != nil && st.ServerName == "" {
				return nameServer(m, c.host, sync), true
			}
			return sync(m), true
		case "a":
			if t := c.station(m, p.name); t != nil && !p.repo {
				return c.chooseAuto(m, t.Target), true
			}
			return func() tea.Msg {
				return flashMsg("仓库在 Valise 打开时自动补齐，没有单独的自动同步设置")
			}, true
		case "x":
			if !p.repo {
				return nil, false
			}
			return m.push(confirm("从列表中移除仓库 "+p.name+"？", []string{"只是不再往这里复制；文件夹里已有的逃生舱不会删除。"},
				func(m *Model) tea.Cmd {
					if err := m.svc.RepoRemove(p.name); err != nil {
						return func() tea.Msg { return failed(err) }
					}
					c.target = 0
					return func() tea.Msg { return flashMsg("已移除仓库 " + p.name + "，文件夹里的逃生舱保留") }
				})), true
		case "d":
			if p.repo {
				return func() tea.Msg {
					return flashMsg("仓库里的周期请直接在文件夹里删除；Valise 不删除本机仓库里的文件")
				}, true
			}
			if c.cursor < len(rows) && rows[c.cursor].remote != nil {
				return confirmLeafDelete(m, c.host, rows[c.cursor].leaf, p.name, func(m *Model) tea.Cmd {
					return tea.Batch(m.loadServer(c.host), c.list(m, p.name))
				}), true
			}
			return func() tea.Msg { return flashMsg("这个周期不在 " + p.name + " 上") }, true
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

func (c *cloudScreen) addPlace(m *Model) tea.Cmd {
	return m.push(pick("添加哪一种？", []choice{
		{label: "仓库 · 本机文件夹", value: "repo", desc: "这台电脑上的文件夹、外接硬盘或网盘的同步文件夹；只在 Valise 打开时更新"},
		{label: "空间站 · 另一台机器", value: "station", desc: "NAS 或 VPS，由服务器经 SSH 直接传过去（rsync，没有时改用 sftp）"},
	}, func(m *Model, kind string) tea.Cmd {
		if kind == "repo" {
			return c.addRepo(m)
		}
		return c.addTarget(m)
	}))
}

func (c *cloudScreen) addRepo(m *Model) tea.Cmd {
	return m.push(newInput("仓库的名字（小写字母、数字、连字符，例如 usb）", "usb", false, func(m *Model, name string) tea.Cmd {
		name = strings.TrimSpace(name)
		return m.push(newInput("文件夹的完整路径（不存在会自动创建）", "~/Valise", false, func(m *Model, path string) tea.Cmd {
			r, err := m.svc.RepoAdd(name, path)
			if err != nil {
				return func() tea.Msg { return failed(err) }
			}
			m.pop()
			m.pop()
			c.repos = m.svc.RepoStatuses(c.host, m.server(c.host).status.ServerName)
			return func() tea.Msg {
				return flashMsg("已添加仓库 " + r.Name + "（" + r.Path + "）；按 s 把逃生舱补过去")
			}
		}))
	}))
}

func (c *cloudScreen) addTarget(m *Model) tea.Cmd {
	return m.push(newInput("空间站的名字（小写字母、数字、连字符，例如 nas）", "nas", false, func(m *Model, name string) tea.Cmd {
		name = strings.TrimSpace(name)
		return m.push(newInput("位置：服务器能用 SSH 登录的 <主机>:<绝对路径>（登录凭据在服务器上由你配置）", "nas:/volume1/valise-vault/", false,
			func(m *Model, dest string) tea.Cmd {
				svc, ctx, host := m.svc, m.ctx, c.host
				t := vaultrpc.Target{Name: name, Kind: "rsync", Dest: strings.TrimSpace(dest)}
				return func() tea.Msg {
					if err := svc.TargetSet(ctx, host, t); err != nil {
						return failed(err)
					}
					return srvRefresh(host, "已添加空间站 "+name)
				}
			}))
	}))
}

// refreshed reports a finished change; the root reloads the server.
type refreshed struct{ host, text string }

func srvRefresh(host, text string) tea.Msg { return refreshed{host: host, text: text} }

func (c *cloudScreen) view(m *Model) string {
	c.m = m
	w := min(m.width-2, 110)
	ps := places(m, c.host)
	if len(ps) == 0 {
		return box("", "还没有仓库或空间站。\n\n逃生舱先写在服务器上；再放一份到别处，服务器坏了也不怕：\n"+
			"  仓库：这台电脑上的文件夹（外接硬盘、网盘同步文件夹都行），不需要任何工具。\n"+
			"  空间站：另一台机器（NAS、VPS），由服务器直接传过去。\n按 "+keyStyle.Render("+")+" 添加。", w)
	}
	p := c.current(m)
	var tabs []string
	for i, x := range ps {
		l := x.label()
		if i == min(c.target, len(ps)-1) {
			l = selStyle.Render("▸ " + l)
		} else {
			l = dimStyle.Render(l)
		}
		tabs = append(tabs, l)
	}
	var head string
	if p.repo {
		rs := c.repoStatus(p.name)
		state, last := dimStyle.Render("从未同步"), ""
		if rs != nil {
			if !rs.Available {
				state = errStyle.Render("文件夹现在不可用（外接硬盘没接上？）")
			} else if rs.State.LastOK != nil {
				state = ago(*rs.State.LastOK, m.now()) + " " + okStyle.Render("成功")
			}
			if rs.State.LastError != "" {
				last = errStyle.Render("上次失败：" + firstLine(rs.State.LastError))
			}
		}
		path := ""
		if rs != nil {
			path = rs.Path
		}
		head = box(p.label(), kv("位置", path, "上次同步", state, "说明", dimStyle.Render("本机文件夹，只在 Valise 打开时更新；Valise 不删除这里的文件")), w)
		if last != "" {
			head += "\n" + last
		}
	} else {
		t := c.station(m, p.name)
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
		head = box(p.label(), kv("位置", dest, "自动同步", auto, "上次同步", last), w)
		if e := c.errs[p.name]; e != nil {
			head += "\n" + errStyle.Render("无法列出 "+p.name+" 上的内容："+firstLine(errText(e)))
		}
	}
	var b strings.Builder
	b.WriteString(strings.Join(tabs, "   ") + "\n" + head + "\n")
	rows := c.rows(m, p)
	b.WriteString(dimStyle.Render("按 agent 和周期浏览（\"两处都有\"才算真正安全）：") + "\n")
	var lines []string
	for i, r := range rows {
		lines = append(lines, cursorLine(i == c.cursor, cloudLine(r)))
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render("  还没有逃生舱"))
	}
	b.WriteString(scrollLines(lines, c.cursor, m.bodyHeight()-lipgloss.Height(head)-4))
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
		where = warnStyle.Render("这里还缺 " + itoa(len(r.server.Snapshots)-len(r.remote.Snapshots)) + " 个")
	case r.server != nil:
		where = warnStyle.Render("只在服务器")
	default:
		where = warnStyle.Render("只在这里")
	}
	cur := ""
	if r.server != nil && r.server.Current {
		cur = dimStyle.Render("  当前周期")
	}
	return padRight(label, 44) + padRight(itoa(n)+" 个", 7) + padRight(size(bytes), 10) + where + cur
}
