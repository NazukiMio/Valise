package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/profiles"
	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/login"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/vault"
	"valise/ui/service"
)

func TestWebEntryProposalNotBulkAndRendersHint(t *testing.T) {
	a, _ := locWorld(t)
	created := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	items := []agentkit.Proposal{
		{
			ID:      "p_cls1",
			Kind:    agentkit.ProposalKindClassify,
			Rule:    profile.Override{Root: "state", Glob: "notes/**", Category: "memory"},
			Reason:  "custom notes",
			Created: created,
		},
		{
			ID:   "p_web1",
			Kind: agentkit.ProposalKindWebEntry,
			WebEntry: &agentkit.WebEntrySpec{
				ID:          "webapp",
				DisplayName: "Agent WebApp",
				Port:        7860,
				Path:        "/ui",
				Hint:        "Run myagent webapp first",
			},
			Reason:  "found loopback port 7860",
			Created: created,
		},
	}
	var lastDecide agentkit.DecideParams
	a.proposalsHook = &proposalsOps{
		list: func(context.Context, string, string) ([]agentkit.Proposal, error) {
			return items, nil
		},
		decide: func(_ context.Context, _ string, p agentkit.DecideParams) (*agentkit.DecideResult, error) {
			lastDecide = p
			items = nil
			return &agentkit.DecideResult{Accepted: items, Remaining: 0}, nil
		},
	}

	tt := ui.NewTester(a.View, 1180, 1400)
	a.agentTab = tabHealth
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)

	hasAll(t, tt.Texts(),
		T("prop.web_entry_tag"),
		"Agent WebApp",
		"127.0.0.1:7860/ui",
		T("prop.web_entry_readonly_note"),
		"Run myagent webapp first",
		T("prop.web_entry_accept"),
	)
	// WebEntry must NOT enable "accept all normal" with only 1 classify item.
	if tt.HasText(T("prop.accept_all_normal")) {
		t.Fatalf("web_entry must stay excluded from bulk accept: %s", tt.Texts())
	}

	click(t, tt, T("prop.web_entry_accept"))
	tt.Advance(time.Second)
	if len(lastDecide.Accept) != 1 || lastDecide.Accept[0] != "p_web1" {
		t.Fatalf("DecideParams = %+v, want [p_web1]", lastDecide)
	}
}

func TestEntryLifecycleAgentHintShellAndRemove(t *testing.T) {
	a, _ := locWorld(t)
	a.agents.value.Items[0].Instance.Entries = []enroll.EntryState{
		{ID: "webapp", DisplayName: "Agent WebApp", Host: "127.0.0.1", Port: 7860, Listening: false},
	}
	a.entryProber = func(context.Context, string, string, string) (*login.EntryProbeResult, error) {
		return &login.EntryProbeResult{
			EntryID:     "webapp",
			Port:        7860,
			Listening:   false,
			AgentHint:   "Run myagent web --port 7860",
			LocalSource: "agent_dossier",
		}, nil
	}
	var removed []string
	a.removeLocalEntryFn = func(_ context.Context, host, inst, eid string) error {
		removed = append(removed, host+"/"+inst+"/"+eid)
		return nil
	}

	tt := ui.NewTester(a.View, 1180, 1400)
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)

	// Open entry triggers probe -> modal displays AgentHint attribution + remove + server shell button.
	click(t, tt, T("entry.open"))
	tt.Advance(time.Second)

	hasAll(t, tt.Texts(),
		T("entry.lifecycle.agent_hint_title"),
		"Run myagent web --port 7860",
		T("agent.server_shell"),
		T("entry.lifecycle.remove_local"),
	)

	// Clicking RemoveLocalEntry removes entry from UI and closes modal.
	click(t, tt, T("entry.lifecycle.remove_local"))
	tt.Advance(time.Second)
	if len(removed) != 1 || removed[0] != "home/xiaoman/webapp" {
		t.Fatalf("removed = %v", removed)
	}
	if a.entryLifecycle != nil || len(a.agents.value.Items[0].Instance.Entries) != 0 {
		t.Fatalf("entry not cleared after removal: lifecycle=%v entries=%+v", a.entryLifecycle, a.agents.value.Items[0].Instance.Entries)
	}
}

