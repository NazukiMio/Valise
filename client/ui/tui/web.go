package tui

import tea "charm.land/bubbletea/v2"

// loginPicker lists the profile's login methods and runs the chosen one in
// the terminal (docs/LOGIN_RELAY.md); the TUI is paused meanwhile.
func loginPicker(m *Model, p *instancePage) tea.Cmd {
	if p.target == nil || len(p.target.Profile.Login) == 0 {
		return func() tea.Msg { return flashMsg("这个 agent 的 profile 没有声明登录方式") }
	}
	var cs []choice
	for _, l := range p.target.Profile.Login {
		cs = append(cs, choice{label: l.DisplayName, value: l.ID, desc: loginKindText(l.Kind)})
	}
	host, inst := p.host, p.inst.InstanceID
	return m.push(pick("登录授权：服务器没有浏览器，Valise 把登录页面转到你这台电脑上完成", cs, func(m *Model, id string) tea.Cmd {
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
