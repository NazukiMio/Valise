package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/enroll"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// instanceScreen shows one instance and its actions.
type instanceScreen struct {
	row  *serverRow
	inst enroll.InstanceSummary
}

func newInstance(r *serverRow, in enroll.InstanceSummary) *instanceScreen {
	return &instanceScreen{row: r, inst: in}
}

func (s *instanceScreen) title() string { return s.row.host + "/" + s.inst.InstanceID }
func (s *instanceScreen) help() string {
	return "f 封存  ·  v 快照与复活  ·  c 体检  ·  p 归类提议  ·  k 逃生舱技能  ·  l 登录中继  ·  o 打开入口"
}
func (s *instanceScreen) init(m *Model) tea.Cmd { return nil }

func (s *instanceScreen) view(m *Model) string {
	in := s.inst
	var b strings.Builder
	status := okStyle.Render("已核实")
	if in.ProfileStatus != "verified" {
		status = warnStyle.Render("未核实")
	}
	fmt.Fprintf(&b, "%s  profile %s v%d（%s）\n", headStyle.Render(in.InstanceID), in.ProfileID, in.ProfileVersion, status)
	if in.Superseded != nil {
		fmt.Fprintf(&b, "%s\n", warnStyle.Render(fmt.Sprintf("已被实例 %s 取代（操作 %s），不能再封存；可在服务器的金库页面清理", in.Superseded.By, in.Superseded.OpID)))
	}
	for _, id := range slices.Sorted(maps.Keys(in.Roots)) {
		fmt.Fprintf(&b, "  目录 %-10s %s\n", id, in.Roots[id])
	}
	for _, sv := range in.Service {
		state := sv.Active
		if !sv.Exists() {
			state = "未安装"
		}
		fmt.Fprintf(&b, "  服务 %s %s %s\n", sv.Unit, mark(sv.Active == "active", sv.Exists()), state)
	}
	if len(in.Service) == 0 {
		b.WriteString(dimStyle.Render("  没有服务单元（前台运行）") + "\n")
	}
	for _, e := range in.Entries {
		fmt.Fprintf(&b, "  入口 %s %s:%d %s\n", e.ID, e.Host, e.Port, mark(e.Listening, true))
	}
	if last, synced := s.row.lastSeal(in.InstanceID); last != nil {
		sync := warnStyle.Render("尚未同步到异地")
		if len(synced) > 0 {
			sync = "已同步到 " + strings.Join(synced, "、")
		}
		fmt.Fprintf(&b, "  最近封存 %s  %s 档  %d 个文件 %s  %s\n", last.CreatedAt.Local().Format("2006-01-02 15:04"), last.Tier, last.FileCount, size(last.PlainBytes), sync)
	} else {
		b.WriteString(warnStyle.Render("  还没有封存过") + "\n")
	}
	if ids := s.row.findings[in.InstanceID]; len(ids) > 0 {
		fmt.Fprintf(&b, "  体检发现 %s\n", warnStyle.Render(strings.Join(ids, "、")))
	}
	return b.String()
}

type vaultRefreshed struct {
	st  *vaultrpc.StatusResult
	err error
}

type sealDone struct {
	rep *vaultrpc.SealReport
	err error
}

func (s *instanceScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case sealDone:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		r := msg.rep
		m.flash, m.flashErr = fmt.Sprintf("已封存 %s：%s 档，%d 个文件，%s；本次新写入金库 %s", r.SnapshotID, r.Tier, r.Files, size(r.PlainBytes), size(r.AddedBytes)), false
		host := s.row.host
		return func() tea.Msg {
			st, err := m.svc.VaultStatus(m.ctx, host)
			return vaultRefreshed{st, err}
		}, true
	case vaultRefreshed:
		if msg.err == nil {
			s.row.vault = msg.st // the overview shares the row
		}
		return nil, true
	case execDone:
		if msg.err != nil {
			m.flash, m.flashErr = "命令没有成功结束："+msg.err.Error(), true
		}
		return nil, true
	case tea.KeyPressMsg:
		host, id := s.row.host, s.inst.InstanceID
		switch msg.String() {
		case "f":
			if s.inst.Superseded != nil {
				return nil, true
			}
			return m.push(pick("封存档位", []choice{
				{"soul：人格、记忆、技能、会话、状态（不含凭据）", vault.TierSoul},
				{"soul-secrets：再加上凭据与未分类文件", vault.TierSoulSecrets},
				{"full：再加上可重建的缓存", vault.TierFull},
				{"raw：实例目录下的全部文件", vault.TierRaw},
			}, func(m *Model, tier string) tea.Cmd {
				return m.push(confirm(fmt.Sprintf("在 %s 上封存 %s 的 %s 档？", host, id, tier), nil, func(m *Model) tea.Cmd {
					m.flash, m.flashErr = "正在封存…", false
					return func() tea.Msg {
						rep, err := m.svc.Seal(m.ctx, host, vaultrpc.SealParams{InstanceID: id, Tier: tier, Reason: "tui"})
						return sealDone{rep, err}
					}
				}))
			})), true
		case "v":
			return m.push(newVault(host, id)), true
		case "c":
			return m.push(newCheckup(host, id)), true
		case "p":
			return m.push(newProposals(host, id)), true
		case "k":
			return m.push(newKit(host, id)), true
		case "l":
			return s.login(m), true
		case "o":
			return m.runCLI("open", host, id), true
		default:
			return nil, false
		}
	}
	return nil, false
}

// login picks a login method and hands the terminal to `valise login`.
func (s *instanceScreen) login(m *Model) tea.Cmd {
	host, id := s.row.host, s.inst.InstanceID
	t, err := m.svc.LoginMethods(m.ctx, host, id)
	if err != nil {
		return func() tea.Msg { return failed(err) }
	}
	var cs []choice
	for _, l := range t.Profile.Login {
		cs = append(cs, choice{label: fmt.Sprintf("%-22s %s（%s）", l.ID, l.DisplayName, l.Kind), value: l.ID})
	}
	if len(cs) == 0 {
		return func() tea.Msg { return flashMsg("profile " + t.Profile.ID + " 没有声明登录方式") }
	}
	return m.push(pick("登录方式（终端会交给登录中继，结束后回到这里）", cs, func(m *Model, method string) tea.Cmd {
		return m.runCLI("login", host, id, "--method", method)
	}))
}
