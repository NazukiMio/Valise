package gui

import (
	"context"
	"testing"
	"time"

	"valise/core/valise"
)

func TestAgentRename(t *testing.T) {
	a, tt := world(t)
	var calls []string
	var retErr error
	a.setAgentLabel = func(_ context.Context, host, inst, name string) error {
		calls = append(calls, host+"/"+inst+"="+name)
		return retErr
	}

	// Active agent has rename button; saving custom name updates header and home card.
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)
	if !tt.HasText(T("agent.rename_open")) {
		t.Fatalf("missing rename button: %q", tt.Texts())
	}
	click(t, tt, T("agent.rename_open"))
	hasAll(t, tt.Texts(), T("agent.rename_title"), T("need.valise"))
	if a.agentRename == nil || a.agentRename.value != "小满" {
		t.Fatalf("unexpected initial value: %+v", a.agentRename)
	}
	a.agentRename.value = "  值班助手  "
	tt.Frame()
	click(t, tt, T("agent.rename_save"))
	tt.Advance(time.Second)
	if len(calls) != 1 || calls[0] != "home/xiaoman=值班助手" {
		t.Fatalf("calls = %v", calls)
	}
	if a.agentRename != nil || !tt.HasText("值班助手") {
		t.Fatalf("renamed header missing: dialog=%v texts=%q", a.agentRename, tt.Texts())
	}

	// Clearing label back to empty reverts displayed name to instance ID.
	click(t, tt, T("agent.rename_open"))
	a.agentRename.value = "   "
	tt.Frame()
	click(t, tt, T("agent.rename_save"))
	tt.Advance(time.Second)
	if len(calls) != 2 || calls[1] != "home/xiaoman=" {
		t.Fatalf("calls = %v", calls)
	}
	if !tt.HasText("xiaoman") {
		t.Fatalf("expected fallback to instance id xiaoman: %q", tt.Texts())
	}

	// Failure keeps modal open and shows error.
	retErr = valise.New("owner_failed", "owner proof failed", "")
	click(t, tt, T("agent.rename_open"))
	a.agentRename.value = "新名字"
	tt.Frame()
	click(t, tt, T("agent.rename_save"))
	tt.Advance(time.Second)
	if a.agentRename == nil || !tt.HasText("owner proof failed") {
		t.Fatalf("expected error in modal: %+v %q", a.agentRename, tt.Texts())
	}
	click(t, tt, T("common.cancel"))

	// Active alan still allows rename.
	a.router.Push("/agents/cloud/alan")
	tt.Advance(time.Second)
	if !tt.HasText(T("agent.rename_open")) {
		t.Fatalf("active alan should allow rename")
	}
}
