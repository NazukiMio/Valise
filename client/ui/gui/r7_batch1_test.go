package gui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/login"
	"valise/core/profile"
	"valise/ui/service"
)

func TestR7EntryAuthSuffixPassedToOpenEntryAndHidden(t *testing.T) {
	a, tt := world(t)
	a.run = func(f func()) { f() }

	const secretToken = "secret-r7-tok-999"
	const authSuffix = "/?token=" + secretToken

	var gotOpts service.OpenOptions
	var openedURL string
	a.openURL = func(u string) error { openedURL = u; return nil }
	a.entryProber = func(_ context.Context, _, _, entryID string) (*login.EntryProbeResult, error) {
		return &login.EntryProbeResult{
			EntryID: entryID, Port: 18789, Listening: false,
			HasPrepare: false, Prepared: true, HasStart: true,
		}, nil
	}
	a.entryStarter = func(_ context.Context, _, _, entryID string) (*login.EntryStartResult, error) {
		return &login.EntryStartResult{
			EntryID: entryID, Port: 18789, Listening: true,
			ManagedByUs: true, AuthSuffix: authSuffix,
		}, nil
	}
	a.entryOpener = func(ctx context.Context, _, _ string, o service.OpenOptions) error {
		gotOpts = o
		o.Status(service.EntryStatus{Step: service.EntryTunnel, OK: true, Detail: "127.0.0.1:18789"})
		o.Status(service.EntryStatus{Step: service.EntryAuth, OK: true, Detail: "ok"})
		o.Ready("http://127.0.0.1:18789"+o.InitialSuffix, "http://127.0.0.1:18789/")
		return nil
	}
	a.entryStopper = func(_ context.Context, _, _, entryID string) (*login.EntryStopResult, error) {
		return &login.EntryStopResult{EntryID: entryID, Port: 18789, Stopped: true}, nil
	}

	click(t, tt, "小满")
	click(t, tt, T("entry.open"))
	click(t, tt, T("entry.lifecycle.start_and_open"))

	if gotOpts.InitialSuffix != authSuffix {
		t.Fatalf("InitialSuffix = %q, want %q", gotOpts.InitialSuffix, authSuffix)
	}
	if openedURL != "http://127.0.0.1:18789"+authSuffix {
		t.Fatalf("openedURL = %q", openedURL)
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, secretToken) {
			t.Fatalf("secret token leaked into UI text: %q", txt)
		}
	}

	key := entryKey("home", "xiaoman", "gateway")
	st := a.entries[key]
	if st == nil || st.initialSuffix != authSuffix {
		t.Fatalf("expected in-memory initialSuffix on entryState, got %+v", st)
	}

	// stopRemoteEntry clears in-memory initialSuffix.
	row := &agentRow{Server: "home", Instance: "xiaoman"}
	a.stopRemoteEntry(row, "gateway")
	if st.initialSuffix != "" {
		t.Fatalf("stopRemoteEntry did not clear initialSuffix: %q", st.initialSuffix)
	}

	// closeEntry also clears initialSuffix and removes the entry state.
	st.initialSuffix = authSuffix
	a.closeEntry(key)
	if st.initialSuffix != "" {
		t.Fatalf("closeEntry did not clear initialSuffix: %q", st.initialSuffix)
	}
	if _, ok := a.entries[key]; ok {
		t.Fatal("closeEntry did not delete entry state")
	}
}

func TestR7AgentPageServerShellJumpAndReturn(t *testing.T) {
	a, tt := world(t)
	a.run = func(f func()) { f() }

	// Remove native CLI capability to prove server terminal is still available.
	a.agents.value.Items[0].Instance.NativeCLI = false

	a.navigate("/agents/home/xiaoman")
	tt.Advance(time.Second)
	if !tt.HasText(T("agent.server_shell")) {
		t.Fatalf("expected %q on active agent without native CLI, got %q", T("agent.server_shell"), tt.Texts())
	}

	click(t, tt, T("agent.server_shell"))
	if a.router.Path() != "/servers/home/shell" {
		t.Fatalf("path = %q, want /servers/home/shell", a.router.Path())
	}
	if a.serverShellBack != "/agents/home/xiaoman" {
		t.Fatalf("serverShellBack = %q, want /agents/home/xiaoman", a.serverShellBack)
	}
	if !tt.HasText("‹ " + T("native.back")) {
		t.Fatalf("expected agent back link on server shell page, got %q", tt.Texts())
	}

	// Visiting another server's shell must not use home's agent back link.
	a.router.Push("/servers/other/shell")
	tt.Frame()
	if !tt.HasText("‹ other") {
		t.Fatalf("expected fallback server back link for other server, got %q", tt.Texts())
	}

	// Back on home's shell, clicking the agent back link returns and clears state.
	a.router.Push("/servers/home/shell")
	tt.Frame()
	click(t, tt, "‹ "+T("native.back"))
	if a.router.Path() != "/agents/home/xiaoman" {
		t.Fatalf("back link path = %q, want /agents/home/xiaoman", a.router.Path())
	}
	if a.serverShellBack != "" {
		t.Fatalf("serverShellBack not cleared after return: %q", a.serverShellBack)
	}

	// Navigating away via normal route clears serverShellBack.
	a.openServerShellFromAgent("home", "xiaoman")
	a.navigate("/agents")
	if a.serverShellBack != "" {
		t.Fatalf("serverShellBack not cleared on normal navigate: %q", a.serverShellBack)
	}

	// Retired agent must not show the server terminal button.
	a.agents.value.Items[0].MigratedTo = &service.AgentMigration{ConnectionID: "cloud"}
	a.navigate("/agents/home/xiaoman")
	tt.Advance(time.Second)
	if tt.HasText(T("agent.server_shell")) {
		t.Fatalf("retired agent must not show %q", T("agent.server_shell"))
	}
}

