package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/core/install"
	"valise/core/valise"
	"valise/ui/service"
)

// Custom official install URL or one-line command (docs/INSTALL.md section 7,
// UI.md 7.5/9.6): parsed and downloaded locally by the client, statically
// checked, previewed in full with SHA-256 and exact server command, then
// uploaded into the staging directory and executed in the embedded terminal.

type customInstallRun struct {
	raw        string
	planning   bool
	plan       *service.CustomInstallPlan
	planErr    error
	sentence   string
	riskErr    error
	running    bool
	step       string
	term       *terminal.Terminal
	text       tailText
	input      string
	conn       *installConn
	res        *service.InstallResult
	err        error
	acceptRisk bool
}

func (a *App) customInstallTile(c *ui.Context, host string) {
	t := pressableCard(card(ui.Row(c)).Gap(spItem).AlignItems(ui.Center), T("install.custom.entry"))
	t.OnClick(func() { a.router.Push("/servers/" + host + "/install-custom") }).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, T("install.custom.entry")).Bold()
			muted(ui.Text(c, T("install.custom.entry_hint"))).FontSize(13).Wrap()
		})
	})
}

func (a *App) renderScriptFindings(c *ui.Context, fs []install.Finding) {
	if len(fs) == 0 {
		ui.Text(c, T("install.check_none")).FontSize(12).TextColor(palette.Ok).Wrap()
		return
	}
	ui.Text(c, T("install.check_hits")).FontSize(12).Bold().TextColor(palette.Warn).Wrap()
	for i, f := range fs {
		key := "install.finding_hit"
		if f.Refuse {
			key = "install.finding_refuse"
		}
		ui.Column(c.Key(i)).Gap(2).Children(func() {
			ui.Text(c, "· "+T(key, "reason", f.Reason)).FontSize(12).TextColor(palette.Warn).Wrap()
			switch {
			case f.Line > 0 && f.Snippet != "":
				mono(muted(ui.Text(c, T("install.finding_where", "line", f.Line, "snippet", f.Snippet)))).FontSize(fsCaption).Wrap()
			case f.Line > 0:
				mono(muted(ui.Text(c, T("install.finding_line", "line", f.Line)))).FontSize(fsCaption).Wrap()
			case f.Snippet != "":
				mono(muted(ui.Text(c, f.Snippet))).FontSize(fsCaption).Wrap()
			}
		})
	}
}

func (a *App) scriptPreview(c *ui.Context, foldKey string, script []byte, openDefault bool) {
	lines := strings.Count(string(script), "\n") + 1
	var fold *bool
	if openDefault {
		fold = a.foldDefault(foldKey, true)
	} else {
		fold = a.fold(foldKey)
	}
	ui.Collapsible(c, T("install.script", "n", lines), fold, func() {
		codeBox(mono(ui.Text(c, string(script))).FontSize(fsCaption).Wrap())
	})
}

func (a *App) openInstallTerminal(conn *installConn, text *tailText) *terminal.Terminal {
	if a.newTerminal != nil {
		if t, err := a.newTerminal(conn); err == nil {
			return t
		}
	}
	a.run(func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.out.Read(buf)
			if n > 0 {
				s := string(buf[:n])
				a.update(func() { text.add(s) })
			}
			if err != nil {
				return
			}
		}
	})
	return nil
}

func (a *App) renderTerminalPane(c *ui.Context, step string, running bool, term *terminal.Terminal, text *tailText, input *string, conn *installConn) {
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, orText(step, T("install.connecting"))).Bold().Grow(1)
			if running {
				ui.Spinner(c)
			}
		})
		muted(ui.Text(c, T("install.type_here_hint")))
		if term != nil {
			terminal.View(c, term).Height(320).AutoFocus()
		} else {
			if text.cut {
				muted(ui.Text(c, T("install.text_cut", "n", maxFallbackText>>10)))
			}
			codeBox(mono(ui.Text(c, orText(text.s, T("install.no_output")))).FontSize(12).Wrap()).Padding(10)
			if running {
				in := ui.TextInput(c, input).Placeholder(T("install.input"))
				if in.Submitted() {
					line := *input + "\n"
					*input = ""
					go conn.inW.Write([]byte(line))
				}
			}
		}
	})
}

