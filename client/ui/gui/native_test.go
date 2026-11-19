package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/enroll"
	"valise/core/install"
	"valise/core/valise"
	"valise/ui/service"
)

func nativeViewWorld(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a, tt := world(t)
	a.natives["home/xiaoman"] = &nativeState{started: true, info: &install.NativeInfo{
		InstanceID: "xiaoman", ProfileID: "fixture",
		Argv:  []string{"fixture", "onboard"},
		Hints: []string{"native fixture hint"},
	}}
	a.agents.value.Items[0].Instance.NativeCLI = true
	a.openAgent("home", "xiaoman")
	tt.Frame()
	tt.Advance(time.Second)
	return a, tt
}

func TestNativeViewShowsAcceptedPlanWithoutStartingAndRefusesMissingTerminal(t *testing.T) {
	a, tt := nativeViewWorld(t)
	click(t, tt, T("native.open"))
	hasAll(t, tt.Texts(), T("native.title"), T("native.onboard_title"), T("native.stop_and_onboard"), T("native.connect_direct"), T("native.privacy"), "native fixture hint")
	if a.native != nil {
		t.Fatal("navigation started remote programme")
	}
	shot(t, tt, "native-before-connect")
	click(t, tt, T("native.connect_direct"))
	if a.native == nil || a.native.running || a.native.err == nil || !tt.HasText(T("native.terminal_unavailable")) {
		t.Fatal("missing terminal started programme", a.native, tt.Texts())
	}
	shot(t, tt, "native-terminal-unavailable")
}

func TestNativeExitIsSeparateFromGatewayAndCloseWaitsForCancellation(t *testing.T) {
	a, tt := nativeViewWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	r := &nativeRun{server: "home", instance: "xiaoman", running: true, cancel: cancel, conn: newInstallConn()}
	a.native = r
	reply := make(chan passReply, 1)
	prompt := &passPrompt{host: "home", kind: askAdmin, value: "fixture-passphrase", open: true, reply: reply}
	a.asks = []*passPrompt{prompt}
	closed := false
	if a.RequestClose(func() { closed = true }) || closed || ctx.Err() != context.Canceled || !r.stopped {
		t.Fatal("close did not cancel and wait")
	}
	if len(a.asks) != 0 || prompt.value != "" {
		t.Fatal("closing left management prompt or contents")
	}
	select {
	case answer := <-reply:
		if answer.err == nil || answer.pass != "" {
			t.Fatal("closing answered a secret")
		}
	default:
		t.Fatal("closing left login worker blocked")
	}
	r.running = false
	r.exit()
	if !closed {
		t.Fatal("native end did not release window close")
	}
	a.navigate("/native/home/xiaoman")
	r.stopped, r.code = false, 1
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("native.exited", "code", 1)) || tt.HasText(T("runtime.gateway_passed")) || tt.HasText(T("runtime.conversation_confirmed")) {
		t.Fatal(tt.Texts())
	}
	shot(t, tt, "native-exited")
}

func TestNativeBackupRiskNeedsAnExplicitClickAndIsNotSaved(t *testing.T) {
	a, tt := nativeViewWorld(t)
	r := &nativeRun{server: "home", instance: "xiaoman", stopService: true, code: -1, err: valise.New("offsite_pod_required", "fixture gate", "fixture next")}
	a.native = r
	a.navigate("/native/home/xiaoman")
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("native.continue_once")) || tt.HasText(T("native.connect")) || a.svc.RiskAccepted {
		t.Fatal("missing backup was auto-accepted")
	}
	click(t, tt, T("native.accept_risk"))
	if !tt.HasText(T("native.continue_once")) || a.native != r || a.svc.RiskAccepted {
		t.Fatal("checkbox ran a command or changed service authorization")
	}
	shot(t, tt, "native-backup-choice")
	click(t, tt, T("native.continue_once"))
	if a.native == r || a.native.running || !a.native.stopService || a.svc.RiskAccepted {
		t.Fatal("one-operation choice persisted, lost stopService, or bypassed terminal availability")
	}
}

func TestUnsupportedNativeTransportDoesNotRenderFutureEntry(t *testing.T) {
	a, _ := nativeViewWorld(t)
	n := a.natives["home/xiaoman"]
	n.info = nil
	n.err = valise.New("unsupported_capability", "old porter", "update")
	tt := ui.NewTester(a.View, 860, 560)
	tt.Frame()
	if tt.HasText(T("native.open")) {
		t.Fatal("old porter rendered unsupported native entry")
	}
}

