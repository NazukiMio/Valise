package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/valise"
	"valise/ui/service"
)

// addServerScreen adds a server: check → porter → register the key →
// name → record (docs/ARCHITECTURE.md section 3, "添加服务器").
type addServerScreen struct {
	alias   string
	name    string
	deploy  service.DeploySource
	steps   []service.SetupStep
	running bool
	done    bool
	err     error
	onDone  func(m *Model) tea.Cmd
}

type setupStepMsg struct{ s service.SetupStep }
type setupDone struct{ err error }

func newAddServer(onDone func(m *Model) tea.Cmd) *addServerScreen {
	return &addServerScreen{onDone: onDone}
}

var setupLabels = []struct{ step, label string }{
	{service.SetupCheck, "检查服务器"},
	{service.SetupDeploy, "部署 porter（服务器上的小程序，只在连接时运行，不常驻、不开端口）"},
	{service.SetupRegister, "登记你的钥匙（服务器只拿到\"只能上锁\"的那一半）"},
	{service.SetupName, "服务器名（仓库和空间站里的文件夹名）"},
	{service.SetupRecord, "记录到这台电脑"},
}

func (a *addServerScreen) title() string { return "添加服务器" }
func (a *addServerScreen) help() string {
	switch {
	case a.running:
		return dimStyle.Render("正在添加…")
	case a.done:
		return menu("enter", "完成")
	case a.err != nil:
		return menu("enter", "重试", "esc", "取消")
	}
	return menu("enter", "开始")
}

func (a *addServerScreen) busy() string {
	if a.running {
		return "正在添加服务器，请等它结束"
	}
	return ""
}

func (a *addServerScreen) init(m *Model) tea.Cmd { return a.chooseAlias(m) }

func (a *addServerScreen) chooseAlias(m *Model) tea.Cmd {
	hosts, err := m.svc.ImportableHosts()
	if err != nil {
		return func() tea.Msg { return failed(err) }
	}
	var cs []choice
	for _, h := range hosts {
		cs = append(cs, choice{label: h, value: h})
	}
	cs = append(cs, choice{label: "手动输入 SSH 别名…", value: ""})
	return m.push(pick("选择要添加的服务器（来自 ~/.ssh/config）", cs, func(m *Model, v string) tea.Cmd {
		if v == "" {
			return m.push(newInput("SSH 别名（~/.ssh/config 中的 Host，或 user@host）", "my-server", false, func(m *Model, v string) tea.Cmd {
				return a.chooseName(m, strings.TrimSpace(v))
			}))
		}
		return a.chooseName(m, v)
	}))
}

func (a *addServerScreen) chooseName(m *Model, alias string) tea.Cmd {
	if alias == "" {
		m.pop()
		return nil
	}
	a.alias = alias
	in := newInput("给服务器起个名字。它会成为仓库和空间站里的文件夹名（valise-vault/<名字>/…），起好后不能改。直接回车就用 "+alias, alias, false,
		func(m *Model, v string) tea.Cmd {
			a.name = strings.TrimSpace(v)
			if a.name == "" {
				a.name = alias
			}
			return a.start(m)
		})
	return m.push(in)
}

func (a *addServerScreen) start(m *Model) tea.Cmd {
	a.steps, a.err, a.running, a.done = nil, nil, true, false
	svc, ctx, send := m.svc, m.ctx, m.send
	alias, opts := a.alias, service.SetupOptions{Name: a.name, Deploy: a.deploy}
	return func() tea.Msg {
		opts.Step = func(s service.SetupStep) { send(setupStepMsg{s}) }
		return setupDone{svc.SetupServer(ctx, alias, opts)}
	}
}

func (a *addServerScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case setupStepMsg:
		a.steps = append(a.steps, msg.s)
		return nil, true
	case setupDone:
		a.running, a.err = false, msg.err
		if msg.err == nil {
			a.done = true
			return m.loadServer(a.alias), true
		}
		if ue, ok := valise.As(msg.err); ok && ue.Code == "setup_need_porter" {
			return a.chooseDeploy(m), true
		}
		return nil, true
	case tea.KeyPressMsg:
		if msg.String() != "enter" || a.running {
			return nil, a.running
		}
		switch {
		case a.done:
			m.pop()
			if a.onDone != nil {
				return a.onDone(m), true
			}
			return nil, true
		case a.alias == "":
			return a.chooseAlias(m), true
		default:
			return a.start(m), true
		}
	}
	return nil, false
}

func (a *addServerScreen) chooseDeploy(m *Model) tea.Cmd {
	return m.push(pick("服务器上还没有 porter。选择它的来源：", []choice{
		{label: "从 GitHub Release 下载", value: "release", desc: "校验签名与 SHA-256"},
		{label: "上传本地构建的文件", value: "local", desc: "你自己构建的 valise-porter"},
	}, func(m *Model, v string) tea.Cmd {
		label, ph := "Release 版本号", "v0.1.0"
		if v == "local" {
			label, ph = "本地 porter 文件路径", "./valise-porter_linux_amd64"
		}
		return m.push(newInput(label, ph, false, func(m *Model, s string) tea.Cmd {
			a.deploy = service.DeploySource{}
			if v == "local" {
				a.deploy.LocalPath = strings.TrimSpace(s)
			} else {
				a.deploy.ReleaseTag = strings.TrimSpace(s)
			}
			return a.start(m)
		}))
	}))
}

func (a *addServerScreen) view(m *Model) string {
	if a.alias == "" {
		return "选择或输入一台服务器。"
	}
	var b strings.Builder
	b.WriteString("添加 " + headStyle.Render(a.alias) + "\n\n")
	done := map[string]service.SetupStep{}
	for _, s := range a.steps {
		done[s.Step] = s
	}
	next := true
	for _, l := range setupLabels {
		s, ok := done[l.step]
		switch {
		case ok:
			b.WriteString(okStyle.Render("✓ ") + l.label)
			if s.Detail != "" {
				b.WriteString(dimStyle.Render("　" + s.Detail))
			}
		case next && a.running:
			b.WriteString(accStyle.Render("▸ ") + l.label)
			next = false
		case next && a.err != nil:
			b.WriteString(errStyle.Render("✗ ") + l.label)
			next = false
		default:
			b.WriteString(faintStyle.Render("○ ") + dimStyle.Render(l.label))
		}
		b.WriteString("\n")
	}
	if a.err != nil {
		b.WriteString("\n" + errStyle.Render(errText(a.err)))
	}
	if a.done {
		b.WriteString("\n" + okStyle.Render("已添加。进入服务器后按 n 把上面的 agent 接入 Valise。"))
	}
	return b.String()
}
