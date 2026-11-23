package gui

import (
	"context"
	"io"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/enroll"
	"valise/core/journey"
	"valise/core/profile"
	"valise/ui/service"
)

// App's state by area. Each struct is one area's own state, embedded in
// App; a helper here is how a page lets go of its state when it is left.

// hooks are what the window borrows from the system and the service; tests
// replace them.
type hooks struct {
	scanTimeline          func(context.Context, service.TimelineScanRequest, func(service.TimelineUpdate)) error
	runRestoreTest        func(context.Context, service.SelectedRestoreTest) (*service.RestoreTest, error)
	entryOpener           func(context.Context, string, string, service.OpenOptions) error
	openURL               func(string) error
	pickImage             func() (string, error)                               // the system's open dialog; "" when cancelled
	pickFolder            func(title, message string) (string, error)          // the system's folder dialog; "" when cancelled
	pickFile              func(title, message string) (string, error)          // one file (a private key, a porter build)
	newTerminal           func(io.ReadWriteCloser) (*terminal.Terminal, error) // nil: output shows as text
	newNativeTestTerminal func(io.ReadWriteCloser) (io.Closer, error)          // tests only
	copyText              func(string) error                                   // the system clipboard; nil hides 复制
	setAgentLabel         func(context.Context, string, string, string) error  // tests only; nil uses svc
	inspectPlacement      func(context.Context, service.TargetPlacementRequest) (*service.TargetPlacement, error)
	forgetInstanceFn      func(context.Context, string, string) (*enroll.InstanceForgetResult, error)
}

// agentData is what was read about the agents, shared by every page.
type agentData struct {
	agents        load[service.AgentListResult]
	harness       map[string]string // profile id → display name
	hasSessionCLI map[string]bool   // profile id → pack declares native_session_cli
	display       map[string]*profile.Display
	hasRepos      bool // a local repository is registered (docs/UI.md 7.2)
	// vaultNames maps a connection to the folder its vault has in the
	// repositories (the server's name, which may differ: “debian-vm” keeps
	// its backups under “debian”). Read with the agents; kept when a read
	// fails.
	vaultNames  map[string]string
	vaults      map[string]vaultRead      // this read's vault states by connection
	images      map[string]ui.ImageSource // chosen pictures by server/instance
	logos       map[string]ui.ImageSource // official logos by profile id
	installable []installable             // harnesses with an official install plan
}

// lookView is how the window looks and speaks: preferences, themes and
// languages (docs/UI.md 7.4, 7.6).
type lookView struct {
	prefs    prefs
	themes   map[string]themeEntry  // imported themes by id, read at start and after an import
	themeErr error                  // the chosen theme could not be used, or an import failed
	locales  map[string]localeEntry // imported catalogs by id
	langErr  error                  // the chosen language could not be used, or an import failed
}

// homeView is the home page.
type homeView struct {
	query       string // the search
	focusSearch bool   // ⌘F was pressed
}

// agentView is one agent's page, apart from its journey and health.
type agentView struct {
	agentTab         int                    // tabOverview, tabJourney, tabHealth, tabManage
	iconErr          error                  // choosing an agent's picture failed
	entries          map[string]*entryState // admin page tunnels by server/instance
	natives          map[string]*nativeState
	native           *nativeRun
	agentRename      *agentRenameState
	agentKits        map[string]*agentKitState
	agentKitHook     *agentKitOps
	agentServices    map[string]*agentServiceState
	agentServiceHook *agentServiceOps
	agentForget      *agentForgetState
}

// journeyView is the timeline and the point open in it.
type journeyView struct {
	timeline         load[service.JourneyPage]
	point            string    // snapshot id open in the journey, "" for the list
	glow             string    // point lit on the journey's line, opening next
	glowAt           time.Time // when its glow started
	journeyMode      int       // 0 points, 1 route
	journeyFilter    int       // 0 all, 1 savepoints
	pointPromotion   *pointPromotion
	timelinePending  []journey.LocationRef
	timelineScanID   uint64
	timelineCancel   context.CancelFunc
	timelineScope    service.TimelineScanRequest
	timelineScopeID  string
	curveScroll      ui.ScrollState
	curveScrollScope string
	curveCount       int
}

