package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/identity"
	"valise/ui/service"
)

// The agents on one server (docs/UI.md 7.5): no agent is presumed. What
// was found and recognized can be adopted; a harness the profiles do not
// know points to the guide; with none, the first agent can be installed.

// serverScan is one look at a server's agents, and each adoption.
type serverScan struct {
	busy   bool
	found  []service.DetectedInstance
	err    error
	adopts map[string]*adoption // by candidate id
}

// adoption is one agent being adopted: the report first, then accepting.
type adoption struct {
	busy    bool
	res     *service.EnrollResult
	err     error
	assign  map[string]string // "<root>:<prefix>" → category; absent: kept as sensitive
	written bool
}

// installable is a harness with an official install plan.
type installable struct{ ID, Name, Takes, Needs string }

// guideURL is the beginner guide to writing a profile (PROFILES.md 附录 A).
const guideURL = "https://github.com/NazukiMio/Valise/blob/main/docs/PROFILES.md#%E9%99%84%E5%BD%95-a%E4%B8%BA-harness-%E5%86%99%E4%B8%80%E4%BB%BD%E8%B5%84%E6%96%99%E5%85%A5%E9%97%A82026-11-08"

func (a *App) scanServer(host string) {
	sc := a.scans[host]
	if sc == nil {
		sc = &serverScan{adopts: map[string]*adoption{}}
		a.scans[host] = sc
	}
	sc.busy, sc.err = true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		found, err := a.svc.DetectInstances(ctx, host)
		a.update(func() { sc.busy, sc.found, sc.err = false, found, err })
	})
}

func (a *App) onServerPage(c *ui.Context, host string) {
	sc := a.scans[host]
	if sc == nil {
		a.scanServer(host)
		sc = a.scans[host]
	}
	if a.wiz != nil && a.wiz.host == host {
		stepper(c, len(wizardSteps())-1)
	} else {
		backLink(c, host, "/servers/"+host)
	}
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			pageHead(c, T("wizard.step.agents"), T("onserver.sub", "host", host))
		})
		if sc.busy {
			ui.Spinner(c)
		} else {
			ui.Button(c, T("onserver.rescan")).OnClick(func() { a.scanServer(host) })
		}
	})
	if sc.err != nil {
		errorBox(c, sc.err, a.fold("scan-err-"+host))
		return
	}
	if sc.busy && sc.found == nil {
		return
	}
	if len(sc.found) > 0 {
		section(c, T("onserver.found"))
		for _, d := range sc.found {
			a.foundCard(c.Key(d.ID+d.StateDir), host, sc, d)
		}
	}
	section(c, T("onserver.unknown_title"))
	card(ui.Row(c)).Gap(14).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, T("onserver.not_listed")).Bold()
			muted(ui.Text(c, T("onserver.not_listed_why"))).FontSize(13).Wrap()
		})
		ui.Link(c, T("onserver.guide"), guideURL)
	})
	// Installing stays offered beside agents already found: another
	// harness can be installed, or the first one (docs/UI.md 7.5).
	a.installTiles(c, host, len(sc.found) == 0)
}

func section(c *ui.Context, title string) {
	muted(ui.Text(c, title)).FontSize(12).LetterSpacing(0.6)
}

