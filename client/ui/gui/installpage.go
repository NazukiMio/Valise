package gui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/core/install"
	"valise/core/profile"
	"valise/ui/service"
)

// Installing the first agent (docs/UI.md 7.5, INSTALL.md 3): the plan
// first, in full; after confirming, its steps run in a terminal in the
// window. “Installed” is what detecting finds, said apart from exit codes.

type installRun struct {
	planning bool
	plan     *service.InstallPlan
	planErr  error
	running  bool
	step     string // the step running now
	term     *terminal.Terminal
	text     tailText // the output, when the terminal could not be loaded
	input    string
	conn     *installConn
	res      *service.InstallResult
	err      error
}

// installConn joins the terminal to RunInstall: what the steps print is
// read by the terminal, what is typed goes to their input.
type installConn struct {
	out      *io.PipeReader
	outW     *io.PipeWriter
	in       *io.PipeReader
	inW      *io.PipeWriter
	shown    bool
	mu       sync.Mutex
	cols     int
	rows     int
	wpx      int
	hpx      int
	onResize func(cols, rows, wpx, hpx int)
}

func newInstallConn() *installConn {
	c := &installConn{}
	c.out, c.outW = io.Pipe()
	c.in, c.inW = io.Pipe()
	return c
}

func (c *installConn) ResizePixels(cols, rows, cellW, cellH int) error {
	wpx, hpx := cols*cellW, rows*cellH
	c.mu.Lock()
	c.cols, c.rows, c.wpx, c.hpx = cols, rows, wpx, hpx
	fn := c.onResize
	c.mu.Unlock()
	if fn != nil {
		fn(cols, rows, wpx, hpx)
	}
	return nil
}

func (c *installConn) SetResize(fn func(cols, rows, wpx, hpx int)) {
	c.mu.Lock()
	c.onResize = fn
	cols, rows, wpx, hpx := c.cols, c.rows, c.wpx, c.hpx
	c.mu.Unlock()
	if fn != nil && cols > 0 && rows > 0 {
		fn(cols, rows, wpx, hpx)
	}
}

func (c *installConn) Read(p []byte) (int, error)  { return c.out.Read(p) }
func (c *installConn) Write(p []byte) (int, error) { return c.inW.Write(p) }
func (c *installConn) Close() error {
	c.outW.Close()
	c.inW.Close()
	return nil
}

func (a *App) installPage(c *ui.Context, host, id string) {
	key := host + "/" + id
	ir := a.installs[key]
	if ir == nil {
		ir = &installRun{planning: true}
		a.installs[key] = ir
		a.run(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			plan, err := a.svc.PlanInstall(ctx, host, id)
			a.update(func() { ir.planning, ir.plan, ir.planErr = false, plan, err })
		})
	}
	name := orText(a.harness[id], id)
	backLink(c, T("wizard.step.agents"), "/servers/"+host+"/agents")
	pageHead(c, T("install.title", "name", name), T("install.sub", "host", host))
	switch {
	case ir.planning:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); ui.Text(c, T("install.planning")) })
		return
	case ir.planErr != nil:
		errorBox(c, ir.planErr, a.fold("plan-err-"+key))
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("install.use_custom")).OnClick(func() { a.router.Push("/servers/" + host + "/install-custom") })
		})
		return
	}
	p := ir.plan
	if p.Existing != nil {
		card(ui.Column(c)).Gap(10).Children(func() {
			sectionTitle(ui.Text(c, T("install.existing", "name", name))).TextColor(palette.Warn)
			mono(muted(ui.Text(c, p.Existing.StateDir))).Wrap()
			ui.Text(c, T("install.existing_why")).FontSize(13).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.PrimaryButton(c, T("install.adopt_existing")).OnClick(func() { a.rescanAndGo(host) })
			})
		})
		return
	}
	if ir.conn == nil {
		a.planCard(c, key, host, ir, name)
		return
	}
	a.runView(c, key, host, ir, name)
}

