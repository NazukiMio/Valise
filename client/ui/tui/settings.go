package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/agentkit"
	"valise/ui/service"
)

// settingsScreen is the global settings (docs/UI.md 9.5).
type settingsScreen struct {
	section int
	cursor  int
	inBody  bool // focus in the section's list instead of the sidebar
	key     *service.KeyInfo
	hosts   []string
	pack    *service.ProfilesStatus
	doctors map[string]*service.DoctorReport
	kits    map[string]*agentkit.KitStatusResult // host/instance
}

var settingsSections = []string{"加密钥匙", "服务器", "仓库与空间站", "导入导出", "更新", "高级", "关于"}

const (
	ssKey = iota
	ssServers
	ssCloud
	ssConfig
	ssUpdate
	ssAdvanced
	ssAbout
)

type settingsLoaded struct {
	key   *service.KeyInfo
	hosts []string
	pack  *service.ProfilesStatus
}
type settingsDoctor struct {
	host string
	r    *service.DoctorReport
}
type settingsKit struct {
	id string
	k  *agentkit.KitStatusResult
}

func newSettings() *settingsScreen {
	return &settingsScreen{doctors: map[string]*service.DoctorReport{}, kits: map[string]*agentkit.KitStatusResult{}}
}

func (s *settingsScreen) title() string { return "设置" }
func (s *settingsScreen) help() string {
	items := []string{"↑↓", "选分栏"}
	if s.inBody {
		items = []string{"↑↓", "选择服务器", "←", "回到分栏"}
	} else if s.hasList() {
		items = append(items, "→", "进入列表")
	}
	switch s.section {
	case ssKey:
		items = append(items, "b", "备份钥匙", "i", "在这台电脑上导入钥匙")
	case ssServers:
		items = append(items, "a", "添加服务器", "x", "移除")
	case ssCloud:
		items = append(items, "enter", "打开仓库与空间站")
	case ssConfig:
		items = append(items, "e", "导出 Valise 配置", "i", "导入 Valise 配置")
	case ssUpdate:
		items = append(items, "p", "在线更新资料包", "f", "从文件安装资料包")
	case ssAdvanced:
		items = append(items, "u", "从这台服务器移除我的钥匙", "d", "忽略红色提醒的时长")
	case ssAbout:
		items = append(items, "g", "第一次使用")
	}
	return menu(items...)
}

// hasList: the section has a server list to move in.
func (s *settingsScreen) hasList() bool {
	return (s.section == ssServers || s.section == ssCloud || s.section == ssAdvanced) && len(s.hosts) > 0
}

func (s *settingsScreen) init(m *Model) tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		key, _ := svc.KeyShow()
		hosts, _ := svc.Servers()
		pack, _ := svc.ProfilesStatus()
		return settingsLoaded{key, hosts, pack}
	}
}

func (s *settingsScreen) loadUpdates(m *Model) tea.Cmd {
	var cmds []tea.Cmd
	svc, ctx := m.svc, m.ctx
	for _, h := range s.hosts {
		h := h
		if s.doctors[h] == nil {
			cmds = append(cmds, func() tea.Msg { r, _ := svc.Doctor(ctx, h); return settingsDoctor{h, r} })
		}
		for _, in := range m.server(h).instances {
			id, inst := h+"/"+in.InstanceID, in.InstanceID
			if in.Superseded == nil && s.kits[id] == nil {
				cmds = append(cmds, func() tea.Msg { k, _ := svc.KitStatus(ctx, h, inst); return settingsKit{id, k} })
			}
		}
	}
	return tea.Batch(cmds...)
}

func (s *settingsScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case settingsLoaded:
		s.key, s.hosts, s.pack = msg.key, msg.hosts, msg.pack
		var cmds []tea.Cmd
		for _, h := range s.hosts {
			if d := m.server(h); !d.loaded && !d.loading {
				cmds = append(cmds, m.loadServer(h))
			}
		}
		return tea.Batch(cmds...), true
	case settingsDoctor:
		s.doctors[msg.host] = msg.r
		return nil, true
	case settingsKit:
		s.kits[msg.id] = msg.k
		return nil, true
	case packFetched:
		return installPackFlow(m, msg, func(m *Model) tea.Cmd { return s.init(m) }), true
	case tea.KeyPressMsg:
		// The sidebar is a vertical list: ↑↓ choose the section, → (or
		// enter) moves into the section's list, ← or esc back out.
		k := msg.String()
		switch {
		case len(k) == 1 && k[0] >= '1' && int(k[0]-'1') < len(settingsSections):
			return s.go_(m, int(k[0]-'1')), true
		case k == "tab":
			return s.go_(m, (s.section+1)%len(settingsSections)), true
		case k == "shift+tab":
			return s.go_(m, (s.section+len(settingsSections)-1)%len(settingsSections)), true
		case s.inBody && (k == "left" || k == "esc"):
			s.inBody = false
			return nil, true
		case s.inBody:
			if c, moved := moveList(s.cursor, len(s.hosts), k); moved {
				s.cursor = c
				return nil, true
			}
		case (k == "right" || k == "enter") && s.hasList():
			s.inBody = true
			return nil, true
		case s.section == ssAbout && k == "g":
			// g opens the checklist here, not the list's top
		default:
			if c, moved := moveList(s.section, len(settingsSections), k); moved {
				return s.go_(m, c), true
			}
		}
		return s.action(m, k)
	}
	return nil, false
}

