package revive

import (
	"context"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
	"valise/porter/internal/backuprun"
	"valise/porter/internal/environments"
)

func (r *Runner) composeEnvironmentTest(ctx context.Context, j *revive.Journal, in *environments.BuildInput) (environment.DataPlan, error) {
	q := j.Plan.IsolatedTest
	if b := q.Baseline; b != nil {
		h, old := q.Header, b.Header
		if !environment.CompatibleTestBaseline(h, old) || !vault.ValidID(b.VaultID) || !vault.ValidID(old.SnapshotID) || len(b.HeaderDigest) != 64 || b.Files.Format != vault.FormatFiles {
			return environment.DataPlan{}, testFailure("environment_baseline_changed")
		}
		ref := identity.SnapshotRef{VaultID: b.VaultID, SnapshotID: old.SnapshotID}
		binding, err := (backuprun.Store{Home: r.Store.Home}).SnapshotBinding(ctx, protection.SnapshotBindingParams{SnapshotRef: ref})
		if err != nil || binding == nil || binding.State != "bound" || binding.AgentRef == nil || *binding.AgentRef != in.Target.Source.Agent {
			return environment.DataPlan{}, testFailure("environment_baseline_changed")
		}
		source := in.Target.Source
		source.Snapshot, source.Time, source.Tier = ref, old.CreatedAt, old.Tier
		in.Baseline = &environment.Material{Source: source, Files: b.Files}
	}
	roots := []string{profile.StateRoot}
	for _, root := range in.Profile.Roots {
		roots = append(roots, root.ID)
	}
	classifier := profile.NewClassifier(&in.Profile, in.Overrides)
	plan, err := environment.Compose(in.Target, in.Baseline, roots, func(root, rel string) string { return classifier.Classify(root, rel).Category })
	if err != nil {
		return plan, testFailure("environment_baseline_changed")
	}
	if plan.NeedsDataFoundation {
		return plan, testFailure("environment_foundation_unavailable")
	}
	return plan, nil
}
