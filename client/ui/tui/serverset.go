package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// serverSettings: name, porter, tools, key (docs/UI.md 9.3 "服务器设置").
type serverSettings struct {
	host string
	doc  *service.DoctorReport
	err  error
}

type doctorDone struct {
	r   *service.DoctorReport
	err error
}

func newServerSettings(host string) *serverSettings { return &serverSettings{host: host} }

func (s *serverSettings) title() string { return "服务器设置" }
func (s *serverSettings) help() string {
	return menu("n", "名字", "m", "改用 Valise 钥匙", "a", "管理钥匙", "u", "更新 porter", "t", "Kit 工具", "k", "登记我的钥匙", "K", "装这台电脑的日常钥匙", "x", "从列表中移除", "r", "刷新")
}

func (s *serverSettings) init(m *Model) tea.Cmd {
	svc, ctx, host := m.svc, m.ctx, s.host
	return func() tea.Msg { r, err := svc.Doctor(ctx, host); return doctorDone{r, err} }
}

func (s *serverSettings) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case doctorDone:
		s.doc, s.err = msg.r, msg.err
		return nil, true
	case tea.KeyPressMsg:
		svc, ctx, host := m.svc, m.ctx, s.host
		switch msg.String() {
		case "r":
			return tea.Batch(s.init(m), m.loadServer(host)), true
		case "t":
			return m.push(newKitTools(host)), true
		case "u":
			return m.push(pick("porter 的来源", []choice{
				{label: "从 GitHub Release 下载", value: "release", desc: "校验签名与 SHA-256"},
				{label: "上传本地构建的文件", value: "local"},
			}, func(m *Model, v string) tea.Cmd {
				label, ph := "Release 版本号", "v0.1.0"
				if v == "local" {
					label, ph = "本地 porter 文件路径", "./valise-porter_linux_amd64"
				}
				return m.push(newInput(label, ph, false, func(m *Model, in string) tea.Cmd {
					src := service.DeploySource{ReleaseTag: strings.TrimSpace(in)}
					if v == "local" {
						src = service.DeploySource{LocalPath: strings.TrimSpace(in)}
					}
					m.flash, m.flashErr = "正在部署 porter…", false
					return func() tea.Msg {
						r, err := svc.DeployPorter(ctx, host, src)
						if err != nil {
							return failed(err)
						}
						m.send(flashMsg("porter 已更新到 " + r.Porter.Info.Version))
						d, err := svc.Doctor(ctx, host)
						return doctorDone{d, err}
					}
				}))
			})), true
		case "n":
			if st := m.server(host).status; st != nil && st.ServerName != "" {
				return m.push(newInput("新的服务器名（小写字母、数字和连字符）", st.ServerName, false, func(m *Model, v string) tea.Cmd {
					v = strings.TrimSpace(v)
					if v == "" || v == st.ServerName {
						return nil
					}
					return m.cliThen(tea.Batch(s.init(m), m.loadServer(host)), false, "server", "rename", host, v)
				})), true
			}
			return nameServer(m, host, func(m *Model) tea.Cmd { return s.init(m) }), true
		case "m":
			return s.migrate(m), true
		case "a":
			return s.adminKey(m), true
		case "k":
			return func() tea.Msg {
				if _, _, err := svc.KeyRegister(ctx, host, ""); err != nil {
					return failed(err)
				}
				return refreshed{host, "已把你的钥匙登记到 " + host}
			}, true
		case "K":
			if !svc.Tiered(host) {
				return func() tea.Msg {
					return flashMsg(host + " 用你自己的 SSH 设置连接，没有 Valise 的日常钥匙")
				}, true
			}
			return m.push(confirm("在 "+host+" 上装这台电脑自己的日常钥匙？", []string{
				"会在终端里用服务器密码（或你的私钥）登录一次，把日常钥匙写进服务器的 ~/.ssh/authorized_keys；Valise 不保存密码。",
				"换电脑、导入配置后，或日常钥匙被从服务器上移除时使用。"},
				func(m *Model) tea.Cmd { return m.cliThen(s.init(m), false, "server", "rekey", host) })), true
		case "x":
			return m.push(confirm("不再在这台电脑上显示 "+host+"？", []string{"只从列表中移除，服务器上的 agent、逃生舱和 porter 都不动。随时可以重新添加。"},
				func(m *Model) tea.Cmd {
					if _, err := svc.RemoveServer(host); err != nil {
						return func() tea.Msg { return failed(err) }
					}
					delete(m.data, host)
					m.flash, m.flashErr = "已从列表中移除 "+host, false
					return m.replaceRoot(newHome())
				})), true
		}
	}
	return nil, false
}