func (s *settingsScreen) go_(m *Model, i int) tea.Cmd {
	s.section, s.cursor, s.inBody = i, 0, false
	if i == ssUpdate {
		return s.loadUpdates(m)
	}
	return nil
}

func (s *settingsScreen) action(m *Model, k string) (tea.Cmd, bool) {
	host := ""
	if s.cursor < len(s.hosts) {
		host = s.hosts[s.cursor]
	}
	switch {
	case s.section == ssAbout && k == "g":
		hosts, _ := m.svc.Servers()
		return m.push(newChecklist(hosts)), true
	case s.section == ssKey && k == "b":
		return m.push(newInput("把恢复套件导出到哪个文件夹？（建议放进 U 盘或密码管理器的附件，文件夹必须是空的或不存在）", "~/Desktop/valise-恢复套件", false,
			func(m *Model, dir string) tea.Cmd {
				dir = expandHome(m, strings.TrimSpace(dir))
				return func() tea.Msg {
					if _, err := m.svc.ExportKit(dir); err != nil {
						return failed(err)
					}
					return flashMsg("已导出恢复套件到 " + dir + "。里面的 RECOVERY.md 写着不依赖 Valise 的恢复方法。")
				}
			})), true
	case s.section == ssKey && k == "i":
		return m.push(newInput("恢复套件文件夹的路径（导入后需要输入口令）", "", false, func(m *Model, dir string) tea.Cmd {
			return m.cliThen(s.init(m), false, "key", "import-kit", expandHome(m, strings.TrimSpace(dir)))
		})), true
	case s.section == ssServers && k == "a":
		return m.push(newAddServer(func(m *Model) tea.Cmd { return s.init(m) })), true
	case s.section == ssServers && k == "x" && host != "":
		return m.push(confirm("不再在这台电脑上显示 "+host+"？", []string{"只从列表中移除，服务器上的东西都不动。"}, func(m *Model) tea.Cmd {
			m.svc.RemoveServer(host)
			delete(m.data, host)
			return s.init(m)
		})), true
	case s.section == ssCloud && k == "enter" && host != "":
		return m.push(newCloud(host)), true
	case s.section == ssUpdate && k == "p":
		m.flash, m.flashErr = "正在从 GitHub Release 下载资料包…", false
		svc, ctx := m.svc, m.ctx
		return func() tea.Msg { f, err := svc.FetchPack(ctx, ""); return packFetched{f, err} }, true
	case s.section == ssUpdate && k == "f":
		return m.push(newInput("资料包文件路径（同一文件夹里的 .minisig 签名会一起读取）", "valise-profiles.tar.gz", false, func(m *Model, p string) tea.Cmd {
			svc := m.svc
			p = expandHome(m, strings.TrimSpace(p))
			return func() tea.Msg { f, err := svc.ReadPackFile(p); return packFetched{f, err} }
		})), true
	case s.section == ssAdvanced && k == "d":
		return m.push(newInput("忽略红色提醒后，多少天再在主页提醒（30–365）", itoa(m.svc.RedDismissDays()), false, func(m *Model, v string) tea.Cmd {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err == nil {
				err = m.svc.SetRedDismissDays(n)
			}
			if err != nil {
				return func() tea.Msg { return failed(err) }
			}
			m.flash, m.flashErr = "已改为 "+itoa(n)+" 天，下次忽略时生效", false
			return nil
		})), true
	case s.section == ssAdvanced && k == "u" && host != "":
		return m.runCLI("key", "unregister", host), true
	case s.section == ssConfig && k == "e":
		return exportConfig(m), true
	case s.section == ssConfig && k == "i":
		return importConfig(m, func(m *Model) tea.Cmd { return s.init(m) }), true
	}
	return nil, false
}

func expandHome(m *Model, p string) string {
	if strings.HasPrefix(p, "~/") {
		return m.svc.Home + p[1:]
	}
	return p
}

