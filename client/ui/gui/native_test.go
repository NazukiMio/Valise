package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/install"
	"valise/core/valise"
	"valise/ui/service"
)

func nativeViewWorld(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a, tt := world(t)
	a.natives["home/xiaoman"] = &nativeState{started: true, info: &install.NativeInfo{InstanceID: "xiaoman", ProfileID: "fixture", Argv: []string{"fixture", "setup"}, Hints: []string{"native fixture hint"}}}
	a.agents.value.Items[0].Instance.NativeCLI = true
	a.openAgent("home", "xiaoman")
	tt.Frame()
	tt.Advance(time.Second)
	return a, tt
}

func TestNativeViewShowsAcceptedPlanWithoutStartingAndRefusesMissingTerminal(t *testing.T) {
	a, tt := nativeViewWorld(t)
	click(t, tt, T("native.open"))
	hasAll(t, tt.Texts(), T("native.title"), T("native.connect"), T("native.privacy"), "native fixture hint")
	if a.native != nil {
		t.Fatal("navigation started remote programme")
	}
	shot(t, tt, "native-before-connect")
	click(t, tt, T("native.connect"))
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
	r := &nativeRun{server: "home", instance: "xiaoman", code: -1, err: valise.New("offsite_pod_required", "fixture gate", "fixture next")}
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
	if a.native == r || a.native.running || a.svc.RiskAccepted {
		t.Fatal("one-operation choice persisted or bypassed terminal availability")
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

// The prepare page offers the native CLI and, where the server's porter
// can open it, the instance's interactive shell. Once a session ended (the
// program exited, as an onboarding does, or the connection dropped) its
// top bar offers to reconnect, to switch to the shell and to go back; the
// ended terminal does not block the page.
func TestNativeEntriesAndReconnectAfterExit(t *testing.T) {
	a, tt := nativeViewWorld(t)
	info := a.natives["home/xiaoman"].info
	show := func() {
		a.navigate("/native/home/xiaoman")
		tt.Frame()
		tt.Advance(time.Second)
	}
	// An older porter: the native CLI only, no entry it cannot serve.
	show()
	if !tt.HasText(T("native.connect")) || tt.HasText(T("native.shell")) {
		t.Fatalf("old porter: %q", tt.Texts())
	}
	info.Shell = true
	show()
	hasAll(t, tt.Texts(), T("native.connect"), T("native.shell"), T("native.shell_hint"))

	// The native CLI exited by itself with code 0.
	ended := &nativeRun{server: "home", instance: "xiaoman", mode: service.NativeCLI, code: 0}
	a.native = ended
	show()
	hasAll(t, tt.Texts(), T("native.exited", "code", 0), T("native.reconnect"), T("native.use_shell"), T("native.to_prepare"), T("native.result_hint"))
	if tt.HasText(T("native.connect")) || tt.HasText(T("native.disconnect")) {
		t.Fatalf("an ended session still shows the entries or a live control: %q", tt.Texts())
	}
	shot(t, tt, "native-exited-actions")
	// Switch to the shell: a new session in shell mode (no terminal
	// component in tests, so nothing is started remotely).
	click(t, tt, T("native.use_shell"))
	if a.native == ended || a.native.mode != service.NativeShell || a.native.running {
		t.Fatalf("switch to shell: %+v", a.native)
	}
	// A shell session that ended offers to reconnect the shell, not to
	// switch to itself.
	a.native = &nativeRun{server: "home", instance: "xiaoman", mode: service.NativeShell, code: 130}
	show()
	if !tt.HasText(T("native.reconnect")) || tt.HasText(T("native.use_shell")) || !tt.HasText(T("native.use_cli")) || !tt.HasText(T("native.to_prepare")) {
		t.Fatalf("ended shell: %q", tt.Texts())
	}
	click(t, tt, T("native.reconnect"))
	if a.native.mode != service.NativeShell {
		t.Fatalf("reconnect changed the mode: %+v", a.native)
	}
	// From an ended shell the native CLI is one click away too.
	a.native = &nativeRun{server: "home", instance: "xiaoman", mode: service.NativeShell, code: 0}
	show()
	click(t, tt, T("native.use_cli"))
	if a.native.mode != service.NativeCLI {
		t.Fatalf("switch to the native CLI: %+v", a.native)
	}
	// A dropped connection: the same bar; going back shows the entries.
	a.native = &nativeRun{server: "home", instance: "xiaoman", code: 255, err: valise.New("terminal_conn_failed", "连接已断开", "检查网络后重新打开")}
	show()
	hasAll(t, tt.Texts(), T("native.reconnect"), T("native.use_shell"), T("native.to_prepare"), "连接已断开")
	click(t, tt, T("native.to_prepare"))
	if a.native != nil || !tt.HasText(T("native.connect")) || !tt.HasText(T("native.shell")) {
		t.Fatalf("back to the prepare page: %+v %q", a.native, tt.Texts())
	}
	// While a session runs there is only its own control.
	_, cancel := context.WithCancel(context.Background())
	a.native = &nativeRun{server: "home", instance: "xiaoman", running: true, cancel: cancel, conn: newInstallConn()}
	show()
	if !tt.HasText(T("native.disconnect")) || tt.HasText(T("native.reconnect")) || tt.HasText(T("native.to_prepare")) {
		t.Fatalf("running: %q", tt.Texts())
	}
	a.stopNative(a.native)
	a.native.running = false
}
