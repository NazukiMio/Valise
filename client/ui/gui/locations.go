package gui

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/vaultrpc"
	"valise/ui/service"
)

// The backup locations (docs/UI.md 7.9, 9.5): every place backups are
// kept besides the servers, by location, with each server under it. What
// is logged in, reachable, synced and checked are said apart.

// Kinds of location.
const (
	locRepo    = "repo"    // a folder on this computer
	locStation = "station" // another machine Valise manages
	locCloud   = "cloud"   // a cloud drive through rclone
	locSSH     = "ssh"     // a host in a server's own SSH configuration
)

// location is one place, of one kind.
type location struct {
	Name, Kind string
}

// locState is what the locations pages read and are doing.
type locState struct {
	read        bool
	reading     int
	statuses    map[string]*vaultrpc.StatusResult // by server
	errs        map[string]error
	syncs       map[string]*locSync // by location
	lists       map[string]*listRead
	auto        *autoEdit
	verify      *verifyRun
	remove      string // the location whose removal is being confirmed
	removeJ     job
	removeScan  *service.TargetRemovalScan
	removeScanJ job
}

// listRead is what one location holds for one server.
type listRead struct {
	busy bool
	list *vaultrpc.TargetListResult
	err  error
}

func (a *App) locs() *locState {
	if a.loc == nil {
		a.loc = &locState{statuses: map[string]*vaultrpc.StatusResult{}, errs: map[string]error{}, syncs: map[string]*locSync{}, lists: map[string]*listRead{}}
	}
	return a.loc
}

// readLocations reads every server's vault (its targets and periods), in
// parallel, over the user pass.
func (a *App) readLocations() {
	ls := a.locs()
	hosts := a.hosts()
	ls.read, ls.reading, ls.lists = true, len(hosts), map[string]*listRead{}
	a.run(func() {
		var wg sync.WaitGroup
		for _, h := range hosts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				st, err := a.svc.VaultStatus(ctx, h)
				a.update(func() {
					ls.reading--
					if err == nil {
						ls.statuses[h] = st
					}
					ls.errs[h] = err
				})
			}()
		}
		wg.Wait()
	})
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

// locations lists the folders, the machines, the clouds, then the hosts
// only a server's SSH configuration knows.
func (a *App) locations() []location {
	ls := a.locs()
	var out []location
	seen := map[string]bool{}
	add := func(name, kind string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, location{name, kind})
		}
	}
	for _, r := range a.svc.Repos() {
		add(r.Name, locRepo)
	}
	for _, st := range a.svc.Stations() {
		add(st.Name, locStation)
	}
	for _, cl := range a.svc.Config.Clouds {
		add(cl.Name, locCloud)
	}
	for _, h := range a.hosts() {
		if st := ls.statuses[h]; st != nil {
			for _, t := range st.Targets {
				if t.Kind == "rclone" {
					add(t.Name, locCloud)
				}
			}
		}
	}
	for _, h := range a.hosts() {
		if st := ls.statuses[h]; st != nil {
			for _, t := range st.Targets {
				add(t.Name, locSSH)
			}
		}
	}
	return out
}

func (a *App) locationOf(name string) (location, bool) {
	for _, l := range a.locations() {
		if l.Name == name {
			return l, true
		}
	}
	return location{}, false
}

// target is a server's record of a location, nil when it has none.
func (a *App) target(host, name string) *vaultrpc.TargetState {
	if st := a.locs().statuses[host]; st != nil {
		for i := range st.Targets {
			if st.Targets[i].Name == name {
				return &st.Targets[i]
			}
		}
	}
	return nil
}

// locHosts are the servers a location serves: every server for a folder,
// a machine and a cloud logged in here; else those that have it.
func (a *App) locHosts(l location) []string {
	if l.Kind == locRepo || l.Kind == locStation || l.Kind == locCloud && a.svc.IsCloudHere(l.Name) {
		return a.hosts()
	}
	var out []string
	for _, h := range a.hosts() {
		if a.target(h, l.Name) != nil {
			out = append(out, h)
		}
	}
	return out
}

func (a *App) locationsPage(c *ui.Context) {
	ls := a.locs()
	if !ls.read {
		a.readLocations()
	}
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Children(func() { pageHead(c, T("nav.locations"), T("locations.sub")) })
		if ls.reading > 0 {
			ui.Spinner(c)
		}
		ui.PrimaryButton(c, T("loc.add")).OnClick(func() { a.openLocAdd() })
	})
	list := a.locations()
	if len(list) == 0 {
		card(ui.Column(c)).Gap(8).Children(func() {
			ui.Text(c, T("loc.none")).FontSize(16).Bold()
			muted(ui.Text(c, T("loc.none_what"))).FontSize(13).Wrap()
		})
		return
	}
	for _, l := range list {
		a.locCard(c.Key("loc-"+l.Name), l)
	}
	if ls.remove != "" {
		a.removeDialog(c)
	}
}

