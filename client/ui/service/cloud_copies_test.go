package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/config"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/rpc"
)

func TestScheduleV3Capability(t *testing.T) {
	old := &rpc.HelloResult{Capabilities: []string{protection.Capability, protection.CapabilityV2}}
	mk := func(s protection.Schedule) protection.PrepareParams {
		p := protection.PrepareParams{}
		p.Desired.Schedule = s
		return p
	}
	for _, s := range []protection.Schedule{{}, {Cadence: "daily"}, {DailyAt: "03:00"}} {
		if err := n2Compatibility(old, protection.MethodPrepare, mk(s)); err != nil {
			t.Fatalf("daily/disabled %+v must work on old porter: %v", s, err)
		}
	}
	hourly := mk(protection.Schedule{Cadence: "hourly", EveryHours: 6})
	wantCode(t, n2Compatibility(old, protection.MethodPrepare, hourly), "unsupported_capability")
	old.Capabilities = append(old.Capabilities, protection.CapabilityV3)
	if err := n2Compatibility(old, protection.MethodPrepare, hourly); err != nil {
		t.Fatal(err)
	}
}

func TestCloudCopiesStoreAndRules(t *testing.T) {
	ctx := context.Background()
	repos := []config.Repo{{Name: "usb", Path: "/x"}}
	clouds := []config.Cloud{{Name: "gd"}, {Name: "od"}}
	dir := t.TempDir()
	store := NewProtectionBindingStore(dir, repos).WithClouds(clouds)
	a := protectionRequest().AgentRef
	on, off := true, false
	repoCopies := []protection.LocalCopy{{RepoID: "usb", AfterBackup: &on, OnClientOpen: &on}}
	refs := map[string]identity.AgentRef{"a": a}

	// No binding: every cloud wanted.
	if !cloudRule(ctx, TriggerBackup, []string{"a"}, refs, store)("gd") {
		t.Fatal("no binding must want clouds")
	}
	// Legacy CAS leaves cloud_copies omitted from the file.
	if _, err := store.CompareAndSwap(ctx, a, 0, 1, repoCopies); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "protection-bindings.json"))
	if strings.Contains(string(raw), "cloud_copies") {
		t.Fatalf("legacy binding grew cloud_copies: %s", raw)
	}
	if !cloudRule(ctx, TriggerOpen, []string{"a"}, refs, store)("od") {
		t.Fatal("binding without cloud entry must want clouds")
	}
	// Unknown cloud is rejected.
	_, err := store.CompareAndSwapCopies(ctx, a, 1, 1, repoCopies, []protection.LocalCopy{{RepoID: "nope", AfterBackup: &on, OnClientOpen: &on}})
	wantCode(t, err, "cloud_unknown")
	b, err := store.CompareAndSwapCopies(ctx, a, 1, 1, repoCopies, []protection.LocalCopy{{RepoID: "gd", AfterBackup: &off, OnClientOpen: &on}})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.CloudCopies) != 1 {
		t.Fatalf("%+v", b)
	}
	if cloudRule(ctx, TriggerBackup, []string{"a"}, refs, store)("gd") || !cloudRule(ctx, TriggerOpen, []string{"a"}, refs, store)("gd") {
		t.Fatal("gd: off after backup, on at open")
	}
	if !cloudRule(ctx, TriggerBackup, []string{"a"}, refs, store)("od") {
		t.Fatal("od has no entry: wanted")
	}
	// A legacy CAS keeps the saved cloud choices.
	b, err = store.CompareAndSwap(ctx, a, 2, 1, repoCopies)
	if err != nil || len(b.CloudCopies) != 1 {
		t.Fatalf("cloud choices lost: %+v %v", b, err)
	}
	repoView, cloudView := effectiveCopies(config.Config{Repos: repos, Clouds: clouds}, b)
	if len(repoView) != 1 || len(cloudView) != 2 || *cloudView[0].AfterBackup || !*cloudView[1].AfterBackup {
		t.Fatalf("view %+v %+v", repoView, cloudView)
	}
	repoView, cloudView = effectiveCopies(config.Config{Repos: repos, Clouds: clouds}, nil)
	if !*repoView[0].AfterBackup || !*cloudView[1].OnClientOpen {
		t.Fatal("nil binding defaults on")
	}
}

func TestAutoCopyCloudBindingOff(t *testing.T) {
	s, _, _, _ := sealedWorld(t)
	ctx := context.Background()
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	s.Config.Clouds = []config.Cloud{{Name: "gd"}}
	refs, err := s.agentRefs(ctx, "box", []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := refs["demo"]
	if !ok {
		t.Fatal("expected demo to have a bound identity")
	}
	off := false
	if _, err := s.bindingStore().CompareAndSwapCopies(ctx, ref, 0, 1,
		[]protection.LocalCopy{},
		[]protection.LocalCopy{{RepoID: "gd", AfterBackup: &off, OnClientOpen: &off}},
	); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		trigger CopyTrigger
		opts    AutoCopyOptions
	}{
		{TriggerBackup, AutoCopyOptions{Instance: "demo"}},
		{TriggerOpen, AutoCopyOptions{}},
	} {
		res, err := s.AutoCopy(ctx, "box", tc.trigger, tc.opts)
		if err != nil {
			t.Fatalf("trigger=%s: %v", tc.trigger, err)
		}
		if len(res.Locations) != 1 || res.Locations[0].Location != "gd" || res.Locations[0].Kind != "cloud" || res.Locations[0].State != CopyOff {
			t.Fatalf("trigger=%s: expected cloud gd CopyOff, got %+v", tc.trigger, res.Locations)
		}
		if res.Changed() {
			t.Fatalf("trigger=%s: CopyOff should not report Changed()", tc.trigger)
		}
	}
}
