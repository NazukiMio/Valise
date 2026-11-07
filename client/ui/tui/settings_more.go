package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/agentkit"
	"valise/ui/service"
)

func (s *settingsScreen) updateView(m *Model, w int) string {
	var b strings.Builder
	b.WriteString(box("Valise 客户端", kv("当前版本", m.svc.Version), w) + "\n")
	var porters []string
	for _, h := range s.hosts {
		v := dimStyle.Render("检查中…")
		if d := s.doctors[h]; d != nil {
			v = warnStyle.Render("没有可用的 porter")
			if d.PorterFound {
				v = d.PorterVersion
			}
		}
		porters = append(porters, padRight(h, 20)+v)
	}
	b.WriteString(box("服务器上的 porter", strings.Join(porters, "\n")+"\n"+dimStyle.Render("在 服务器 › 服务器设置 中更新（从 GitHub Release 下载并验签）。"), w) + "\n")
	pack := dimStyle.Render("读取中…")
	if st := s.pack; st != nil {
		switch {
		case st.Pack != nil:
			var ps []string
			for _, p := range st.Pack.Profiles {
				ps = append(ps, p.ID+" v"+itoa(p.ProfileVersion))
			}
			pack = okStyle.Render("已安装 "+st.Pack.Version+"（已验签）") + "\n" + strings.Join(ps, " · ")
		case st.PackProblem != "":
			pack = errStyle.Render("不可用：" + st.PackProblem)
		default:
			pack = warnStyle.Render("还没有安装")
		}
		if len(st.Local) > 0 {
			pack += "\n" + dimStyle.Render("本地 harness 资料（未经审核）："+strings.Join(st.Local, "、"))
		}
	}
	b.WriteString(box("harness 资料包", pack+"\n"+keyStyle.Render("p")+" 在线更新   "+keyStyle.Render("f")+" 从文件安装", w) + "\n")
	var kits []string
	ids := make([]string, 0, len(s.kits))
	for id := range s.kits {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		k := s.kits[id]
		state := dimStyle.Render("未安装")
		if k != nil && k.Dir != "" {
			cur, mod, miss := 0, 0, 0
			for _, sk := range k.Skills {
				switch sk.State {
				case agentkit.SkillCurrent:
					cur++
				case agentkit.SkillModified:
					mod++
				default:
					miss++
				}
			}
			switch {
			case cur == len(k.Skills):
				state = okStyle.Render("最新")
			case cur+mod == 0:
				state = dimStyle.Render("未安装")
			case mod > 0:
				state = warnStyle.Render(itoa(mod) + " 个被改过")
			default:
				state = warnStyle.Render("有新版本")
			}
		}
		kits = append(kits, padRight(id, 32)+state)
	}
	if len(kits) == 0 {
		kits = append(kits, dimStyle.Render("读取中…"))
	}
	b.WriteString(box("Agent 保护技能", strings.Join(kits, "\n")+"\n"+dimStyle.Render("在 实例 › 设置 中更新。"), w))
	return b.String()
}

func (s *settingsScreen) advancedView(m *Model, w int) string {
	var b strings.Builder
	b.WriteString(warnStyle.Render("这里的操作会影响谁能打开备份，请先读清楚说明。") + "\n\n")
	var rows []string
	for i, h := range s.hosts {
		d := m.server(h)
		state := dimStyle.Render("读取中")
		if d.status != nil {
			var fps []string
			for _, fp := range d.status.Recipients {
				if s.key != nil && fp == s.key.Fingerprint {
					fp += "（本机）"
				}
				fps = append(fps, fp)
			}
			state = strings.Join(fps, "  ")
		}
		rows = append(rows, cursorLine(s.inBody && i == s.cursor, padRight(h, 20)+state))
	}
	b.WriteString(box("每台服务器上能打开备份的钥匙（指纹）", strings.Join(rows, "\n"), w) + "\n")
	b.WriteString(dimStyle.Render("从服务器移除一把钥匙后，以后的备份不再给它；以前的备份它仍然能打开，加密没法追溯撤销。最后一把钥匙不能移除。") + "\n\n")
	b.WriteString(box("生成一把新钥匙", dimStyle.Render("只在钥匙泄露时使用。旧钥匙仍然是打开以前备份的唯一方式，请先备份。需要时在终端运行 valise key init（会先要求处理现有钥匙）。"), w) + "\n")
	chat := okStyle.Render("开") + dimStyle.Render("　agent 页的「对话」分栏：打开 harness 自己的对话、复制技能提示词。按 c 关掉")
	if m.svc.Config.ChatEntryOff {
		chat = dimStyle.Render("关　按 c 打开")
	}
	if s.cursor < len(s.hosts) {
		b.WriteString(box("接管模式 · "+s.hosts[s.cursor], takeoverRow(m, s.hosts[s.cursor]), w) + "\n")
	}
	b.WriteString(box("对话分栏", chat, w) + "\n")
	b.WriteString(box("忽略红色提醒的时长", itoa(m.svc.RedDismissDays())+" 天"+dimStyle.Render("　黄色提醒忽略后不再出现；红色的忽略后只是主页不提醒，到期再提醒，健康页里一直显示。按 d 修改（30–365 天）"), w))
	return b.String()
}

