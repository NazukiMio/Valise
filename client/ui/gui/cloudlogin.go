package gui

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/core/kittools"
	"valise/ui/service"
)

// A cloud login running (docs/UI.md 7.9, ARCHITECTURE.md 7.3): rclone's
// questions as a form, the browser step through the relay. When the form
// cannot be used (a drive without the loopback login, a busy port, an
// older porter) the same login runs in the window's terminal.

// formQuestion is one of rclone's questions waiting for the person.
type formQuestion struct {
	q      kittools.Question
	answer string
	reply  chan string
}

// ownApp is the user's own OAuth app: optional, passed to rclone once,
// never kept (2026-11-10 user decision).
func (a *App) ownApp(c *ui.Context, ca *cloudAdd, cl *kittools.Cloud) {
	ui.Column(c).Gap(8).Padding(10, 14).Radius(10).Background(palette.Board).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("loc.app_title")).Bold().Grow(1)
			if cl.AppHelp != "" {
				ui.Link(c, T("loc.cloud_app_help"), cl.AppHelp)
			}
		})
		ui.Row(c).Gap(10).Children(func() {
			labeled(c, "client_id", func() { ui.TextInput(c, &ca.appID).Label("client_id") }).Grow(1)
			labeled(c, "client_secret", func() { ui.TextInput(c, &ca.appKey).Password().Label("client_secret") }).Grow(1)
		})
		note := T("loc.app_shared")
		if ca.appID != "" {
			note = T("loc.app_kept")
		}
		muted(ui.Text(c, note)).Wrap()
	})
}

// startCloudLogin logs in on this computer when where is "", else on that
// one server. The user's own app key stays in memory for this page.
func (a *App) startCloudLogin(ca *cloudAdd, cl *kittools.Cloud, where string) {
	if where != "" {
		a.startCloudBatchLogin(ca, cl, []string{where})
		return
	}
	lg := &cloudLogin{form: true}
	ca.login = lg
	name, id := strings.TrimSpace(ca.name), cl.ID
	app := kittools.App{ClientID: strings.TrimSpace(ca.appID), ClientSecret: strings.TrimSpace(ca.appKey)}
	opts := service.CloudOptions{AllowPending: ca.allowPending, Bucket: strings.TrimSpace(ca.bucket)}
	a.start(&lg.j, 20*time.Minute, func(ctx context.Context) error {
		err := a.svc.CloudLoginHereForm(ctx, name, id, a.cloudForm(ctx, lg, app, opts))
		if !errors.Is(err, service.ErrFormUnavailable) {
			return err
		}
		return a.terminalLogin(ctx, lg, "", name, id, opts)
	}, func(err error) {
		if err == nil {
			ca.saved = appendUniq(ca.saved, T("loc.here"))
			a.locs().read = false
		}
	})
}

// askForm shows a question and waits for its answer.
func (a *App) askForm(ctx context.Context, lg *cloudLogin, q kittools.Question) (string, error) {
	fq := &formQuestion{q: q, answer: q.Default, reply: make(chan string, 1)}
	a.update(func() { lg.q = fq })
	defer a.update(func() { lg.q = nil })
	select {
	case ans := <-fq.reply:
		return ans, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// askYes asks before a step that leaves the window (the browser).
func (a *App) askYes(ctx context.Context, lg *cloudLogin, text, yes string) bool {
	q := &cloudAsk{text: text, yes: yes, answer: make(chan bool, 1)}
	a.update(func() { lg.ask = q })
	defer a.update(func() { lg.ask = nil })
	select {
	case ok := <-q.answer:
		return ok
	case <-ctx.Done():
		return false
	}
}

// terminalLogin is the login in the window's terminal: rclone asks there,
// and the headless authorization stays out of command lines.
func (a *App) terminalLogin(ctx context.Context, lg *cloudLogin, where, name, id string, opts service.CloudOptions) error {
	ready := make(chan struct{})
	a.update(func() {
		lg.form, lg.conn = false, newInstallConn()
		if a.newTerminal != nil {
			if t, err := a.newTerminal(lg.conn); err == nil {
				lg.term = t
			}
		}
		close(ready)
	})
	<-ready
	if lg.term == nil {
		go a.pipeText(lg.conn.out, func(s string) { lg.text += s })
	}
	defer lg.conn.outW.Close()
	if where == "" {
		return a.svc.CloudLoginHere(ctx, name, id, opts, lg.conn.in, lg.conn.outW)
	}
	_, err := a.svc.CloudLoginServer(ctx, where, name, id, opts, service.CloudLoginIO{
		LoginIO: service.LoginIO{Stdin: lg.conn.in, Stdout: lg.conn.outW, Cols: 100, Rows: 30,
			Status: func(s service.LoginStatus) { a.update(func() { lg.status = loginStatusText(s) }) },
			ConfirmOpen: func(link, domain string) bool {
				return a.askYes(ctx, lg, T("loc.open_ask", "domain", domain), T("loc.open_go"))
			},
			OpenBrowser: a.openURL},
		ConfirmAuthorize: func(line string) bool {
			return a.askYes(ctx, lg, T("loc.authorize_ask", "line", line), T("loc.authorize_go"))
		},
	})
	return err
}

func (a *App) pipeText(r io.Reader, add func(string)) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s := string(buf[:n])
			a.update(func() { add(s) })
		}
		if err != nil {
			return
		}
	}
}