func (a *App) customInstallPage(c *ui.Context, host string) {
	if a.customInstalls == nil {
		a.customInstalls = map[string]*customInstallRun{}
	}
	ci := a.customInstalls[host]
	if ci == nil {
		ci = &customInstallRun{}
		a.customInstalls[host] = ci
	}
	backLink(c, T("wizard.step.agents"), "/servers/"+host+"/agents")
	pageHead(c, T("install.custom.title"), T("install.custom.sub", "host", host))

	if ci.conn != nil {
		a.customRunView(c, host, ci)
		return
	}
	if ci.plan != nil {
		a.customPlanView(c, host, ci)
		return
	}
	card(ui.Column(c)).Gap(12).Children(func() {
		labeled(c, T("install.custom.input_label"), func() {
			ui.TextArea(c, &ci.raw).Height(84).Label(T("install.custom.input_label")).Placeholder(T("install.custom.input_placeholder"))
		})
		if ci.planning {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, T("install.custom.loading"))
			})
			return
		}
		if ci.planErr != nil {
			errorBox(c, ci.planErr, a.fold("custom-plan-err-"+host))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() {
				delete(a.customInstalls, host)
				a.router.Push("/servers/" + host + "/agents")
			})
			ui.PrimaryButton(c, T("install.custom.prepare")).OnClick(func() { a.prepareCustomInstall(host, ci) })
		})
	})
}

func (a *App) prepareCustomInstall(host string, ci *customInstallRun) {
	raw := ci.raw
	ci.planning, ci.planErr, ci.plan, ci.sentence, ci.riskErr = true, nil, nil, "", nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		plan, err := a.svc.PlanCustomInstall(ctx, host, raw, "")
		a.update(func() { ci.planning, ci.plan, ci.planErr = false, plan, err })
	})
}

func (a *App) customPlanView(c *ui.Context, host string, ci *customInstallRun) {
	p := ci.plan
	card(ui.Column(c)).Gap(12).Children(func() {
		sectionTitle(ui.Text(c, T("install.will_do")))
		muted(ui.Text(c, T("install.custom.source"))).Wrap()
		if p.URL != "" {
			mono(muted(ui.Text(c, T("install.custom.url", "url", p.URL)))).Wrap()
		}
		if p.SHA256 != "" {
			mono(muted(ui.Text(c, T("install.custom.sha", "sha", p.SHA256)))).Wrap()
		}
		ui.Text(c, T("install.custom.will_run")).FontSize(12).Bold()
		codeBox(mono(ui.Text(c, strings.Join(p.Command, " "))).FontSize(12).Wrap())

		bg, border := palette.Board, palette.CardBorder
		if p.Refused {
			bg, border = palette.ErrorBg, palette.ErrorBorder
		} else if len(p.Findings) > 0 {
			bg, border = palette.AlertBg, palette.AlertBorder
		}
		ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(bg).Border(1, border).Children(func() {
			if p.Refused {
				ui.Text(c, T("install.custom.refused")).Bold().TextColor(palette.Warn).Wrap()
			}
			a.renderScriptFindings(c, p.Findings)
			if p.Refused {
				ui.Text(c, T("install.custom.refused_hint")).FontSize(12).TextColor(palette.Warn).Wrap()
				ui.Row(c).Children(func() {
					textLink(c, T("install.custom.open_shell"), func() { a.router.Push("/servers/" + host + "/shell") })
				})
			}
		})

		if len(p.Script) > 0 {
			a.scriptPreview(c, "custom-script-"+p.ID, p.Script, true)
		}

		if !p.Refused && p.NeedsConfirm {
			ui.Text(c, T("install.custom.risk_prompt", "sentence", install.RiskSentence)).FontSize(13).Bold().Wrap()
			ui.TextInput(c, &ci.sentence).Label(T("install.custom.risk_label")).Placeholder(install.RiskSentence)
			if ci.riskErr != nil {
				errorBox(c, ci.riskErr, a.fold("custom-risk-err-"+host))
			}
		}
		if !p.Refused {
			muted(ui.Text(c, T("install.needs_admin"))).Wrap()
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("install.custom.edit")).OnClick(func() { ci.plan, ci.riskErr = nil, nil })
			if !p.Refused {
				label := T("install.custom.run")
				if p.Kind != "script" {
					label = T("install.custom.run_cmd")
				}
				ui.PrimaryButton(c, label).OnClick(func() {
					if p.NeedsConfirm && strings.TrimSpace(ci.sentence) != install.RiskSentence {
						ci.riskErr = valise.New("install_risk_sentence", T("install.custom.risk_mismatch", "sentence", install.RiskSentence), "")
						return
					}
					ci.riskErr = nil
					a.startCustomInstall(host, ci, false)
				})
			}
		})
	})
}

