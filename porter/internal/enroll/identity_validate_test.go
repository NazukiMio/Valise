package enroll

import (
	"context"
	"os"
	"testing"

	"valise/core/enroll"
	model "valise/core/identity"
	"valise/core/profile"
	"valise/core/valise"
	identitystore "valise/porter/internal/identity"
)

func TestValidateIdentityAcceptedFactsAndDrift(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	r := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": "/home/test/state"}}
	_, a, err := AcceptRecord(home, r, p)
	if err != nil {
		t.Fatal(err)
	}
	ref := a.Ref
	if err := ValidateIdentity(ctx, home, ref); err != nil {
		t.Fatal(err)
	}
	other := ref
	other.Generation = "older"
	if err := ValidateIdentity(ctx, home, other); err == nil {
		t.Fatal("old generation accepted")
	}
	// A plain facts write does not accept a newly observed directory.
	r.Roots["state"] = "/home/test/changed"
	if _, err := WriteRecord(home, r, p); err != nil {
		t.Fatal(err)
	}
	expectConflict := func() {
		t.Helper()
		err := ValidateIdentity(ctx, home, ref)
		ue, ok := valise.As(err)
		if !ok || ue.Code != "identity_conflict" {
			t.Fatalf("expected conflict, got %v", err)
		}
	}
	expectConflict()
	_, a, err = AcceptRecord(home, r, p)
	if err != nil || a.Ref != ref {
		t.Fatalf("accepted roots changed generation: %+v %v", a, err)
	}
	if err := ValidateIdentity(ctx, home, ref); err != nil {
		t.Fatal(err)
	}
	// The profile copy also requires explicit acceptance after a rules change.
	p.ProfileVersion, r.ProfileVersion = 2, 2
	if _, err := WriteRecord(home, r, p); err != nil {
		t.Fatal(err)
	}
	expectConflict()
	_, a, err = AcceptRecord(home, r, p)
	if err != nil || a.Ref != ref {
		t.Fatalf("accepted profile changed generation: %+v %v", a, err)
	}
	if err := ValidateIdentity(ctx, home, ref); err != nil {
		t.Fatal(err)
	}
	if err := MarkSuperseded(home, r.InstanceID, enroll.Supersession{}); err != nil {
		t.Fatal(err)
	}
	expectConflict()
}

func TestValidateIdentityUnboundAndCancelledDoNotInitialize(t *testing.T) {
	home := t.TempDir()
	ref := model.AgentRef{ServerUID: "unknown", InstallID: "demo", InstanceID: "demo", Generation: "unknown"}
	err := ValidateIdentity(context.Background(), home, ref)
	ue, ok := valise.As(err)
	if !ok || ue.Code != "identity_unbound" {
		t.Fatalf("unbound: %v", err)
	}
	if _, err := os.Stat(identitystore.Dir(home)); !os.IsNotExist(err) {
		t.Fatal("validator initialized identity")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateIdentity(ctx, home, ref); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
}

func TestValidateIdentityNamedSubAgentBidirectionalAndTamper(t *testing.T) {
	ctx := context.Background()
	fullProf := profile.Profile{
		ID:             "demo",
		ProfileVersion: 1,
		Detect: &profile.Detect{
			Named: []profile.Named{{Glob: "${HOME}/.demo-*", Service: "shared"}},
		},
		Paths: []profile.PathRule{
			{Root: "state", Glob: "demo.json", Category: "config"},
		},
		Provenance: &profile.Provenance{Kind: "binary", Binary: &profile.BinaryProvenance{Name: "demo"}},
		Service:    &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "demo.service"}}},
		Entries:    []profile.Entry{{ID: "web", Kind: "web", Host: "127.0.0.1", Port: 8080}},
	}
	subProf := fullProf.ForAgent("worker")
	mainRec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": "/home/test/.demo"}}
	subRec := enroll.Record{InstanceID: "demo-worker", ProfileID: "demo", ProfileVersion: 1, Name: "worker", Roots: map[string]string{"state": "/home/test/.demo-worker"}}

	// Direction 1: main accepted first (P_full on disk), then sub-agent accepted with stripped P_sub.
	t.Run("main_then_sub", func(t *testing.T) {
		home := t.TempDir()
		_, mainAgent, err := AcceptRecord(home, mainRec, fullProf)
		if err != nil {
			t.Fatalf("accept main: %v", err)
		}
		eff := EffectiveDossierProfile(home, subRec, subProf)
		if len(eff.Entries) != 1 || eff.Provenance == nil {
			t.Fatalf("EffectiveDossierProfile did not preserve full profile: %+v", eff)
		}
		_, subAgent, err := AcceptRecord(home, subRec, subProf)
		if err != nil {
			t.Fatalf("accept sub: %v", err)
		}
		if err := ValidateIdentity(ctx, home, mainAgent.Ref); err != nil {
			t.Fatalf("ValidateIdentity(main): %v", err)
		}
		if err := ValidateIdentity(ctx, home, subAgent.Ref); err != nil {
			t.Fatalf("ValidateIdentity(sub): %v", err)
		}
	})

	// Direction 2: sub-agent accepted first (P_sub on disk), then main accepted later (upgrades disk to P_full).
	t.Run("sub_then_main_and_tamper_rejected", func(t *testing.T) {
		home := t.TempDir()
		_, subAgent, err := AcceptRecord(home, subRec, subProf)
		if err != nil {
			t.Fatalf("accept sub: %v", err)
		}
		if err := ValidateIdentity(ctx, home, subAgent.Ref); err != nil {
			t.Fatalf("ValidateIdentity(sub) before main: %v", err)
		}
		_, mainAgent, err := AcceptRecord(home, mainRec, fullProf)
		if err != nil {
			t.Fatalf("accept main: %v", err)
		}
		if err := ValidateIdentity(ctx, home, mainAgent.Ref); err != nil {
			t.Fatalf("ValidateIdentity(main): %v", err)
		}
		if err := ValidateIdentity(ctx, home, subAgent.Ref); err != nil {
			t.Fatalf("ValidateIdentity(sub) after main upgraded dossier profile: %v", err)
		}

		expectSubConflict := func(label string) {
			t.Helper()
			err := ValidateIdentity(ctx, home, subAgent.Ref)
			ue, ok := valise.As(err)
			if !ok || ue.Code != "identity_conflict" {
				t.Fatalf("%s: expected identity_conflict, got %v", label, err)
			}
		}

		// Tampering a rule under same ProfileVersion must still be rejected.
		tamperedPaths := fullProf
		tamperedPaths.Paths = append([]profile.PathRule(nil), fullProf.Paths...)
		tamperedPaths.Paths = append(tamperedPaths.Paths, profile.PathRule{Root: "state", Glob: "extra.json", Category: "config"})
		if err := writeDossierProfile(home, "demo", tamperedPaths); err != nil {
			t.Fatal(err)
		}
		expectSubConflict("tampered_paths")

		// Bumping ProfileVersion without re-accepting sub-agent must be rejected.
		v2 := fullProf
		v2.ProfileVersion = 2
		if err := writeDossierProfile(home, "demo", v2); err != nil {
			t.Fatal(err)
		}
		expectSubConflict("version_bump")

		// Tampering ProfileID must be rejected.
		wrongID := fullProf
		wrongID.ID = "evil"
		if err := writeDossierProfile(home, "demo", wrongID); err != nil {
			t.Fatal(err)
		}
		expectSubConflict("profile_id_tamper")
	})
}
