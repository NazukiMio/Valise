package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
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

func TestScanAndUnblockTargetAcrossServersAndInstances(t *testing.T) {
	e := fakesshenv.New(t)
	s := newService(t, e.SSH)
	ctx := context.Background()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"demo-a", "demo-b", "demo-c"} {
		write(filepath.Join(s.Dirs.Data, "profiles", id+".yaml"), `schema: valise-profile/1
id: `+id+`
display_name: `+id+`
profile_version: 1
status: draft
detect: { binaries: [`+id+`], state_dir_candidates: ["${HOME}/.`+id+`"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: `+id+` } }
`)
	}
	setupHost := func(host string, profiles []string) string {
		home := e.AddHost(host)
		e.InstallPorter(host)
		write(filepath.Join(home, ".local/bin/systemctl"), n2Systemctl)
		write(filepath.Join(home, ".local/bin/loginctl"), "#!/bin/sh\nprintf 'yes\\n'\n")
		for _, id := range profiles {
			write(filepath.Join(home, "."+id+"/demo.json"), "{}")
			write(filepath.Join(home, ".local/bin/"+id), "#!/bin/sh\necho "+id+" 1.0\n")
		}
		return home
	}
	home1 := setupHost("box1", []string{"demo-a", "demo-b", "demo-c"})
	home2 := setupHost("box2", []string{"demo-a", "demo-b"})

	if _, err := s.AddServers("box1", "box2", "offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"box1", "box2"} {
		if _, _, err := s.KeyRegister(ctx, host, "test"); err != nil {
			t.Fatal(err)
		}
		if err := s.ServerNameSet(ctx, host, host); err != nil {
			t.Fatal(err)
		}
		for _, tg := range []string{"nas", "other"} {
			if err := s.TargetSet(ctx, host, vaultrpc.Target{Name: tg, Kind: "rsync", Dest: "sink:/" + tg}); err != nil {
				t.Fatal(err)
			}
		}
	}
	allowBox2 := true
	s.ProtectionReview = func(_ context.Context, host string, _ protection.FrozenChange) (bool, error) {
		return host != "box2" || allowBox2, nil
	}

	acceptPlan := func(host, profile, instance string, targets []string) identity.AgentRef {
		if _, err := s.Enroll(ctx, host, EnrollOptions{ProfileID: profile, InstanceID: instance, Accept: true}); err != nil {
			t.Fatal(err)
		}
		var b identity.Binding
		if err := s.call(ctx, host, identity.MethodGet, identity.GetParams{InstanceID: instance}, &b); err != nil || b.Agent == nil {
			t.Fatalf("binding %s/%s: %+v %v", host, instance, b, err)
		}
		on := true
		d := protection.NewDefault()
		d.Schedule.DailyAt, d.Schedule.Timezone = "04:40", "UTC"
		d.Period = protection.Period{Unit: "month", Timezone: "UTC"}
		d.ServerCopies = []protection.ServerCopy{}
		for _, tg := range targets {
			d.ServerCopies = append(d.ServerCopies, protection.ServerCopy{
				TargetRef: protection.LocationRef{Kind: "legacy_target", ID: tg, AuthorityID: b.Agent.Ref.ServerUID},
				Enabled:   &on, MinimumInterval: "1h",
			})
		}
		fc, err := s.PrepareProtectionChange(ctx, host, protection.PrepareParams{AgentRef: b.Agent.Ref, Desired: d})
		if err != nil {
			t.Fatal(err)
		}
		if task, err := s.StartProtectionChange(ctx, fc.PlanID, "init-"+host+"-"+instance); err != nil || task.State != "succeeded" {
			t.Fatalf("plan %s/%s: %+v %v", host, instance, task, err)
		}
		return b.Agent.Ref
	}

	// box1: demo-a -> [nas], demo-b -> [nas, other], demo-c -> [other]
	ref1A := acceptPlan("box1", "demo-a", "demo-a", []string{"nas"})
	ref1B := acceptPlan("box1", "demo-b", "demo-b", []string{"nas", "other"})
	acceptPlan("box1", "demo-c", "demo-c", []string{"other"})
	// box2: demo-a -> [nas], demo-b -> [nas] with an earlier-generation (superseded) plan, which must not block.
	ref2A := acceptPlan("box2", "demo-a", "demo-a", []string{"nas"})
	ref2B := acceptPlan("box2", "demo-b", "demo-b", []string{"nas"})
	plan2BPath := filepath.Join(home2, ".config/valise/protection/demo-b.json")
	raw2B, err := os.ReadFile(plan2BPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan2BPath, []byte(strings.ReplaceAll(string(raw2B), ref2B.Generation, "earlier-gen")), 0o600); err != nil {
		t.Fatal(err)
	}
	if v2B, err := s.GetProtection(ctx, "box2", protection.GetParams{AgentRef: ref2B}); err != nil || v2B.Mode != "superseded" {
		t.Fatalf("expected demo-b superseded, got %+v %v", v2B, err)
	}

	scan, err := s.ScanTargetRemoval(ctx, "nas")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(scan.OnServers, []string{"box1", "box2"}) {
		t.Fatalf("OnServers = %v", scan.OnServers)
	}
	if len(scan.Unread) != 1 || scan.Unread[0].Host != "offline" {
		t.Fatalf("Unread = %+v", scan.Unread)
	}
	var gotBlockers []string
	for _, bl := range scan.Blockers {
		gotBlockers = append(gotBlockers, bl.Host+"/"+bl.InstanceID)
	}
	wantBlockers := []string{"box1/demo-a", "box1/demo-b", "box2/demo-a"}
	if !slices.Equal(gotBlockers, wantBlockers) {
		t.Fatalf("Blockers = %v, want %v", gotBlockers, wantBlockers)
	}

	// Partial failure on box2: box1 blockers are updated, box2 stops and returns remaining.
	allowBox2 = false
	remaining, err := s.UnblockTarget(ctx, "nas", scan.Blockers, UnblockOptions{})
	if err == nil {
		t.Fatal("expected failure on box2")
	}
	if ue, ok := valise.As(err); !ok || !strings.Contains(ue.Reason, "box2 / demo-a") {
		t.Fatalf("error should identify failing host/instance: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Host != "box2" || remaining[0].InstanceID != "demo-a" {
		t.Fatalf("remaining = %+v", remaining)
	}
	// Verify box1 plans were updated (demo-a empty, demo-b still keeps "other").
	v1A, err := s.GetProtection(ctx, "box1", protection.GetParams{AgentRef: ref1A})
	if err != nil || v1A.ServerPlan == nil || len(v1A.ServerPlan.Desired.ServerCopies) != 0 {
		t.Fatalf("box1/demo-a plan = %+v, err=%v", v1A, err)
	}
	v1B, err := s.GetProtection(ctx, "box1", protection.GetParams{AgentRef: ref1B})
	if err != nil || v1B.ServerPlan == nil || len(v1B.ServerPlan.Desired.ServerCopies) != 1 || v1B.ServerPlan.Desired.ServerCopies[0].TargetRef.ID != "other" {
		t.Fatalf("box1/demo-b plan = %+v, err=%v", v1B, err)
	}
	// box1/targets.json still has "nas" until all blockers are cleared and TargetRemove runs.
	if b, _ := os.ReadFile(filepath.Join(home1, ".config/valise/targets.json")); !strings.Contains(string(b), `"nas"`) {
		t.Fatalf("box1 targets removed prematurely: %s", b)
	}

	// Retry remaining blocker with approval restored.
	allowBox2 = true
	remaining, err = s.UnblockTarget(ctx, "nas", remaining, UnblockOptions{})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("retry unblock failed: %v remaining=%+v", err, remaining)
	}
	v2A, err := s.GetProtection(ctx, "box2", protection.GetParams{AgentRef: ref2A})
	if err != nil || v2A.ServerPlan == nil || len(v2A.ServerPlan.Desired.ServerCopies) != 0 {
		t.Fatalf("box2/demo-a plan = %+v, err=%v", v2A, err)
	}
	for _, host := range scan.OnServers {
		if err := s.TargetRemove(ctx, host, "nas"); err != nil {
			t.Fatalf("TargetRemove(%s): %v", host, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(home1, ".config/valise/targets.json")); strings.Contains(string(b), `"nas"`) || !strings.Contains(string(b), `"other"`) {
		t.Fatalf("box1 targets after remove: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(home2, ".config/valise/targets.json")); strings.Contains(string(b), `"nas"`) {
		t.Fatalf("box2 targets after remove: %s", b)
	}
}
