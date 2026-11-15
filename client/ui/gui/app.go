// Package gui is the desktop interface, drawn with mygo's native UI
// (docs/UI.md sections 2-7). It holds no business logic: every fact comes
// from client/ui/service, read on goroutines and applied on the main thread.
package gui

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/core/journey"
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

	glow            string    // point lit on the journey's line, opening next
	glowAt          time.Time // when its glow started
	journeyView     int       // 0 points, 1 route
	timelinePending []journey.LocationRef
	timelineScanID  uint64
	timelineCancel  context.CancelFunc
	timelineScope   service.TimelineScanRequest
	timelineScopeID string
	scanTimeline    func(context.Context, service.TimelineScanRequest, func(service.TimelineUpdate)) error

	entries     map[string]*entryState // admin page tunnels by server/instance
	entryOpener func(context.Context, string, string, service.OpenOptions) error
	openURL     func(string) error
	pickImage   func() (string, error) // the system's open dialog; "" when cancelled

	prefs  prefs
	images map[string]ui.ImageSource // chosen pictures by server/instance
	logos  map[string]ui.ImageSource // official logos by profile id
	query  string                    // the home page's search

	focusSearch    bool                    // ⌘F was pressed
	iconErr        error                   // choosing an agent's picture failed
	health         map[string]*healthState // by server/instance
	loc            *locState               // the backup locations pages
	locAdding      *locAdd                 // adding a location
	restoreTest    *restoreTestFlow
	runRestoreTest func(context.Context, service.SelectedRestoreTest) (*service.RestoreTest, error)
	cloudAdding    *cloudAdd     // adding a cloud drive
	asks           []*passPrompt // secrets being asked for, first shown

	pickFolder func(title, message string) (string, error) // the system's folder dialog; "" when cancelled
	themes     map[string]themeEntry                       // imported themes by id, read at start and after an import
	themeErr   error                                       // the chosen theme could not be used, or an import failed
	// vaultNames maps a connection to the folder its vault has in the
	// repositories (the server's name, which may differ: “debian-vm” keeps
	// its backups under “debian”). Read with the agents; kept when a read
	// fails.
	vaultNames map[string]string
	vaults     map[string]vaultRead // this read's vault states by connection

	wiz         *wizard // the add-server wizard, nil when closed
	pickFile    func(title, message string) (string, error)
	scans       map[string]*serverScan // agents found by server
	installs    map[string]*installRun // by server/profile
	natives     map[string]*nativeState
	native      *nativeRun
	installable []installable // harnesses with an official install plan
	newTerminal func(io.ReadWriteCloser) (*terminal.Terminal, error)
	passes      map[string]*passState // admin pass actions by server
	settings    map[string]*settingsState
	serverTab   int                     // the server detail's tab: 0 overview, 1 settings
	removed     []service.RemovedServer // removed servers, read with the list
	setTab      int                     // 0 keys, 1 backup
	keyFlow     *keyFlow                // the key dialog, nil when closed
	keyNote     string                  // a done key action, said once
	lockOff     bool                    // the backup lock's off confirmation is open
	prots       map[string]*protState   // protection plans by server/instance
	recover     *recoverState           // finding the passphrase again, nil when not
	copyText    func(string) error      // the system clipboard; nil hides 复制
	rebuild     *rebuildState           // rebuilding the admin passes
	forgetting  string                  // the removed server whose record is being forgotten
	forgetErr   error

	locales         map[string]localeEntry // imported catalogs by id (docs/UI.md 7.6)
	restoreLocation *restoreLocation
	restore         *restoreFlow
	restoreTasks    []service.RestoreTask
	restoreTasksErr error
	langErr         error // the chosen language could not be used, or an import failed
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
	a := &App{svc: svc, router: ui.NewRouter("/agents"), harness: map[string]string{}, display: map[string]*profile.Display{},
		run: func(f func()) { go f() }, update: func(f func()) { f() },
		entries: map[string]*entryState{}, entryOpener: svc.OpenEntry, openURL: service.OpenURL,
		images: map[string]ui.ImageSource{}, logos: map[string]ui.ImageSource{},
		health: map[string]*healthState{}, vaultNames: map[string]string{},
		scans: map[string]*serverScan{}, installs: map[string]*installRun{}, passes: map[string]*passState{},
		settings: map[string]*settingsState{}, prots: map[string]*protState{},
		natives: map[string]*nativeState{},
		prefs:   prefs{View: "cards", Dismissed: map[string]string{}}}
	a.scanTimeline = svc.ScanTimelineInScope
	a.runRestoreTest = svc.RunSelectedRestoreTest
	a.timelineScope = service.TimelineScanRequest{LocalOnly: true}
	a.installReviews()
	return a
}

