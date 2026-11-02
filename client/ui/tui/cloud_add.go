package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/vaultrpc"
)

func (c *cloudScreen) addPlace(m *Model) tea.Cmd {
	return m.push(pick("添加哪一种？", []choice{
		{label: "仓库 · 本机文件夹", value: "repo", desc: "这台电脑上的文件夹、外接硬盘或网盘的同步文件夹；只在 Valise 打开时更新"},
		{label: "空间站 · 另一台机器", value: "station", desc: "NAS 或 VPS；服务器能连到就直传，连不上就经这台电脑中转"},
		{label: "空间站 · 服务器已配置的 SSH 主机（高级）", value: "target", desc: "服务器的 ~/.ssh/config 里已经写好了这台主机"},
	}, func(m *Model, kind string) tea.Cmd {
		switch kind {
		case "repo":
			return c.addRepo(m)
		case "station":
			return c.addStation(m)
		}
		return c.addTarget(m)
	}))
}

// addStation collects the station's details; fingerprint and the one
// password prompt happen in the terminal (docs/ARCHITECTURE.md section 7).
func (c *cloudScreen) addStation(m *Model) tea.Cmd {
	var name, addr, user string
	ask := func(label, ph string, next func(m *Model, v string) tea.Cmd) tea.Cmd {
		return m.push(newInput(label, ph, false, func(m *Model, v string) tea.Cmd { return next(m, strings.TrimSpace(v)) }))
	}
	return ask("空间站的名字（小写字母、数字、连字符，例如 nas）", "nas", func(m *Model, v string) tea.Cmd {
		name = v
		return ask("地址（IP 或域名；端口不是 22 时写成 地址:端口）", "192.168.1.20", func(m *Model, v string) tea.Cmd {
			addr = v
			return ask("登录用户（建议在 NAS 上为 Valise 开一个专用用户，只给它一个目录）", "valise", func(m *Model, v string) tea.Cmd {
				user = v
				return ask("存放逃生舱的目录（绝对路径）", "/volume1/valise", func(m *Model, path string) tea.Cmd {
					args := []string{"station", "add", name, "--user", user, "--path", path}
					host, port, ok := strings.Cut(addr, ":")
					args = append(args, "--address", host)
					if ok {
						args = append(args, "--port", port)
					}
					for range 4 {
						m.pop()
					}
					return m.push(pick("服务器给空间站的证书要限定来源地址吗？", []choice{
						{label: "限定（推荐）", value: "fixed", desc: "钥匙被拷到别处也用不了；服务器地址固定时选这个"},
						{label: "不限定", value: "any", desc: "服务器的地址会变（家宽、动态地址）时选这个"},
					}, func(m *Model, v string) tea.Cmd {
						if v == "any" {
							args = append(args, "--any-source")
						}
						return tea.Sequence(m.runCLI(args...), func() tea.Msg { return srvRefresh(c.host, "") })
					}))
				})
			})
		})
	})
}

func (c *cloudScreen) addRepo(m *Model) tea.Cmd {
	return m.push(newInput("仓库的名字（小写字母、数字、连字符，例如 usb）", "usb", false, func(m *Model, name string) tea.Cmd {
		name = strings.TrimSpace(name)
		return m.push(newInput("文件夹的完整路径（不存在会自动创建）", "~/Valise", false, func(m *Model, path string) tea.Cmd {
			r, err := m.svc.RepoAdd(name, path)
			if err != nil {
				return func() tea.Msg { return failed(err) }
			}
			m.pop()
			m.pop()
			c.repos = m.svc.RepoStatuses(c.host, m.server(c.host).status.ServerName)
			return func() tea.Msg {
				return flashMsg("已添加仓库 " + r.Name + "（" + r.Path + "）；按 s 把逃生舱补过去")
			}
		}))
	}))
}

func (c *cloudScreen) addTarget(m *Model) tea.Cmd {
	return m.push(newInput("空间站的名字（小写字母、数字、连字符，例如 nas）", "nas", false, func(m *Model, name string) tea.Cmd {
		name = strings.TrimSpace(name)
		return m.push(newInput("位置：服务器能用 SSH 登录的 <主机>:<绝对路径>（登录凭据在服务器上由你配置）", "nas:/volume1/valise-vault/", false,
			func(m *Model, dest string) tea.Cmd {
				svc, ctx, host := m.svc, m.ctx, c.host
				t := vaultrpc.Target{Name: name, Kind: "rsync", Dest: strings.TrimSpace(dest)}
				return func() tea.Msg {
					if err := svc.TargetSet(ctx, host, t); err != nil {
						return failed(err)
					}
					return srvRefresh(host, "已添加空间站 "+name)
				}
			}))
	}))
}

// stationHead describes a station this server does not know yet.
func (c *cloudScreen) stationHead(m *Model, p place, w int) string {
	loc := ""
	for _, st := range m.svc.Stations() {
		if st.Name == p.name {
			loc = st.User + "@" + st.Address + ":" + st.Path
		}
	}
	head := box(p.label(), kv("位置", loc, "方式", dimStyle.Render("Valise 管理：服务器直传，连不上时经这台电脑中转"),
		"这台服务器", dimStyle.Render("还没有用过它；第一次上云时 Valise 会给服务器一把自己的钥匙")), w)
	if e := c.errs[p.name]; e != nil {
		head += "\n" + errStyle.Render("无法列出 "+p.name+" 上的内容："+firstLine(errText(e)))
	}
	return head
}
