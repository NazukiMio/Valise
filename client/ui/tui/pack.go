package tui

import (
	tea "charm.land/bubbletea/v2"

	"valise/client/profiles"
	"valise/ui/service"
)

// packFetched carries a downloaded or read profile pack.
type packFetched struct {
	f   *service.PackFile
	err error
}

// installPackFlow installs a signed pack, or offers an unsigned one as
// local profiles after listing the commands it may run.
func installPackFlow(m *Model, msg packFetched, after func(m *Model) tea.Cmd) tea.Cmd {
	if msg.err != nil {
		return func() tea.Msg { return failed(msg.err) }
	}
	f := msg.f
	var list []string
	for _, p := range f.Pack.Manifest.Profiles {
		list = append(list, "  "+p.ID+" v"+itoa(p.ProfileVersion))
	}
	if f.SignatureErr == nil {
		return m.push(confirm("安装资料包 "+f.Pack.Manifest.Version+"？（签名校验通过）", list, func(m *Model) tea.Cmd {
			if err := m.svc.InstallPack(f); err != nil {
				return func() tea.Msg { return failed(err) }
			}
			m.prof, _ = m.svc.Profiles()
			m.flash, m.flashErr = "已安装资料包 "+f.Pack.Manifest.Version, false
			return after(m)
		}))
	}
	detail := append([]string{errText(f.SignatureErr), "", "可以把其中的 harness 资料作为\"本地 harness 资料\"使用，它们未经审核，可能让 Valise 在你的服务器上运行："}, list...)
	for _, d := range f.Pack.Docs {
		for _, c := range profiles.Commands(d.Profile) {
			detail = append(detail, "  ["+d.Profile.ID+"] "+c)
		}
	}
	return m.push(confirm("作为本地 harness 资料安装？", detail, func(m *Model) tea.Cmd {
		if _, err := m.svc.InstallPackAsLocal(f); err != nil {
			return func() tea.Msg { return failed(err) }
		}
		m.prof, _ = m.svc.Profiles()
		m.flash, m.flashErr = "已作为本地 harness 资料安装", false
		return after(m)
	}))
}
