package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/install"
	"valise/core/valise"
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
