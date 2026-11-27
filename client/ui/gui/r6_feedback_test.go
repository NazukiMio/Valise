package gui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/login"
	"valise/core/profile"
	"valise/ui/service"
)

// (a) Terminal quick action & service install modals can be stopped & closed
// at any time, sending Ctrl+C (0x03) and cancelling the context.
func TestR6TerminalActionStopAndClose(t *testing.T) {
	a, _ := world(t)
	var mu sync.Mutex
	tt := ui.NewTester(a.View, 1180, 1200)
	step := func(fn func()) { mu.Lock(); defer mu.Unlock(); fn(); tt.Frame() }
	a.update = func(fn func()) { mu.Lock(); defer mu.Unlock(); fn() }
	a.run = func(fn func()) { go fn() }

	gotCtrlC := make(chan bool, 1)
	ctxCanceled := make(chan bool, 1)
	started := make(chan struct{})
	a.agentServiceHook = &agentServiceOps{
		status: func(context.Context, string, string) (*service.InstanceServiceInfo, error) {
			return &service.InstanceServiceInfo{
				Instance: "xiaoman", ProfileID: "demo", Unit: "demo.service",
				HasUnit: true, Active: "active",
			}, nil
		},
		actions: func(context.Context, string, string) (*service.ActionList, error) {
			return &service.ActionList{
				Actions: []profile.Action{{
					ID: "tail-logs", Name: "查看实时日志", Risk: profile.RiskRead,
					Terminal: true, Argv: []string{"journalctl", "-f"},
				}},
			}, nil
		},
		access: func(string, profile.Action) string { return "日常通行证" },
		runAct: func(ctx context.Context, _, _, _ string, opts service.ActionOptions) (*service.ActionResult, error) {
			close(started)
			go func() {
				buf := make([]byte, 8)
				if opts.IO.Stdin != nil {
					if n, _ := opts.IO.Stdin.Read(buf); n > 0 && buf[0] == 3 {
						gotCtrlC <- true
					}
				}
			}()
			<-ctx.Done()
			ctxCanceled <- true
			return nil, ctx.Err()
		},
	}

	step(func() { click(t, tt, "小满") })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		tt.Frame()
		ok := tt.HasText("查看实时日志")
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	step(func() { click(t, tt, "查看实时日志") })
	step(func() { click(t, tt, T("health.run")) })
	<-started
	step(func() {})

	mu.Lock()
	hasStop := tt.HasText(T("agent.term.stop_close"))
	mu.Unlock()
	if !hasStop {
		t.Fatalf("missing stop-and-close button while terminal action is running")
	}

	step(func() { click(t, tt, T("agent.term.stop_close")) })
	select {
	case <-ctxCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected action context to be canceled")
	}
	select {
	case <-gotCtrlC:
	case <-time.After(2 * time.Second):
		t.Fatal("expected Ctrl+C byte 0x03 on stdin")
	}
	mu.Lock()
	st := a.agentServices["home/xiaoman"]
	if st != nil && st.actionAsk != nil {
		mu.Unlock()
		t.Fatal("action modal should be closed after stop-and-close")
	}
	mu.Unlock()
}