func TestGenericEnrollModalPreviewAndConfirm(t *testing.T) {
	a, _ := locWorld(t)
	a.scans["home"] = &serverScan{found: []service.DetectedInstance{}}
	var calls []bool
	a.enrollGenericFn = func(_ context.Context, host string, in profiles.GenericProfileInput, accept bool) (*service.EnrollResult, error) {
		calls = append(calls, accept)
		return &service.EnrollResult{
			Host:         host,
			ProfileLocal: true,
			Report: &enroll.Report{
				InstanceID:   "custom-myagent",
				Unclassified: enroll.Tally{Files: 4},
			},
		}, nil
	}

	tt := ui.NewTester(a.View, 1180, 1400)
	a.router.Push("/servers/home/agents")
	tt.Advance(time.Second)

	hasAll(t, tt.Texts(), T("onserver.generic.open"))
	click(t, tt, T("onserver.generic.open"))
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("onserver.generic.title"), T("onserver.generic.scan"))

	// Fill Name and StateDir directly and run preview then confirm.
	a.genericEnroll.name = "MyAgent"
	a.genericEnroll.stateDir = "~/.myagent"
	a.runGenericEnroll(a.genericEnroll, false)
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("onserver.generic.instance"), "custom-myagent", T("onserver.generic.no_secrets_warn"), T("onserver.generic.confirm"))

	click(t, tt, T("onserver.generic.confirm"))
	tt.Advance(time.Second)
	if len(calls) != 2 || calls[0] || !calls[1] || a.genericEnroll != nil {
		t.Fatalf("unexpected calls=%v modal=%v", calls, a.genericEnroll)
	}
}

func TestNoSecretRulesHidesTierSoulInManageTab(t *testing.T) {
	a, _ := locWorld(t)
	noSec := false
	a.agents.value.Items[0].Instance.HasSecretRules = &noSec
	ref := identity.AgentRef{ServerUID: "u", InstallID: "custom-a", InstanceID: "xiaoman", Generation: "g"}
	a.agents.value.Items[0].IdentityState, a.agents.value.Items[0].AgentRef = identity.Bound, &ref
	on := true
	ps := &protState{
		ref: &ref,
		view: &protection.View{
			ServerPlan: &protection.ServerPlan{
				Revision: 1,
				Desired: protection.Desired{
					Scope:        protection.Scope{Kind: "preset", Tier: vault.TierFull},
					Schedule:     protection.Schedule{Enabled: &on, DailyAt: "04:40", Timezone: "UTC"},
					Period:       protection.Period{Unit: "month", Timezone: "UTC"},
					ServerCopies: []protection.ServerCopy{},
				},
			},
		},
	}
	a.prots["home/xiaoman"] = ps

	tt := ui.NewTester(a.View, 1180, 1400)
	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)

	// Manual savepoint omits soul-only and displays no-secrets guard notice.
	hasAll(t, tt.Texts(), T("protect.tier.no_secrets_guard"), T("tier.full"))
	if tt.HasText(T("tier.soul")) {
		t.Fatalf("tier.soul radio must be hidden when NoSecretRules=true: %s", tt.Texts())
	}

	// Tier change modal also hides TierSoul.
	click(t, tt, T("protect.change_tier"))
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("protect.tier.title"), T("protect.tier.full.label"), T("protect.tier.soul_secrets.label"))
	if tt.HasText(T("protect.tier.soul.label")) {
		t.Fatalf("protect.tier.soul.label must be hidden when NoSecretRules=true: %s", tt.Texts())
	}
}
