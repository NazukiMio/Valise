package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

// vaultScreen lists the server's snapshots (of one instance when inst is
// set) and the off-site targets.
type vaultScreen struct {
	host, inst string
	status     *vaultrpc.StatusResult
	snaps      []vault.Meta
	cursor     int
	loading    bool
}

func newVault(host, inst string) *vaultScreen { return &vaultScreen{host: host, inst: inst} }

type vaultLoaded struct {
	st  *vaultrpc.StatusResult
	err error
}

type syncDone struct {
	res *vaultrpc.SyncResult
	err error
}

func (v *vaultScreen) title() string {
	if v.inst != "" {
		return "快照：" + v.inst
	}
	return "金库：" + v.host
}
func (v *vaultScreen) help() string {
	return "↑↓ 选择  ·  enter 用这个快照复活  ·  y 同步到异地  ·  t 添加同步目标  ·  P 交换前状态  ·  O 旧实例  ·  r 刷新"
}

func (v *vaultScreen) init(m *Model) tea.Cmd {
	v.loading = true
	return func() tea.Msg {
		st, err := m.svc.VaultStatus(m.ctx, v.host)
		return vaultLoaded{st, err}
	}
}

func (v *vaultScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case vaultLoaded:
		v.loading = false
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		v.status, v.snaps = msg.st, nil
		for _, s := range msg.st.Snapshots {
			if v.inst == "" || s.InstanceID == v.inst {
				v.snaps = append(v.snaps, s)
			}
		}
		slices.SortFunc(v.snaps, func(a, b vault.Meta) int { return b.CreatedAt.Compare(a.CreatedAt) })
		v.cursor = min(v.cursor, max(0, len(v.snaps)-1))
		return nil, true
	case syncDone:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		r := msg.res
		return tea.Batch(func() tea.Msg {
			return flashMsg(fmt.Sprintf("已同步到 %s：%d 个快照", r.Target, len(r.Snapshots)))
		}, v.init(m)), true
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			v.cursor = max(0, v.cursor-1)
		case "down", "j":
			v.cursor = min(len(v.snaps)-1, v.cursor+1)
		case "r":
			return v.init(m), true
		case "enter":
			if v.cursor < len(v.snaps) {
				return m.push(newRevive(v.host, v.snaps[v.cursor])), true
			}
		case "y":
			return v.sync(m), true
		case "t":
			return v.addTarget(m), true
		case "P":
			return m.push(newPrev(v.host)), true
		case "O":
			return m.push(newOld(v.host)), true
		default:
			return nil, false
		}
		return nil, true
	}
	return nil, false
}

func (v *vaultScreen) sync(m *Model) tea.Cmd {
	if v.status == nil || len(v.status.Targets) == 0 {
		return func() tea.Msg { return flashMsg("还没有同步目标；按 t 添加") }
	}
	var cs []choice
	for _, t := range v.status.Targets {
		cs = append(cs, choice{label: t.Name + "  " + dimStyle.Render(t.Kind+" "+t.Dest), value: t.Name})
	}
	host := v.host
	return m.push(pick("同步到哪个异地目标", cs, func(m *Model, name string) tea.Cmd {
		return m.push(confirm("把 "+host+" 的金库同步到 "+name+"？", nil, func(m *Model) tea.Cmd {
			m.flash, m.flashErr = "正在同步…", false
			return func() tea.Msg {
				res, err := m.svc.Sync(m.ctx, host, name)
				return syncDone{res, err}
			}
		}))
	}))
}

func (v *vaultScreen) addTarget(m *Model) tea.Cmd {
	host := v.host
	return m.push(newInput("同步目标的名称", "nas", false, func(m *Model, name string) tea.Cmd {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil
		}
		return m.push(newInput("rsync 目标（服务器能通过自己的 SSH 配置访问，形如 主机:路径）", "nas-test:/mnt/test/nas/valise-vault/", false, func(m *Model, dest string) tea.Cmd {
			return func() tea.Msg {
				if err := m.svc.TargetSet(m.ctx, host, vaultrpc.Target{Name: name, Kind: "rsync", Dest: strings.TrimSpace(dest)}); err != nil {
					return failed(err)
				}
				return flashMsg("已添加同步目标 " + name)
			}
		}))
	}))
}

func (v *vaultScreen) view(m *Model) string {
	if v.loading && v.status == nil {
		return dimStyle.Render("读取金库…")
	}
	var b strings.Builder
	if st := v.status; st != nil {
		if !st.Exists {
			b.WriteString(warnStyle.Render("这台服务器上还没有金库（第一次封存时创建）") + "\n")
		}
		fmt.Fprintf(&b, "收件人指纹：%s\n", strings.Join(st.Recipients, "、"))
		for _, t := range st.Targets {
			last := warnStyle.Render("从未同步")
			if t.LastOK != nil {
				last = "最近同步 " + t.LastOK.Local().Format("2006-01-02 15:04")
			}
			if t.LastError != "" {
				last += "  " + errStyle.Render("上次失败："+t.LastError)
			}
			fmt.Fprintf(&b, "同步目标 %s（%s %s）：%s\n", t.Name, t.Kind, t.Dest, last)
		}
		b.WriteString("\n")
	}
	if len(v.snaps) == 0 {
		b.WriteString(dimStyle.Render("没有快照。"))
	}
	for i, s := range v.snaps {
		line := fmt.Sprintf("%s  %-10s %-12s %s  %5d 个文件 %9s", s.SnapshotID, s.InstanceID, s.Tier,
			s.CreatedAt.Local().Format("2006-01-02 15:04"), s.FileCount, size(s.PlainBytes))
		b.WriteString(cursorLine(i == v.cursor, line) + "\n")
	}
	return b.String()
}
