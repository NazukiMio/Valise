package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// webTab opens the management page and runs logins (docs/UI.md 9.4,
// docs/LOGIN_RELAY.md 4.1). Both need the raw terminal, so they run the
// CLI while the TUI is paused.
type webTab struct{}

func (t *webTab) name() string { return "管理页与登录" }
func (t *webTab) help(p *instancePage) string {
	return menu("o", "打开管理页", "s", "打开服务器终端", "l", "登录授权")
}
func (t *webTab) load(m *Model, p *instancePage) tea.Cmd { return nil }

func (t *webTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "o":
		if p.target == nil || len(p.target.Profile.Entries) == 0 {
			return func() tea.Msg { return flashMsg("这个 agent 没有网页管理界面") }, true
		}
		return m.runCLI("open", p.host, p.inst.InstanceID), true
	case "s":
		return m.runCLI("shell", p.host), true
	case "l":
		return t.login(m, p), true
	}
	return nil, false
}

func (t *webTab) login(m *Model, p *instancePage) tea.Cmd {
	if p.target == nil || len(p.target.Profile.Login) == 0 {
		return func() tea.Msg { return flashMsg("这个 agent 的 profile 没有声明登录方式") }
	}
	var cs []choice
	for _, l := range p.target.Profile.Login {
		cs = append(cs, choice{label: l.DisplayName, value: l.ID, desc: loginKindText(l.Kind)})
	}
	host, inst := p.host, p.inst.InstanceID
	return m.push(pick("选择登录方式", cs, func(m *Model, id string) tea.Cmd {
		return m.runCLI("login", host, inst, "--method", id)
	}))
}

func loginKindText(kind string) string {
	switch kind {
	case "loopback_oauth":
		return "在你的浏览器里登录并授权"
	case "device_code":
		return "在浏览器里输入一个短码"
	case "paste_code":
		return "授权后把代码粘贴回来"
	}
	return ""
}

func (t *webTab) view(m *Model, p *instancePage, height int) string {
	w := min(m.width-2, 100)
	var b strings.Builder
	if p.target == nil {
		return dimStyle.Render("读取中…")
	}
	prof := p.target.Profile
	if len(prof.Entries) > 0 {
		auto := "需要令牌时，Valise 会用 " + prof.ID + " 自己的命令取得，直接放进浏览器地址（不显示，也不保存）。"
		hint := ""
		for _, e := range prof.Entries {
			if e.Auth == nil || (len(e.Auth.URLArgv) == 0 && len(e.Auth.TokenArgv) == 0) {
				auto = "如果页面要求令牌或密码，Valise 没法替你取得：按 s 打开服务器终端，运行 " + prof.ID + " 自己的命令获取，再粘贴到页面里。"
			}
			if e.Auth != nil && e.Auth.Hint != "" {
				hint = e.Auth.Hint
			}
		}
		body := prof.DisplayName + " 自带一个网页管理界面，运行在服务器上。Valise 把它安全地转到你这台电脑的浏览器里打开（只有本机能访问），30 分钟没有使用会自动关闭。\n\n" +
			auto + "\n\n" + keyStyle.Render("o") + " 打开管理页"
		if hint != "" {
			body += "\n\n" + dimStyle.Render("页面仍然要求令牌时："+hint)
		}
		b.WriteString(box("管理页", body, w) + "\n")
	} else {
		b.WriteString(box("管理页", dimStyle.Render(prof.DisplayName+" 没有网页管理界面。"), w) + "\n")
	}
	b.WriteString(box("服务器终端", "在服务器上打开一个终端，运行 harness 自己的命令（例如取令牌、查看日志）。Valise 不读取这个终端里的任何内容。\n\n"+keyStyle.Render("s")+" 打开服务器终端", w) + "\n")
	login := "有些 agent 需要你登录 AI 服务（例如用 ChatGPT 账号授权）。服务器上没有浏览器，Valise 把登录页面转到你这台电脑上完成，授权结果直接交回给服务器上的 agent。Valise 不保存任何登录信息。\n\n"
	if len(prof.Login) == 0 {
		login += dimStyle.Render("这个 agent 的 profile 没有声明登录方式。")
	} else {
		var names []string
		for _, l := range prof.Login {
			names = append(names, l.DisplayName)
		}
		login += "可用的方式：" + strings.Join(names, "、") + "\n\n" + keyStyle.Render("l") + " 开始登录授权"
	}
	b.WriteString(box("登录授权", login, w))
	return b.String()
}