// locCard is one location: what it is in one line, its servers under it,
// and what the last sync did.
func (a *App) locCard(c *ui.Context, l location) {
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, locIcon(l.Kind)).FontSize(22).TextColor(palette.Brand)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Link(c, l.Name, "/locations/"+l.Name).FontSize(16).Bold()
					plainTag(c, T("loc.kind."+l.Kind))
				})
				muted(ui.Text(c, a.locLine(l, c.Now()))).Wrap()
			})
			a.syncButton(c, l)
		})
		hosts := a.locHosts(l)
		if len(hosts) == 0 {
			muted(ui.Text(c, T("locations.no_servers")))
		}
		for _, h := range hosts {
			a.locHostLine(c.Key(h), l, h)
		}
		if l.Kind == locRepo {
			a.removedRepoLines(c, l.Name)
		}
		a.syncResult(c, l)
	})
}

func locIcon(kind string) *ui.SVG {
	switch kind {
	case locRepo:
		return icons.folder
	case locCloud:
		return icons.cloud
	}
	return icons.server
}

// locLine says what the location is and how it is updated.
func (a *App) locLine(l location, now time.Time) string {
	switch l.Kind {
	case locRepo:
		for _, r := range a.svc.Repos() {
			if r.Name == l.Name {
				return r.Path + T("common.dot_sep") + T("loc.repo_how")
			}
		}
	case locStation:
		for _, st := range a.svc.Stations() {
			if st.Name == l.Name {
				line := st.User + "@" + st.Address + ":" + itoa(st.PortOr22()) + " " + st.Path
				if t := a.certLine(l.Name, now); t != "" {
					line += T("common.dot_sep") + t
				}
				return line
			}
		}
	case locCloud:
		var on []string
		for _, h := range a.hosts() {
			if a.target(h, l.Name) != nil {
				on = append(on, h)
			}
		}
		here := T("loc.cloud_here_no")
		if a.svc.IsCloudHere(l.Name) {
			here = T("loc.cloud_here_yes")
		}
		server := T("loc.cloud_server_none")
		if len(on) > 0 {
			server = T("loc.cloud_server", "hosts", joinList(on))
		}
		return server + T("common.dot_sep") + here
	case locSSH:
		for _, h := range a.hosts() {
			if t := a.target(h, l.Name); t != nil {
				return t.Dest + T("common.dot_sep") + T("loc.ssh_how")
			}
		}
	}
	return ""
}

// certLine is the soonest a server's certificate for a machine ends.
func (a *App) certLine(name string, now time.Time) string {
	var soon time.Time
	for _, h := range a.hosts() {
		if c, ok := a.svc.Cert(h, name); ok && (soon.IsZero() || c.Expires.Before(soon)) {
			soon = c.Expires
		}
	}
	switch {
	case soon.IsZero():
		return ""
	case !soon.After(now):
		return T("loc.cert_expired")
	}
	return T("loc.cert_days", "n", int(soon.Sub(now).Hours()/24))
}

// locHostLine is one server under a location: when it last synced, any
// failure, and the automatic setting, each said apart.
func (a *App) locHostLine(c *ui.Context, l location, host string) {
	ui.Row(c).Gap(12).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.server).FontSize(16).TextColor(palette.TextSub)
		ui.Text(c, host).FontSize(14).Bold().Width(140)
		muted(ui.Text(c, a.hostState(l, host, c.Now()))).Wrap().Grow(1)
		if n := a.missing(l, host); n > 0 {
			ui.Text(c, T("loc.missing", "n", n)).FontSize(12).TextColor(palette.Warn)
		}
	})
}

func (a *App) hostState(l location, host string, now time.Time) string {
	if l.Kind == locRepo {
		var st service.RepoStatus
		for _, s := range a.svc.RepoStatuses(host, a.serverName(host)) {
			if s.Name == l.Name {
				st = s
			}
		}
		if !st.Available {
			return T("locations.unavailable")
		}
		return repoStateLine(st, now)
	}
	t := a.target(host, l.Name)
	if t == nil {
		if a.locs().errs[host] != nil {
			return T("loc.host_unread")
		}
		return T("loc.host_not_yet")
	}
	line := T("loc.never")
	if t.LastOK != nil {
		line = T("loc.last_ok", "when", when(*t.LastOK, now))
	}
	if t.LastError != "" {
		line += T("common.dot_sep") + T("loc.last_failed")
	}
	if t.Auto != nil {
		line += T("common.dot_sep") + T("loc.auto_on", "every", everyText(t.Auto.Every))
	}
	return line
}
