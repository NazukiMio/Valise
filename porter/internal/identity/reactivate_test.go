package identity

import (
	"testing"

	"valise/core/enroll"
	model "valise/core/identity"
	"valise/core/valise"
)

func TestReactivateSwitchBackAndCoexist(t *testing.T) {
	home := t.TempDir()
	prior, err := Accept(home, AcceptInput{
		InstallID:     "demo",
		InstanceID:    "demo",
		Roots:         map[string]string{"state": "/tmp/demo"},
		ProfileDigest: "digest-1",
	}, func() error { return nil })
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	targetRef := model.AgentRef{
		ServerUID:  "server-b-uid",
		InstallID:  "demo",
		InstanceID: "demo",
		Generation: "gen-b-1",
	}

	// Switch back: issues new generation and commits reverse migration_completed relation.
	res, err := Reactivate(home, enroll.ReactivateParams{
		InstanceID:        "demo",
		Mode:              enroll.ReactivateModeSwitchBack,
		SwitchFrom:        &targetRef,
		SourceDisposition: model.StoppedVerified,
	})
	if err != nil {
		t.Fatalf("reactivate switch_back: %v", err)
	}
	if res.Agent.Ref.Generation == prior.Ref.Generation || res.Agent.Ref.ServerUID != prior.Ref.ServerUID {
		t.Fatalf("unexpected generation: prior=%+v new=%+v", prior.Ref, res.Agent.Ref)
	}
	if res.Agent.State != "active" || res.Relation == nil {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Relation.Kind != model.MigrationCompleted || res.Relation.SourceDisposition != model.StoppedVerified {
		t.Fatalf("unexpected relation: %+v", res.Relation)
	}
	if res.Relation.SourceAgent == nil || *res.Relation.SourceAgent != targetRef || res.Relation.TargetAgent != res.Agent.Ref {
		t.Fatalf("relation refs mismatch: %+v", res.Relation)
	}

	// Read must validate relation & operation record strictly without reconciliation error.
	binding, err := Read(home, "demo")
	if err != nil {
		t.Fatalf("read after switch_back: %v", err)
	}
	if binding.IdentityState != model.Bound || binding.Agent == nil || binding.Agent.Ref != res.Agent.Ref {
		t.Fatalf("unexpected binding: %+v", binding)
	}
	if len(binding.Relations) != 1 || binding.Relations[0].Kind != model.MigrationCompleted || *binding.Relations[0].SourceAgent != targetRef {
		t.Fatalf("unexpected relations: %+v", binding.Relations)
	}

	// Coexist: issues another new generation with NO reverse migration relation.
	res2, err := Reactivate(home, enroll.ReactivateParams{
		InstanceID: "demo",
		Mode:       enroll.ReactivateModeCoexist,
	})
	if err != nil {
		t.Fatalf("reactivate coexist: %v", err)
	}
	if res2.Agent.Ref.Generation == res.Agent.Ref.Generation || res2.Relation != nil {
		t.Fatalf("unexpected coexist result: %+v", res2)
	}
	binding2, err := Read(home, "demo")
	if err != nil {
		t.Fatalf("read after coexist: %v", err)
	}
	if len(binding2.Relations) != 0 || binding2.Agent.Ref != res2.Agent.Ref {
		t.Fatalf("unexpected coexist binding: %+v", binding2)
	}

	// Invalid mode / missing SwitchFrom rejected.
	if _, err := Reactivate(home, enroll.ReactivateParams{InstanceID: "demo", Mode: enroll.ReactivateModeSwitchBack}); err == nil {
		t.Fatal("expected error for missing switch_from")
	} else if ue, ok := valise.As(err); !ok || ue.Code != "switch_from_required" {
		t.Fatalf("unexpected error: %v", err)
	}
}