func (s *serverSettings) view(m *Model) string {
	w := min(m.width-2, 100)
	if s.err != nil {
		return errText(s.err)
	}
	if s.doc == nil {
		return dimStyle.Render("正在检查 " + s.host + " …")
	}
	d := m.server(s.host)
	name := warnStyle.Render("还没有名字（上云前需要起名）；按 n 起名")
	keyState := dimStyle.Render("读取中…")
	if d.status != nil {
		if d.status.ServerName != "" {
			name = d.status.ServerName + dimStyle.Render("（仓库和空间站里的文件夹名；按 n 改名，文件夹会一起改）")
		}
		keyState = warnStyle.Render("没有登记（这台服务器建立的逃生舱你打不开）；按 k 登记")
		if info, err := m.svc.KeyShow(); err == nil && slices.Contains(d.status.Recipients, info.Fingerprint) {
			keyState = okStyle.Render("已登记到这台服务器")
		}
	}
	porter := warnStyle.Render("没有可用的 porter")
	if s.doc.PorterFound {
		porter = s.doc.PorterVersion + dimStyle.Render("　"+s.doc.PorterPath)
	}
	if s.doc.Problem != nil {
		porter += "\n" + errStyle.Render(s.doc.Problem.Reason)
	}
	var tools []string
	for _, t := range s.doc.Tools {
		tools = append(tools, t.Name+" "+mark(t.Available, true))
	}
	daily := okStyle.Render("已装好") + dimStyle.Render("（只能唤起 porter 的日常模式）")
	admin := dimStyle.Render("没有创建（每次管理操作输入服务器密码）；按 a 了解与创建")
	switch {
	case !m.svc.Tiered(s.host):
		admin = dimStyle.Render("改用 Valise 钥匙后才能创建")
	case m.svc.HasAdminKey(s.host):
		admin = okStyle.Render("已创建") + dimStyle.Render("（按 a 移除）")
	}
	switch {
	case !m.svc.Tiered(s.host):
		daily = warnStyle.Render("用你自己的 SSH 设置连接") + dimStyle.Render("：你的钥匙能做任何事；按 m 改用 Valise 钥匙，日常只能做安全的操作")
	case !m.svc.HasDailyKey(s.host):
		daily = warnStyle.Render("这台电脑还没有日常钥匙；按 K 装上（需要服务器密码登录一次）")
	case !m.svc.DailyTTY(s.host):
		daily = warnStyle.Render("日常钥匙是旧格式：登录授权仍需开发者模式；按 K 用服务器密码重新装一次即可")
	}
	body := kv("服务器名", name, "SSH 别名", s.host, "系统", s.doc.Platform.String(), "porter", porter,
		"可用工具", strings.Join(tools, "  "), "连接方式", daily, "管理钥匙", admin, "你的钥匙", keyState)
	return box(s.host, body, w) + "\n" + dimStyle.Render("porter 是 Valise 放在服务器上的小程序，只在你连接时由 SSH 唤起，不常驻、不开端口。")
}