func TestNativeEntriesSessionOnboardAndReconnectAfterExit(t *testing.T) {
	a, tt := nativeViewWorld(t)
	info := a.natives["home/xiaoman"].info
	// When no service is running, direct onboard shows native.connect without service warning.
	info.Service = []enroll.ServiceState{{Unit: "openclaw-gateway.service", LoadState: "loaded", Active: "inactive"}}
	show := func() {
		a.navigate("/native/home/xiaoman")
		tt.Frame()
		tt.Advance(time.Second)
	}
	// An older porter: only onboarding CLI, no session entry. The server
	// command line is never an Agent entry, even when porter reports one.
	info.Shell = true
	show()
	if !tt.HasText(T("native.connect")) || tt.HasText(T("native.session_hint")) || tt.HasText(T("server.shell_open")) || tt.HasText(T("native.stop_and_onboard")) {
		t.Fatalf("old porter: %q", tt.Texts())
	}
	info.Session = true
	info.SessionArgv = []string{"fixture", "tui"}
	show()
	hasAll(t, tt.Texts(), T("native.daily_title"), T("native.session"), T("native.onboard_title"), T("native.connect"))
	if tt.HasText(T("server.shell_open")) {
		t.Fatalf("agent page offers the server command line: %q", tt.Texts())
	}
	shot(t, tt, "native-daily")

	// Onboarding exited with code 0: session and onboarding are the only switches.
	ended := &nativeRun{server: "home", instance: "xiaoman", mode: service.NativeCLI, code: 0}
	a.native = ended
	show()
	hasAll(t, tt.Texts(), T("native.exited", "code", 0), T("native.reconnect"), T("native.use_session"), T("native.to_prepare"), T("native.result_hint"))
	if tt.HasText(T("native.connect")) || tt.HasText(T("native.disconnect")) || tt.HasText(T("native.use_cli")) {
		t.Fatalf("ended onboarding still shows prepare entries or self-switch: %q", tt.Texts())
	}
	shot(t, tt, "native-exited-actions")
	click(t, tt, T("native.use_session"))
	if a.native == ended || a.native.mode != service.NativeSession || a.native.running {
		t.Fatalf("switch to session: %+v", a.native)
	}

	// Ended session offers onboarding, not itself.
	a.native = &nativeRun{server: "home", instance: "xiaoman", mode: service.NativeSession, code: 0}
	show()
	hasAll(t, tt.Texts(), T("native.session_exited", "code", 0), T("native.reconnect"), T("native.use_cli"), T("native.to_prepare"))
	if tt.HasText(T("native.use_session")) {
		t.Fatalf("ended session offered self-switch: %q", tt.Texts())
	}
	click(t, tt, T("native.use_cli"))
	if a.native.mode != service.NativeCLI {
		t.Fatalf("switch to onboarding: %+v", a.native)
	}

	// Dropped connection: back to prepare restores entries.
	a.native = &nativeRun{server: "home", instance: "xiaoman", code: 255, err: valise.New("terminal_conn_failed", "连接已断开", "检查网络后重新打开")}
	show()
	hasAll(t, tt.Texts(), T("native.reconnect"), T("native.use_session"), T("native.to_prepare"), "连接已断开")
	click(t, tt, T("native.to_prepare"))
	if a.native != nil || !tt.HasText(T("native.session_hint")) || !tt.HasText(T("native.connect")) {
		t.Fatalf("back to prepare: %+v %q", a.native, tt.Texts())
	}

	// Active service warning appears when unit becomes active.
	info.Service = []enroll.ServiceState{{Unit: "openclaw-gateway.service", LoadState: "loaded", Active: "active"}}
	tt.Frame()
	hasAll(t, tt.Texts(), T("native.onboard_service_warn", "units", "openclaw-gateway.service"), T("native.stop_and_onboard"), T("native.connect_direct"))

	// While a session runs there is only its own control, and clicking disconnect
	// immediately switches UI to disconnected state without waiting for backend exit.
	_, cancel := context.WithCancel(context.Background())
	a.native = &nativeRun{server: "home", instance: "xiaoman", mode: service.NativeSession, running: true, cancel: cancel, conn: newInstallConn()}
	show()
	hasAll(t, tt.Texts(), T("native.title"), T("native.session_running"), T("native.disconnect"))
	if tt.HasText(T("native.reconnect")) || tt.HasText(T("native.to_prepare")) {
		t.Fatalf("running: %q", tt.Texts())
	}
	if T("native.title") != "原生对话与向导" || T("native.open") != "打开原生对话与向导…" ||
		T("native.session") != "原生对话" || T("native.onboard_title") != "首次设置向导" {
		t.Fatalf("unexpected native labels: %q %q %q %q", T("native.title"), T("native.open"), T("native.session"), T("native.onboard_title"))
	}
	click(t, tt, T("native.disconnect"))
	if !a.native.stopped || !tt.HasText(T("native.disconnected")) || tt.HasText(T("native.disconnect")) {
		t.Fatalf("disconnect did not immediately switch UI: stopped=%v texts=%q", a.native.stopped, tt.Texts())
	}
	a.native.running = false
}
