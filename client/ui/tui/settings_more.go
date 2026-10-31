package tui

import (
	"slices"
	"strings"

	"valise/core/agentkit"
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
			pack += "\n" + dimStyle.Render("本地 profile（未经审核）："+strings.Join(st.Local, "、"))
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
	b.WriteString(box("逃生舱技能", strings.Join(kits, "\n")+"\n"+dimStyle.Render("在 实例 › 设置 中更新。"), w))
	return b.String()
}

func (s *settingsScreen) advancedView(m *Model, w int) string {
	var b strings.Builder
	b.WriteString(warnStyle.Render("这里的操作会影响谁能打开逃生舱，请先读清楚说明。") + "\n\n")
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
		rows = append(rows, cursorLine(i == s.cursor, padRight(h, 20)+state))
	}
	b.WriteString(box("每台服务器上能打开逃生舱的钥匙（指纹）", strings.Join(rows, "\n"), w) + "\n")
	b.WriteString(dimStyle.Render("从服务器移除一把钥匙后，以后的逃生舱不再给它；以前的逃生舱它仍然能打开，加密没法追溯撤销。最后一把钥匙不能移除。") + "\n\n")
	b.WriteString(box("生成一把新钥匙", dimStyle.Render("只在钥匙泄露时使用。旧钥匙仍然是打开以前逃生舱的唯一方式，请先备份。需要时在终端运行 valise key init（会先要求处理现有钥匙）。"), w))
	return b.String()
}