func (s *settingsScreen) view(m *Model) string {
	var side []string
	for i, n := range settingsSections {
		label := itoa(i+1) + " " + n
		switch {
		case i == s.section && s.inBody:
			side = append(side, accStyle.Render("  "+label))
		case i == s.section:
			side = append(side, selStyle.Render("▸ "+label))
		default:
			side = append(side, dimStyle.Render("  "+label))
		}
	}
	w := min(m.width-20, 96)
	var body string
	switch s.section {
	case ssKey:
		body = s.keyView(w)
	case ssServers:
		body = s.serversView(m, w)
	case ssCloud:
		body = s.cloudView(m, w)
	case ssConfig:
		body = configView(w)
	case ssUpdate:
		body = s.updateView(m, w)
	case ssAdvanced:
		body = s.advancedView(m, w)
	case ssAbout:
		body = box("关于", kv("版本", m.svc.Version, "项目", "github.com/NazukiMio/Valise",
			"不用 Valise 也能恢复", "金库格式公开：用 age、zstd 和仓库里的 tools/standalone-restore/ 即可，把它指向任一周期文件夹",
			"第一次使用", "上手清单与各功能的小引导：按 g 打开"), w)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(16).Render(strings.Join(side, "\n")), body)
}

func (s *settingsScreen) keyView(w int) string {
	if s.key == nil {
		return box("加密钥匙", warnStyle.Render("这台电脑上还没有钥匙。"), w)
	}
	return box(okStyle.Render("✓")+" 你的钥匙已创建", kv("存放在", s.key.Path, "保护方式", "你的口令（Valise 不保存口令）", "指纹", s.key.Fingerprint), w) + "\n\n" +
		"· 所有逃生舱都用这把钥匙加密。\n" +
		"· 服务器只拿到\"只能上锁、不能开锁\"的那一半，服务器被攻破也打不开逃生舱。\n" +
		"· 换电脑时，在新电脑上导入同一把钥匙并输入口令，就能打开以前所有的逃生舱。\n" +
		"· 这台电脑坏了或丢了，钥匙也就没了：" + headStyle.Render("请备份一份恢复套件") + "。\n\n" +
		keyStyle.Render("b") + " 备份钥匙（导出恢复套件）     " + keyStyle.Render("i") + " 在这台电脑上导入钥匙"
}

func (s *settingsScreen) serversView(m *Model, w int) string {
	if len(s.hosts) == 0 {
		return box("服务器", "还没有添加服务器。按 "+keyStyle.Render("a")+" 添加。", w)
	}
	var rows []string
	for i, h := range s.hosts {
		d := m.server(h)
		state := dimStyle.Render("连接中")
		switch {
		case d.err != nil:
			state = errStyle.Render("连不上")
		case d.loaded:
			state = okStyle.Render("在线") + dimStyle.Render("　"+itoa(len(d.instances))+" 个 agent")
		}
		rows = append(rows, cursorLine(s.inBody && i == s.cursor, padRight(h, 20)+state))
	}
	return box("服务器", strings.Join(rows, "\n"), w) + "\n" + dimStyle.Render("添加服务器时会自动部署 porter（缺少时）并登记你的钥匙。")
}

func (s *settingsScreen) cloudView(m *Model, w int) string {
	var rows []string
	for i, h := range s.hosts {
		d := m.server(h)
		state := dimStyle.Render("读取中")
		if d.status != nil {
			if len(d.status.Targets) == 0 {
				state = warnStyle.Render("还没有空间站")
			} else {
				var parts []string
				for _, t := range d.status.Targets {
					p := t.Name
					if t.Auto != nil {
						p += " · 自动每 " + t.Auto.Every
					}
					if t.LastOK != nil {
						p += " · " + ago(*t.LastOK, m.now())
					}
					parts = append(parts, p)
				}
				state = strings.Join(parts, "；")
			}
		}
		rows = append(rows, cursorLine(s.inBody && i == s.cursor, padRight(h, 20)+state))
	}
	if len(rows) == 0 {
		rows = append(rows, dimStyle.Render("还没有服务器"))
	}
	repos := dimStyle.Render("还没有仓库")
	if rs := m.svc.Repos(); len(rs) > 0 {
		var parts []string
		for _, r := range rs {
			parts = append(parts, r.Name+" "+dimStyle.Render(r.Path))
		}
		repos = strings.Join(parts, "\n")
	}
	return box("仓库（本机文件夹，所有服务器共用）", repos, w) + "\n" + box("空间站", strings.Join(rows, "\n"), w) + "\n" +
		dimStyle.Render("仓库在 Valise 打开时补齐；空间站配置在每台服务器上；新 agent 的周期默认按月。")
}