// (b) Multiple entries can be forwarded concurrently without overwriting each
// other, and closing a local tunnel stays strictly separate from stopping a
// Valise-managed remote panel. Also verifies concurrent custom port forwards.
func TestR6ConcurrentEntriesAndDecoupledRemoteStop(t *testing.T) {
	a, _ := world(t)
	a.agents.value.Items[0].Instance.Entries = []enroll.EntryState{
		{ID: "dashboard", Port: 18789, Listening: true},
		{ID: "webapp", Port: 18790, Listening: true},
	}
	a.entryDisplay["openclaw/dashboard"] = "控制台"
	a.entryDisplay["openclaw/webapp"] = "Web 面板"

	var openedMu sync.Mutex
	var openedURLs []string
	a.openURL = func(u string) error {
		openedMu.Lock()
		openedURLs = append(openedURLs, u)
		openedMu.Unlock()
		return nil
	}
	a.entryProber = func(_ context.Context, _, _, entryID string) (*login.EntryProbeResult, error) {
		return &login.EntryProbeResult{
			EntryID: entryID, Port: 18789, Listening: true,
			ManagedByUs: entryID == "webapp", ManagedAlive: entryID == "webapp",
		}, nil
	}
	var stoppedRemote []string
	a.entryStopper = func(_ context.Context, _, _, entryID string) (*login.EntryStopResult, error) {
		stoppedRemote = append(stoppedRemote, entryID)
		return &login.EntryStopResult{EntryID: entryID, Stopped: true, Listening: false}, nil
	}
	// Keep tunnels alive until explicitly canceled.
	a.entryOpener = func(ctx context.Context, _, _ string, o service.OpenOptions) error {
		url := "http://127.0.0.1/" + o.Entry
		o.Status(service.EntryStatus{Step: service.EntryTunnel, OK: true})
		o.Status(service.EntryStatus{Step: service.EntryRemote, OK: true})
		o.Ready(url, url)
		<-ctx.Done()
		return nil
	}
	var mu sync.Mutex
	tt := ui.NewTester(a.View, 1180, 1200)
	a.update = func(fn func()) { mu.Lock(); defer mu.Unlock(); fn() }
	a.run = func(fn func()) { go fn() }
	step := func(fn func()) { mu.Lock(); defer mu.Unlock(); fn(); tt.Frame() }

	step(func() { click(t, tt, "小满") })
	// Open first entry ("dashboard")
	step(func() {
		a.requestOpenEntry(&agentRow{Server: "home", Instance: "xiaoman", Profile: "openclaw"}, "dashboard")
	})
	// Open second entry ("webapp") concurrently
	step(func() {
		a.requestOpenEntry(&agentRow{Server: "home", Instance: "xiaoman", Profile: "openclaw"}, "webapp")
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		d := a.entries[entryKey("home", "xiaoman", "dashboard")]
		w := a.entries[entryKey("home", "xiaoman", "webapp")]
		ready := d != nil && d.open && w != nil && w.open
		mu.Unlock()
		if ready {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	step(func() {})

	mu.Lock()
	if len(a.entries) != 2 {
		mu.Unlock()
		t.Fatalf("expected 2 concurrent entries, got %d", len(a.entries))
	}
	texts := tt.Texts()
	mu.Unlock()
	hasAll(t, texts, "控制台", "Web 面板", T("entry.close_tunnel"), T("entry.stop_remote"))

	// Closing local tunnel for webapp must NOT stop remote panel.
	step(func() { a.closeEntry(entryKey("home", "xiaoman", "webapp")) })
	mu.Lock()
	if len(stoppedRemote) != 0 {
		mu.Unlock()
		t.Fatalf("closing tunnel must not stop remote panel, got %v", stoppedRemote)
	}
	if !a.isEntryManaged(entryKey("home", "xiaoman", "webapp")) {
		mu.Unlock()
		t.Fatal("webapp should remain marked managed after closing local tunnel")
	}
	mu.Unlock()

	// Explicitly clicking Stop Remote Panel calls StopEntry.
	step(func() { click(t, tt, T("entry.stop_remote")) })
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(stoppedRemote) == 1 && !a.isEntryManaged(entryKey("home", "xiaoman", "webapp"))
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	mu.Lock()
	if len(stoppedRemote) != 1 || stoppedRemote[0] != "webapp" {
		mu.Unlock()
		t.Fatalf("expected remote stop for webapp, got %v", stoppedRemote)
	}
	// Clean up remaining tunnel.
	a.closeEntry(entryKey("home", "xiaoman", "dashboard"))
	mu.Unlock()

	// Multiple custom port forwards stay active concurrently.
	a.forwardPageFn = func(ctx context.Context, _ string, ft *service.ForwardTarget, o service.OpenOptions) error {
		o.Ready(ft.Display, ft.Display)
		<-ctx.Done()
		return nil
	}
	step(func() {
		row := &agentRow{Server: "home", Instance: "xiaoman"}
		st := a.forwardOf("home/xiaoman")
		a.startForward(row, st, "59801")
		a.startForward(row, st, "59802")
	})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		st := a.forwards["home/xiaoman"]
		ok := st != nil && len(st.items) == 2 && st.items[0].active && st.items[1].active
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	mu.Lock()
	st := a.forwards["home/xiaoman"]
	if len(st.items) != 2 {
		mu.Unlock()
		t.Fatalf("expected 2 concurrent custom forwards, got %d", len(st.items))
	}
	a.stopForwardItem(st, st.items[0])
	if len(st.items) != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 remaining forward after stopping one, got %d", len(st.items))
	}
	a.stopForward(st)
	mu.Unlock()
}

// (c) Unlistening entry triggers ProbeEntry -> prepare/start modal ->
// PrepareEntry -> StartEntry -> openEntry.
func TestR6ProbePrepareStartAndOpenEntry(t *testing.T) {
	a, tt := world(t)
	a.run = func(f func()) { f() }

	var calls []string
	var openedURL string
	a.openURL = func(u string) error { openedURL = u; return nil }
	a.entryProber = func(_ context.Context, _, _, entryID string) (*login.EntryProbeResult, error) {
		calls = append(calls, "probe:"+entryID)
		return &login.EntryProbeResult{
			EntryID: entryID, Port: 18789, Listening: false,
			HasPrepare: true, Prepared: false, PrepareNote: "启用内置 Web 服务配置",
			HasStart: true,
		}, nil
	}
	a.entryPreparer = func(_ context.Context, _, _, entryID string) (*login.EntryPrepareResult, error) {
		calls = append(calls, "prepare:"+entryID)
		return &login.EntryPrepareResult{EntryID: entryID, StepsRun: 1, Prepared: true}, nil
	}
	a.entryStarter = func(_ context.Context, _, _, entryID string) (*login.EntryStartResult, error) {
		calls = append(calls, "start:"+entryID)
		return &login.EntryStartResult{EntryID: entryID, Port: 18789, Listening: true, ManagedByUs: true}, nil
	}
	a.entryOpener = func(_ context.Context, _, _ string, o service.OpenOptions) error {
		calls = append(calls, "open:"+o.Entry)
		o.Ready("http://127.0.0.1:18789/", "http://127.0.0.1:18789/")
		return nil
	}

	click(t, tt, "小满")
	click(t, tt, T("entry.open"))
	hasAll(t, tt.Texts(), "启用内置 Web 服务配置", T("entry.lifecycle.prepare_and_start"), T("entry.lifecycle.try_forward"))

	click(t, tt, T("entry.lifecycle.prepare_and_start"))
	want := "probe:gateway,prepare:gateway,start:gateway,open:gateway"
	if got := strings.Join(calls, ","); got != want {
		t.Fatalf("call order = %q, want %q", got, want)
	}
	if openedURL != "http://127.0.0.1:18789/" {
		t.Fatalf("openedURL = %q", openedURL)
	}
	if !a.isEntryManaged(entryKey("home", "xiaoman", "gateway")) {
		t.Fatal("expected entry to be marked managed after StartEntry")
	}
}

// (d) Migrated-out agent shows reactivation button, coexist blocks submission
// until bot-conflict checkbox is checked, and switch_back calls ReactivateAgent
// and restores active state in the agent list.
func TestR6MigratedAgentReactivateFlow(t *testing.T) {
	a, tt := world(t)
	a.run = func(f func()) { f() }
	a.agents.value.Items[0].MigratedTo = &service.AgentMigration{
		ConnectionID: "cloud",
		AgentRef:     identity.AgentRef{InstanceID: "xiaoman-new"},
	}

	var gotReq *service.ReactivateAgentRequest
	a.reactivateFn = func(_ context.Context, req service.ReactivateAgentRequest) (*service.ReactivateAgentResult, error) {
		cp := req
		gotReq = &cp
		return &service.ReactivateAgentResult{
			ConnectionID: req.ConnectionID, InstanceID: req.InstanceID, Mode: req.Mode, TargetStopped: true,
		}, nil
	}

	a.navigate("/agents/home/xiaoman")
	tt.Advance(time.Second)
	if !tt.HasText(T("agent.reactivate.open")) {
		t.Fatalf("expected reactivate primary button on migrated agent header, got %q", tt.Texts())
	}

	// Open modal, switch to coexist without checking confirmation -> blocked.
	click(t, tt, T("agent.reactivate.open"))
	click(t, tt, T("agent.reactivate.mode_coexist"))
	hasAll(t, tt.Texts(), T("agent.reactivate.coexist_warn_title"), T("agent.reactivate.coexist_confirm"))
	click(t, tt, T("agent.reactivate.submit_coexist"))
	if gotReq != nil {
		t.Fatal("coexist without bot-conflict confirmation must be blocked")
	}
	if !tt.HasText(T("agent.reactivate.coexist_need_confirm")) {
		t.Fatalf("expected frontend intercept warning, got %q", tt.Texts())
	}

	// Switch back to switch_back and submit -> calls ReactivateAgent and clears MigratedTo.
	click(t, tt, T("agent.reactivate.mode_switch_back"))
	click(t, tt, T("agent.reactivate.submit_switch_back"))
	if gotReq == nil {
		t.Fatal("expected ReactivateAgent to be called")
	}
	if gotReq.Mode != enroll.ReactivateModeSwitchBack || !gotReq.StartService ||
		gotReq.TargetConnectionID != "cloud" || gotReq.TargetInstanceID != "xiaoman-new" {
		t.Fatalf("unexpected reactivate request: %+v", *gotReq)
	}
	if a.agents.value.Items[0].MigratedTo != nil {
		t.Fatal("expected MigratedTo to be cleared after switch_back")
	}
	if tt.HasText(T("agent.reactivate.open")) {
		t.Fatal("reactivate button should disappear once agent is active again")
	}
}

// (e) Guardrails: every gui/*.go file <= 400 lines and no hardcoded harness IDs.
func TestR6FileLimitsAndNoHardcodedHarnesses(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	harnessRe := regexp.MustCompile(`(?i)\b(openclaw|hermes|codex|claude-code|zeroclaw|nanobot|picoclaw|openfang)\b`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(b), "\n")
		if len(lines) > 401 {
			t.Errorf("%s has %d lines (max 400)", f, len(lines)-1)
		}
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for i, line := range lines {
			code, _, _ := strings.Cut(line, "//")
			if m := harnessRe.FindString(code); m != "" {
				t.Errorf("%s:%d: hardcoded harness %q", f, i+1, m)
			}
		}
	}
}
