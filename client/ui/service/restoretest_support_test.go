package service

import (
	"testing"
	"valise/core/enroll"
	"valise/core/profile"
)

func TestFullTestRequiresMatchingDefaultProfileAndImplementedSource(t *testing.T) {
	p := profile.Profile{ID: "fixture", ProfileVersion: 23, Environments: &profile.Environments{Engine: "linux-bwrap"}, Provenance: &profile.Provenance{Kind: profile.ProvenanceNPMGlobal}}
	in := enroll.InstanceSummary{ProfileID: "fixture", ProfileVersion: 23}
	if !declaredEnvironmentTest(p, in) {
		t.Fatal("supported default source hidden")
	}
	for _, change := range []string{"old_profile", "named", "no_environment", "other_source"} {
		t.Run(change, func(t *testing.T) {
			q, instance := p, in
			switch change {
			case "old_profile":
				instance.ProfileVersion = 22
			case "named":
				instance.Name = "test"
			case "no_environment":
				q.Environments = nil
			case "other_source":
				q.Provenance = &profile.Provenance{Kind: profile.ProvenanceGit}
			}
			if declaredEnvironmentTest(q, instance) {
				t.Fatal("unsupported profile advertised complete test")
			}
		})
	}
}
