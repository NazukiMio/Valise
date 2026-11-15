package revive

import (
	"context"
	"os"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/activeview"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

// ReviewCurrentOldCleanup discovers an existing accepted operation for the
// settings page. It neither creates a registry nor changes saved policy.
func (a *ActivationRunner) ReviewCurrentOldCleanup(ctx context.Context, agent identity.AgentRef) (*environment.OldCleanupReview, error) {
	if ctx == nil {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	r, err := (environments.Store{Home: a.home()}).Load(agent)
	if err != nil {
		return nil, err
	}
	if r.Mode != environment.Direct || r.Operation == nil {
		return nil, oldCleanupError("environment_cleanup_unavailable")
	}
	return a.ReviewOldCleanup(ctx, agent, r.Operation.ID)
}

type oldCleanupResource struct {
	Item activationrecord.CleanupItem
	View environment.OldCleanupItem
	Tree string
}
type oldCleanupScope struct {
	View      environment.OldCleanupReview
	Record    *ActivationRecord
	Registry  environment.Registry
	Child     *revive.Journal
	Current   *activeview.Binding
	Resources []oldCleanupResource
	Leases    []*os.File
}

func (a *ActivationRunner) ReviewOldCleanup(ctx context.Context, agent identity.AgentRef, op string) (*environment.OldCleanupReview, error) {
	if ctx == nil {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	if _, err := loadActivation(a.home(), agent, op); err != nil {
		return nil, err
	}
	unlock, err := (seal.Paths{Home: a.home()}).ObservationLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := a.reviewOldCleanupLocked(ctx, agent, op)
	if err != nil {
		return nil, err
	}
	defer closePreparationLeases(s.Leases)
	return &s.View, nil
}

func (a *ActivationRunner) reviewOldCleanupLocked(ctx context.Context, agent identity.AgentRef, op string) (*oldCleanupScope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rec, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	r, err := a.registry(rec)
	if err != nil {
		return nil, err
	}
	o := r.Operation
	if o.Mode != environment.Compact && r.Mode != environment.Direct || !o.Confirmed || (o.Phase != environment.Accepted && o.Phase != environment.Cleaning) {
		return nil, oldCleanupError("environment_cleanup_unavailable")
	}
	b, err := (activeview.Reader{Home: a.home(), Control: a.runtime}).Read(ctx, agent)
	if err != nil || b == nil || b.Ref != o.Candidate || !b.ConversationConfirmed || (!b.Current.Running && !b.Current.Stopped) || b.Current.PendingJob || b.Definition.Digest() != rec.CandidateDefinition || environments.Digest(b.Registry) != environments.Digest(r) {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	j, err := a.runner.Store.Load(rec.Plan.DataOpID)
	if err != nil {
		return nil, err
	}
	want, err := a.oldCleanupClaims(rec, r, j)
	if err != nil {
		return nil, err
	}
	if c := rec.Cleanup; c != nil {
		if len(c.Items) != len(want) {
			return nil, oldCleanupError("environment_cleanup_unknown")
		}
		for i := range want {
			item := c.Items[i]
			if item.Kind != want[i].Kind || item.Root != want[i].Root || item.Identity != want[i].Identity {
				return nil, oldCleanupError("environment_cleanup_unknown")
			}
			want[i] = item
		}
	} else if o.OriginalCleaned {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	leases, err := a.oldCleanupLeases(rec)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			closePreparationLeases(leases)
		}
	}()
	s := &oldCleanupScope{Record: rec, Registry: r, Child: j, Current: b,
		View: environment.OldCleanupReview{PreparationParams: environment.PreparationParams{Agent: agent, OperationID: op}, Snapshot: rec.Plan.Snapshot, Mode: o.Mode, PolicyMode: r.Mode, Target: o.Original, Current: b.Ref, State: "ready"}}
	s.Leases = leases
	if o.Phase == environment.Cleaning {
		s.View.State = "pending"
	}
	if o.OriginalCleaned {
		if rec.Cleanup == nil || !rec.Cleanup.Complete {
			return nil, oldCleanupError("environment_cleanup_unknown")
		}
		s.View.State = "cleaned"
	}
	for _, item := range want {
		path, err := oldCleanupPath(a.home(), rec, j, item)
		if err != nil {
			return nil, err
		}
		if err := a.protectOldCleanup(ctx, rec, path, item.Kind); err != nil {
			return nil, err
		}
		present, err := inspectOldCleanupIdentity(path, item)
		if err != nil {
			return nil, err
		}
		resource := oldCleanupResource{Item: item, View: environment.OldCleanupItem{Kind: item.Kind, Root: item.Root, Present: present}}
		if present {
			resource.Tree, resource.View.Files, resource.View.Bytes, err = oldCleanupTree(ctx, path, item.Kind != "preparation")
			if err != nil {
				return nil, err
			}
			if still, err := inspectOldCleanupIdentity(path, item); err != nil || !still {
				return nil, oldCleanupError("environment_cleanup_unknown")
			}
		}
		s.Resources = append(s.Resources, resource)
		s.View.Items = append(s.View.Items, resource.View)
	}
	latest, err := loadActivation(a.home(), agent, op)
	last, regErr := a.registry(rec)
	if err != nil || regErr != nil || environments.Digest(latest) != environments.Digest(rec) || environments.Digest(last) != environments.Digest(r) {
		return nil, oldCleanupError("environment_cleanup_changed")
	}
	// Current data and gateway are not cleanup targets. Normal candidate
	// writes and HTTP variation cannot invalidate a frozen deletion scope.
	s.View.Digest = environments.Digest(struct {
		Record     *ActivationRecord
		Registry   environment.Registry
		Child      *revive.Journal
		Definition string
		Current    managedruntime.Observation
		Resources  []oldCleanupResource
	}{rec, r, j, b.Definition.Digest(), b.Current, s.Resources})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	success = true
	return s, nil
}
