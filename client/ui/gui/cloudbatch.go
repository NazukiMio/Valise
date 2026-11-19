package gui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/kittools"
	"valise/ui/service"
)

// Logging in on several servers one after another (docs/UI.md 7.9). One
// operation covers the batch; each server's result is kept apart, so a
// failure on one is said plainly and the rest can be retried without
// typing the user's own app key again.

// cloudBatchResult is one server's end: nil error is a done login.
type cloudBatchResult struct {
	host string
	err  error
}

func appendUniq(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// cloudForm is the question-and-browser side of one form login.
func (a *App) cloudForm(ctx context.Context, lg *cloudLogin, app kittools.App, opts service.CloudOptions) service.CloudForm {
	return service.CloudForm{
		App:          app,
		Bucket:       opts.Bucket,
		AllowPending: opts.AllowPending,
		Ask:          func(q kittools.Question) (string, error) { return a.askForm(ctx, lg, q) },
		Status:       func(s service.LoginStatus) { a.update(func() { lg.status = loginStatusText(s) }) },
		ConfirmOpen: func(link, domain string) bool {
			return a.askYes(ctx, lg, T("loc.open_ask", "domain", domain), T("loc.open_go"))
		},
		OpenBrowser: a.openURL,
	}
}

// startCloudBatchLogin logs in on each host in order inside one job.
func (a *App) startCloudBatchLogin(ca *cloudAdd, cl *kittools.Cloud, hosts []string) {
	if len(hosts) == 0 {
		return
	}
	hosts = slices.Clone(hosts)
	lg := &cloudLogin{hosts: hosts, where: hosts[0], form: true}
	ca.login = lg
	name, id := strings.TrimSpace(ca.name), cl.ID
	app := kittools.App{ClientID: strings.TrimSpace(ca.appID), ClientSecret: strings.TrimSpace(ca.appKey)}
	opts := service.CloudOptions{AllowPending: ca.allowPending, Bucket: strings.TrimSpace(ca.bucket)}
	a.start(&lg.j, time.Duration(len(hosts))*20*time.Minute, func(ctx context.Context) error {
		var err error
		for i, host := range hosts {
			a.update(func() {
				lg.index, lg.where, lg.form, lg.status = i, host, true, ""
				lg.q, lg.ask, lg.conn, lg.term, lg.text = nil, nil, nil, nil, ""
			})
			err = a.loginOneServer(ctx, lg, host, name, id, app, opts)
			a.update(func() {
				lg.results = append(lg.results, cloudBatchResult{host: host, err: err})
				if err != nil {
					return
				}
				ca.saved = appendUniq(ca.saved, host)
				if st := ca.hostTools[host]; st != nil {
					st.pick = false
				}
				a.locs().read = false
			})
			if ctx.Err() != nil {
				break
			}
		}
		if len(hosts) > 1 {
			return nil // each server's own result is listed
		}
		return err
	}, nil)
}

// loginOneServer is the form login on host, or the terminal when the form
// cannot be used there.
func (a *App) loginOneServer(ctx context.Context, lg *cloudLogin, host, name, id string, app kittools.App, opts service.CloudOptions) error {
	err := a.kit().loginServer(ctx, host, name, id, a.cloudForm(ctx, lg, app, opts))
	if errors.Is(err, service.ErrFormUnavailable) {
		return a.terminalLogin(ctx, lg, host, name, id, opts)
	}
	return err
}

// batchResultsView lists each finished server of a batch.
func (a *App) batchResultsView(c *ui.Context, lg *cloudLogin) {
	if len(lg.hosts) < 2 {
		return
	}
	for _, r := range lg.results {
		if r.err == nil {
			ui.Text(c.Key("br-"+r.host), T("loc.cloud_batch_ok", "host", r.host)).TextColor(palette.Ok).Bold()
			continue
		}
		ui.Text(c.Key("br-"+r.host), T("loc.cloud_batch_fail", "host", r.host, "reason", reasonOf(r.err))).TextColor(palette.Warn).Bold().Wrap()
		errorBox(c.Key("bre-"+r.host), r.err, a.fold("cloudbatch-err-"+r.host))
	}
}
