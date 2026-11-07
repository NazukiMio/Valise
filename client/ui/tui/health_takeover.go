package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// Takeover mode (docs/SERVICE_CONTRACTS.md 16.F2): with it on for
// a server, a fix that needs sudo runs here rather than in the terminal.
// Advanced mode still has to be turned on by the user, here, through the
// same service checks as the CLI (EnableAdvanced: the INSTALL.md 4.1
// preconditions, the 4.2 escape pod elsewhere, both passphrases and the
// consent sentence), using the existing confirm and masked input screens.
// Turned on for the fix, it ends when the fix ends. The steps run with
// sudo -n; a server whose sudo asks for a password is sent to the
// terminal, and sudo's password is never asked for here.

// takeoverNote is the confirmation's last word on how the fix will run.
func takeoverNote(m *Model, host string) string {
	if m.svc.AdvancedHost() == host {
		return "接管模式：高级模式已为 " + host + " 开启，按 enter 后在这里用 sudo -n 执行。sudo 需要密码时不会在这里询问，会给出在终端里执行的命令。"
	}
	return "接管模式：需要管理员权限。按 enter 后在这里由你开启高级模式（Valise 口令、管理口令与确认句），开启后保持到你在顶栏按 L 关闭；再用 sudo -n 执行。sudo 需要密码时不会在这里询问，会给出在终端里执行的命令。"
}

// startTakeover runs a confirmed takeover fix, turning advanced mode on
// first when it is not on for host.
func startTakeover(m *Model, host, inst string, f *service.SystemFix) tea.Cmd {
	run := func() func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			risk := m.riskOnce
			m.riskOnce = false
			return m.push(&systemFixResult{host: host, inst: inst, fix: f, running: true, risk: risk})
		}
	}
	if m.svc.AdvancedHost() == host {
		if f.Destructive() {
			return gateThen(m, host, "", run())
		}
		return run()(m)
	}
	if err := m.svc.AdvancedReady(host); err != nil {
		m.flash, m.flashErr = "接管模式要先为 "+host+" 开启高级模式，目前不能开启："+errText(err)+"。也可以自己在服务器上执行："+commandList(f.Manual), true
		return nil
	}
	// The escape pod elsewhere is a precondition of advanced mode itself
	// (INSTALL.md 4.2); checking it first lets the user back up or accept
	// the risk here before typing any passphrase.
	return gateThen(m, host, "", func(m *Model) tea.Cmd {
		risk := m.riskOnce
		m.riskOnce = false
		return enableAdvancedThen(m, host, risk, func(m *Model) tea.Cmd {
			m.riskOnce = risk
			return run()(m)
		})
	})
}

// enableAdvancedThen asks for the risks to be read, both passphrases and
// the consent sentence, has the service enable advanced mode for host, and
// continues with next. Nothing typed is kept after the call.
func enableAdvancedThen(m *Model, host string, risk bool, next func(m *Model) tea.Cmd) tea.Cmd {
	detail := []string{"开启后，Valise 会在这台服务器上以你的身份、用管理员权限执行上一页列出的命令："}
	for _, r := range service.AdvancedRisks {
		detail = append(detail, "  ⚠ "+r)
	}
	detail = append(detail, "",
		"开启后一直保持，直到你在顶栏按 L 立即关闭、切换到另一台服务器或退出 Valise；管理钥匙的时间窗口到期后暂停，继续前要重新输入管理口令。开启期间顶栏显示红色的“高级模式”。",
		"接下来依次输入 Valise 口令、"+host+" 的管理口令和确认句。")
	return m.push(confirm("为 "+host+" 开启高级模式？", detail, func(m *Model) tea.Cmd {
		return m.push(newInput("Valise 口令（开启高级模式 · "+host+"）", "", true, func(m *Model, vp string) tea.Cmd {
			return m.push(newInput(host+" 的管理口令（开启高级模式）", "", true, func(m *Model, ap string) tea.Cmd {
				return m.push(newInput("请照原样输入确认句："+service.AdvancedConsent, "", false, func(m *Model, consent string) tea.Cmd {
					svc, ctx := m.svc, m.ctx
					if risk {
						ctx = service.WithRiskAccepted(ctx)
					}
					m.flash, m.flashErr = "正在核对口令并开启高级模式…", false
					return func() tea.Msg {
						if err := svc.EnableAdvanced(ctx, host, vp, ap, strings.TrimSpace(consent)); err != nil {
							return failed(err)
						}
						return contMsg{next}
					}
				}))
			}))
		}))
	}))
}
