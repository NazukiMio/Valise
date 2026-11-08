package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// Copies only this computer makes (local repositories, clouds logged in
// here) run through the shared service (docs/ARCHITECTURE.md 7.4): once
// per server and run when it first loads, and after a backup made here.

type autoCopied struct {
	host string
	res  *service.AutoCopyResult
	err  error
}

// repoAuto starts the open trigger for a freshly loaded server.
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
	return autoCopy(m, d.host, service.TriggerOpen, "")
}

// autoCopy runs one trigger in the background.
func autoCopy(m *Model, host string, trigger service.CopyTrigger, instance string) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	return func() tea.Msg {
		res, err := svc.AutoCopy(ctx, host, trigger, service.AutoCopyOptions{Instance: instance})
		return autoCopied{host: host, res: res, err: err}
	}
}

// hasLocalCopies reports whether anything is copied by this computer.
func hasLocalCopies(m *Model) bool {
	return len(m.svc.Repos()) > 0 || len(m.svc.Config.Clouds) > 0
}

// handleAutoCopied reports a background copy; quiet when nothing changed.
func (m *Model) handleAutoCopied(msg autoCopied) {
	switch {
	case msg.err != nil:
		m.flash, m.flashErr = msg.host+" 自动补齐失败："+firstLine(errText(msg.err)), true
	case msg.res != nil && msg.res.Changed():
		failed := false
		for _, o := range msg.res.Locations {
			failed = failed || o.State == service.CopyFailed
		}
		m.flash, m.flashErr = msg.host+"："+strings.Join(service.AutoCopyLines(msg.res, false), "；"), failed || msg.res.CertError != "" || len(msg.res.LostSource) > 0
	}
}
