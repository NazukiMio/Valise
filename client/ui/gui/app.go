// Package gui is the desktop interface, drawn with mygo's native UI
// (docs/UI.md sections 2-7). It holds no business logic: every fact comes
// from client/ui/service, read on goroutines and applied on the main thread.
package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/profile"
	"valise/ui/service"
)

// App is the window's state.
type App struct {
	svc    *service.Service
	router *ui.Router
	// run starts work off the main thread; update applies its result on
	// the main thread and redraws. Tests replace both with direct calls.
	run    func(func())
	update func(func())

	agents   load[service.AgentListResult]
	timeline load[service.JourneyPage]
	servers  load[[]serverRow]
	harness  map[string]string // profile id → display name
	display  map[string]*profile.Display
	hasRepos bool // a local repository is registered (docs/UI.md 7.2)

	agentTab int    // tabOverview, tabJourney, tabHealth
	point    string // snapshot id open in the journey, "" for the list
	folds    map[string]*bool

	glow        string    // point lit on the journey's line, opening next
	glowAt      time.Time // when its glow started
	journeyView int       // 0 points, 1 route

	entries     map[string]*entryState // admin page tunnels by server/instance
	entryOpener func(context.Context, string, string, service.OpenOptions) error
	openURL     func(string) error
	pickImage   func() (string, error) // the system's open dialog; "" when cancelled

	prefs  prefs
	images map[string]ui.ImageSource // chosen pictures by server/instance
	logos  map[string]ui.ImageSource // official logos by profile id
	query  string                    // the home page's search

	focusSearch bool                    // ⌘F was pressed
	iconErr     error                   // choosing an agent's picture failed
	health      map[string]*healthState // by server/instance
	syncs       map[string]*syncState   // by repo/server
	asking      *passPrompt             // the admin passphrase being asked for
}

// load is one read and what came of it.
type load[T any] struct {
	started bool
	busy    bool
	value   T
	err     error
	at      time.Time
}

// New makes the state; nothing is read until Start.
func New(svc *service.Service) *App {
	return &App{svc: svc, router: ui.NewRouter("/agents"), harness: map[string]string{}, display: map[string]*profile.Display{},
		run: func(f func()) { go f() }, update: func(f func()) { f() },
		entries: map[string]*entryState{}, entryOpener: svc.OpenEntry, openURL: service.OpenURL,
		images: map[string]ui.ImageSource{}, logos: map[string]ui.ImageSource{},
		health: map[string]*healthState{}, syncs: map[string]*syncState{},
		prefs: prefs{View: "cards", Dismissed: map[string]string{}}}
}

// SetImagePicker gives the system's open dialog for an agent's picture.
func (a *App) SetImagePicker(pick func() (string, error)) { a.pickImage = pick }

// SetUpdate gives the window's Update (mygo.Window.Update).
func (a *App) SetUpdate(update func(func())) { a.update = update }

// Start begins the first reads. Called after the app is ready, never from
// main before it (docs/UI.md 4: no side effects at start).
func (a *App) Start() {
	a.hasRepos = len(a.svc.Repos()) > 0
	a.loadPrefs()
	a.loadHarness()
	a.refreshAgents()
	a.refreshTimeline()
}

func (a *App) loadHarness() {
	set, err := a.svc.Profiles()
	if err != nil || set == nil {
		return
	}
	for _, p := range set.Effective() {
		a.harness[p.ID] = p.DisplayName
		if p.Display != nil {
			d := *p.Display
			a.display[p.ID] = &d
		}
	}
}

// refreshAgents reads every server's agents (SSH, so off the main thread).
func (a *App) refreshAgents() {
	if a.agents.busy {
		return
	}
	a.agents.busy, a.agents.started = true, true
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := a.svc.ListAgents(ctx, service.AgentListRequest{})
		a.update(func() {
			a.agents = load[service.AgentListResult]{started: true, value: res, err: err, at: time.Now()}
		})
	})
}