// foundCard is one recognized agent: already adopted, or the report and
// accepting it.
func (a *App) foundCard(c *ui.Context, host string, sc *serverScan, d service.DetectedInstance) {
	ad := sc.adopts[d.ID]
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			a.avatar(c, agentRow{Server: host, Instance: orText(d.Enrolled, d.ID), Profile: d.ProfileID, Name: a.harness[d.ProfileID]}, 36)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				name := orText(a.harness[d.ProfileID], d.ProfileID)
				if d.Name != "" {
					name += " · " + d.Name
				}
				ui.Text(c, name).FontSize(15).Bold()
				line := d.StateDir
				if d.Version != "" {
					line += " · " + d.Version
				}
				mono(muted(ui.Text(c, line))).MaxLines(1)
			})
			switch {
			case d.Enrolled != "" && a.unbound(host, d.Enrolled) && (ad == nil || ad.res == nil && !ad.busy):
				ui.Text(c, T("adopt.no_identity")).FontSize(12).TextColor(palette.Warn)
				ui.PrimaryButton(c, T("adopt.again")).OnClick(func() { a.adopt(host, sc, d, false) })
			case d.Enrolled != "" && d.ProfileVersion > 0 && d.EnrolledProfileVersion > 0 && d.ProfileVersion > d.EnrolledProfileVersion && (ad == nil || !ad.busy && ad.res == nil && !ad.written):
				ui.Text(c, T("adopt.profile_outdated")).FontSize(12).TextColor(palette.Warn)
				ui.PrimaryButton(c, T("adopt.update_profile")).OnClick(func() { a.adopt(host, sc, d, false) })
				ui.Button(c, T("common.view")).OnClick(func() { a.router.Push("/agents/" + host + "/" + orText(d.Enrolled, d.ID)) })
			case d.Enrolled != "" && (ad == nil || !ad.busy && ad.res == nil) || ad != nil && ad.written:
				ui.Text(c, T("adopt.done")).FontSize(12).TextColor(palette.Ok)
				ui.Button(c, T("common.view")).OnClick(func() { a.router.Push("/agents/" + host + "/" + orText(d.Enrolled, d.ID)) })
			case ad != nil && ad.busy:
				ui.Spinner(c)
			case ad == nil || ad.res == nil:
				ui.PrimaryButton(c, T("adopt.go")).OnClick(func() { a.adopt(host, sc, d, false) })
			}
		})
		if ad == nil {
			return
		}
		if ad.err != nil {
			errorBox(c, ad.err, a.fold("adopt-err-"+host+d.ID))
		}
		if ad.res != nil && ad.res.Report != nil && !ad.written {
			a.adoptReport(c, host, sc, d, ad)
		}
	})
}

func (a *App) adopt(host string, sc *serverScan, d service.DetectedInstance, accept bool) {
	ad := sc.adopts[d.ID]
	if ad == nil {
		ad = &adoption{assign: map[string]string{}}
		sc.adopts[d.ID] = ad
	}
	ad.busy, ad.err = true, nil
	opts := service.EnrollOptions{ProfileID: d.ProfileID, Name: d.Name, InstanceID: orText(d.Enrolled, d.ID), Accept: accept}
	for k, cat := range ad.assign {
		opts.Assign = append(opts.Assign, assignRule(k, cat))
	}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := a.svc.Enroll(ctx, host, opts)
		a.update(func() {
			ad.busy, ad.err = false, err
			if err != nil {
				return
			}
			ad.res = res
			if accept && res.Written != "" {
				ad.written = true
				delete(a.natives, host+"/"+opts.InstanceID)
				a.refreshAgents()
			}
		})
	})
}

// unbound says an enrolled instance has no identity yet (enrolled the old
// way); accepting it again gives it one (SERVICE_CONTRACTS.md 3.1).
func (a *App) unbound(host, id string) bool {
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == host && it.Instance.InstanceID == id {
			return it.IdentityState == identity.LegacyUnbound
		}
	}
	return false
}

// assignRule is one decision as Enroll takes it: a folder group covers
// everything under it.
func assignRule(key, cat string) string {
	glob := key
	if len(key) > 0 && key[len(key)-1] == '/' {
		glob += "**"
	}
	return glob + "=" + cat
}

// categoryChoices are what an unrecognized file can be filed as; the first
// is the default, keeping it with the secrets (ARCHITECTURE.md 5).
var categoryChoices = []struct{ id, label string }{
	{"", "category.sensitive"}, {"memory", "category.memory"}, {"config", "category.config"}, {"sessions", "category.sessions"}, {"state", "category.state"},
	{"workspace", "category.workspace"}, {"cache", "category.cache"}, {"ignore", "category.ignore"},
}

