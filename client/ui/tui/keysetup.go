package tui

import (
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"valise/core/valise"
	"valise/ui/service"
)

// The encryption key is set the first time it is needed (docs/UI.md 9.1,
// 2026-11-03): building the first escape pod asks for a passphrase, then
// offers to back the key up, then registers it on the server.

// ensureKey runs then once there is a key and host knows it.
func ensureKey(m *Model, host string, then func(m *Model) tea.Cmd) tea.Cmd {
	info, err := m.svc.KeyShow()
	if err != nil {
		return m.push(confirm("建立备份要先有一把加密钥匙，现在设口令？", []string{
			"备份用这把钥匙加密，口令用来保护钥匙。Valise 不保存口令，" + warnStyle.Render("口令丢了，以前的备份就打不开了。"),
			dimStyle.Render("在别的电脑上已经有钥匙？取消后在 设置 › 加密钥匙 里导入恢复套件。"),
		}, func(m *Model) tea.Cmd {
			return askPassphrase(m, func(m *Model, k *service.KeyInfo) tea.Cmd {
				return offerKitBackup(m, func(m *Model) tea.Cmd { return registerKey(m, host, then) })
			})
		}))
	}
	if st := m.server(host).status; st != nil && !slices.Contains(st.Recipients, info.Fingerprint) {
		return registerKey(m, host, then)
	}
	return then(m)
}

// registerKey gives host the key's public half, then continues.
func registerKey(m *Model, host string, then func(m *Model) tea.Cmd) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	m.flash, m.flashErr = "正在把钥匙登记到 "+host+"（服务器只拿到\"只能上锁\"的那一半）…", false
	return func() tea.Msg {
		if _, _, err := svc.KeyRegister(ctx, host, ""); err != nil {
			return failed(err)
		}
		return contMsg{func(m *Model) tea.Cmd { return tea.Batch(m.loadServer(host), then(m)) }}
	}
}

// askPassphrase asks twice and creates the key; the passphrase lives only
// in these closures.
func askPassphrase(m *Model, done func(m *Model, k *service.KeyInfo) tea.Cmd) tea.Cmd {
	return m.push(newInput("设一个口令（至少 12 个字符，可以是一句你记得住的话）", "", true, func(m *Model, first string) tea.Cmd {
		if utf8.RuneCountInString(first) < 12 {
			return func() tea.Msg {
				return failed(valise.New("passphrase_short", "口令至少 12 个字符", "换一句更长的话，再试一次"))
			}
		}
		return m.push(newInput("再输一次口令", "", true, func(m *Model, second string) tea.Cmd {
			if second != first {
				return func() tea.Msg {
					return failed(valise.New("passphrase_mismatch", "两次输入的口令不一致", "再试一次"))
				}
			}
			svc := m.svc
			return func() tea.Msg {
				k, err := svc.KeyInit(first)
				if err != nil {
					return failed(err)
				}
				return contMsg{func(m *Model) tea.Cmd { return done(m, k) }}
			}
		}))
	}))
}

// offerKitBackup suggests exporting the recovery kit right away.
func offerKitBackup(m *Model, then func(m *Model) tea.Cmd) tea.Cmd {
	return m.push(pick("钥匙已生成。这台电脑坏了或丢了，钥匙也就没了：现在备份一份？", []choice{
		{label: "现在备份（推荐）", value: "now", desc: "导出\"恢复套件\"文件夹，放进 U 盘或密码管理器"},
		{label: "稍后", value: "later", desc: "以后在 设置 › 加密钥匙 里按 b"},
	}, func(m *Model, v string) tea.Cmd {
		if v != "now" {
			return then(m)
		}
		return m.push(newInput("把恢复套件导出到哪个文件夹？（文件夹必须是空的或不存在）", "~/Desktop/valise-恢复套件", false, func(m *Model, dir string) tea.Cmd {
			dir = expandHome(m, strings.TrimSpace(dir))
			if _, err := m.svc.ExportKit(dir); err != nil {
				return func() tea.Msg { return failed(err) }
			}
			m.flash, m.flashErr = "已导出到 "+dir, false
			return then(m)
		}))
	}))
}
