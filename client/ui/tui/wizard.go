package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// wizardScreen is the first-run guide (docs/UI.md 9.1, 2026-11-03): the
// profile pack, the first server, then the first agent; the encryption key
// is set the first time it is needed (keysetup.go), or now on request.
type wizardScreen struct {
	step int
	err  error
}

const (
	wzWelcome = iota
	wzPack
	wzServer
	wzSteps
)

func newWizard() *wizardScreen { return &wizardScreen{} }

// needsWizard: a first run, with no key, no server, and the guide not yet
// finished or skipped.
func needsWizard(svc *service.Service) bool {
	if _, err := svc.KeyShow(); err == nil {
		return false
	}
	if hosts, _ := svc.Servers(); len(hosts) > 0 {
		return false
	}
	return !svc.LoadGuide().WizardDone
}

func (w *wizardScreen) title() string {
	return "第一次启动 · 第 " + itoa(w.step+1) + " / " + itoa(wzSteps) + " 步"
}
func (w *wizardScreen) help() string {
	switch w.step {
	case wzPack:
		return menu("p", "在线安装", "f", "从文件安装", "enter", "跳过")
	case wzServer:
		return menu("enter", "添加服务器", "s", "稍后再说")
	}
	return menu("enter", "开始", "k", "现在就设口令", "i", "我已经有钥匙（导入恢复套件）")
}

func (w *wizardScreen) init(m *Model) tea.Cmd { return nil }

func (w *wizardScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case packFetched:
		return installPackFlow(m, msg, func(m *Model) tea.Cmd { w.step = wzServer; return nil }), true
	case tea.KeyPressMsg:
		return w.key_(m, msg.String())
	}
	return nil, false
}

// finish leaves the guide for good; with a server, its page is next: the
// first agent is installed or enrolled there.
func (w *wizardScreen) finish(m *Model, host string) tea.Cmd {
	g := m.svc.LoadGuide()
	g.WizardDone = true
	_ = m.svc.SaveGuide(g)
	cmd := m.replaceRoot(newHome())
	if host == "" {
		return cmd
	}
	next := m.push(newServerPage(host))
	m.flash, m.flashErr = "最后一步：按 i 安装一个 harness，或按 n 把已经装好的 agent 接入 Valise", false
	return tea.Batch(cmd, next)
}

func (w *wizardScreen) key_(m *Model, k string) (tea.Cmd, bool) {
	switch w.step {
	case wzWelcome:
		switch k {
		case "enter":
			w.step = wzPack
			return nil, true
		case "k":
			return askPassphrase(m, func(m *Model, _ *service.KeyInfo) tea.Cmd {
				return offerKitBackup(m, func(m *Model) tea.Cmd { w.step = wzPack; return nil })
			}), true
		case "i":
			return m.push(newInput("恢复套件文件夹的路径（接下来会在终端里输入口令）", "", false, func(m *Model, dir string) tea.Cmd {
				return m.cliThen(func() tea.Msg {
					return contMsg{func(m *Model) tea.Cmd { w.step = wzPack; return nil }}
				}, false, "key", "import-kit", expandHome(m, strings.TrimSpace(dir)))
			})), true
		}
	case wzPack:
		switch k {
		case "p":
			m.flash, m.flashErr = "正在从 GitHub Release 下载资料包…", false
			svc, ctx := m.svc, m.ctx
			return func() tea.Msg { f, err := svc.FetchPack(ctx, ""); return packFetched{f, err} }, true
		case "f":
			return m.push(newInput("资料包文件路径", "valise-profiles.tar.gz", false, func(m *Model, p string) tea.Cmd {
				svc, p := m.svc, expandHome(m, strings.TrimSpace(p))
				return func() tea.Msg { f, err := svc.ReadPackFile(p); return packFetched{f, err} }
			})), true
		case "enter":
			w.step = wzServer
			return nil, true
		}
	case wzServer:
		switch k {
		case "enter":
			a := newAddServer(nil)
			a.onDone = func(m *Model) tea.Cmd { return w.finish(m, a.alias) }
			return m.push(a), true
		case "s":
			return w.finish(m, ""), true
		}
	}
	return nil, false
}

func (w *wizardScreen) view(m *Model) string {
	width := min(m.width-2, 90)
	var body string
	switch w.step {
	case wzWelcome:
		body = titleStyle.Render("欢迎使用 Valise") + "\n\n" +
			"Valise 在你的服务器上安装和管理 agent，并替它做好备份。\n" +
			"装好的 agent 会出现在这里：它是否在跑、怎么打开它的管理页、有没有要处理的事，一眼就能看到。\n\n" +
			dimStyle.Render("接下来几步：安装 harness 资料包 → 添加第一台服务器 → 安装或接入第一个 agent。\n"+
				"加密钥匙在你第一次建立备份时再设；想现在就设，按 k。")
	case wzPack:
		body = headStyle.Render("安装 harness 资料包") + "\n\n" +
			"资料包告诉 Valise 各种 agent（OpenClaw、Hermes 等）的文件放在哪里、怎么安装和启动。它单独发布、带官方签名，Valise 会先验签再安装。\n\n" +
			dimStyle.Render("也可以跳过，以后在 设置 › 更新 中安装。")
	case wzServer:
		body = headStyle.Render("添加第一台服务器") + "\n\n" +
			"可以手动填写地址、端口和用户名（不需要 SSH 配置），也可以从 ~/.ssh/config 导入。Valise 不保存任何密码。\n" +
			dimStyle.Render("接下来依次：检查服务器 → 部署 porter → 给服务器起名。之后就可以安装或接入 agent 了。")
	}
	if w.err != nil {
		body += "\n\n" + errStyle.Render(errText(w.err))
	}
	return box("", body, width)
}
