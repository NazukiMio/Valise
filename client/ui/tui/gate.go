package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// gateThen continues with next once an advanced feature may run
// (docs/INSTALL.md 4.2). Without an escape pod in a repository or on a
// station it offers to back up into a repository and then try again; a
// stale one is shown and can be accepted. instance "" checks every agent on
// the server.
func gateThen(m *Model, host, instance string, next func(m *Model) tea.Cmd) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	m.flash, m.flashErr = "正在确认仓库或空间站里有能独立恢复的备份…", false
	return func() tea.Msg {
		g, err := svc.AdvancedGate(ctx, host, instance)
		if err != nil {
			return failed(err)
		}
		if !g.OK() {
			return pushMsg{gateRefused(m, host, instance, g, next)}
		}
		if w := g.Warning(); w != "" {
			return pushMsg{confirm("仓库或空间站里的备份有些旧了，知道这个风险，仍然继续？", strings.Split(w, "\n"), next)}
		}
		return contMsg{next}
	}
}

// gateRefused offers to back up the agents that lack a copy into a
// repository, then runs the gate again.
func gateRefused(m *Model, host, instance string, g *service.GateResult, next func(m *Model) tea.Cmd) screen {
	var missing, detail []string
	for _, a := range g.Agents {
		if a.Pod == nil {
			missing = append(missing, a.Instance)
			detail = append(detail, "  "+a.Instance+"："+dimStyle.Render(a.Reason))
		}
	}
	detail = append([]string{dimStyle.Render("这一步会以你的身份直接改动服务器。为了出事时能恢复，先把 agent 备份到仓库或空间站。")}, detail...)
	var cs []choice
	for _, r := range m.svc.Repos() {
		cs = append(cs,
			choice{label: "备份并复制到仓库 " + r.Name + "（记忆与人格 + 凭据，推荐）", value: vault.TierSoulSecrets + "|" + r.Name, desc: "恢复后开箱即用"},
			choice{label: "备份并复制到仓库 " + r.Name + "（只要记忆与人格）", value: vault.TierSoul + "|" + r.Name, desc: "恢复后要重新填写凭据"})
	}
	cs = append(cs, choice{label: "先去添加仓库或空间站", value: "add"}, choice{label: "取消", value: "cancel"})
	p := pick("这一步要先在仓库或空间站里有一份能独立恢复的备份", cs, func(m *Model, v string) tea.Cmd {
		switch v {
		case "cancel":
			return nil
		case "add":
			return m.push(newCloud(host))
		}
		tier, repo, _ := strings.Cut(v, "|")
		return backupToRepo(m, host, missing, tier, repo, func(m *Model) tea.Cmd { return gateThen(m, host, instance, next) })
	})
	p.detail = detail
	return p
}

// backupToRepo seals each instance and copies the server's escape pods
// into the repository, then continues.
func backupToRepo(m *Model, host string, instances []string, tier, repo string, then func(m *Model) tea.Cmd) tea.Cmd {
	svc, ctx, send := m.svc, m.ctx, m.send
	m.flash, m.flashErr = "正在备份…", false
	return func() tea.Msg {
		for _, id := range instances {
			send(flashMsg("正在备份 " + id + "…"))
			if _, err := svc.Seal(ctx, host, vaultrpc.SealParams{InstanceID: id, Tier: tier, Reason: "高级功能之前：备份到仓库 " + repo}); err != nil {
				return failed(err)
			}
		}
		send(flashMsg("正在复制到仓库 " + repo + "…"))
		if _, err := svc.RepoSync(ctx, host, repo); err != nil {
			return failed(err)
		}
		send(flashMsg("已备份并复制到仓库 " + repo))
		return contMsg{then}
	}
}

// gatedCLI runs a CLI command that opens an advanced feature on host (a
// terminal or the native UI) once the gate lets it.
func gatedCLI(m *Model, host string, args ...string) tea.Cmd {
	return gateThen(m, host, "", func(m *Model) tea.Cmd { return m.runCLI(args...) })
}
