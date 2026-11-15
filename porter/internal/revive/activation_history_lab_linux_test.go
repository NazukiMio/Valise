//go:build linux

package revive

import (
	"context"
	"testing"

	"valise/core/environment"
	"valise/porter/internal/environments"
)

func assertHistoricalRestoreRead(t *testing.T, ctx context.Context, a *ActivationRunner, w *world, first *ActivationRecord) {
	t.Helper()
	before := privateReadFingerprint(t, w.home)
	status, err := a.SwitchStatus(ctx, first.Plan.Agent, first.Plan.OperationID)
	if err != nil || !status.Historical || !status.HistoricalConfirmed || status.Phase != environment.Accepted || status.Current != nil || status.CurrentObserved || status.ConversationKnown || status.CanReviewOldCleanup || status.IssueCode != "" {
		t.Fatal("superseded restore confused with current environment", status, err)
	}
	review, err := a.ReviewSwitch(ctx, first.Plan.Agent, first.Plan.OperationID)
	if err != nil || !review.Status.Historical || review.CanSwitch || review.CanReturn || review.CanConfirm || review.Digest != "" || review.ConfirmationDigest != "" {
		t.Fatal("superseded restore offered execution", review, err)
	}
	if privateReadFingerprint(t, w.home) != before {
		t.Fatal("historical query mutated private state")
	}
	store := environments.Store{Home: w.home}
	registry, err := store.Load(first.Plan.Agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReturnReviewed(ctx, first.Plan.Agent, first.Plan.OperationID, first.BindingDigest); err == nil {
		t.Fatal("historical return accepted stale review")
	}
	// A rejected management request may update mutex-holder metadata. It
	// must not change the operation, active revision or historical acceptance.
	after, err := store.Load(first.Plan.Agent)
	if err != nil || environments.Digest(after) != environments.Digest(registry) {
		t.Fatal("rejected historical return changed environment registry", err)
	}
}
