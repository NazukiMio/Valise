package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/enroll"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// The "第一次使用" checklist and its short guides (docs/UI.md 9.1). A guide
// is two or three pages (what it is, when to use it, what this step will
// and will not do), then the real page where the user does it once.

// guideFacts gathers what the checklist is ticked from.
func guideFacts(m *Model, hosts []string) service.GuideFacts {
	f := service.GuideFacts{Servers: len(hosts), Status: map[string]*vaultrpc.StatusResult{}}
	for _, h := range hosts {
		d := m.server(h)
		f.Agents += len(live(d.instances))
		f.Status[h] = d.status
	}
	return f
}

// guideCard is the home page's three-line card, "" when hidden or done.
func guideCard(m *Model, hosts []string, w int) string {
	if m.svc.LoadGuide().Hidden {
		return ""
	}
	items := m.svc.Guide(guideFacts(m, hosts))
	done, required, next := service.GuideProgress(items)
	if next == nil {
		return ""
	}
	cells := 20
	n := done * cells / required
	bar := okStyle.Render(strings.Repeat("█", n)) + faintStyle.Render(strings.Repeat("░", cells-n))
	body := accStyle.Render("第一次使用") + "  " + dimStyle.Render("已完成 "+itoa(done)+" / "+itoa(required)) + "   " + bar + "\n" +
		dimStyle.Render("下一步：") + next.Title + "    " + keyStyle.Render("g") + dimStyle.Render(" 打开清单")
	return box("", body, w)
}

// firstAgent is the first enrolled agent on the listed servers.
func firstAgent(m *Model, hosts []string) (string, enroll.InstanceSummary, bool) {
	for _, h := range hosts {
		if ins := live(m.server(h).instances); len(ins) > 0 {
			return h, ins[0], true
		}
	}
	return "", enroll.InstanceSummary{}, false
}

// checklistScreen lists the items; enter starts one.
type checklistScreen struct {
	hosts  []string
	cursor int
}

func newChecklist(hosts []string) *checklistScreen { return &checklistScreen{hosts: hosts} }

func (c *checklistScreen) title() string { return "第一次使用" }
func (c *checklistScreen) help() string {
	return menu("↑↓", "选择", "enter", "开始这一项", "x", "跳过 / 取消跳过", "h", "首页显示 / 不显示")
}
func (c *checklistScreen) init(m *Model) tea.Cmd { return nil }

func (c *checklistScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	items := m.svc.Guide(guideFacts(m, c.hosts))
	if cur, moved := moveList(c.cursor, len(items), k.String()); moved {
		c.cursor = cur
		return nil, true
	}
	it := items[min(c.cursor, len(items)-1)]
	switch k.String() {
	case "enter":
		return m.push(guideFor(m, c.hosts, it)), true
	case "x":
		if !it.Done {
			_ = m.svc.GuideMark(it.ID, false, true)
		}
		return nil, true
	case "h":
		g := m.svc.LoadGuide()
		g.Hidden = !g.Hidden
		_ = m.svc.SaveGuide(g)
		m.flash, m.flashErr = "首页不再显示清单；在这里或 设置 › 关于 里随时能打开", false
		if !g.Hidden {
			m.flash = "首页会显示清单"
		}
		return nil, true
	}
	return nil, false
}

func (c *checklistScreen) view(m *Model) string {
	items := m.svc.Guide(guideFacts(m, c.hosts))
	var b strings.Builder
	b.WriteString("每一项都可以跳过，也可以之后再回来。做完的会自动打勾。\n\n")
	for i, it := range items {
		mark := warnStyle.Render("○")
		switch {
		case it.Done:
			mark = okStyle.Render("✓")
		case it.Skipped:
			mark = faintStyle.Render("–")
		case it.Optional:
			mark = dimStyle.Render("○")
		}
		desc := it.Desc
		if it.Optional {
			desc = "可选 · " + desc
		}
		if it.Skipped {
			desc = "已跳过 · " + desc
		}
		line := mark + " " + padRight(it.Title, 18) + dimStyle.Render(desc)
		b.WriteString("  " + cursorLine(i == c.cursor, line) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("做完前五项，Valise 的基本用法就走过一遍了。后两项是可选的，读一读就能勾上。"))
	return b.String()
}

// guideScreen pages through a guide, then does its step.
type guideScreen struct {
	id, name string
	pages    []string
	page     int
	// then runs after the last page (the real page, or nothing).
	then func(m *Model) tea.Cmd
}

func (g *guideScreen) title() string { return g.name }
func (g *guideScreen) help() string {
	if g.page < len(g.pages)-1 {
		return menu("enter", "下一步", "b", "上一步", "esc", "稍后再做")
	}
	return menu("enter", "开始", "b", "上一步", "esc", "稍后再做")
}
func (g *guideScreen) init(m *Model) tea.Cmd { return nil }

func (g *guideScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "enter":
		if g.page < len(g.pages)-1 {
			g.page++
			return nil, true
		}
		_ = m.svc.GuideMark(g.id, true, false)
		m.pop()
		if g.then == nil {
			return nil, true
		}
		return g.then(m), true
	case "b":
		g.page = max(0, g.page-1)
		return nil, true
	}
	return nil, false
}

func (g *guideScreen) view(m *Model) string {
	head := accStyle.Render(g.name) + dimStyle.Render("   "+itoa(g.page+1)+" / "+itoa(len(g.pages)))
	return head + "\n\n" + g.pages[g.page]
}

