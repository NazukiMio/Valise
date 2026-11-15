package gui

import (
	"context"
	"testing"

	"valise/core/environment"
	"valise/ui/service"
)

func TestEnvironmentDepthFreezesWithSelectedBackup(t *testing.T) {
	a, tt := healthRestoreWorld(t)
	a.health["home/xiaoman"].canEnvironmentTest = true
	click(t, tt, T("restoretest.open"))
	click(t, tt, T("restoretest.depth.isolated"))
	if a.restoreTest.depth != "isolated" || !tt.HasText(T("restoretest.environment.prepare_hint")) {
		t.Fatal("complete test selection missing")
	}
	shot(t, tt, "restore-test-isolated-selection")
	var workers []func()
	a.run = func(f func()) { workers = append(workers, f) }
	called := false
	a.runRestoreTest = func(_ context.Context, req service.SelectedRestoreTest) (*service.RestoreTest, error) {
		called = true
		if req.Depth != "isolated" {
			t.Fatal("key flow silently downgraded complete test")
		}
		return &service.RestoreTest{Depth: req.Depth, State: "failed"}, nil
	}
	click(t, tt, T("restoretest.start"))
	if a.keyFlow == nil || a.keyFlow.then == nil {
		t.Fatal("missing key gate")
	}
	a.restoreTest.depth = "files"
	resume := a.keyFlow.then
	a.keyFlow = nil
	resume()
	workers[0]()
	if !called {
		t.Fatal("confirmed test did not execute")
	}
}

func TestEnvironmentReportKeepsFileAndGatewayEvidenceSeparate(t *testing.T) {
	a, tt := healthRestoreWorld(t)
	a.restoreTest = &restoreTestFlow{server: "home", instance: "xiaoman", result: &service.RestoreTest{Depth: "isolated", State: "failed", Tier: "full", Files: 12, Bytes: 1234, LocalVerified: true, ServerVerified: true,
		Environment: &environment.TestResult{State: "finished", CandidateBuilt: true, ProbeAttempted: true, Probe: environment.ProbeResult{ProofKind: "unix-json", ProcessStarted: true, SocketListening: true, OfflineResponded: true, Completed: true, ProcessesStopped: true}}}}
	tt.Frame()
	hasAll(t, tt.Texts(), T("restoretest.environment.materials"), T("restoretest.environment.socket"), T("restoretest.environment.offline"), T("restoretest.environment.gateway"), T("restoretest.environment.no"))
	if tt.HasText(T("restoretest.environment.passed")) || tt.HasText(T("restoretest.environment.http")) {
		t.Fatal("file or offline response was promoted to complete gateway success")
	}
	shot(t, tt, "restore-test-isolated-offline-result")
}

func TestOlderPorterDoesNotOfferEnvironmentDepth(t *testing.T) {
	_, tt := healthRestoreWorld(t)
	click(t, tt, T("restoretest.open"))
	if tt.HasText(T("restoretest.depth.isolated")) {
		t.Fatal("unsupported complete test rendered")
	}
}

func TestUnadaptedProfileOnlyOffersFilesWithExplanation(t *testing.T) {
	a, tt := healthRestoreWorld(t)
	a.health["home/xiaoman"].environmentUnavailable = true
	click(t, tt, T("restoretest.open"))
	if tt.HasText(T("restoretest.depth.isolated")) || !tt.HasText(T("restoretest.environment.unavailable_profile")) {
		t.Fatal("unadapted profile offered complete test without explanation")
	}
}
