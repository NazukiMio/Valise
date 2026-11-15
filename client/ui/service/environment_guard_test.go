package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"valise/core/environment"
	"valise/core/revive"
)

func TestLegacyRestoreEntryOwnershipOverFakeSSH(t *testing.T) {
	s, p, home := desktopRestorePlan(t)
	defer p.Revive.Close()
	ctx := context.Background()
	o, err := s.ownerPorter(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	var stage revive.Journal
	if err := s.callOwner(ctx, o, nil, revive.MethodStageBegin, revive.StageBeginParams{Plan: p.Revive.Plan}, &stage); err != nil {
		t.Fatal(err)
	}
	settings, err := s.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a trusted environment owner arriving after an old client's
	// stage was accepted. No systemd behavior is claimed by fake-ssh here.
	r, err := environment.New(settings.Agent)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local/state/valise/environments")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, environment.Key(r.Agent)+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{revive.MethodSwap, revive.MethodStageVerify, revive.MethodResume} {
		var params any = revive.OpParams{OpID: p.Revive.Plan.OpID}
		if method == revive.MethodStageVerify {
			params = revive.StageVerifyParams{OpID: p.Revive.Plan.OpID}
		}
		if method == revive.MethodResume {
			params = revive.ResumeParams{OpID: p.Revive.Plan.OpID, Action: "back"}
		}
		var out any
		if err := s.callOwner(ctx, o, nil, method, params, &out); journeyCode(err) != "environment_in_use" {
			t.Fatalf("old %s bypassed owner: %v", method, err)
		}
	}
	var review revive.ReviewResult
	if err := s.call(ctx, "box", revive.MethodReview, revive.OpParams{OpID: p.Revive.Plan.OpID}, &review); err != nil || len(review.Actions) != 0 {
		t.Fatal("review advertised forbidden branches", review.Actions, err)
	}
	if got := readFile(t, filepath.Join(home, ".demo/memory/a.md")); got != "m" {
		t.Fatal("refused old request altered current Agent", got)
	}
	instances, err := s.Instances(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, in := range instances {
		if in.InstanceID != "demo" {
			continue
		}
		found = true
		if in.Runtime == nil || in.Runtime.Known || in.Runtime.Running || in.Runtime.GatewayPassed || len(in.Service) != 0 || len(in.Entries) != 0 {
			t.Fatal("unknown managed state fell back to old live service", in)
		}
	}
	if !found {
		t.Fatal("Agent disappeared while binding was unknown")
	}
}
