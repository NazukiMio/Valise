package gui

import (
	"context"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// The backup locations on this computer (docs/UI.md 7.3).

// syncState is one “现在补到本机” and what came of it.
type syncState struct {
	busy   bool
	result *service.RepoSyncResult
	err    error
}

// hosts are the servers the agent list knows, in name order.
func (a *App) hosts() []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, it := range a.agents.value.Items {
		add(it.ConnectionID)
	}
	for _, o := range a.agents.value.Observations {
		add(o.ConnectionID)
	}
	sort.Strings(out)
	return out
}

// locationsPage lists the local repositories, whether each can be used,
// and copies what a server has and a repository lacks, one at a time. No
// key is needed (docs/UI.md 7.3).
func (a *App) locationsPage(c *ui.Context) {
	pageHead(c, T("nav.locations"), T("locations.sub"))
	repos := a.svc.Repos()
	if len(repos) == 0 {
		muted(ui.Text(c, T("locations.no_repos"))).FontSize(14)
		return
	}
	hosts := a.hosts()
	for _, r := range repos {
		card(ui.Column(c.Key(r.Name))).Gap(12).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.folder).FontSize(22).TextColor(palette.Brand)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, r.Name).FontSize(16).Bold()
					muted(ui.Text(c, r.Path)).Font(cur.Fonts.Mono)
				})
			})
			if len(hosts) == 0 {
				muted(ui.Text(c, T("locations.no_servers")))
			}
			for _, h := range hosts {
				a.repoLine(c.Key(h), r.Name, h)
			}
		})
	}
	muted(ui.Text(c, T("locations.note"))).FontSize(11).Wrap()
}

func (a *App) repoLine(c *ui.Context, repo, host string) {
	var st service.RepoStatus
	for _, s := range a.svc.RepoStatuses(host, "") {
		if s.Name == repo {
			st = s
		}
	}
	key := repo + "/" + host
	sy := a.syncs[key]
	ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.server).FontSize(16).TextColor(palette.TextSub)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, host).FontSize(14).Bold()
				muted(ui.Text(c, repoStateLine(st, c.Now()))).Wrap()
			})
			switch {
			case !st.Available:
				ui.Text(c, T("locations.unavailable")).FontSize(12).TextColor(palette.Warn)
			case sy != nil && sy.busy:
				ui.Spinner(c)
			default:
				ui.Button(c, T("locations.sync")).OnClick(func() { a.syncRepo(repo, host) })
			}
		})
		if sy == nil || sy.busy {
			return
		}
		if sy.err != nil {
			errorBox(c, sy.err, a.fold("sync-err-"+key))
			return
		}
		if r := sy.result; r != nil {
			ui.Text(c, T("locations.synced", "n", len(r.Snapshots), "size", size(r.Bytes))).FontSize(12)
			if t := service.CheckSummary(r.Checks); t != "" {
				muted(ui.Text(c, t)).Wrap()
			}
		}
	})
}

// repoStateLine is when this repository last received the server's
// backups, and the last failure, each said apart.
func repoStateLine(st service.RepoStatus, now time.Time) string {
	line := T("locations.never")
	if st.State.LastOK != nil {
		line = T("locations.last_ok", "when", when(*st.State.LastOK, now))
	}
	if st.State.LastError != "" {
		line += T("common.dot_sep") + T("locations.last_err", "err", st.State.LastError)
	}
	return line
}

func (a *App) syncRepo(repo, host string) {
	sy := &syncState{busy: true}
	a.syncs[repo+"/"+host] = sy
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		r, err := a.svc.RepoSync(ctx, host, repo)
		a.update(func() {
			sy.busy, sy.result, sy.err = false, r, err
			a.refreshTimeline()
		})
	})
}