func (a *App) startCustomInstall(host string, ci *customInstallRun, acceptRisk bool) {
	conn := newInstallConn()
	ci.conn, ci.running, ci.err, ci.res, ci.text = conn, true, nil, nil, tailText{}
	ci.term = a.openInstallTerminal(conn, &ci.text)
	plan, sentence := ci.plan, ci.sentence
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if acceptRisk {
			ctx = service.WithRiskAccepted(ctx)
		}
		res, err := a.svc.RunCustomInstall(ctx, plan, sentence, service.InstallIO{Stdin: conn.in, Stdout: conn.outW, Stderr: conn.outW,
			Step: func(i int, st service.PlanStep) {
				a.update(func() { ci.step = T("install.step", "n", i+1, "what", st.Summary) })
			}})
		conn.Close()
		a.update(func() { ci.running, ci.res, ci.err = false, res, err })
	})
}

func (a *App) customRunView(c *ui.Context, host string, ci *customInstallRun) {
	a.renderTerminalPane(c, ci.step, ci.running, ci.term, &ci.text, &ci.input, ci.conn)
	if ci.running {
		return
	}
	r := ci.res
	gated := false
	if e, ok := valise.As(ci.err); ok && e.Code == service.GateCode {
		gated = true
	}
	card(ui.Column(c)).Gap(8).Children(func() {
		switch {
		case r != nil && r.Found != nil:
			name := orText(a.harness[r.Found.ProfileID], r.Found.ProfileID)
			sectionTitle(ui.Text(c, T("install.found"))).TextColor(palette.Ok)
			muted(ui.Text(c, T("install.found_where", "name", name, "dir", r.Found.StateDir))).Wrap()
		case r != nil && r.Completed:
			sectionTitle(ui.Text(c, T("install.not_found"))).TextColor(palette.Warn)
			muted(ui.Text(c, T("install.not_found_why"))).Wrap()
		default:
			sectionTitle(ui.Text(c, T("install.unfinished"))).TextColor(palette.Warn)
		}
		if r != nil && len(r.Exits) > 0 {
			muted(ui.Text(c, exitLine(r.Exits))).Wrap()
		}
		if ci.err != nil {
			if gated {
				ui.Text(c, T("install.custom.backup_needed")).Wrap()
				ui.Checkbox(c, &ci.acceptRisk, T("install.custom.accept_risk"))
			} else {
				errorBox(c, ci.err, a.fold("custom-run-err-"+host))
			}
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("install.custom.edit")).OnClick(func() { ci.conn, ci.err, ci.res = nil, nil, nil })
			if gated && ci.acceptRisk {
				ui.PrimaryButton(c, T("install.custom.continue_once")).OnClick(func() { a.startCustomInstall(host, ci, true) })
			}
			ui.Button(c, T("install.back_server")).OnClick(func() { delete(a.customInstalls, host); a.rescanAndGo(host) })
			if r != nil && r.Found != nil {
				ui.PrimaryButton(c, T("install.adopt")).OnClick(func() { delete(a.customInstalls, host); a.rescanAndGo(host) })
			}
		})
	})
}
