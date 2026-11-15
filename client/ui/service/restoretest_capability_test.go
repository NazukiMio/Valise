package service

import (
	"testing"

	"valise/core/environment"
	"valise/core/revive"
	"valise/core/rpc"
)

func TestComposedStageRequiresVersionTwoOnFreshConnection(t *testing.T) {
	p := revive.StageBeginParams{Plan: revive.Plan{TestOnly: true, IsolatedTest: &environment.TestInput{Baseline: &environment.TestSnapshot{}}}}
	if err := n2Compatibility(&rpc.HelloResult{Capabilities: []string{environment.TestCapability}}, revive.MethodStageBegin, p); err == nil {
		t.Fatal("v1 peer could silently ignore baseline")
	}
	if err := n2Compatibility(&rpc.HelloResult{Capabilities: []string{environment.TestCapability, environment.TestCompositionCapability}}, revive.MethodStageBegin, p); err != nil {
		t.Fatal(err)
	}
	p.Plan.IsolatedTest.Baseline = nil
	if err := n2Compatibility(&rpc.HelloResult{Capabilities: []string{environment.TestCapability}}, revive.MethodStageBegin, p); err != nil {
		t.Fatal("full-only v1 request lost compatibility", err)
	}
}