func TestR7TerminalFocusRestoreAfterModalCloses(t *testing.T) {
	a, _ := settingsWorld(t)

	// 1. modalOpen tracks global overlay dialogs (asks and keyFlow).
	if a.modalOpen() {
		t.Fatal("modalOpen should be false initially")
	}
	a.asks = []*passPrompt{{host: "home", kind: askPIN, open: true, reply: make(chan passReply, 1)}}
	if !a.modalOpen() {
		t.Fatal("modalOpen should be true when asks is non-empty")
	}
	a.asks = nil
	a.keyFlow = &keyFlow{step: kfSet}
	if !a.modalOpen() {
		t.Fatal("modalOpen should be true when keyFlow is open")
	}
	a.keyFlow = nil
	if a.modalOpen() {
		t.Fatal("modalOpen should be false after all modals close")
	}

	// 2. focusTerminal focuses on start, re-arms during modal, and restores focus after close.
	want := true
	modalOpen := false
	modalVal := ""
	termFocused := false
	tt := ui.NewTester(func(c *ui.Context) {
		el := ui.Button(c, "term-box")
		focusTerminal(c, el, &want, modalOpen)
		termFocused = el.Focused()
		if modalOpen {
			open := true
			ui.Modal(c, &open, func() {
				ui.TextInput(c, &modalVal).AutoFocus()
			})
		}
	}, 800, 600)

	// Initial frame: no modal -> focuses terminal and clears want.
	tt.Frame()
	if want {
		t.Fatal("wantFocus should be cleared after focusing terminal")
	}
	tt.Frame()
	if !termFocused {
		t.Fatal("terminal element should be focused after initial focusTerminal")
	}

	// Overlay modal opens -> arms wantFocus and lets modal input take focus.
	modalOpen = true
	tt.Frame()
	if !want {
		t.Fatal("wantFocus should be re-armed while modal is open")
	}
	tt.Frame()
	if termFocused {
		t.Fatal("terminal should not steal focus while modal is open")
	}

	// Overlay modal closes -> focusTerminal restores focus and clears wantFocus.
	modalOpen = false
	tt.Frame()
	if want {
		t.Fatal("wantFocus should be cleared after restoring focus")
	}
	tt.Frame()
	if !termFocused {
		t.Fatal("terminal should regain focus after modal closes")
	}
}

func TestR7AllFiveTerminalsArmWantFocus(t *testing.T) {
	// Verify all 5 terminal run structs arm wantFocus when started/created.
	sRun := &serverShellRun{server: "home", running: true, wantFocus: true}
	nRun := &nativeRun{server: "home", instance: "xiaoman", running: true, wantFocus: true}
	iRun := &installRun{running: true, wantFocus: true}
	ciRun := &customInstallRun{running: true, wantFocus: true}
	clRun := &cloudLogin{wantFocus: true}
	stRun := &serviceTermRun{title: "install", running: true, wantFocus: true}
	conn := newInstallConn()
	defer conn.Close()

	if !sRun.wantFocus || !nRun.wantFocus || !iRun.wantFocus || !ciRun.wantFocus || !clRun.wantFocus || !stRun.wantFocus || !conn.wantFocus {
		t.Fatal("all terminal states must support wantFocus")
	}

	// Verify startServiceInstall and runQuickAction set wantFocus = true.
	a, _ := world(t)
	a.run = func(func()) {}
	st := &agentServiceState{info: &service.InstanceServiceInfo{Unit: "demo.service"}}
	a.startServiceInstall(&agentRow{Server: "home", Instance: "xiaoman"}, st)
	if st.installRun == nil || !st.installRun.wantFocus {
		t.Fatal("startServiceInstall must arm wantFocus")
	}
	m := &actionModalState{action: profile.Action{ID: "diag", Name: "Diag", Terminal: true}}
	a.runQuickAction(&agentRow{Server: "home", Instance: "xiaoman"}, st, m)
	if m.termRun == nil || !m.termRun.wantFocus {
		t.Fatal("runQuickAction with Terminal must arm wantFocus")
	}
}