// refreshTimeline reads the local repositories (no SSH, no key).
func (a *App) refreshTimeline() {
	if a.timeline.busy {
		return
	}
	a.timeline.busy, a.timeline.started = true, true
	a.run(func() {
		page, err := a.allTimeline()
		a.update(func() {
			a.timeline = load[service.JourneyPage]{started: true, value: page, err: err, at: time.Now()}
		})
	})
}

// allTimeline follows the cursor to the end; the list is metadata only.
func (a *App) allTimeline() (service.JourneyPage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var all service.JourneyPage
	req := service.JourneyListRequest{Limit: 200}
	for i := 0; i < 50; i++ {
		page, err := a.svc.ListTimeline(ctx, req)
		if err != nil {
			return all, err
		}
		all.Items = append(all.Items, page.Items...)
		all.Gaps = append(all.Gaps, page.Gaps...)
		all.Observations = page.Observations
		all.Partial = all.Partial || page.Partial
		if page.NextCursor == "" {
			break
		}
		req.Cursor = page.NextCursor
	}
	return all, nil
}

func (a *App) refreshServers() {
	if a.servers.busy {
		return
	}
	a.servers.busy, a.servers.started = true, true
	a.run(func() {
		list, err := a.svc.ServerList()
		rows := serverRows(list)
		a.update(func() {
			a.servers = load[[]serverRow]{started: true, value: rows, err: err, at: time.Now()}
		})
	})
}

// View builds the window.
func (a *App) View(c *ui.Context) {
	c.SetTheme(theme(c.Theme()))
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(palette.Paper).Children(func() {
		a.sidebar(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(36, 44).Gap(20).MaxWidth(980).Children(func() {
				a.router.View(c, func(r *ui.Route) {
					r.Page().Gap(20)
					switch {
					case r.Match("/agents"):
						r.Title(text.navAgents)
						a.homePage(c)
					case r.Match("/agents/{server}/{instance}"):
						a.agentPage(c, r.Param("server"), r.Param("instance"))
					case r.Match("/servers"):
						r.Title(text.navServers)
						a.serversPage(c)
					case r.Match("/servers/{name}"):
						a.serverPage(c, r.Param("name"))
					case r.Match("/locations"):
						r.Title("备份位置")
						a.locationsPage(c)
					default:
						ui.Text(c, text.notFound)
					}
				})
			})
		})
	})
	a.passDialog(c)
}

func (a *App) sidebar(c *ui.Context) {
	page := a.router.Path()
	// The sidebar marks the first-level entry the page belongs to.
	for _, top := range []string{"/agents", "/servers", "/locations"} {
		if page == top || len(page) > len(top) && page[:len(top)+1] == top+"/" {
			page = top
		}
	}
	// The sidebar recedes so the page stays the brightest part (UI.md 7.1).
	ui.Column(c).Width(212).Padding(22, 12).Gap(18).Background(palette.Side).TextColor(palette.SideText).
		BorderWidth(0, 1, 0, 0).BorderColor(palette.CardBorder).Children(func() {
		ui.Row(c).Padding(0, 8, 8, 8).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.valise).FontSize(26).TextColor(palette.Brand)
			ui.Text(c, "Valise").FontSize(22).Bold().TextColor(palette.Text).Font(displayFont)
		})
		if ui.Sidebar(c, &page, func() {
			ui.SidebarItem(c, "/agents", icons.agent, text.navAgents)
			ui.SidebarItem(c, "/servers", icons.server, text.navServers)
			ui.SidebarItem(c, "/locations", icons.folder, "备份位置")
		}).Grow(1).Background(ui.RGBA(0, 0, 0, 0)).Changed() {
			a.router.Push(page)
			if page == "/servers" && !a.servers.started {
				a.refreshServers()
			}
		}
		muted(ui.Text(c, "带着你的 Agent 去旅行。")).FontSize(11).Padding(0, 8)
	})
}
