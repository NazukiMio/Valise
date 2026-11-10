package gui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

// A location's page (docs/UI.md 7.9): for each server, each agent's
// periods, whether the server and this location have them, and what the
// last check of the copy here found. Read only until a button is pressed.

// periodRow is one period of one server, here and on the server.
type periodRow struct {
	leaf     string
	server   *vaultrpc.LeafState
	here     []string // snapshots here; nil when not here
	hereSize int64
}

func (a *App) locationPage(c *ui.Context, name string) {
	ls := a.locs()
	if !ls.read {
		a.readLocations()
	}
	ui.Link(c, "‹ "+T("nav.locations"), "/locations")
	l, ok := a.locationOf(name)
	if !ok {
		if ls.reading > 0 {
			ui.Spinner(c)
		} else {
			muted(ui.Text(c, T("loc.gone", "name", name)))
		}
		return
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, locIcon(l.Kind)).FontSize(24).TextColor(palette.Brand)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, l.Name).FontSize(22).Bold().Font(cur.Fonts.Display)
				plainTag(c, T("loc.kind."+l.Kind))
			})
			muted(ui.Text(c, a.locLine(l, c.Now()))).Wrap()
		})
		a.syncButton(c, l)
		if l.Kind != locSSH { // the server's own SSH hosts are removed on the server
			ui.Button(c, T("loc.remove_open")).OnClick(func() { ls.remove, ls.removeJ = l.Name, job{} })
		}
	})
	card(ui.Column(c)).Gap(8).Children(func() { a.syncResult(c, l) })
	hosts := a.locHosts(l)
	if l.Kind != locRepo {
		a.autoRows(c, l, hosts)
	}
	for _, h := range hosts {
		a.hostPeriods(c.Key("hp-"+h), l, h)
	}
	if ls.auto != nil {
		a.autoDialog(c)
	}
	if ls.verify != nil {
		a.verifyDialog(c)
	}
	if ls.remove != "" {
		a.removeDialog(c)
	}
}

// autoRows are the servers' automatic syncs to this location: after a
// backup, at most once in the chosen time; nothing runs in between.
func (a *App) autoRows(c *ui.Context, l location, hosts []string) {
	card(ui.Column(c)).Gap(8).Children(func() {
		for _, h := range hosts {
			t := a.target(h, l.Name)
			if t == nil {
				settingRow(c.Key("auto-"+h), T("loc.auto_title", "host", h), T("loc.auto_after_sync"), "", nil)
				continue
			}
			now := T("loc.auto_off")
			if t.Auto != nil {
				now = T("loc.auto_on_long", "every", everyText(t.Auto.Every))
			}
			tg := t.Target
			settingRow(c.Key("auto-"+h), T("loc.auto_title", "host", h), now, "", func() {
				ui.Button(c, T("protect.change")).OnClick(func() {
					every := "off"
					if tg.Auto != nil {
						every = tg.Auto.Every
					}
					a.locs().auto = &autoEdit{host: h, target: tg, every: every}
				})
			})
		}
	})
}

// hostPeriods lists one server's periods by agent.
func (a *App) hostPeriods(c *ui.Context, l location, host string) {
	rows, err, busy := a.periodRows(l, host)
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.server).FontSize(18).TextColor(palette.TextSub)
			ui.Text(c, host).FontSize(16).Bold().Grow(1)
			if busy {
				ui.Spinner(c)
			}
		})
		if err != nil {
			errorBox(c, err, a.fold("list-err-"+l.Name+host))
		}
		if len(rows) == 0 && !busy {
			muted(ui.Text(c, T("loc.no_periods")))
		}
		inst := ""
		for _, r := range rows {
			who, _, _ := vault.SplitLeaf(r.leaf)
			if who != inst {
				inst = who
				ui.Text(c, a.agentName(host, who)).FontSize(14).Bold().Padding(6, 0, 0, 0)
			}
			a.periodLine(c.Key("pr-"+r.leaf), l, host, r)
		}
	})
}

// agentName is the agent's own name when the list knows it.
func (a *App) agentName(host, instance string) string {
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == host && it.Instance.InstanceID == instance && it.Instance.Label != "" {
			return it.Instance.Label
		}
	}
	return instance
}

func (a *App) periodLine(c *ui.Context, l location, host string, r periodRow) {
	ui.Row(c).Gap(12).Padding(9, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
		label := periodText(r.leaf)
		if r.server != nil && r.server.Current {
			label += T("loc.current")
		}
		ui.Text(c, label).Width(220)
		n, bytes := len(r.here), r.hereSize
		if r.server != nil {
			n, bytes = len(r.server.Snapshots), r.server.Bytes
		}
		muted(ui.Text(c, T("loc.backups", "n", n, "size", size(bytes)))).Width(120)
		have := T("loc.no")
		if r.server != nil {
			have = T("loc.yes")
		}
		muted(ui.Text(c, T("loc.on_server", "have", have))).Width(90)
		ui.Text(c, a.hereText(l, host, r)).FontSize(12).Grow(1).Wrap()
		if r.here == nil {
			return
		}
		ui.Button(c, T("loc.verify_open")).OnClick(func() { a.startVerify(host, r.leaf, l.Name, false) })
		if l.Kind != locRepo && (r.server == nil || !r.server.Current) {
			ui.Button(c, T("loc.delete_open")).OnClick(func() { a.startVerify(host, r.leaf, l.Name, true) })
		}
	})
}

