package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/kittools"
	"valise/ui/service"
)

// kitToolsScreen lists the Valise Kit tools of one server (docs/AGENT_KIT.md
// 8.4.1): whether it has them, what for, what happens without. Enhanced
// tools install here; expert ones run in the terminal in advanced mode.
type kitToolsScreen struct {
	host   string
	list   []service.KitTool
	err    error
	cursor int
}

type kitToolsLoaded struct {
	list []service.KitTool
	err  error
}

func newKitTools(host string) *kitToolsScreen { return &kitToolsScreen{host: host} }

func (k *kitToolsScreen) title() string    { return "Kit 工具" }
func (k *kitToolsScreen) hostName() string { return k.host }
func (k *kitToolsScreen) help() string {
	return menu("↑↓", "选择", "enter", "安装", "d", "卸载", "r", "刷新")
}

func (k *kitToolsScreen) init(m *Model) tea.Cmd {
	svc, ctx, host := m.svc, m.ctx, k.host
	return func() tea.Msg { l, err := svc.KitTools(ctx, host); return kitToolsLoaded{l, err} }
}

func (k *kitToolsScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case kitToolsLoaded:
		k.list, k.err = msg.list, msg.err
		return nil, true
	case tea.KeyPressMsg:
		if cur, moved := moveList(k.cursor, len(k.list), msg.String()); moved {
			k.cursor = cur
			return nil, true
		}
		if k.cursor >= len(k.list) {
			return nil, false
		}
		t := k.list[k.cursor]
		switch msg.String() {
		case "r":
			return k.init(m), true
		case "enter":
			return k.install(m, t), true
		case "d":
			return k.uninstall(m, t), true
		}
	}
	return nil, false
}

func (k *kitToolsScreen) install(m *Model, t service.KitTool) tea.Cmd {
	host, id := k.host, t.ID
	switch {
	case t.Present:
		m.flash, m.flashErr = t.Name+" 已经有了", false
		return nil
	case t.Why != "":
		m.flash, m.flashErr = t.Name+" 不能由 Valise 安装："+t.Why+"；可以在服务器终端里自己装", false
		return nil
	case t.Tier == kittools.TierExpert:
		// Advanced mode asks for both passphrases in the terminal.
		return gateThen(m, host, "", func(m *Model) tea.Cmd {
			return m.cliThen(k.init(m), true, "kit", "tool", "install", host, id)
		})
	}
	return m.push(confirm("下载 "+t.Name+" 的官方版本并装到服务器？", []string{
		"核对清单记录的 SHA-256 后写到 ~/.local/bin/" + t.Check.Binary + "；不会覆盖不是 Valise 装的同名文件。"},
		func(m *Model) tea.Cmd {
			svc, ctx := m.svc, m.ctx
			m.flash, m.flashErr = "正在下载并安装 "+t.Name+"…", false
			return func() tea.Msg {
				if _, err := svc.InstallKitBinary(ctx, host, id); err != nil {
					return failed(err)
				}
				m.send(flashMsg("已安装 " + t.Name))
				l, err := svc.KitTools(ctx, host)
				return kitToolsLoaded{l, err}
			}
		}))
}

func (k *kitToolsScreen) uninstall(m *Model, t service.KitTool) tea.Cmd {
	host, id := k.host, t.ID
	switch {
	case !t.Present:
		return nil
	case t.Tier == kittools.TierExpert && t.Uninstall == nil:
		m.flash, m.flashErr = "Valise 不卸载 "+t.Name+"（系统包可能被别的程序使用）", false
		return nil
	case t.Tier == kittools.TierExpert:
		return gateThen(m, host, "", func(m *Model) tea.Cmd {
			return m.cliThen(k.init(m), true, "kit", "tool", "uninstall", host, id)
		})
	case !t.Ours:
		m.flash, m.flashErr = t.Name+" 不是 Valise 装的，Valise 不删除它", false
		return nil
	}
	return m.push(confirm("删除 Valise 装的 "+t.Name+"？", nil, func(m *Model) tea.Cmd {
		svc, ctx := m.svc, m.ctx
		return func() tea.Msg {
			if err := svc.RemoveKitBinary(ctx, host, id); err != nil {
				return failed(err)
			}
			m.send(flashMsg("已删除 " + t.Name))
			l, err := svc.KitTools(ctx, host)
			return kitToolsLoaded{l, err}
		}
	}))
}

var kitTierLabel = map[string]string{kittools.TierEnhanced: "增强", kittools.TierExpert: "专家 · 高级模式"}

func (k *kitToolsScreen) view(m *Model) string {
	switch {
	case k.err != nil:
		return errStyle.Render(errText(k.err))
	case k.list == nil:
		return dimStyle.Render("正在检查 " + k.host + " …")
	}
	w := min(m.width-2, 100)
	var rows []string
	for i, t := range k.list {
		state := warnStyle.Render("没有")
		if t.Present {
			state = okStyle.Render("已有")
			if t.Ours {
				state += dimStyle.Render("（Valise 装的）")
			}
		}
		rows = append(rows, cursorLine(i == k.cursor, padRight(t.Name, 14)+padRight(state, 10)+dimStyle.Render(kitTierLabel[t.Tier])))
	}
	t := k.list[min(k.cursor, len(k.list)-1)]
	detail := []string{"用途：" + t.Purpose, "不装会怎样：" + t.Without}
	switch {
	case t.Present:
	case t.Why != "":
		detail = append(detail, warnStyle.Render("不能由 Valise 安装："+t.Why), dimStyle.Render("可以在服务器终端里自己装（管理 › 打开这台服务器的终端）。"))
	case t.Tier == kittools.TierExpert:
		detail = append(detail, "安装命令："+strings.Join(t.Command, " "), dimStyle.Render("按 enter 在终端里开启高级模式执行（需要 Valise 口令与管理口令，sudo 密码在终端里输入）。"))
	default:
		detail = append(detail, dimStyle.Render("按 enter 下载官方版本，核对 SHA-256 后装到 ~/.local/bin。"))
	}
	head := dimStyle.Render("只有 Valise 资料包里审核过的工具；每一项都由你决定装不装。")
	return head + "\n\n" + strings.Join(rows, "\n") + "\n\n" + box(t.Name, strings.Join(detail, "\n"), w)
}
