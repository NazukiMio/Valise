package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// settingsScreen manages the key (trust root) and server registration.
// Steps that read a passphrase run the CLI command with the TUI paused.
type settingsScreen struct {
	info   *service.KeyInfo
	err    error
	cursor int
}

func newSettings() *settingsScreen { return &settingsScreen{} }

type keyLoaded struct {
	info *service.KeyInfo
	err  error
}

var settingsActions = []choice{
	{"登记本机公钥到服务器（之后的封存加密给它）", "register"},
	{"从服务器移除一个公钥", "unregister"},
	{"导出离线恢复套件", "export-kit"},
	{"生成新密钥", "init"},
	{"导入已有的 age 身份", "import"},
	{"从离线恢复套件导入", "import-kit"},
}

func (s *settingsScreen) title() string { return "设置与信任根" }
func (s *settingsScreen) help() string  { return "↑↓ 选择  ·  enter 执行" }
func (s *settingsScreen) init(m *Model) tea.Cmd {
	return func() tea.Msg {
		info, err := m.svc.KeyShow()
		return keyLoaded{info, err}
	}
}

func (s *settingsScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case keyLoaded:
		s.info, s.err = msg.info, msg.err
		return nil, true
	case execDone:
		if msg.err != nil {
			m.flash, m.flashErr = "命令没有成功结束："+msg.err.Error(), true
		}
		return s.init(m), true
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			s.cursor = max(0, s.cursor-1)
		case "down", "j":
			s.cursor = min(len(settingsActions)-1, s.cursor+1)
		case "enter":
			return s.run(m, settingsActions[s.cursor].value), true
		default:
			return nil, false
		}
		return nil, true
	}
	return nil, false
}

func (s *settingsScreen) pickServer(m *Model, label string, f func(m *Model, host string) tea.Cmd) tea.Cmd {
	hosts, err := m.svc.Servers()
	if err != nil {
		return func() tea.Msg { return failed(err) }
	}
	var cs []choice
	for _, h := range hosts {
		cs = append(cs, choice{label: h, value: h})
	}
	return m.push(pick(label, cs, f))
}

func (s *settingsScreen) run(m *Model, action string) tea.Cmd {
	switch action {
	case "register":
		return s.pickServer(m, "登记到哪台服务器", func(m *Model, host string) tea.Cmd {
			return m.push(newInput("说明（写进服务器 recipients.txt 的注释，可留空）", "my laptop", false, func(m *Model, label string) tea.Cmd {
				return func() tea.Msg {
					res, _, err := m.svc.KeyRegister(m.ctx, host, strings.TrimSpace(label))
					if err != nil {
						return failed(err)
					}
					if !res.Added {
						return flashMsg("这把公钥已经是 " + host + " 的收件人")
					}
					return flashMsg("已登记到 " + host + "；之后的封存会加密给它")
				}
			}))
		})
	case "unregister":
		return s.pickServer(m, "从哪台服务器移除", func(m *Model, host string) tea.Cmd {
			return m.runCLI("key", "unregister", host)
		})
	case "export-kit":
		return m.push(newInput("导出到哪个目录（会新建）", "~/valise-recovery-kit", false, func(m *Model, dir string) tea.Cmd {
			if strings.TrimSpace(dir) == "" {
				return nil
			}
			return m.runCLI("key", "export-kit", strings.TrimSpace(dir))
		}))
	case "init":
		return m.runCLI("key", "init")
	case "import":
		return m.push(newInput("age 身份文件的路径", "identity.txt", false, func(m *Model, path string) tea.Cmd {
			return m.runCLI("key", "import", "--file", strings.TrimSpace(path))
		}))
	case "import-kit":
		return m.push(newInput("恢复套件目录", "", false, func(m *Model, dir string) tea.Cmd {
			return m.runCLI("key", "import-kit", strings.TrimSpace(dir))
		}))
	}
	return nil
}

func (s *settingsScreen) view(m *Model) string {
	var b strings.Builder
	switch {
	case s.info != nil:
		fmt.Fprintf(&b, "本机公钥：%s\n指纹：%s\n\n", s.info.Recipient, headStyle.Render(s.info.Fingerprint))
	case s.err != nil:
		b.WriteString(warnStyle.Render(strings.SplitN(errText(s.err), "\n", 2)[0]) + "\n\n")
	default:
		b.WriteString(dimStyle.Render("读取密钥…") + "\n\n")
	}
	for i, a := range settingsActions {
		b.WriteString(cursorLine(i == s.cursor, a.label) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("需要输入口令的操作会暂时切换到命令行，完成后回到这里。"))
	return b.String()
}
