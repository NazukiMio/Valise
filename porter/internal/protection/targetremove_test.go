package protection

import (
	"context"
	"os"
	"strings"
	"testing"

	"valise/core/identity"
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
