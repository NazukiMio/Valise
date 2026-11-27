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

	"valise/core/profile"
	"valise/ui/service"
)

// App is the window's state: what every page shares, then each area's
// own state (appstate.go). The areas are embedded, so a view reads
// a.health or a.restore as before; grouping says whose it is.
type App struct {
	svc    *service.Service
	router *ui.Router
	// run starts work off the main thread; update applies its result on
	// the main thread and redraws. Tests replace both with direct calls.
	run    func(func())
	update func(func())

	folds map[string]*bool // open sections, by name, across pages
	asks  []*passPrompt    // secrets being asked for, first shown
	// confirms are the plans the person confirmed in this window, taken
	// once by the service's review hooks (backupnow.go). They belong to
	// this window only; the hooks read them off the main thread.
	confirms struct {
		sync.Mutex
		ids map[string]bool
	}
	openDebugDir func() error
	lastRoute    string
	debugNote    string
	debugNoteErr bool

	hooks
	agentData
	lookView
	homeView
	agentView
	journeyView
	healthView
	protectView
	locationsView
	serversView
	keysView
	restoreView
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
	a := &App{svc: svc, router: ui.NewRouter("/agents"), lastRoute: "/agents",
		openDebugDir: svc.OpenDebugLogDir,
		run:          func(f func()) { go f() }, update: func(f func()) { f() }}
	a.hooks = hooks{entryOpener: svc.OpenEntry, openURL: service.OpenURL,
		scanTimeline: svc.ScanTimelineInScope, runRestoreTest: svc.RunSelectedRestoreTest,
		inspectPlacement: svc.InspectTargetPlacement}
	a.agentData = agentData{harness: map[string]string{}, entryDisplay: map[string]string{}, hasSessionCLI: map[string]bool{}, display: map[string]*profile.Display{}, vaultNames: map[string]string{},
		images: map[string]ui.ImageSource{}, logos: map[string]ui.ImageSource{}}
	a.prefs = prefs{View: "cards", Dismissed: map[string]string{}}
	a.entries, a.entryChoice, a.entryManaged, a.entryStopJobs, a.entryProbeJobs = map[string]*entryState{}, map[string]string{}, map[string]bool{}, map[string]*job{}, map[string]*job{}
	a.forwards, a.reenrollJobs = map[string]*forwardState{}, map[string]*job{}
	a.natives, a.agentKits, a.agentServices, a.agentDossiers = map[string]*nativeState{}, map[string]*agentKitState{}, map[string]*agentServiceState{}, map[string]*agentDossierState{}
	a.timelineScope = service.TimelineScanRequest{LocalOnly: true}
	a.health, a.proposals = map[string]*healthState{}, map[string]*proposalsState{}
	a.prots = map[string]*protState{}
	a.serversView = serversView{scans: map[string]*serverScan{}, installs: map[string]*installRun{},
		customInstalls: map[string]*customInstallRun{}, passes: map[string]*passState{},
		settings: map[string]*settingsState{}}
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
	a.harness = map[string]string{}
	a.entryDisplay = map[string]string{}
	a.hasSessionCLI = map[string]bool{}
	a.display = map[string]*profile.Display{}
	a.installable = nil
	set, err := a.svc.Profiles()
	if err != nil || set == nil {
		return
	}
	for _, p := range set.Effective() {
		a.harness[p.ID] = p.DisplayName
		for _, e := range p.Entries {
			if e.DisplayName != "" {
				a.entryDisplay[p.ID+"/"+e.ID] = e.DisplayName
			}
		}
		if len(p.NativeSessionCLI) > 0 {
			a.hasSessionCLI[p.ID] = true
		}
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

// reloadProfiles refreshes in-memory profile metadata and clears cached server
// scan/cloud add/native/kit views so newly installed profile versions appear without restart.
func (a *App) reloadProfiles() {
	a.loadHarness()
	a.scans = map[string]*serverScan{}
	a.natives = map[string]*nativeState{}
	a.agentKits = map[string]*agentKitState{}
	for _, st := range a.agentServices {
		if st != nil {
			st.started = false
		}
	}
	if a.profiles != nil && !a.profiles.loading {
		a.profiles.started = false
	}
	if a.cloudAdding == nil || a.cloudAdding.login == nil {
		a.closeAdding()
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
			if err == nil {
				a.syncAgentsToServiceStates(res.Items)
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