// nameServer asks for the server name (its cloud backup folder, set once)
// and records it; then runs after it is saved.
func nameServer(m *Model, host string, then func(m *Model) tea.Cmd) tea.Cmd {
	return m.push(newInput("给 "+host+" 起名：仓库和空间站里的文件夹名，只能用小写字母、数字和连字符；之后可以改名", host, false,
		func(m *Model, v string) tea.Cmd {
			name := strings.TrimSpace(v)
			if name == "" {
				name = host
			}
			m.pop()
			svc, ctx, load := m.svc, m.ctx, m.loadServer(host)
			return func() tea.Msg {
				if err := svc.ServerNameSet(ctx, host, name); err != nil {
					return failed(err)
				}
				return thenMsg{load().(srvLoaded), host + " 的服务器名是 " + name, then}
			}
		}))
}

// migrate moves a server imported from ~/.ssh/config to Valise's keys
// (docs/UI.md 9.4 "服务器设置").
func (s *serverSettings) migrate(m *Model) tea.Cmd {
	host := s.host
	if m.svc.Tiered(host) {
		return func() tea.Msg { return flashMsg(host + " 已经在用 Valise 钥匙") }
	}
	return m.push(pick("改用 Valise 钥匙", []choice{
		{label: "只改用日常钥匙", value: "daily", desc: "管理操作每次输入服务器密码（或用你自己的私钥登录）"},
		{label: "同时创建管理钥匙", value: "admin", desc: "设一个管理口令，解锁后一段时间内不再询问"},
	}, func(m *Model, v string) tea.Cmd {
		args := []string{"server", "migrate", host}
		if v == "admin" {
			args = append(args, "--admin-key")
		}
		return m.push(confirm("把 "+host+" 改用 Valise 钥匙？", []string{
			"现在你的钥匙能在服务器上做任何事。改用后，Valise 日常只用一把受限的钥匙：看状态、建逃生舱、打开管理页；拿不到 shell。",
			"安装、恢复、删除周期等敏感操作，要再出示一次管理登录。",
			"会用你现在的登录方式在服务器上写入钥匙；你自己的 ~/.ssh/config 不会改动。"},
			func(m *Model) tea.Cmd { return m.cliThen(tea.Batch(s.init(m), m.loadServer(host)), false, args...) }))
	}))
}

// adminKey explains the admin key, then creates or removes it.
func (s *serverSettings) adminKey(m *Model) tea.Cmd {
	host := s.host
	if !m.svc.Tiered(host) {
		return func() tea.Msg {
			return flashMsg(host + " 用你自己的 SSH 设置连接；先按 m 改用 Valise 钥匙，才能创建管理钥匙")
		}
	}
	if m.svc.HasAdminKey(host) {
		return m.push(confirm("移除 "+host+" 的管理钥匙？", []string{
			"服务器上和这台电脑上的管理钥匙都会删掉。之后每次管理操作都要在终端里输入服务器密码。",
			"随时可以重新创建。"},
			func(m *Model) tea.Cmd { return m.cliThen(s.init(m), false, "server", "admin-key", host, "--remove") }))
	}
	return m.push(confirm("管理钥匙", adminKeyIntro(m, host), func(m *Model) tea.Cmd {
		return m.cliThen(s.init(m), false, "server", "admin-key", host)
	}))
}

// adminKeyIntro explains daily and admin logins (also the guide's page).
func adminKeyIntro(m *Model, host string) []string {
	return []string{
		"Valise 平时用「日常钥匙」连服务器：它只能做安全的事，看状态、建逃生舱、打开管理页。",
		"敏感的事（安装、恢复、删除周期、原生界面）要再出示一次「管理登录」，有两种：",
		"  · 每次输服务器密码：默认，什么都不用准备，但要切到终端里输入。",
		"  · 管理钥匙：用单独的管理口令加密（和 Valise 口令不同）。解锁一次，" + itoa(int(m.svc.AdminWindow().Minutes())) + " 分钟内不再询问，在终端界面里就能完成；随时可以移除。",
		"创建时会在终端里用服务器密码登录一次，把管理钥匙装到 " + host + " 上。确认就创建。",
	}
}