// hereText says whether the location has the period and what its last
// check found.
func (a *App) hereText(l location, host string, r periodRow) string {
	if r.here == nil {
		return T("loc.here_no")
	}
	if r.server != nil && len(r.here) < len(r.server.Snapshots) {
		return T("loc.here_short", "n", len(r.server.Snapshots)-len(r.here))
	}
	state := T("loc.unchecked")
	var last time.Time
	for _, e := range a.svc.CopyEvidenceFor(host, l.Name, r.leaf) {
		if e.CheckedAt.After(last) {
			last = e.CheckedAt
			state = T("loc.check." + e.Result)
		}
	}
	return T("loc.here_yes", "state", state)
}

// periodText is a period as a person reads it.
func periodText(leaf string) string {
	if leaf == vault.LegacyLeaf {
		return T("loc.legacy")
	}
	_, period, _ := vault.SplitLeaf(leaf)
	from, to, ok := strings.Cut(period, "_")
	if !ok {
		return period
	}
	return T("loc.period", "from", from, "to", to)
}

// periodRows joins the server's periods with what the location holds,
// newest first; the location's list is read once per page visit.
func (a *App) periodRows(l location, host string) ([]periodRow, error, bool) {
	ls := a.locs()
	byLeaf := map[string]*periodRow{}
	if st := ls.statuses[host]; st != nil {
		for i := range st.Leaves {
			lf := &st.Leaves[i]
			byLeaf[lf.Leaf] = &periodRow{leaf: lf.Leaf, server: lf}
		}
	}
	add := func(leaf string, snaps []string, bytes int64) {
		r := byLeaf[leaf]
		if r == nil {
			r = &periodRow{leaf: leaf}
			byLeaf[leaf] = r
		}
		r.here, r.hereSize = append([]string{}, snaps...), bytes
	}
	var err error
	busy := ls.reading > 0
	if l.Kind == locRepo {
		for _, s := range a.svc.RepoStatuses(host, a.serverName(host)) {
			if s.Name == l.Name {
				for leaf, ids := range s.Leaves {
					add(leaf, ids, 0)
				}
			}
		}
	} else {
		lr := a.listOf(l, host)
		busy = busy || lr.busy
		err = lr.err
		if lr.list != nil {
			for _, rl := range lr.list.Leaves {
				add(rl.Leaf, rl.Snapshots, rl.Bytes)
			}
		}
	}
	var out []periodRow
	for _, r := range byLeaf {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].leaf > out[j].leaf })
	return out, err, busy
}

// listOf reads what a location holds for a server, once.
func (a *App) listOf(l location, host string) *listRead {
	ls := a.locs()
	key := l.Name + "/" + host
	if lr := ls.lists[key]; lr != nil {
		return lr
	}
	lr := &listRead{busy: true}
	ls.lists[key] = lr
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var list *vaultrpc.TargetListResult
		var err error
		if l.Kind == locStation {
			list, err = a.svc.StationList(ctx, host, l.Name)
		} else {
			list, err = a.svc.TargetList(ctx, host, l.Name)
		}
		a.update(func() { lr.busy, lr.list, lr.err = false, list, err })
	})
	return lr
}

// autoEdit is the automatic sync of one server to one location.
type autoEdit struct {
	host   string
	target vaultrpc.Target
	every  string
	j      job
}

func (a *App) autoDialog(c *ui.Context) {
	ls := a.locs()
	e := ls.auto
	open := true
	if ui.Modal(c, &open, func() {
		ui.Column(c).Width(420).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("loc.auto_title", "host", e.host)).FontSize(18).Bold()
			muted(ui.Text(c, T("loc.auto_what"))).FontSize(13).Wrap()
			ui.RadioGroup(c, func() {
				for _, v := range []string{"off", "6h", "1d", "7d"} {
					label := T("loc.auto_off")
					if v != "off" {
						label = T("loc.every." + v)
					}
					ui.Radio(c.Key("ae-"+v), &e.every, v, label)
				}
			}).Gap(8).Label(T("loc.auto_title", "host", e.host))
			if e.j.busy {
				a.jobLine(c, &e.j)
				return
			}
			if e.j.err != nil {
				errorBox(c, e.j.err, a.fold("auto-err"))
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { ls.auto = nil })
				ui.PrimaryButton(c, T("protect.save")).OnClick(func() {
					t := e.target
					t.Auto = nil
					if e.every != "off" {
						t.Auto = &vaultrpc.AutoSync{Every: e.every}
					}
					a.start(&e.j, time.Minute, func(ctx context.Context) error { return a.svc.TargetSet(ctx, e.host, t) },
						func(err error) {
							if err == nil {
								ls.auto, ls.read = nil, false
							}
						})
				})
			})
		})
	}).Dismissed() && !e.j.busy {
		ls.auto = nil
	}
}
