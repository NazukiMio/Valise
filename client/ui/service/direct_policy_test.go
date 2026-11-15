package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"valise/core/environment"
	"valise/core/revive"
)

func selectDirectTestMode(t *testing.T, s *Service, host, instance string) {
	t.Helper()
	ctx := context.Background()
	v, err := s.InstanceEnvironmentSettings(ctx, host, instance)
	if err != nil {
		t.Fatal(err)
	}
	previous := s.OwnerAuth.Passphrase
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	defer func() { s.OwnerAuth.Passphrase = previous }()
	if _, err := s.SaveEnvironmentSettings(ctx, host, v, environment.Direct); err != nil {
		t.Fatal(err)
	}
}

// Call the old RPCs directly, bypassing the current desktop/service checks.
// This proves the server enforces policy even for an older client.
func TestLegacyDirectRPCsCannotBypassServerRestoreMode(t *testing.T) {
	s, p, home := desktopRestorePlan(t)
	defer p.Revive.Close()
	ctx := context.Background()
	o := p.Revive.target
	plan := p.Revive.Plan
	v, err := s.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	set := func(mode environment.Mode) {
		t.Helper()
		v, err = s.SaveEnvironmentSettings(ctx, "box", v, mode)
		if err != nil {
			t.Fatal(err)
		}
	}
	set(environment.Parallel)
	var j revive.Journal
	err = s.callOwner(ctx, o, nil, revive.MethodStageBegin, revive.StageBeginParams{Plan: plan}, &j)
	if journeyCode(err) != "environment_parallel_required" {
		t.Fatal("old stage_begin bypassed parallel", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local/state/valise/journal", plan.OpID+".json")); !os.IsNotExist(err) {
		t.Fatal("refused request created journal", err)
	}
	set(environment.Direct)
	if err := s.callOwner(ctx, o, nil, revive.MethodStageBegin, revive.StageBeginParams{Plan: plan}, &j); err != nil {
		t.Fatal(err)
	}
	var verified revive.StageVerifyResult
	if err := s.callOwner(ctx, o, nil, revive.MethodStageVerify, revive.StageVerifyParams{OpID: plan.OpID}, &verified); err != nil {
		t.Fatal(err)
	}
	var review revive.ReviewResult
	if err := s.callOwner(ctx, o, nil, revive.MethodReview, revive.OpParams{OpID: plan.OpID}, &review); err != nil || !slices.Contains(review.Actions, "continue") {
		t.Fatal("direct plan missing continuation", review.Actions, err)
	}
	oldDigest := review.Digest
	set(environment.Compact)
	var result revive.SwapResult
	err = s.callOwner(ctx, o, nil, revive.MethodSwap, revive.OpParams{OpID: plan.OpID}, &result)
	if journeyCode(err) != "environment_parallel_required" {
		t.Fatal("old swap bypassed compact mode", err)
	}
	err = s.callOwner(ctx, o, nil, revive.MethodResume, revive.ResumeParams{OpID: plan.OpID, Action: "continue"}, &result)
	if journeyCode(err) != "environment_parallel_required" {
		t.Fatal("old resume bypassed compact mode", err)
	}
	if err := s.callOwner(ctx, o, nil, revive.MethodReview, revive.OpParams{OpID: plan.OpID}, &review); err != nil || !slices.Equal(review.Actions, []string{"back"}) || review.Digest == oldDigest {
		t.Fatal("review did not retain only back", review, err)
	}
	err = s.callOwner(ctx, o, nil, revive.MethodResumeChecked, revive.CheckedResumeParams{OpID: plan.OpID, Digest: oldDigest, Action: "continue"}, &result)
	if journeyCode(err) != "plan_stale" {
		t.Fatal("old review survived policy change", err)
	}
	if err := s.callOwner(ctx, o, nil, revive.MethodResume, revive.ResumeParams{OpID: plan.OpID, Action: "back"}, &result); err != nil {
		t.Fatal("old back was blocked by current mode", err)
	}
	if got := readFile(t, filepath.Join(home, ".demo/memory/a.md")); got != "m" {
		t.Fatal("policy check changed live data", got)
	}
}
