package service

import (
	"context"
	"slices"
	"testing"

	"valise/core/valise"
	"valise/core/vault"
)

func TestNormalizeManualTier(t *testing.T) {
	cases := map[string]string{
		"soul-only":    vault.TierSoul,
		"soul":         vault.TierSoul,
		"soul-secrets": vault.TierSoulSecrets,
		"full":         vault.TierFull,
		"raw":          vault.TierRaw,
	}
	for in, want := range cases {
		got, err := normalizeManualTier(in)
		if err != nil || got != want {
			t.Errorf("normalizeManualTier(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
	if _, err := normalizeManualTier("invalid"); err == nil {
		t.Fatal("invalid tier accepted")
	}
}

func TestNormalizeBundleMembers(t *testing.T) {
	if got := normalizeBundleMembers("main", nil); len(got) != 0 {
		t.Fatalf("empty raw should stay nil: %v", got)
	}
	got := normalizeBundleMembers("main", []string{"sub-b", "sub-a", "sub-a"})
	want := []string{"main", "sub-a", "sub-b"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCreateManualSavepointValidation(t *testing.T) {
	s := &Service{}
	for name, req := range map[string]ManualSavepointRequest{
		"missing host":     {InstanceID: "a", Tier: "full"},
		"invalid instance": {Host: "h", InstanceID: "BAD!", Tier: "full"},
		"invalid tier":     {Host: "h", InstanceID: "a", Tier: "nope"},
		"bad note":         {Host: "h", InstanceID: "a", Tier: "soul-only", Note: "line1\nline2"},
		"bad bundle id":    {Host: "h", InstanceID: "a", Tier: "full", BundleMembers: []string{"BAD!"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.CreateManualSavepoint(context.Background(), req)
			if err == nil {
				t.Fatalf("expected error for %s", name)
			}
			if _, ok := valise.As(err); !ok {
				t.Fatalf("expected UserError, got %T: %v", err, err)
			}
		})
	}
}

func TestCreateManualSavepointAutoRegistersRecipient(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	// Do NOT call s.KeyRegister first; server has 0 recipients.
	st0, err := s.VaultStatus(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	if len(st0.Recipients) != 0 {
		t.Fatalf("expected 0 initial recipients, got %v", st0.Recipients)
	}

	res, err := s.CreateManualSavepoint(ctx, ManualSavepointRequest{
		Host:       "box",
		InstanceID: "demo",
		Tier:       "soul-only",
		Note:       "first-manual",
	})
	if err != nil {
		t.Fatalf("CreateManualSavepoint on fresh server failed: %v", err)
	}
	if len(res.Reports) != 1 || res.Reports[0].SnapshotID == "" {
		t.Fatalf("unexpected reports: %+v", res)
	}
	st1, err := s.VaultStatus(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	if len(st1.Recipients) != 1 {
		t.Fatalf("expected 1 registered recipient, got %v", st1.Recipients)
	}

	// Second call must succeed idempotently without duplicate registration error.
	res2, err := s.CreateManualSavepoint(ctx, ManualSavepointRequest{
		Host:       "box",
		InstanceID: "demo",
		Tier:       "soul-only",
		Note:       "second-manual",
	})
	if err != nil || len(res2.Reports) != 1 {
		t.Fatalf("second CreateManualSavepoint failed: %+v %v", res2, err)
	}
}
