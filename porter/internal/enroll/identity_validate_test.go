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