// SetClipboard gives the system clipboard, for copying a command.
func (a *App) SetClipboard(copy func(string) error) { a.copyText = copy }

// SetImagePicker gives the system's open dialog for an agent's picture.
func (a *App) SetImagePicker(pick func() (string, error)) { a.pickImage = pick }

// SetFolderPicker gives the system's folder dialog, for importing themes.
func (a *App) SetFolderPicker(pick func(title, message string) (string, error)) { a.pickFolder = pick }

// SetFilePicker sets the system's dialog for choosing one file (a private
// key, a porter build).
func (a *App) SetFilePicker(pick func(title, message string) (string, error)) { a.pickFile = pick }

// SetTerminal sets how the install screen makes its terminal; without it
// the output shows as text.
func (a *App) SetTerminal(f func(io.ReadWriteCloser) (*terminal.Terminal, error)) { a.newTerminal = f }

// SetUpdate gives the window's Update (mygo.Window.Update).
func (a *App) SetUpdate(update func(func())) { a.update = update }

// Start begins the first reads. Called after the app is ready, never from
// main before it (docs/UI.md 4: no side effects at start).
func (a *App) Start() {
	a.hasRepos = len(a.svc.Repos()) > 0
	a.loadPrefs()
	a.loadThemes()
	a.loadLocales()
	a.chooseLocale()
	a.loadHarness()
	a.refreshAgents()
	a.refreshLocalTimeline()
	a.run(func() {
		tasks, err := a.svc.ListRestoreTasks()
		a.update(func() { a.restoreTasks, a.restoreTasksErr = tasks, err })
	})
}

func (a *App) loadHarness() {
	set, err := a.svc.Profiles()
	if err != nil || set == nil {
		return
	}
	for _, p := range set.Effective() {
		a.harness[p.ID] = p.DisplayName
		if in := p.Install; in != nil {
			var needs []string
			for _, r := range in.Requires {
				needs = append(needs, strings.TrimSpace(r.Binary+" "+r.Version))
			}
			a.installable = append(a.installable, installable{ID: p.ID, Name: p.DisplayName, Takes: in.Takes, Needs: strings.Join(needs, "、")})
		}
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
		vaults := a.readVaults(ctx, res)
		a.update(func() {
			a.agents = load[service.AgentListResult]{started: true, value: res, err: err, at: time.Now()}
			a.vaults = vaults
			for conn, vr := range vaults {
				if vr.name != "" {
					a.vaultNames[conn] = vr.name
				}
			}
		})
	})
}

// vaultRead is what one server's vault said this time: its folder name in
// the repositories and each instance's newest backup there.
type vaultRead struct {
	name   string
	newest map[string]time.Time // instance → newest backup on the server
}

// readVaults asks each server read this time about its vault, in parallel
// (daily access, read only). A server that does not answer is left out.
func (a *App) readVaults(ctx context.Context, res service.AgentListResult) map[string]vaultRead {
	out := map[string]vaultRead{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, obs := range res.Observations {
		if obs.Status != "fresh" {
			continue
		}
		wg.Add(1)
		go func(conn string) {
			defer wg.Done()
			st, err := a.svc.VaultStatus(ctx, conn)
			if err != nil {
				return
			}
			vr := vaultRead{name: st.ServerName, newest: map[string]time.Time{}}
			for _, m := range st.Snapshots {
				if m.CreatedAt.After(vr.newest[m.InstanceID]) {
					vr.newest[m.InstanceID] = m.CreatedAt
				}
			}
			mu.Lock()
			out[conn] = vr
			mu.Unlock()
		}(obs.ConnectionID)
	}
	wg.Wait()
	return out
}

func (a *App) refreshServers() {
	if a.servers.busy {
		return
	}
	a.servers.busy, a.servers.started = true, true
	a.run(func() {
		list, err := a.svc.ServerList()
		rows := serverRows(list)
		removed, _ := a.svc.RemovedServers()
		a.update(func() {
			a.servers = load[[]serverRow]{started: true, value: rows, err: err, at: time.Now()}
			a.removed = removed
		})
	})
}
