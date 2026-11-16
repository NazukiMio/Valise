package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/protection"
	"valise/core/vaultrpc"
)

func targetNames(t *testing.T, s *Service) string {
	t.Helper()
	st, err := s.VaultStatus(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tg := range st.Targets {
		names = append(names, tg.Name)
	}
	return strings.Join(names, ",")
}

// Over fake-ssh: a copy location a server no longer needs is forgotten
// there and stops coming back in its status; one the plan in force copies
// to stays until the plan lets it go. What the location holds is not
// touched.
func TestTargetRemoveOverFakeSSH(t *testing.T) {
	s, home, ref := acceptedPlanWorld(t)
	ctx := context.Background()
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nas", "old"} {
		if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: name, Kind: "rsync", Dest: "sink:/backup/" + name}); err != nil {
			t.Fatal(err)
		}
	}
	// Something saved at the location, as a file the removal must not touch.
	kept := filepath.Join(home, "backup-at-old")
	os.WriteFile(kept, []byte("saved"), 0o600)
	// The plan copies to "nas".
	on := true
	v, err := s.GetProtection(ctx, "box", protection.GetParams{AgentRef: ref})
	if err != nil || v.ServerPlan == nil {
		t.Fatalf("%+v %v", v, err)
	}
	d := v.ServerPlan.Desired
	d.ServerCopies = []protection.ServerCopy{{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: ref.ServerUID}, Enabled: &on, MinimumInterval: "1h"}}
	plan, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{AgentRef: ref, ExpectedRevision: v.ServerPlan.Revision, Desired: d})
	if err != nil {
		t.Fatal(err)
	}
	if done, err := s.StartProtectionChange(ctx, plan.PlanID, "copy-to-nas"); err != nil || done.State != "succeeded" {
		t.Fatalf("%+v %v", done, err)
	}
	if got := targetNames(t, s); !strings.Contains(got, "nas") || !strings.Contains(got, "old") {
		t.Fatalf("targets %s", got)
	}

	// In use: refused with the reason and what to do; still listed.
	err = s.TargetRemove(ctx, "box", "nas")
	wantCode(t, err, "target_in_use")
	if !strings.Contains(err.Error(), "保护计划") || !strings.Contains(targetNames(t, s), "nas") {
		t.Fatalf("%v, targets %s", err, targetNames(t, s))
	}
	// Not in use: gone from the status, and it stays gone.
	if err := s.TargetRemove(ctx, "box", "old"); err != nil {
		t.Fatal(err)
	}
	if got := targetNames(t, s); got != "nas" {
		t.Fatalf("targets after removal: %s", got)
	}
	if err := s.TargetRemove(ctx, "box", "old"); err != nil {
		t.Fatalf("removing a location already gone: %v", err)
	}
	if b, _ := os.ReadFile(kept); string(b) != "saved" {
		t.Fatal("the removal touched saved files")
	}
	// The plan lets "nas" go: now it can be removed.
	v, _ = s.GetProtection(ctx, "box", protection.GetParams{AgentRef: ref})
	d = v.ServerPlan.Desired
	d.ServerCopies = []protection.ServerCopy{}
	plan, err = s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{AgentRef: ref, ExpectedRevision: v.ServerPlan.Revision, Desired: d})
	if err != nil {
		t.Fatal(err)
	}
	if done, err := s.StartProtectionChange(ctx, plan.PlanID, "no-copies"); err != nil || done.State != "succeeded" {
		t.Fatalf("%+v %v", done, err)
	}
	if err := s.TargetRemove(ctx, "box", "nas"); err != nil {
		t.Fatal(err)
	}
	if got := targetNames(t, s); got != "" {
		t.Fatalf("targets at the end: %q", got)
	}
}