func configView(w int) string {
	return box("Valise 配置", "把这台电脑上的服务器列表、服务器的主机指纹和设置导出成一个文件，换电脑时导入。\n\n"+
		okStyle.Render("包含")+"　服务器（别名、地址、端口、用户、登录方式）· 主机指纹 · 管理窗口时长\n"+
		warnStyle.Render("不包含")+"　任何钥匙：加密钥匙用「加密钥匙 › 备份钥匙」导出恢复套件；日常钥匙和管理钥匙不离开这台电脑\n\n"+
		dimStyle.Render("导入只添加这台电脑上没有的服务器，同名的跳过并列出，不覆盖。导入后在每台服务器的「服务器设置」里按 K，用服务器密码登录一次，装上这台电脑自己的日常钥匙。\n文件是明文 JSON，里面有服务器地址和用户名，请不要公开分享。")+"\n\n"+
		keyStyle.Render("e")+" 导出 Valise 配置     "+keyStyle.Render("i")+" 导入 Valise 配置", w)
}

func exportConfig(m *Model) tea.Cmd {
	return m.push(newInput("导出到哪个文件？（新文件，不会覆盖已有的）", "~/Desktop/valise-config.json", false, func(m *Model, p string) tea.Cmd {
		p = expandHome(m, strings.TrimSpace(p))
		if p == "" {
			p = expandHome(m, "~/Desktop/valise-config.json")
		}
		svc := m.svc
		return func() tea.Msg {
			r, err := svc.ExportConfig(p)
			if err != nil {
				return failed(err)
			}
			return flashMsg("已导出 " + itoa(r.Servers) + " 台服务器到 " + r.Path + "（不含任何钥匙；有服务器地址和用户名，请不要公开分享）")
		}
	}))
}

func importConfig(m *Model, after func(m *Model) tea.Cmd) tea.Cmd {
	return m.push(newInput("要导入的配置文件路径", "~/Desktop/valise-config.json", false, func(m *Model, p string) tea.Cmd {
		p = expandHome(m, strings.TrimSpace(p))
		r, err := m.svc.ImportConfig(p)
		if err != nil {
			return func() tea.Msg { return failed(err) }
		}
		var cmds []tea.Cmd
		for _, h := range r.Added {
			cmds = append(cmds, m.loadServer(h))
		}
		m.flash, m.flashErr = strings.Join(importLines(r), "\n"), false
		return tea.Batch(append(cmds, after(m))...)
	}))
}

// importLines is the import report on the status line.
func importLines(r *service.ConfigImportResult) []string {
	var out []string
	if len(r.Added) == 0 {
		out = append(out, "没有新的服务器需要导入。")
	} else {
		out = append(out, "已导入："+strings.Join(r.Added, "、"))
	}
	if len(r.Skipped) > 0 {
		out = append(out, "跳过（这台电脑上已有同名服务器，没有覆盖）："+strings.Join(r.Skipped, "、"))
	}
	if r.Settings != "" {
		out = append(out, "设置："+r.Settings)
	}
	if len(r.NeedKeys) > 0 {
		out = append(out, "还需要装这台电脑的日常钥匙："+strings.Join(r.NeedKeys, "、")+"（服务器 › 服务器设置 › K）")
	}
	if len(r.NeedSSHConfig) > 0 {
		out = append(out, "这些用你自己的 SSH 设置，请确认 ~/.ssh/config 里有同名 Host："+strings.Join(r.NeedSSHConfig, "、"))
	}
	return out
}

// takeoverRow is the settings line of takeover mode for host (docs/
// docs/SERVICE_CONTRACTS.md 16.F2; per server, default off).
func takeoverRow(m *Model, host string) string {
	if m.svc.Takeover(host) {
		return okStyle.Render("开") + dimStyle.Render("　健康页里需要 sudo 的修复，在你开启高级模式后于这里用 sudo -n 执行；sudo 需要密码时仍转到终端。按 t 关掉")
	}
	return dimStyle.Render("关　健康页里需要 sudo 的修复转到终端执行。按 t 打开：开启高级模式后在这里用 sudo -n 执行（不在这里输入 sudo 密码）")
}

// toggleTakeover is the t key of the advanced settings.
func toggleTakeover(m *Model, host string) tea.Cmd {
	on := !m.svc.Takeover(host)
	if err := m.svc.SetTakeover(host, on); err != nil {
		return func() tea.Msg { return failed(err) }
	}
	m.flash, m.flashErr = host+" 的接管模式已关闭：需要 sudo 的修复转到终端执行", false
	if on {
		m.flash = host + " 的接管模式已开启：需要 sudo 的修复在你开启高级模式后于这里用 sudo -n 执行"
	}
	return nil
}