func (a *App) adoptReport(c *ui.Context, host string, sc *serverScan, d service.DetectedInstance, ad *adoption) {
	r := ad.res.Report
	ui.Column(c).Gap(8).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
		var found int
		for _, t := range r.Categories {
			found += t.Files
		}
		line := T("adopt.found", "n", found)
		if r.Unclassified.Files > 0 {
			line = T("adopt.found_unknown", "line", line, "n", r.Unclassified.Files)
		}
		ui.Text(c, line).FontSize(13).Wrap()
		if len(r.Secrets) > 0 {
			muted(ui.Text(c, T("adopt.secrets", "n", len(r.Secrets))))
		}
		if ad.res.ProfileLocal {
			ui.Text(c, T("profile.local")).FontSize(12).TextColor(palette.Warn)
		}
		if len(r.Pending) > 0 {
			ui.Text(c, T("adopt.restore_untested")).FontSize(12).TextColor(palette.Warn)
		}
		if len(r.UnclassGroups) > 0 {
			ui.Collapsible(c, T("adopt.assign"), a.fold("assign-"+host+d.ID), func() {
				labels := make([]string, len(categoryChoices))
				for i, ch := range categoryChoices {
					labels[i] = T(ch.label)
				}
				for _, g := range r.UnclassGroups {
					key := g.Root + ":" + g.Prefix
					ui.Row(c.Key(key)).Gap(10).AlignItems(ui.Center).Children(func() {
						mono(ui.Text(c, g.Root+"/"+g.Prefix)).FontSize(12).Grow(1).MaxLines(1)
						muted(ui.Text(c, T("adopt.group", "n", g.Files, "size", size(g.Bytes))))
						choice := labelOf(ad.assign[key])
						if ui.Select(c, &choice, labels).Width(160).Changed() {
							if id := idOf(choice); id == "" {
								delete(ad.assign, key)
							} else {
								ad.assign[key] = id
							}
						}
					})
				}
			})
		}
		muted(ui.Text(c, T("adopt.readonly"))).Wrap()
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() { delete(sc.adopts, d.ID) })
			ui.PrimaryButton(c, T("adopt.confirm")).OnClick(func() { a.adopt(host, sc, d, true) })
		})
	})
}

func labelOf(id string) string {
	for _, ch := range categoryChoices {
		if ch.id == id {
			return T(ch.label)
		}
	}
	return T(categoryChoices[0].label)
}

func idOf(label string) string {
	for _, ch := range categoryChoices {
		if T(ch.label) == label {
			return ch.id
		}
	}
	return ""
}

// installTiles offer the harnesses with an official install plan: as the
// way to a first agent on an empty server, or under the agents found as a
// way to another one, together with pasting a custom official URL/command.
func (a *App) installTiles(c *ui.Context, host string, first bool) {
	if !first && len(a.installable) == 0 {
		a.customInstallTile(c, host)
		return
	}
	title, sub := T("onserver.more_title"), T("onserver.more_sub")
	if first {
		title, sub = T("onserver.none_title"), T("onserver.none_sub")
	}
	section(c, title)
	muted(ui.Text(c, sub)).FontSize(13).Wrap()
	if len(a.installable) == 0 {
		muted(ui.Text(c, T("onserver.no_plans")))
		a.customInstallTile(c, host)
		return
	}
	ui.Grid(c).Columns(3).Gap(12).Children(func() {
		for _, h := range a.installable {
			t := pressableCard(card(ui.Column(c.Key(h.ID))).Gap(spInside), T("install.title", "name", h.Name))
			t.OnClick(func() { a.router.Push("/servers/" + host + "/install/" + h.ID) }).Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					a.avatar(c, agentRow{Profile: h.ID, Name: h.Name}, 28)
					ui.Text(c, h.Name).Bold()
				})
				line := T("install.tile.plan")
				if h.Takes != "" {
					line += T("common.dot_sep") + T("install.tile.takes", "takes", h.Takes)
				}
				if h.Needs != "" {
					line += T("common.dot_sep") + T("install.tile.needs", "needs", h.Needs)
				}
				muted(ui.Text(c, line)).Wrap()
			})
		}
	})
	a.customInstallTile(c, host)
}