// loginStatusText says each step of the relay apart.
func loginStatusText(s service.LoginStatus) string {
	switch s.State {
	case service.LoginRelayReady:
		return T("loc.relay_ready", "port", s.Port)
	case service.LoginBrowserOpened:
		return T("loc.browser_opened")
	case service.LoginCallbackReceived:
		return T("loc.callback")
	case service.LoginExited:
		return T("loc.exited", "code", s.ExitCode)
	}
	return ""
}

func (a *App) cloudLoginView(c *ui.Context, ca *cloudAdd) {
	lg := ca.login
	card(ui.Column(c)).Gap(10).Children(func() {
		where := orText(lg.where, T("loc.here"))
		if len(lg.hosts) > 1 {
			ui.Text(c, T("loc.cloud_batch_step", "cur", lg.index+1, "total", len(lg.hosts), "where", lg.where)).FontSize(12).TextColor(palette.TextSub)
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("loc.cloud_logging", "where", where)).Bold().Grow(1)
			if lg.j.busy {
				ui.Spinner(c)
			}
		})
		if lg.status != "" {
			muted(ui.Text(c, lg.status))
		}
		switch {
		case !lg.form:
			muted(ui.Text(c, T("loc.cloud_terminal")))
			if lg.term != nil {
				terminal.View(c, lg.term).Height(320).AutoFocus()
			} else {
				mono(ui.Text(c, orText(lg.text, T("install.no_output")))).FontSize(12).Wrap().Padding(10).Radius(8).Background(palette.Board)
			}
		case lg.q != nil:
			formQuestionView(c, lg.q)
		case lg.j.busy && lg.ask == nil:
			muted(ui.Text(c, T("loc.cloud_waiting")))
		}
		if q := lg.ask; q != nil {
			ui.Row(c).Gap(10).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
				ui.Text(c, q.text).Wrap().Grow(1)
				ui.Button(c, T("loc.skip")).OnClick(func() { q.reply(false) })
				ui.PrimaryButton(c, q.yes).OnClick(func() { q.reply(true) })
			})
		}
		a.batchResultsView(c, lg)
		if lg.j.busy {
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.stop")).OnClick(func() { a.stopJob(&lg.j) })
			})
			return
		}
		switch {
		case lg.j.err != nil:
			errorBox(c, lg.j.err, a.fold("cloudlogin-err"))
		case len(lg.hosts) > 1:
		default:
			ui.Text(c, T("loc.cloud_saved", "where", where, "name", ca.name)).TextColor(palette.Ok).Bold()
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.PrimaryButton(c, T("common.back")).OnClick(func() { ca.login = nil })
		})
	})
}

// formQuestionView is one of rclone's questions: its first line, the
// rest folded; choices as options, else a field.
func formQuestionView(c *ui.Context, fq *formQuestion) {
	q := fq.q
	head, rest, _ := strings.Cut(strings.TrimSpace(q.Help), "\n")
	ui.Column(c.Key("fq-state-"+q.State)).Gap(8).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
		ui.Text(c, orText(head, q.Name)).Bold().Wrap()
		if q.Error != "" {
			ui.Text(c, q.Error).FontSize(12).TextColor(palette.Warn).Wrap()
		}
		if rest != "" {
			muted(ui.Text(c, rest)).Wrap()
		}
		if len(q.Choices) > 0 {
			choices := func() {
				ui.RadioGroup(c, func() {
					for i, ch := range q.Choices {
						label := ch.Value
						if ch.Help != "" {
							label += " · " + strings.SplitN(ch.Help, "\n", 2)[0]
						}
						ui.Radio(c.Key("fq-"+strconv.Itoa(i)+"-"+ch.Value), &fq.answer, ch.Value, label)
					}
				}).Gap(6).Label(orText(head, q.Name))
			}
			if len(q.Choices) > 8 {
				ui.Scroll(c).Height(240).Children(choices)
			} else {
				choices()
			}
		}
		if len(q.Choices) == 0 || !q.Only {
			in := ui.TextInput(c, &fq.answer).Label(q.Name)
			if q.Password {
				in.Password()
			}
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			b := ui.PrimaryButton(c, T("common.next")).OnClick(func() {
				select {
				case fq.reply <- fq.answer:
				default:
				}
			})
			if q.Required && fq.answer == "" {
				b.Disabled(true)
			}
		})
	})
}
