package revive

import (
	"context"

	"valise/core/environment"
	"valise/core/profile"
	"valise/core/revive"
	"valise/porter/internal/runtimeguard"
)

func legacyPlanScope(p revive.Plan) runtimeguard.Scope {
	s := runtimeguard.Scope{}
	if !p.TestOnly {
		s.Instances = []string{p.InstanceID}
		for _, r := range p.Roots {
			s.Paths = append(s.Paths, r.Path)
		}
		if p.Unit != "" {
			s.Units = append(s.Units, profile.ServiceUnit{Scope: "user", Unit: p.Unit})
		}
	}
	// A test may read an occupied Agent, but cannot write into private material
	// by pretending its root is an arbitrary programme or preparation child.
	for _, r := range p.Roots {
		if r.SwapRoot == "" {
			s.Paths = append(s.Paths, sibling(r.Path, "stage", p.OpID, r.ID))
		}
	}
	return s
}
func guardLegacyPlan(ctx context.Context, s Store, p revive.Plan) error {
	return runtimeguard.Check(ctx, s.Home, legacyPlanScope(p))
}

// LegacyAvailable is a read-only projection for the old operation review.
// It uses the same actual write scope as execution, including file-only tests.
func LegacyAvailable(ctx context.Context, s Store, j *revive.Journal) bool {
	if j.ManagedData != nil {
		return false
	}
	scope := legacyPlanScope(j.Plan)
	scope.Paths = append(scope.Paths, leftovers(j)...)
	return runtimeguard.Check(ctx, s.Home, scope) == nil
}
func guardStage(ctx context.Context, s Store, j *revive.Journal) error {
	if j.ManagedData != nil {
		return checkManagedData(s, j, environment.Building, environment.Prepared)
	}
	return guardLegacyPlan(ctx, s, j.Plan)
}
