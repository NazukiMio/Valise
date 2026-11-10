package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// Syncing a location: every server it serves, one after another. Copies
// only add (VAULT_FORMAT.md 10); each result says which way it went.

// locSync is one sync of a location and what came of it per server.
type locSync struct {
	j     job
	lines []syncLine
}

// syncLine is one server's result.
type syncLine struct {
	host   string
	text   string // what was copied
	path   string // which way, when not direct
	note   string // the tool's own note (a slower method, say)
	checks string // the check after copying
	err    error
}

func (a *App) syncButton(c *ui.Context, l location) {
	sy := a.locs().syncs[l.Name]
	if sy != nil && sy.j.busy {
		ui.Spinner(c)
		return
	}
	label := T("loc.sync")
	if l.Kind == locRepo {
		label = T("loc.fill")
	}
	b := ui.Button(c, label).OnClick(func() { a.syncLocation(l) })
	if len(a.locHosts(l)) == 0 {
		b.Disabled(true)
	}
}

// syncLocation copies every server's backups to the location; a failure
// at one server is written down and the next goes on.
func (a *App) syncLocation(l location) {
	ls := a.locs()
	sy := &locSync{}
	ls.syncs[l.Name] = sy
	hosts := a.locHosts(l)
	a.start(&sy.j, 2*time.Hour, func(ctx context.Context) error {
		for _, h := range hosts {
			a.says(&sy.j)(T("loc.syncing", "host", h))
			line := a.syncOne(ctx, l, h)
			a.update(func() { sy.lines = append(sy.lines, line) })
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		return nil
	}, func(error) {
		ls.read = false
		a.refreshTimeline()
	})
}

func (a *App) syncOne(ctx context.Context, l location, host string) syncLine {
	line := syncLine{host: host}
	switch l.Kind {
	case locRepo:
		r, err := a.svc.RepoSync(ctx, host, l.Name)
		if err != nil {
			line.err = err
			return line
		}
		line.text = T("locations.synced", "n", len(r.Snapshots), "size", size(r.Bytes))
		line.checks = service.CheckSummary(r.Checks)
		return line
	case locSSH:
		r, err := a.svc.Sync(ctx, host, l.Name)
		if err != nil {
			line.err = err
			return line
		}
		line.text, line.note = T("loc.synced", "n", len(r.Snapshots), "periods", len(r.Leaves)), r.Note
		line.checks = service.CheckSummary(r.Checks)
		return line
	}
	var r *service.StationSyncResult
	var err error
	if l.Kind == locCloud {
		r, err = a.svc.SyncCloud(ctx, host, l.Name)
	} else {
		r, err = a.svc.SyncStation(ctx, host, l.Name)
	}
	if err != nil {
		line.err = err
		return line
	}
	line.text, line.note = T("loc.synced", "n", len(r.Snapshots), "periods", len(r.Leaves)), r.Note
	line.path = pathText(r)
	line.checks = service.CheckSummary(r.Checks)
	return line
}

// pathText says why a copy did not go straight from the server.
func pathText(r *service.StationSyncResult) string {
	switch r.Path {
	case service.PathRepo:
		return T("loc.path_repo", "repo", r.Repo, "why", r.Direct)
	case service.PathTemp:
		return T("loc.path_temp", "why", r.Direct)
	}
	return T("loc.path_direct")
}

// syncResult shows the last sync of a location, a line per server.
func (a *App) syncResult(c *ui.Context, l location) {
	sy := a.locs().syncs[l.Name]
	if sy == nil {
		return
	}
	if sy.j.busy {
		a.jobLine(c, &sy.j)
		muted(ui.Text(c, T("loc.keep_open")))
	}
	for i, ln := range sy.lines {
		ui.Column(c.Key("sr-"+itoa(i))).Gap(2).Padding(8, 14).Children(func() {
			if ln.err != nil {
				ui.Text(c, T("loc.sync_failed", "host", ln.host)).FontSize(13).Bold().TextColor(palette.Warn)
				errorBox(c, ln.err, a.fold("sync-err-"+l.Name+ln.host))
				return
			}
			ui.Text(c, ln.host+T("common.dot_sep")+ln.text).FontSize(13).Bold().TextColor(palette.Ok)
			for _, t := range []string{ln.path, ln.note, ln.checks} {
				if t != "" {
					muted(ui.Text(c, t)).Wrap()
				}
			}
		})
	}
	if !sy.j.busy && sy.j.err != nil {
		errorBox(c, sy.j.err, a.fold("sync-err-"+l.Name))
	}
}

// missing counts the server's backups a folder lacks; other kinds are
// counted on their own page.
func (a *App) missing(l location, host string) int {
	st := a.locs().statuses[host]
	if l.Kind != locRepo || st == nil {
		return 0
	}
	var have map[string][]string
	for _, s := range a.svc.RepoStatuses(host, a.serverName(host)) {
		if s.Name == l.Name && s.Available {
			have = s.Leaves
		}
	}
	if have == nil {
		return 0
	}
	n := 0
	for _, leaf := range st.Leaves {
		got := map[string]bool{}
		for _, id := range have[leaf.Leaf] {
			got[id] = true
		}
		for _, id := range leaf.Snapshots {
			if !got[id] {
				n++
			}
		}
	}
	return n
}

// serverName is the folder a server's backups have in a location.
func (a *App) serverName(host string) string {
	if st := a.locs().statuses[host]; st != nil && st.ServerName != "" {
		return st.ServerName
	}
	return orText(a.vaultNames[host], host)
}

// repoStateLine is when this folder last received the server's backups,
// and the last failure, each said apart.
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

func everyText(every string) string {
	switch every {
	case "6h", "1d", "7d":
		return T("loc.every." + every)
	}
	return every
}

func joinList(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += T("common.list_sep")
		}
		out += x
	}
	return out
}