// guideFor builds the guide of one item.
func guideFor(m *Model, hosts []string, it service.GuideItem) screen {
	g := &guideScreen{id: it.ID, name: it.Title}
	host, in, hasAgent := firstAgent(m, hosts)
	needAgent := func(m *Model) tea.Cmd {
		return func() tea.Msg { return flashMsg("先在服务器上安装或接入一个 agent（清单第二项）") }
	}
	switch it.ID {
	case service.GuidePrep:
		g.pages = []string{"准备工作包括：安装 harness 资料包、添加第一台服务器。加密钥匙在第一次建立逃生舱时再设。\n\n" +
			"还没做完的，可以在 设置 里补上：更新（资料包）、服务器。"}
	case service.GuideFirstAgent:
		g.pages = []string{
			"harness 是 agent 的「身体」，比如 OpenClaw、Hermes。agent 是它里面那个有记忆、有设置的助手。\n\n" +
				"Valise 有两种方式让一个 agent 进来：\n" +
				"  · 安装：在服务器上按官方方案装一个新的 harness，装好后直接接入。\n" +
				"  · 接入：服务器上已经装好的 agent，Valise 只读取和记录，不改动它的任何文件。",
			"接下来会打开服务器页。\n\n按 " + keyStyle.Render("i") + " 安装 harness，或按 " + keyStyle.Render("n") + " 把已有的 agent 接入 Valise。",
		}
		if len(hosts) > 0 {
			h := hosts[0]
			g.then = func(m *Model) tea.Cmd { return m.push(newServerPage(h)) }
		}
	case service.GuideFirstPod:
		g.pages = []string{
			"一个逃生舱保存所选内容在此刻的状态：记忆、设置、会话等；是否包含凭据和工作区，由你选择的备份内容决定。\n" +
				"它用你的钥匙加密后存在服务器上，只能追加，不会被覆盖或悄悄删掉。\n\n" +
				"升级 harness、让 agent 做大改动、或者只是想留个底的时候，建一个。\n" +
				"出了问题，可以把 agent 退回到任何一个逃生舱的样子（原地恢复），也能搬到另一台服务器。",
			"这一步会：为 " + in.InstanceID + " 建一个逃生舱，备份内容由你选（推荐\"标准\"，带凭据）。\n\n" +
				"不会：停下 agent，或改动它的任何文件。建立时 agent 照常运行。",
		}
		g.then = needAgent
		if hasAgent {
			g.then = func(m *Model) tea.Cmd {
				p := newInstancePage(host, in)
				return tea.Batch(m.push(p), startSeal(m, p))
			}
		}
	case service.GuideOffsite:
		g.pages = []string{
			"逃生舱默认存在服务器上。服务器坏了、被删了，它们也就没了。所以要在别处再存一份：\n\n" +
				"  · 本机仓库：这台电脑上的一个文件夹（外接硬盘、网盘同步文件夹都行）。只在 Valise 打开时更新。\n" +
				"  · 空间站：另一台机器（比如 NAS）。服务器可以直接把逃生舱发过去，连不上时经这台电脑中转。\n\n" +
				"两种可以同时用。复制只追加，不会删掉那边已有的东西。",
			"接下来会打开「仓库与空间站」页：按 " + keyStyle.Render("a") + " 添加，然后上云一次。",
		}
		g.then = needAgent
		if hasAgent {
			g.then = func(m *Model) tea.Cmd { return m.push(newCloud(host)) }
		}
	case service.GuideRestoreTest:
		g.pages = []string{
			"恢复测试确认逃生舱真的能打开：在这台电脑和服务器上各解开一次最新的逃生舱，逐个核对文件。\n\n" +
				"不会：切换 agent、停下服务、改动它的任何文件。结束后临时文件都会删掉。需要 Valise 口令。",
			"接下来会打开 " + in.InstanceID + " 的健康页，按 " + keyStyle.Render("t") + " 开始恢复测试。",
		}
		g.then = needAgent
		if hasAgent {
			g.then = func(m *Model) tea.Cmd {
				p := newInstancePage(host, in)
				for i, t := range p.tabs {
					if _, ok := t.(*healthTab); ok {
						p.active = i
					}
				}
				cmd := m.push(p)
				m.flash, m.flashErr = "按 t 开始恢复测试", false
				return cmd
			}
		}
	case service.GuideAdminKey:
		h := host
		if h == "" && len(hosts) > 0 {
			h = hosts[0]
		}
		g.pages = []string{strings.Join(adminKeyIntro(m, h), "\n\n"),
			"管理钥匙是可选的。要创建或移除：服务器设置 › 按 " + keyStyle.Render("a") + "。\n\n" +
				"从 ~/.ssh/config 导入的服务器用的是你自己的钥匙，要先在服务器设置里按 " + keyStyle.Render("m") + " 改用 Valise 钥匙。"}
		if h != "" {
			g.then = func(m *Model) tea.Cmd { return m.push(newServerSettings(h)) }
		}
	case service.GuideKit:
		g.pages = []string{
			"Valise Kit 给服务器补几个好用的工具，分三级：\n" +
				"  · 基础：porter 自带，什么都不用装。\n" +
				"  · 增强：rclone 等官方程序，Valise 下载并核对哈希后放到 ~/.local/bin，可以随时删掉。\n" +
				"  · 专家：rsync、linger 等系统级的东西，要在高级模式里执行。\n\n" +
				"在哪里：服务器设置 › Kit 工具（" + keyStyle.Render("t") + "）。",
			"高级模式让 Valise 代你装运行时和依赖，包括需要 sudo 的命令。默认关闭。\n\n" +
				"开启要过两道口令（Valise 口令和管理口令）并确认免责；明显有害的命令任何时候都不执行。\n" +
				"开启前，最近 7 天内要有一份逃生舱存在仓库或空间站。\n\n" +
				"在哪里：安装 harness 或 Kit 专家工具时，缺少依赖会提示开启。读完这一页就算完成。",
		}
	}
	return g
}