func (a *App) planCard(c *ui.Context, key, host string, ir *installRun, name string) {
	p := ir.plan
	card(ui.Column(c)).Gap(12).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			sectionTitle(ui.Text(c, T("install.will_do"))).Grow(1)
			if len(p.Missing) == 0 && len(p.Unfixable) == 0 {
				ui.Text(c, T("install.ready")).FontSize(12).TextColor(palette.Ok)
			}
		})
		for i := range p.Steps {
			a.planStep(c.Key(i), key, p, i, &p.Steps[i])
		}
		var facts []string
		if p.Where != "" {
			facts = append(facts, T("install.where", "where", p.Where))
		}
		if p.Takes != "" {
			facts = append(facts, T("install.takes", "takes", p.Takes))
		}
		facts = append(facts, T("install.needs_admin"))
		factsKey := "install.facts"
		if planHasFindings(p) {
			factsKey = "install.facts_plain"
		}
		muted(ui.Text(c, T(factsKey, "facts", strings.Join(facts, T("common.dot_sep"))))).Wrap()
		for _, n := range p.Notes {
			ui.Text(c, n).FontSize(12).Wrap()
		}
		if p.ProfileLocal {
			ui.Text(c, T("profile.local")).FontSize(12).TextColor(palette.Warn)
		}
		missing := append(append([]string{}, reasons(p.Missing)...), reasons(p.Unfixable)...)
		if len(missing) > 0 {
			ui.Column(c).Gap(4).Padding(12, 14).Radius(10).Background(palette.AlertBg).Children(func() {
				ui.Text(c, T("install.missing")).Bold().TextColor(palette.AlertText)
				for _, m := range missing {
					ui.Text(c.Key(m), "· "+m).FontSize(13).TextColor(palette.AlertText).Wrap()
				}
				ui.Text(c, T("install.missing_how")).FontSize(12).TextColor(palette.AlertText).Wrap()
			})
		}
		if len(missing) == 0 && !p.HasRefusedScript() && p.HasUnconfirmedScript() {
			muted(ui.Text(c, T("install.script_confirm_needed"))).Wrap()
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() { a.closeInstall(key); a.router.Push("/servers/" + host + "/agents") })
			if p.HasRefusedScript() || p.HasUnconfirmedScript() {
				ui.Button(c, T("install.use_custom")).OnClick(func() { a.router.Push("/servers/" + host + "/install-custom") })
			}
			if len(missing) == 0 && !p.HasRefusedScript() && !p.HasUnconfirmedScript() {
				ui.PrimaryButton(c, T("install.start")).OnClick(func() { a.startInstall(host, ir) })
			}
		})
	})
}

func planHasFindings(p *service.InstallPlan) bool {
	for _, st := range p.Steps {
		if len(st.ScriptFindings) > 0 {
			return true
		}
	}
	return false
}

// reasons are the requirements not met, in plain words.
func reasons(rs []install.Requirement) []string {
	var out []string
	for _, r := range rs {
		out = append(out, orText(r.Reason, r.Binary))
	}
	return out
}

func (a *App) planStep(c *ui.Context, key string, p *service.InstallPlan, i int, st *service.PlanStep) {
	subPanel(ui.Column(c)).Gap(6).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if st.Optional {
				ui.Checkbox(c, &st.Selected, T("install.optional", "what", st.Summary))
			} else {
				cardTitle(ui.Text(c, fmt.Sprintf("%d. %s", i+1, st.Summary)))
			}
		})
		if st.URL != "" {
			mono(muted(ui.Text(c, st.URL))).Wrap().Selectable()
		}
		if st.ScriptChanged {
			a.changedScriptBox(c, p, i, st)
		} else if st.SHA256 != "" {
			muted(ui.Text(c, T("install.sha_checked", "sha", st.SHA256))).Wrap().Selectable()
		}
		if len(st.Command) > 0 && st.Kind != profile.StepScript {
			mono(ui.Text(c, strings.Join(st.Command, " "))).FontSize(12).Wrap().Selectable()
		}
		if st.Optional && st.SkipEffect != "" {
			muted(ui.Text(c, T("install.skip_effect", "what", st.SkipEffect))).Wrap()
		}
		if len(st.Script) > 0 {
			a.scriptPreview(c, "script-"+key+itoa(i), st.Script, st.ScriptChanged)
		}
	})
}

