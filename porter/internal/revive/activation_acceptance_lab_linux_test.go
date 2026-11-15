//go:build linux

package revive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/environment"
	"valise/porter/internal/activeview"
	"valise/porter/internal/environments"
)

// Called only by the explicitly enabled Linux lab using a synthetic Agent.
// This tests the human adapter's fixed action, never an actual conversation.
func assertExplicitHumanAcceptanceRetainsOriginal(t *testing.T, ctx context.Context, a *ActivationRunner, rec *ActivationRecord, live string) {
	t.Helper()
	agent, op := rec.Plan.Agent, rec.Plan.OperationID
	before := privateReadFingerprint(t, a.home())
	review, err := a.ReviewSwitch(ctx, agent, op)
	if err != nil || !review.CanConfirm || review.Status.ConversationKnown || review.Status.Phase != environment.AwaitingHuman {
		t.Fatal("candidate not pending an explicit human decision", review, err)
	}
	if before != privateReadFingerprint(t, a.home()) {
		t.Fatal("acceptance review wrote state")
	}
	write(t, filepath.Join(live, "dialogue-writes"), "synthetic interaction writes", 0600)
	again, err := a.ReviewSwitch(ctx, agent, op)
	if err != nil || again.ConfirmationDigest != review.ConfirmationDigest {
		t.Fatal("active interaction writes invalidated acceptance review", again, err)
	}
	reader := activeview.Reader{Home: a.home(), Control: a.runtime}
	running, err := reader.Read(ctx, agent)
	if err != nil || running == nil || !running.Current.Running {
		t.Fatal("missing current synthetic candidate", err)
	}
	if _, err := a.ConfirmReviewed(ctx, agent, op, strings.Repeat("f", 64)); err == nil {
		t.Fatal("old/wrong review confirmed another runtime")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.ConfirmReviewed(cancelled, agent, op, review.ConfirmationDigest); err == nil {
		t.Fatal("cancelled action confirmed runtime")
	}
	// A broken independent fallback blocks return, while the actual candidate
	// can still receive the human's explicit acceptance.
	programs := environments.ProgramStore{Home: a.home()}
	original, _, err := programs.Open(ctx, agent, rec.Plan.Original, rec.Plan.OriginalMaterials)
	if err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(original.ProgramDir(), ".demo/tools/npm/lib/node_modules/fixture/extra.js")
	write(t, extra, "changed fallback only", 0600)
	t.Cleanup(func() { _ = os.Remove(extra) })
	review, err = a.ReviewSwitch(ctx, agent, op)
	if err != nil || !review.CanConfirm || review.CanReturn || review.OriginalProgramChecked || review.ConfirmationDigest != again.ConfirmationDigest {
		t.Fatal("fallback programme substituted for candidate acceptance", review, err)
	}
	confirmationDigest := review.ConfirmationDigest
	write(t, filepath.Join(live, "gateway-unavailable"), "synthetic HTTP status change", 0600)
	review, err = a.ReviewSwitch(ctx, agent, op)
	if err != nil || !review.CanConfirm || review.Status.GatewayPassed || review.ConfirmationDigest != confirmationDigest {
		t.Fatal("HTTP observation became a hidden human-confirmation condition", review, err)
	}
	accepted, err := a.ConfirmReviewed(ctx, agent, op, confirmationDigest)
	if err != nil || accepted.Phase != environment.Accepted {
		t.Fatal("explicit human action was not committed", accepted, err)
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(live, "gateway-unavailable")); err != nil {
		t.Fatal(err)
	}
	current, err := reader.Read(ctx, agent)
	if err != nil || current == nil || !current.ConversationConfirmed || current.Ref != rec.Plan.Candidate || current.Operation.OriginalCleaned || current.Current.MainPID != running.Current.MainPID || current.Current.KernelStart != running.Current.KernelStart {
		t.Fatal("confirmation changed runtime or removed original", current, err)
	}
	child, err := a.runner.Store.Load(rec.Plan.DataOpID)
	if err != nil || len(child.Roots) != 1 || read(t, filepath.Join(child.Roots[0].Prev, "SOUL.md")) != "current soul" || read(t, filepath.Join(live, "dialogue-writes")) != "synthetic interaction writes" {
		t.Fatal("confirmation changed either data environment", child, err)
	}
	saved, err := loadActivation(a.home(), agent, op)
	if err != nil {
		t.Fatal(err)
	}
	fact := saved.Facts["human_acceptance"]
	if fact.IntentAt.IsZero() || fact.FinishedAt.IsZero() || fact.Command != nil || fact.Gateway != nil || fact.DataState != "" {
		t.Fatal("acceptance did not retain a minimal human action", fact)
	}
	before = privateReadFingerprint(t, a.home())
	if repeated, err := a.ConfirmReviewed(ctx, agent, op, confirmationDigest); err != nil || repeated.Phase != environment.Accepted {
		t.Fatal("acknowledged acceptance could not be queried", repeated, err)
	}
	if before != privateReadFingerprint(t, a.home()) {
		t.Fatal("repeat acceptance rewrote committed confirmation")
	}
}
