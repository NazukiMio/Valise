package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/enroll"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// serverRow is one recorded server and what was read from it.
type serverRow struct {
	host     string
	loading  bool
	err      error
	insts    []enroll.InstanceSummary
	vault    *vaultrpc.StatusResult
	findings map[string][]string // instance -> finding ids
}

// lastSeal is the newest snapshot of an instance and the targets it was
// synced to.
func (r *serverRow) lastSeal(inst string) (*vault.Meta, []string) {
	if r.vault == nil {
		return nil, nil
	}
	var last *vault.Meta
	for i := range r.vault.Snapshots {
		s := &r.vault.Snapshots[i]
		if s.InstanceID == inst && (last == nil || s.CreatedAt.After(last.CreatedAt)) {
			last = s
		}
	}
	var synced []string
	if last != nil {
		for _, t := range r.vault.Targets {
			if slices.Contains(t.LastSynced, last.SnapshotID) {
				synced = append(synced, t.Name)
			}
		}
	}
	return last, synced
}

// item is a selectable line: a server header (inst == "") or an instance.
type item struct {
	row  *serverRow
	inst *enroll.InstanceSummary
}

type overviewScreen struct {
	rows   []*serverRow
	cursor int
}

func newOverview() *overviewScreen { return &overviewScreen{} }

type serversLoaded struct {
	hosts []string
	err   error
}

type serverLoaded struct {
	host  string
	insts []enroll.InstanceSummary
	vault *vaultrpc.StatusResult
	err   error
}

type findingsLoaded struct {
	host, inst string
	ids        []string
}

func (o *overviewScreen) title() string { return "服务器" }
func (o *overviewScreen) help() string {
	return "↑↓ 选择  ·  enter 打开  ·  a 添加服务器  ·  i 从 SSH 配置导入  ·  x 不再记录  ·  r 刷新  ·  s 设置  ·  q 退出"
}

func (o *overviewScreen) init(m *Model) tea.Cmd {
	return func() tea.Msg {
		hosts, err := m.svc.Servers()
		return serversLoaded{hosts, err}
	}
}

func (o *overviewScreen) loadServer(m *Model, host string) tea.Cmd {
	return func() tea.Msg {
		insts, err := m.svc.Instances(m.ctx, host)
		if err != nil {
			return serverLoaded{host: host, err: err}
		}
		st, err := m.svc.VaultStatus(m.ctx, host)
		return serverLoaded{host: host, insts: insts, vault: st, err: err}
	}
}

func (o *overviewScreen) loadFindings(m *Model, host, inst string) tea.Cmd {
	return func() tea.Msg {
		r, err := m.svc.Checkup(m.ctx, host, inst)
		if err != nil {
			return findingsLoaded{host: host, inst: inst}
		}
		var ids []string
		for _, f := range r.Findings {
			ids = append(ids, f.ID)
		}
		return findingsLoaded{host: host, inst: inst, ids: ids}
	}
}

func (o *overviewScreen) items() []item {
	var out []item
	for _, r := range o.rows {
		out = append(out, item{row: r})
		for i := range r.insts {
			out = append(out, item{row: r, inst: &r.insts[i]})
		}
	}
	return out
}

func (o *overviewScreen) row(host string) *serverRow {
	for _, r := range o.rows {
		if r.host == host {
			return r
		}
	}
	return nil
}

