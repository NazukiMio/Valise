package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// Machine-migration config export & import (docs/ARCHITECTURE.md 3.1): plain
// JSON with the server list, confirmed host keys and settings. Never exports
// private keys or the encryption key.

type configTransferState struct {
	exportRes *service.ConfigExportResult
	importRes *service.ConfigImportResult
	err       error
}

func (a *App) cfgTransferState() *configTransferState {
	if a.cfgTransfer == nil {
		a.cfgTransfer = &configTransferState{}
	}
	return a.cfgTransfer
}

func (a *App) configTransferCard(c *ui.Context) {
	st := a.cfgTransferState()
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("cfgx.title")).FontSize(16).Bold().Grow(1)
		})
		muted(ui.Text(c, T("cfgx.what"))).Wrap()
		ui.Row(c).Gap(8).Children(func() {
			if a.pickFolder != nil {
				ui.Button(c, T("cfgx.export_open")).OnClick(func() { a.exportConfigChosen(c.Now()) })
			}
			if a.pickFile != nil {
				ui.Button(c, T("cfgx.import_open")).OnClick(a.importConfigChosen)
			}
		})
		if st.err != nil {
			errorBox(c, st.err, a.fold("cfgx-err"))
		}
		if r := st.exportRes; r != nil {
			subPanel(ui.Column(c)).Gap(6).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, T("cfgx.exported", "path", r.Path, "servers", r.Servers, "hosts", r.KnownHosts)).TextColor(palette.Ok).Bold().Grow(1).Wrap()
					quietButton(c, T("common.close")).OnClick(func() { st.exportRes = nil })
				})
				muted(ui.Text(c, T("cfgx.export_note"))).Wrap()
			})
		}
		if r := st.importRes; r != nil {
			a.importResultPanel(c, st, r)
		}
	})
}

func (a *App) importResultPanel(c *ui.Context, st *configTransferState, r *service.ConfigImportResult) {
	sep := T("common.list_sep")
	subPanel(ui.Column(c)).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("cfgx.imported_title")).FontSize(14).Bold().Grow(1)
			quietButton(c, T("common.close")).OnClick(func() { st.importRes = nil })
		})
		if len(r.Added) > 0 {
			ui.Text(c, T("cfgx.added", "list", strings.Join(r.Added, sep))).TextColor(palette.Ok).Wrap()
		} else {
			muted(ui.Text(c, T("cfgx.added_none"))).Wrap()
		}
		if len(r.Same) > 0 {
			muted(ui.Text(c, T("cfgx.same", "list", strings.Join(r.Same, sep)))).Wrap()
		}
		if len(r.Skipped) > 0 {
			ui.Text(c, T("cfgx.skipped", "list", strings.Join(r.Skipped, sep))).TextColor(palette.Warn).Wrap()
		}
		if r.Settings != "" {
			muted(ui.Text(c, T("cfgx.settings_line", "text", r.Settings))).Wrap()
		}
		if len(r.NeedSSHConfig) > 0 {
			ui.Text(c, T("cfgx.need_ssh", "list", strings.Join(r.NeedSSHConfig, sep))).TextColor(palette.Warn).Wrap()
		}
		for _, host := range r.NeedKeys {
			host := host
			ui.Row(c.Key("cfgx-rk-" + host)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, T("cfgx.need_key", "host", host)).Grow(1).Wrap()
				ui.Button(c, T("cfgx.rekey_go")).OnClick(func() {
					a.serverTab = 1
					a.navigate("/servers/" + host)
				})
			})
		}
	})
}

func (a *App) exportConfigChosen(now time.Time) {
	if a.pickFolder == nil {
		return
	}
	dir, err := a.pickFolder(T("cfgx.pick_dir_title"), T("cfgx.pick_dir_msg"))
	st := a.cfgTransferState()
	if err != nil {
		st.err, st.exportRes, st.importRes = err, nil, nil
		return
	}
	if dir == "" {
		return
	}
	res, err := a.svc.ExportConfig(nextConfigExportPath(dir, now))
	if err != nil {
		st.err, st.exportRes, st.importRes = err, nil, nil
		return
	}
	st.err, st.exportRes, st.importRes = nil, res, nil
}

func (a *App) importConfigChosen() {
	if a.pickFile == nil {
		return
	}
	path, err := a.pickFile(T("cfgx.pick_file_title"), T("cfgx.pick_file_msg"))
	st := a.cfgTransferState()
	if err != nil {
		st.err, st.exportRes, st.importRes = err, nil, nil
		return
	}
	if path == "" {
		return
	}
	res, err := a.svc.ImportConfig(path)
	if err != nil {
		st.err, st.exportRes, st.importRes = err, nil, nil
		return
	}
	st.err, st.exportRes, st.importRes = nil, nil, res
	a.refreshServers()
	if len(res.Added) > 0 {
		a.refreshAgents()
	}
}

func nextConfigExportPath(dir string, now time.Time) string {
	stem := "valise-config-" + now.Format("20060102-150405")
	p := filepath.Join(dir, stem+".json")
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return p
	}
	for i := 2; ; i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s-%d.json", stem, i))
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
	}
}