func (a *App) changedScriptBox(c *ui.Context, p *service.InstallPlan, i int, st *service.PlanStep) {
	bg, border := palette.AlertBg, palette.AlertBorder
	if st.ScriptRefused {
		bg, border = palette.ErrorBg, palette.ErrorBorder
	}
	ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(bg).Border(1, border).Children(func() {
		if st.ScriptRefused {
			ui.Text(c, T("install.script_refused_title")).Bold().TextColor(palette.Warn).Wrap()
		} else {
			ui.Text(c, T("install.script_updated")).Bold().TextColor(palette.Warn).Wrap()
		}
		mono(ui.Text(c, T("install.sha_expected", "sha", st.ExpectedSHA256))).FontSize(fsCaption).Wrap().Selectable()
		mono(ui.Text(c, T("install.sha_actual", "sha", st.SHA256))).FontSize(fsCaption).Wrap().Selectable()
		a.renderScriptFindings(c, st.ScriptFindings)
		if st.ScriptRefused {
			ui.Text(c, T("install.script_refused_hint")).FontSize(12).TextColor(palette.Warn).Wrap()
		} else {
			checked := st.ScriptAccepted
			if ui.Checkbox(c, &checked, T("install.script_accept_updated")).Changed() {
				p.AcceptScript(i, checked)
			}
		}
	})
}

func (a *App) startInstall(host string, ir *installRun) {
	conn := newInstallConn()
	ir.conn, ir.running, ir.err, ir.res, ir.text = conn, true, nil, nil, tailText{}
	ir.term = a.openInstallTerminal(conn, &ir.text)
	plan := ir.plan
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		res, err := a.svc.RunInstall(ctx, plan, service.InstallIO{
			Stdin:    conn.in,
			Stdout:   conn.outW,
			Stderr:   conn.outW,
			PTY:      ir.term != nil,
			OnResize: conn.SetResize,
			Step: func(i int, st service.PlanStep) {
				a.update(func() { ir.step = T("install.step", "n", i+1, "what", st.Summary) })
			}})
		conn.Close()
		a.update(func() { ir.running, ir.res, ir.err = false, res, err })
	})
}

func (a *App) runView(c *ui.Context, key, host string, ir *installRun, name string) {
	a.renderTerminalPane(c, ir.step, ir.running, ir.term, &ir.text, &ir.input, ir.conn)
	if ir.running {
		return
	}
	a.installResult(c, key, host, ir, name)
}

// installResult says what the commands did and what detecting found, apart.
func (a *App) installResult(c *ui.Context, key, host string, ir *installRun, name string) {
	r := ir.res
	card(ui.Column(c)).Gap(8).Children(func() {
		switch {
		case r != nil && r.Found != nil:
			sectionTitle(ui.Text(c, T("install.found"))).TextColor(palette.Ok)
			muted(ui.Text(c, T("install.found_where", "name", name, "dir", r.Found.StateDir))).Wrap().Selectable()
		case r != nil && r.Completed:
			sectionTitle(ui.Text(c, T("install.not_found"))).TextColor(palette.Warn)
			muted(ui.Text(c, T("install.not_found_why"))).Wrap()
		default:
			sectionTitle(ui.Text(c, T("install.unfinished"))).TextColor(palette.Warn)
		}
		if r != nil && len(r.Exits) > 0 {
			muted(ui.Text(c, exitLine(r.Exits))).Wrap().Selectable()
		}
		if ir.err != nil {
			errorBox(c, ir.err, a.fold("install-err-"+key))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if r == nil || r.Found == nil {
				ui.Button(c, T("install.use_custom")).OnClick(func() { a.closeInstall(key); a.router.Push("/servers/" + host + "/install-custom") })
			}
			ui.Button(c, T("install.back_server")).OnClick(func() { a.closeInstall(key); a.rescanAndGo(host) })
			if r != nil && r.Found != nil {
				ui.PrimaryButton(c, T("install.adopt")).OnClick(func() { a.closeInstall(key); a.rescanAndGo(host) })
			}
		})
	})
}

// exitLine says the exit codes as they were, skipped steps apart.
func exitLine(exits []int) string {
	var parts []string
	for i, e := range exits {
		if e == -1 {
			parts = append(parts, T("install.exit.skipped", "n", i+1))
		} else {
			parts = append(parts, T("install.exit.code", "n", i+1, "code", e))
		}
	}
	return T("install.exits", "list", strings.Join(parts, T("common.comma")))
}

func (a *App) rescanAndGo(host string) {
	a.scanServer(host)
	a.router.Push("/servers/" + host + "/agents")
}
