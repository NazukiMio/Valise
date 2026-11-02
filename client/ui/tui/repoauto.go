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

// repoAuto starts the first copy of this run for a freshly loaded server.
func (m *Model) repoAuto(d *srvData) tea.Cmd {
	if d.status == nil || d.status.ServerName == "" || !d.status.Exists || len(m.svc.Repos()) == 0 {
		return nil
	}
	if m.repoDone == nil {
		m.repoDone = map[string]bool{}
	}
	if m.repoDone[d.host] {
		return nil
	}
	m.repoDone[d.host] = true
	return repoSyncAll(m, d.host)
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