// closePoint goes back from a point to the list.
func (v *journeyView) closePoint() { v.point = "" }

// healthView is the health pages and the restore test started from them.
type healthView struct {
	health        map[string]*healthState // by server/instance
	restoreTest   *restoreTestFlow
	proposals     map[string]*proposalsState
	proposalsHook *proposalsOps
}

// protectView is the protection plans.
type protectView struct {
	prots map[string]*protState // by server/instance
}

// locationsView is the backup locations pages.
type locationsView struct {
	loc             *locState // the pages' reads
	locAdding       *locAdd   // adding a location
	cloudAdding     *cloudAdd // adding a cloud drive
	restoreLocation *restoreLocation
}

// closeAdding ends adding a location, a cloud drive's login with it.
func (v *locationsView) closeAdding() {
	v.clearCloudSecret()
	v.locAdding, v.cloudAdding = nil, nil
}

// clearCloudSecret forgets the user's own app key held only in memory for
// this login session.
func (v *locationsView) clearCloudSecret() {
	if v.cloudAdding != nil {
		v.cloudAdding.appKey = ""
	}
}

// serversView is the servers pages: the list, adding, installing, settings.
type serversView struct {
	servers             load[[]serverRow]
	removed             []service.RemovedServer      // removed servers, read with the list
	wiz                 *wizard                      // the add-server wizard, nil when closed
	scans               map[string]*serverScan       // agents found by server
	installs            map[string]*installRun       // by server/profile
	customInstalls      map[string]*customInstallRun // by server
	savedInstalls       []config.SavedInstall
	savedInstallsLoaded bool
	savedInstallsErr    error
	passes              map[string]*passState // admin pass actions by server
	settings            map[string]*settingsState
	serverTab           int           // the server detail's tab: 0 overview, 1 settings
	rebuild             *rebuildState // rebuilding the admin passes
	forgetting          string        // the removed server whose record is being forgotten
	forgetErr           error
	serverShell         *serverShellRun // the server command line, nil when none
	kitHook             *kitOps         // tests only
	cleanupHook         *cleanupOps     // tests only
}

// closeInstall lets go of one install screen's plan, output and result.
func (v *serversView) closeInstall(key string) { delete(v.installs, key) }

// forgetServer lets go of everything cached in memory about a server that left.
func (a *App) forgetServer(name string) {
	delete(a.scans, name)
	delete(a.customInstalls, name)
	delete(a.settings, name)
	delete(a.passes, name)
	prefix := name + "/"
	deleteByPrefix(a.installs, prefix)
	deleteByPrefix(a.prots, prefix)
	deleteByPrefix(a.natives, prefix)
	deleteByPrefix(a.agentKits, prefix)
	deleteByPrefix(a.health, prefix)
	deleteByPrefix(a.proposals, prefix)
	deleteByPrefix(a.agentServices, prefix)
	for k := range a.entries {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			a.closeEntry(k)
		}
	}
}

// forgetInstance clears per-instance cached state after an instance is forgotten.
func (a *App) forgetInstance(host, instance string) {
	key := host + "/" + instance
	delete(a.prots, key)
	delete(a.natives, key)
	delete(a.agentKits, key)
	delete(a.health, key)
	delete(a.proposals, key)
	delete(a.agentServices, key)
	a.closeEntry(key)
	delete(a.scans, host)
}

func deleteByPrefix[V any](m map[string]V, prefix string) {
	for k := range m {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(m, k)
		}
	}
}

// keysView is the settings page's keys, backup, personalization and profiles.
type keysView struct {
	setTab      int           // 0 keys, 1 backup, 2 personalization, 3 profiles
	keyFlow     *keyFlow      // the key dialog, nil when closed
	keyNote     string        // a done key action, said once
	lockOff     bool          // the backup lock's off confirmation is open
	recover     *recoverState // finding the passphrase again, nil when not
	profiles    *profilesState
	cfgTransfer *configTransferState
}

// restoreView is restoring, migrating and copying.
type restoreView struct {
	restore         *restoreFlow
	restoreTasks    []service.RestoreTask
	restoreTasksErr error
}
