package protection

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/valise"
	"valise/porter/internal/seal"
)

// A copy location can be forgotten unless a plan in force copies to it;
// an earlier generation's plan does not keep it. Forgetting removes the
// line in targets.json and nothing else.
func TestTargetRemove(t *testing.T) {
	s, a, _ := targetWorld(t) // the accepted plan copies to "nas"; "other" is unreferenced
	ctx := context.Background()
	p := seal.Paths{Home: s.Home}
	remove := func(name string) error {
		return s.WithTargetRemove(ctx, name, func() error { return seal.RemoveTarget(p, name, "client") })
	}
	targets := func() string {
		b, err := os.ReadFile(s.targetsPath())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// In use by the plan in force: refused, with what to do, nothing changed.
	err := remove("nas")
	if code(err) != "target_in_use" || !strings.Contains(err.Error(), a.InstanceID) || !strings.Contains(err.Error(), "保护计划") {
		t.Fatalf("referenced target: %v", err)
	}
	if !strings.Contains(targets(), `"nas"`) {
		t.Fatal("a refused removal changed targets.json")
	}
	// Unreferenced: removed; the other location stays.
	if err := remove("other"); err != nil {
		t.Fatal(err)
	}
	if got := targets(); strings.Contains(got, `"other"`) || !strings.Contains(got, `"nas"`) {
		t.Fatalf("targets after removing other: %s", got)
	}
	// Already gone: done, not an error.
	if err := remove("other"); err != nil {
		t.Fatal(err)
	}
	if err := remove("Bad Name"); code(err) != "target_name_invalid" {
		t.Fatalf("invalid name: %v", err)
	}
	// The agent was enrolled again: the plan is an earlier generation's and
	// no longer in force, so its location may go.
	current := a
	current.Generation = "generation-2"
	s.ValidateAgent = func(_ context.Context, ref identity.AgentRef) error {
		if ref != current {
			return fail("identity_conflict", "测试世代不匹配")
		}
		return nil
	}
	if err := remove("nas"); err != nil {
		t.Fatalf("a superseded plan kept its location: %v", err)
	}
	if strings.Contains(targets(), `"nas"`) {
		t.Fatalf("targets after removing nas: %s", targets())
	}
	audit, _ := os.ReadFile(p.Audit())
	if strings.Count(string(audit), `"op":"target_remove"`) != 2 {
		t.Fatalf("audit: %s", audit)
	}
}

// When the plan's generation cannot be told (not a known earlier one), the
// location stays; and nothing is removed while a plan change is unfinished.
func TestTargetRemoveStaysWhenUnsure(t *testing.T) {
	s, _, _ := targetWorld(t)
	ctx := context.Background()
	s.ValidateAgent = func(context.Context, identity.AgentRef) error { return fail("identity_unbound", "读不到身份") }
	ran := false
	err := s.WithTargetRemove(ctx, "nas", func() error { ran = true; return nil })
	if code(err) != "identity_unbound" || ran {
		t.Fatalf("unsure generation: %v ran %v", err, ran)
	}
}

// WithTargetRemove scans every protection plan on the server and returns all
// active blocking instances together rather than stopping on the first match.
func TestTargetRemoveListsAllBlockingInstances(t *testing.T) {
	s, a, d := targetWorld(t) // "agent" copies to "nas"
	ctx := context.Background()
	yes := true
	b := identity.AgentRef{ServerUID: a.ServerUID, InstallID: "install-b", InstanceID: "beta", Generation: "gen-b"}
	old := identity.AgentRef{ServerUID: a.ServerUID, InstallID: "install-old", InstanceID: "old-gen", Generation: "gen-1"}
	otherOnly := identity.AgentRef{ServerUID: a.ServerUID, InstallID: "install-o", InstanceID: "gamma", Generation: "gen-g"}
	active := map[identity.AgentRef]bool{a: true, b: true, otherOnly: true}
	s.ValidateAgent = func(_ context.Context, ref identity.AgentRef) error {
		if !active[ref] {
			return fail("identity_conflict", "测试世代不匹配")
		}
		return nil
	}

	dBeta := d
	dBeta.ServerCopies = []cp.ServerCopy{
		{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: a.ServerUID}, Enabled: &yes, MinimumInterval: "1h"},
		{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: "other", AuthorityID: a.ServerUID}, Enabled: &yes, MinimumInterval: "1h"},
	}
	apply(t, s, prepare(t, s, b, dBeta, 0))

	dOther := d
	dOther.ServerCopies = []cp.ServerCopy{
		{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: "other", AuthorityID: a.ServerUID}, Enabled: &yes, MinimumInterval: "1h"},
	}
	apply(t, s, prepare(t, s, otherOnly, dOther, 0))

	// Write an earlier-generation plan for old-gen referencing "nas"; it must not block.
	active[old] = true
	apply(t, s, prepare(t, s, old, d, 0))
	delete(active, old)

	p := seal.Paths{Home: s.Home}
	remove := func(name string) error {
		return s.WithTargetRemove(ctx, name, func() error { return seal.RemoveTarget(p, name, "client") })
	}

	err := remove("nas")
	ue, ok := valise.As(err)
	if !ok || ue.Code != "target_in_use" {
		t.Fatalf("expected target_in_use, got %v", err)
	}
	if !strings.Contains(ue.Reason, a.InstanceID) || !strings.Contains(ue.Reason, b.InstanceID) {
		t.Fatalf("reason missing blocking instances: %q", ue.Reason)
	}
	if strings.Contains(ue.Reason, old.InstanceID) || strings.Contains(ue.Reason, otherOnly.InstanceID) {
		t.Fatalf("reason includes non-blocking instance: %q", ue.Reason)
	}
	var detail struct {
		Target            string   `json:"target"`
		BlockingInstances []string `json:"blocking_instances"`
	}
	if err := json.Unmarshal([]byte(ue.Detail), &detail); err != nil {
		t.Fatalf("structured detail unmarshal: %v (%q)", err, ue.Detail)
	}
	if detail.Target != "nas" || !slices.Equal(detail.BlockingInstances, []string{"agent", "beta"}) {
		t.Fatalf("unexpected detail: %+v", detail)
	}

	// Removing "nas" from only one instance still blocks on the remaining one.
	dDrop := d
	dDrop.ServerCopies = []cp.ServerCopy{}
	apply(t, s, prepare(t, s, a, dDrop, 1))
	err = remove("nas")
	ue, ok = valise.As(err)
	if !ok || ue.Code != "target_in_use" || !strings.Contains(ue.Reason, "beta") || strings.Contains(ue.Reason, "agent") {
		t.Fatalf("expected only beta blocking, got %v", err)
	}

	// Removing "nas" from beta too lets removal succeed while keeping "other".
	dBetaDrop := dBeta
	dBetaDrop.ServerCopies = []cp.ServerCopy{
		{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: "other", AuthorityID: a.ServerUID}, Enabled: &yes, MinimumInterval: "1h"},
	}
	apply(t, s, prepare(t, s, b, dBetaDrop, 1))
	if err := remove("nas"); err != nil {
		t.Fatalf("expected removal after unblocking all plans, got %v", err)
	}
	targets, _ := os.ReadFile(s.targetsPath())
	if strings.Contains(string(targets), `"nas"`) || !strings.Contains(string(targets), `"other"`) {
		t.Fatalf("unexpected targets.json: %s", targets)
	}
}
