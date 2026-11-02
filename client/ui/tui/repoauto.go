package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Repositories are filled by the client (docs/ARCHITECTURE.md section 7):
// when Valise opens (once per server and run) and after an escape pod is
// built here, in the background.

type repoSynced struct {
	host string
	text string
	err  error
}

// repoAuto starts this run's background work for a freshly loaded
// server (once per server and run): copying to the repositories, and
// renewing station certificates while the Valise passphrase is unlocked.
func (m *Model) repoAuto(d *srvData) tea.Cmd {
	if d.status == nil || d.status.ServerName == "" {
		return nil
	}
	if m.repoDone == nil {
		m.repoDone = map[string]bool{}
	}
	if m.repoDone[d.host] {
		return nil
	}
	m.repoDone[d.host] = true
	var cmds []tea.Cmd
	if d.status.Exists && len(m.svc.Repos()) > 0 {
		cmds = append(cmds, repoSyncAll(m, d.host))
	}
	if exp := m.svc.ExpiringCerts(d.host); len(exp) > 0 {
		svc, ctx, host := m.svc, m.ctx, d.host
		cmds = append(cmds, func() tea.Msg {
			done, err := svc.RenewCerts(ctx, host)
			switch {
			case err != nil:
				return repoSynced{host: host, err: err}
			case len(done) > 0:
				return repoSynced{host: host, text: host + " 到空间站 " + joinNames(done) + " 的证书已续期"}
			}
			return repoSynced{host: host, text: host + " 到空间站 " + joinNames(exp) + " 的证书快到期了：上云一次即可续期（自动同步才不会中断）"}
		})
	}
	return tea.Batch(cmds...)
}

// repoSyncAll copies host's escape pods to every repository.
func repoSyncAll(m *Model, host string) tea.Cmd {
	if len(m.svc.Repos()) == 0 {
		return nil
	}
	svc, ctx := m.svc, m.ctx
	return func() tea.Msg {
		var done []string
		for _, r := range svc.Repos() {
			res, err := svc.RepoSync(ctx, host, r.Name)
			if err != nil {
				return repoSynced{host: host, err: err}
			}
			if res.Downloaded > 0 {
				done = append(done, r.Name)
			}
		}
		if len(done) == 0 {
			return repoSynced{host: host}
		}
		return repoSynced{host: host, text: host + " 的新逃生舱已补到仓库 " + joinNames(done)}
	}
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += "、"
		}
		out += n
	}
	return out
}

// handleRepoSynced reports a background copy; quiet when nothing changed.
func (m *Model) handleRepoSynced(msg repoSynced) {
	switch {
	case msg.err != nil:
		m.flash, m.flashErr = "补到仓库失败："+firstLine(errText(msg.err)), true
	case msg.text != "":
		m.flash, m.flashErr = msg.text, false
	}
}