func (o *overviewScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case serversLoaded:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		o.rows = nil
		var cmds []tea.Cmd
		for _, h := range msg.hosts {
			o.rows = append(o.rows, &serverRow{host: h, loading: true, findings: map[string][]string{}})
			cmds = append(cmds, o.loadServer(m, h))
		}
		o.cursor = min(o.cursor, max(0, len(o.items())-1))
		return tea.Batch(cmds...), true
	case serverLoaded:
		r := o.row(msg.host)
		if r == nil {
			return nil, true
		}
		r.loading, r.err, r.insts, r.vault = false, msg.err, msg.insts, msg.vault
		var cmds []tea.Cmd
		for _, in := range r.insts {
			if in.Superseded == nil {
				cmds = append(cmds, o.loadFindings(m, r.host, in.InstanceID))
			}
		}
		return tea.Batch(cmds...), true
	case findingsLoaded:
		if r := o.row(msg.host); r != nil {
			r.findings[msg.inst] = msg.ids
		}
		return nil, true
	case execDone:
		return o.init(m), true
	case tea.KeyPressMsg:
		items := o.items()
		switch msg.String() {
		case "up", "k":
			o.cursor = max(0, o.cursor-1)
		case "down", "j":
			o.cursor = min(len(items)-1, o.cursor+1)
		case "r":
			return o.init(m), true
		case "a":
			return m.push(newInput("添加服务器（~/.ssh/config 中的 Host 别名，或 user@host）", "ubuntu-vm", false, func(m *Model, v string) tea.Cmd {
				return o.addServers(m, strings.Fields(v)...)
			})), true
		case "i":
			return o.importHosts(m), true
		case "s":
			return m.push(newSettings()), true
		case "x":
			if o.cursor < len(items) {
				host := items[o.cursor].row.host
				return m.push(confirm("不再记录服务器 "+host+"？（服务器上的内容没有任何改动）", nil, func(m *Model) tea.Cmd {
					if _, err := m.svc.RemoveServer(host); err != nil {
						return func() tea.Msg { return failed(err) }
					}
					return o.init(m)
				})), true
			}
		case "enter":
			if o.cursor < len(items) {
				it := items[o.cursor]
				if it.inst != nil {
					return m.push(newInstance(it.row, *it.inst)), true
				}
				return m.push(newVault(it.row.host, "")), true
			}
		default:
			return nil, false
		}
		return nil, true
	}
	return nil, false
}

func (o *overviewScreen) addServers(m *Model, hosts ...string) tea.Cmd {
	if len(hosts) == 0 {
		return nil
	}
	if _, err := m.svc.AddServers(hosts...); err != nil {
		return func() tea.Msg { return failed(err) }
	}
	return o.init(m)
}

func (o *overviewScreen) importHosts(m *Model) tea.Cmd {
	hosts, err := m.svc.ImportableHosts()
	if err != nil {
		return func() tea.Msg { return failed(err) }
	}
	var cs []choice
	for _, h := range hosts {
		cs = append(cs, choice{label: h, value: h})
	}
	return m.push(pick("从 ~/.ssh/config 导入（只记录别名）", cs, func(m *Model, v string) tea.Cmd {
		return o.addServers(m, v)
	}))
}

func (o *overviewScreen) view(m *Model) string {
	if len(o.rows) == 0 {
		return "还没有已记录的服务器。按 a 添加，或按 i 从 ~/.ssh/config 导入。"
	}
	var b strings.Builder
	for i, it := range o.items() {
		sel := i == o.cursor
		if it.inst == nil {
			b.WriteString(cursorLine(sel, headStyle.Render(it.row.host)+" "+serverState(it.row)) + "\n")
			continue
		}
		b.WriteString(cursorLine(sel, "  "+instanceLine(it.row, it.inst)) + "\n")
	}
	return b.String()
}

func serverState(r *serverRow) string {
	switch {
	case r.loading:
		return dimStyle.Render("读取中…")
	case r.err != nil:
		return errStyle.Render(strings.SplitN(errText(r.err), "\n", 2)[0])
	case len(r.insts) == 0:
		return dimStyle.Render("（没有登记的实例）")
	}
	return dimStyle.Render(fmt.Sprintf("%d 个实例", len(r.insts)))
}

func instanceLine(r *serverRow, in *enroll.InstanceSummary) string {
	parts := []string{headStyle.Render(in.InstanceID), dimStyle.Render(fmt.Sprintf("%s v%d", in.ProfileID, in.ProfileVersion))}
	if in.Superseded != nil {
		return strings.Join(append(parts, warnStyle.Render("已被 "+in.Superseded.By+" 取代")), "  ")
	}
	if in.ProfileStatus != "verified" {
		parts = append(parts, warnStyle.Render("未核实"))
	}
	for _, s := range in.Service {
		parts = append(parts, "服务 "+mark(s.Active == "active", s.Exists()))
	}
	for _, e := range in.Entries {
		parts = append(parts, "入口 "+e.ID+" "+mark(e.Listening, true))
	}
	if last, synced := r.lastSeal(in.InstanceID); last != nil {
		s := fmt.Sprintf("封存 %s %s", last.CreatedAt.Local().Format("01-02 15:04"), last.Tier)
		if len(synced) > 0 {
			s += " 已同步"
		} else {
			s += warnStyle.Render(" 未同步")
		}
		parts = append(parts, s)
	} else {
		parts = append(parts, warnStyle.Render("从未封存"))
	}
	if ids, ok := r.findings[in.InstanceID]; ok && len(ids) > 0 {
		parts = append(parts, warnStyle.Render("体检："+strings.Join(ids, ",")))
	}
	return strings.Join(parts, "  ")
}
