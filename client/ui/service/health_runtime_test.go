package service

import (
	"strings"
	"testing"

	"valise/core/enroll"
)

func TestManagedHealthNeverUsesNativeRepairOrGatewayAsConversation(t *testing.T) {
	unknown := runtimeServiceItem(&enroll.RuntimeState{IssueCode: "environment_binding_unverified"})
	if unknown.Level != HealthWarn || unknown.ActionID != "" {
		t.Fatal("unknown binding became native success or repair", unknown)
	}
	v := &enroll.RuntimeState{Known: true, Slot: "B", Revision: 1, Running: true, GatewayPassed: true}
	if got := runtimeServiceItem(v); got.Level == HealthOK {
		t.Fatal("gateway without independent listener facts accepted", got)
	}
	v.PortOwned, v.HTTPResponded = true, true
	if got := runtimeServiceItem(v); got.Level != HealthOK || v.ConversationConfirmed {
		t.Fatal("gateway and conversation merged", got, v)
	}
	text := strings.Join(RuntimeHealthLines(v), "\n")
	if !strings.Contains(text, "网关：检查通过") || !strings.Contains(text, "对话确认：尚无确认记录") || strings.Contains(text, "你已确认可以对话") {
		t.Fatal("text frontend merged gateway and conversation", text)
	}
	if got := runtimeServiceItem(&enroll.RuntimeState{Known: true, Stopped: true}); got.Level != HealthBad {
		t.Fatal(got)
	}
	s := &Service{}
	it := HealthItem{Level: HealthBad}
	s.bindServiceRepair(&it, enroll.InstanceSummary{Runtime: v})
	if it.ActionID != "" || it.Key != "" {
		t.Fatal("old native repair offered for managed environment", it)
	}
}
