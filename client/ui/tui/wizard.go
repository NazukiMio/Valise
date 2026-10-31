package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"valise/core/valise"
	"valise/ui/service"
)

// wizardScreen is the first-run guide when there is no key (docs/UI.md
// 9.1): passphrase → back up the key → profile pack → first server.
type wizardScreen struct {
	step int
	key  *service.KeyInfo
	err  error
}

const (
	wzWelcome = iota
	wzKey
	wzBackup
	wzPack
	wzServer
)

type keyCreated struct {
	k   *service.KeyInfo
	err error
}

func newWizard() *wizardScreen { return &wizardScreen{} }

func (w *wizardScreen) title() string { return "第一次启动 · 第 " + itoa(w.step+1) + " / 5 步" }
func (w *wizardScreen) help() string {
	switch w.step {
	case wzKey:
		return menu("enter", "设口令", "i", "我已经有钥匙（导入恢复套件）")
	case wzBackup:
		return menu("b", "现在备份（推荐）", "enter", "稍后再说")
	case wzPack:
		return menu("p", "在线安装", "f", "从文件安装", "enter", "跳过")
	case wzServer:
		return menu("enter", "添加服务器", "s", "稍后再说")
	}
	return menu("enter", "开始")
}

func (w *wizardScreen) init(m *Model) tea.Cmd { return nil }

func (w *wizardScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case keyCreated:
		w.key, w.err = msg.k, msg.err
		if msg.err == nil {
			w.step = wzBackup
		}
		return nil, true
	case packFetched:
		return installPackFlow(m, msg, func(m *Model) tea.Cmd { w.step = wzServer; return nil }), true
	case execDone:
		if k, err := m.svc.KeyShow(); err == nil {
			w.key, w.step = k, wzPack
		}
		return nil, true
	case tea.KeyPressMsg:
		return w.key_(m, msg.String())
	}
	return nil, false
}

func (w *wizardScreen) key_(m *Model, k string) (tea.Cmd, bool) {
	switch w.step {
	case wzWelcome:
		if k == "enter" {
			w.step = wzKey
			return nil, true
		}
	case wzKey:
		switch k {
		case "enter":
			return w.askPassphrase(m), true
		case "i":
			return m.push(newInput("恢复套件文件夹的路径（接下来会在终端里输入口令）", "", false, func(m *Model, dir string) tea.Cmd {
				return m.runCLI("key", "import-kit", expandHome(m, strings.TrimSpace(dir)))
			})), true
		}
	case wzBackup:
		switch k {
		case "b":
			return m.push(newInput("把恢复套件导出到哪个文件夹？（建议 U 盘或密码管理器；文件夹必须是空的或不存在）", "~/Desktop/valise-恢复套件", false,
				func(m *Model, dir string) tea.Cmd {
					dir = expandHome(m, strings.TrimSpace(dir))
					if _, err := m.svc.ExportKit(dir); err != nil {
						return func() tea.Msg { return failed(err) }
					}
					w.step = wzPack
					m.flash, m.flashErr = "已导出到 "+dir, false
					return nil
				})), true
		case "enter":
			w.step = wzPack
			return nil, true
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
			return m.push(newAddServer(func(m *Model) tea.Cmd { return m.replaceRoot(newHome()) })), true
		case "s":
			return m.replaceRoot(newHome()), true
		}
	}
	return nil, false
}

// askPassphrase asks twice; the value lives only in this closure.
func (w *wizardScreen) askPassphrase(m *Model) tea.Cmd {
	return m.push(newInput("设一个口令（至少 12 个字符，可以是一句你记得住的话）", "", true, func(m *Model, first string) tea.Cmd {
		if utf8.RuneCountInString(first) < 12 {
			w.err = valise.New("passphrase_short", "口令至少 12 个字符", "换一句更长的话，再按回车重新设置")
			return nil
		}
		return m.push(newInput("再输一次口令", "", true, func(m *Model, second string) tea.Cmd {
			if second != first {
				w.err = valise.New("passphrase_mismatch", "两次输入的口令不一致", "按回车重新设置")
				return nil
			}
			w.err = nil
			svc := m.svc
			return func() tea.Msg { k, err := svc.KeyInit(first); return keyCreated{k, err} }
		}))
	}))
}

func (w *wizardScreen) view(m *Model) string {
	width := min(m.width-2, 90)
	var body string
	switch w.step {
	case wzWelcome:
		body = titleStyle.Render("欢迎使用 Valise") + "\n\n" +
			"Valise 给你服务器上的 agent 建立\"逃生舱\"：把它的记忆、人格、技能和配置加密保存下来。\n" +
			"agent 出问题、服务器坏了、想换一台机器时，可以让它从逃生舱着陆，回到原来的样子。\n\n" +
			dimStyle.Render("接下来几步：设一个口令 → 备份你的钥匙 → 安装 harness 资料包 → 添加第一台服务器。")
	case wzKey:
		body = headStyle.Render("设一个口令") + "\n\n" +
			"逃生舱用一把钥匙加密，这个口令用来保护这把钥匙。Valise 不保存口令，" + warnStyle.Render("口令丢了，以前的逃生舱就打不开了。") + "\n\n" +
			dimStyle.Render("在别的电脑上已经有钥匙？按 i 导入恢复套件，不用新建。")
	case wzBackup:
		body = okStyle.Render("✓ 钥匙已生成") + "\n\n" + kv("保存在", w.key.Path, "指纹", w.key.Fingerprint) + "\n\n" +
			"这台电脑坏了或丢了，钥匙也就没了。" + headStyle.Render("强烈建议现在备份一份") + "：导出一个\"恢复套件\"文件夹，放进 U 盘或密码管理器。\n" +
			"换电脑时导入它并输入口令，就能打开以前所有的逃生舱。"
	case wzPack:
		body = headStyle.Render("安装 harness 资料包") + "\n\n" +
			"资料包告诉 Valise 各种 agent（OpenClaw、Hermes 等）的文件放在哪里、怎么启动。它单独发布、带官方签名，Valise 会先验签再安装。\n\n" +
			dimStyle.Render("也可以跳过，以后在 设置 › 更新 中安装。")
	case wzServer:
		body = headStyle.Render("添加第一台服务器") + "\n\n" +
			"Valise 通过你电脑上已经配置好的 SSH 连接服务器，不保存任何密码或私钥。\n" +
			dimStyle.Render("接下来依次：检查服务器 → 部署 porter → 登记你的钥匙 → 给服务器起名。")
	}
	if w.err != nil {
		body += "\n\n" + errStyle.Render(errText(w.err))
	}
	return box("", body, width)
}
