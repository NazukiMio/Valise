package environment

import (
	"testing"
	"time"

	"valise/core/vault"
)

func source(tier string, second int64) Source {
	return Source{Agent: agent, Snapshot: snapshot(int(second)), Time: time.Unix(second, 0), Tier: tier,
		ProfileDigest: "profile", ProgramDigest: "recorded-program", LayoutDigest: "layout", Verified: true}
}

func TestBaselineIdentityCompatibilityAndTime(t *testing.T) {
	target := source(vault.TierSoul, 200)
	old := source(vault.TierFull, 100)
	closest := source(vault.TierFull, 190)
	future := source(vault.TierFull, 201)
	cases := []struct {
		name string
		edit func(*Source)
	}{
		{"generation", func(s *Source) { s.Agent.Generation = "other" }},
		{"server", func(s *Source) { s.Agent.ServerUID = "other" }},
		{"installation", func(s *Source) { s.Agent.InstallID = "other" }},
		{"instance", func(s *Source) { s.Agent.InstanceID = "other" }},
		{"profile", func(s *Source) { s.ProfileDigest = "other" }},
		{"program", func(s *Source) { s.ProgramDigest = "other" }},
		{"layout", func(s *Source) { s.LayoutDigest = "other" }},
		{"unverified", func(s *Source) { s.Verified = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			incompatible := source(vault.TierFull, 199)
			tc.edit(&incompatible)
			best, err := SelectBaseline(target, []Source{old, future, incompatible, closest})
			if err != nil || best == nil || best.Snapshot != closest.Snapshot {
				t.Fatalf("unexpected baseline: %+v, %v", best, err)
			}
		})
	}
	best, err := SelectBaseline(target, []Source{future})
	if err != nil || best != nil {
		t.Fatal("missing baseline did not request recorded-source reconstruction")
	}
	target.Verified = false
	if _, err := SelectBaseline(target, []Source{old}); err == nil {
		t.Fatal("unproven target accepted")
	}
}
