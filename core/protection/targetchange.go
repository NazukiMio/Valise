package protection

import (
	"encoding/json"
	"fmt"
	"time"

	"valise/core/identity"
	"valise/core/vaultrpc"
)

// Versioned change of a copy target that accepted plans reference
// (protection-plan/2). Unreferenced targets keep the legacy target_set.
const (
	MethodTargetChangePrepare = "target_change_prepare"
	MethodTargetChangeApply   = "target_change_apply"
	TargetChangeSchema        = "valise-target-change/1"
)

// TargetChangePrepareParams carries the complete new definition of an existing
// target. The name is its identity: renaming and removal are not supported.
type TargetChangePrepareParams struct {
	Target vaultrpc.Target `json:"target"`
}

func (p *TargetChangePrepareParams) UnmarshalJSON(b []byte) error {
	type plain TargetChangePrepareParams
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	if raw, ok := fields["target"]; !ok || string(raw) == "null" {
		return fmt.Errorf("target is required")
	}
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = TargetChangePrepareParams(v)
	return nil
}

// TargetPlanRef names one accepted plan that copies to the target, at the
// exact revision the change was prepared against.
type TargetPlanRef struct {
	AgentRef identity.AgentRef `json:"agent_ref"`
	Revision Revision          `json:"revision"`
}

// FrozenTargetChange is an inert DTO; it never carries confirmation or authority.
type FrozenTargetChange struct {
	PlanID    string    `json:"plan_id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// Before is the target's current definition; Request.Target replaces it.
	Before              vaultrpc.Target           `json:"before"`
	Request             TargetChangePrepareParams `json:"request"`
	Plans               []TargetPlanRef           `json:"plans"`
	PreconditionsDigest string                    `json:"preconditions_digest"`
	PlanDigest          string                    `json:"plan_digest"`
}

func (p FrozenTargetChange) Digest() (string, error) { p.PlanDigest = ""; return Digest(p) }

type TargetChangeApplyParams struct {
	Plan     FrozenTargetChange `json:"plan"`
	ChangeID string             `json:"change_id"`
}

// TargetChange is the durable outcome of one apply. Plans lists the
// referencing plans at their old revisions; each is published at revision+1.
type TargetChange struct {
	Schema            string          `json:"schema"`
	ChangeID          string          `json:"change_id"`
	TargetName        string          `json:"target_name"`
	RequestDigest     string          `json:"request_digest"`
	State             string          `json:"state"`
	Plans             []TargetPlanRef `json:"plans"`
	Committed         bool            `json:"committed"`
	ReconcileRequired bool            `json:"reconcile_required"`
	RollbackSucceeded bool            `json:"rollback_succeeded"`
	ErrorCode         string          `json:"error_code,omitempty"`
	UpdatedAt         time.Time       `json:"updated_at"`
}
