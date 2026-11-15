package gui

import (
	"github.com/egoist/mygo/ui"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/ui/service"
)

func TestHealthCurrentEnvironmentSeparatesNativeGatewayAndConversation(t *testing.T) {
	a, tt := healthRestoreWorld(t)
	r := a.health["home/xiaoman"].report
	r.Runtime = &enroll.RuntimeState{Known: true, Slot: "B", Revision: 1, Running: true, PortOwned: true, HTTPResponded: true, GatewayPassed: true}
	tt.Frame()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("runtime.title"), T("runtime.slot", "slot", "B"), T("runtime.managed"), T("runtime.running"), T("runtime.gateway_passed"), T("runtime.conversation_pending"))
	if tt.HasText(T("runtime.conversation_confirmed")) {
		t.Fatal("gateway substituted for human conversation")
	}
	shot(t, tt, "health-current-managed-environment")
	r.Runtime.ConversationConfirmed = true
	tt.Frame()
	if !tt.HasText(T("runtime.conversation_confirmed")) || tt.HasText(T("runtime.conversation_pending")) {
		t.Fatal("human observation not separate")
	}
	r.Runtime.Native = true
	tt.Frame()
	if !tt.HasText(T("runtime.native")) {
		t.Fatal("private A copy described as already running")
	}
	r.Runtime = &enroll.RuntimeState{IssueCode: "environment_binding_changing"}
	tt.Frame()
	if !tt.HasText(T("runtime.changing")) || tt.HasText(T("runtime.managed")) || tt.HasText(T("runtime.conversation_confirmed")) || tt.HasText(T("runtime.gateway_passed")) {
		t.Fatal("unknown binding retained prior facts")
	}
	shot(t, tt, "health-current-environment-changing")
}

func TestAgentOverviewDoesNotTreatUnknownManagedAsStoppedOrUnmanaged(t *testing.T) {
	a, _ := world(t)
	row := a.agentRows(service.AgentListResult{Items: []service.AgentSummary{{ConnectionID: "home", Instance: enroll.InstanceSummary{InstanceID: "demo", Runtime: &enroll.RuntimeState{}}}}})[0]
	if row.Service != T("service.unknown") || !row.HasService || row.ServiceTone != "warn" {
		t.Fatal(row)
	}
	text, attention := agentVerdict(row, nil, time.Now())
	if !attention || text != T("agent.verdict.unknown") {
		t.Fatal("unknown environment claimed Agent cannot respond", text)
	}
}

func TestCurrentRuntimeCardSmallWindowMarksCachedRead(t *testing.T) {
	a, _ := world(t)
	v := &enroll.RuntimeState{Known: true, Slot: "A", Revision: 1, Native: true, Running: true}
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(800).Padding(16).Children(func() { a.currentRuntime(c, v, at, true) })
	}, 860, 560)
	tt.Frame()
	hasAll(t, tt.Texts(), T("runtime.refreshing"), T("runtime.read_at", "time", "2026-11-14 12:00:00"), T("runtime.native"), T("runtime.gateway_pending"))
	if tt.HasText(T("runtime.gateway_passed")) || tt.HasText("确认可用") || tt.HasText("清理旧环境") {
		t.Fatal("unknown gateway or unimplemented execution rendered")
	}
	shot(t, tt, "health-current-native-small")
}
