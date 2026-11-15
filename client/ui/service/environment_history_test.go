package service

import (
	"context"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/revive"
)

func TestParallelHistoricalStatusCannotBorrowCurrentFacts(t *testing.T) {
	agent := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "generation"}
	task := RestoreTask{TargetAgent: &agent, OpID: "operation", EnvironmentMode: environment.Parallel, Snapshot: journey.SnapshotRef{VaultID: "vault", SnapshotID: "snapshot"}}
	v := environment.SwitchStatus{PreparationParams: parallelTaskParams(task), Snapshot: identity.SnapshotRef{VaultID: "vault", SnapshotID: "snapshot"}, Mode: environment.Parallel,
		Original: environment.Ref{Slot: environment.A, Revision: 1}, Candidate: environment.Ref{Slot: environment.B, Revision: 1}, Phase: environment.Accepted, Historical: true, HistoricalConfirmed: true}
	if err := validateSwitchStatus(task, &v); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*environment.SwitchStatus){
		func(v *environment.SwitchStatus) { v.Current = &v.Candidate },
		func(v *environment.SwitchStatus) { v.CurrentObserved = true },
		func(v *environment.SwitchStatus) { v.CurrentRunning = true },
		func(v *environment.SwitchStatus) { v.OriginalObserved = true },
		func(v *environment.SwitchStatus) { v.GatewayPassed = true },
		func(v *environment.SwitchStatus) { v.ConversationKnown = true },
		func(v *environment.SwitchStatus) { v.CanReviewOldCleanup = true },
		func(v *environment.SwitchStatus) { v.Phase = environment.AwaitingHuman },
	} {
		bad := v
		change(&bad)
		if err := validateSwitchStatus(task, &bad); err == nil {
			t.Fatal("historical status borrowed current facts", bad)
		}
	}
}

func TestParallelReuseChoiceIsFrozenAndCannotBeRemovedFromDisplay(t *testing.T) {
	s, _ := enrollWorld(t)
	p := &PreparedParallelRestore{plan: &RevivePlan{}, reuseRequired: true, Expires: time.Now().Add(time.Minute), View: revive.EnvironmentPlan{Reuse: &revive.EnvironmentReuse{}}}
	p.View.Reuse = nil // A caller cannot remove the private review requirement.
	if _, err := s.RunParallelPreparation(context.Background(), p, ParallelPrepareChoice{}, nil); journeyCode(err) != "confirmation_required" || p.consumed {
		t.Fatal("unreviewed old slot reuse submitted", err)
	}
}
