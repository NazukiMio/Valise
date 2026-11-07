// Package protection defines the versioned preset protection contract.
package protection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"valise/core/identity"
)

const Schema = "valise-protection/1"
const Capability = "protection-plan/1"

// CapabilityV2 adds schedule.catch_up, missed_runs and versioned changes of
// plan-referenced copy targets. A client sends catch_up or calls the target
// change methods only to a porter declaring it: older porters decode unknown
// JSON fields leniently and would silently drop catch_up.
const CapabilityV2 = "protection-plan/2"
const (
	MethodGet       = "protection_get"
	MethodPrepare   = "protection_prepare"
	MethodApply     = "protection_apply"
	MethodStatus    = "protection_status"
	MethodReconcile = "protection_reconcile"
)

// Revision is transmitted as an exact decimal string, including zero.
type Revision uint64

func (r Revision) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(uint64(r), 10))
}
func (r *Revision) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("revision must be a decimal string")
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != s {
		return fmt.Errorf("invalid revision")
	}
	*r = Revision(n)
	return nil
}

type Scope struct {
	Kind string `json:"kind"`
	Tier string `json:"tier"`
}
type Schedule struct {
	Enabled  *bool  `json:"enabled"`
	DailyAt  string `json:"daily_at"`
	Timezone string `json:"timezone"`
	// CatchUp (protection-plan/2) runs one missed backup after boot or
	// resume (systemd Persistent=true). Absent means off.
	CatchUp *bool `json:"catch_up,omitempty"`
}

// CatchUpEnabled treats an absent catch_up as off.
func (s Schedule) CatchUpEnabled() bool { return s.CatchUp != nil && *s.CatchUp }

type Period struct {
	Unit     string `json:"unit"`
	Timezone string `json:"timezone"`
}
type LocationRef struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	AuthorityID string `json:"authority_id"`
}
type ServerCopy struct {
	TargetRef       LocationRef `json:"target_ref"`
	Enabled         *bool       `json:"enabled"`
	MinimumInterval string      `json:"minimum_interval"`
}
type Desired struct {
	Scope        Scope        `json:"scope"`
	Schedule     Schedule     `json:"schedule"`
	Period       Period       `json:"period"`
	ServerCopies []ServerCopy `json:"server_copies"`
}
type ServerPlan struct {
	SchemaVersion string            `json:"schema_version"`
	AgentRef      identity.AgentRef `json:"agent_ref"`
	Revision      Revision          `json:"revision"`
	Desired
	AcceptedAt time.Time `json:"accepted_at"`
}
type GetParams struct {
	AgentRef identity.AgentRef `json:"agent_ref"`
}
type PrepareParams struct {
	AgentRef         identity.AgentRef `json:"agent_ref"`
	ExpectedRevision Revision          `json:"expected_revision"`
	Desired          Desired           `json:"desired"`
}

func (p *PrepareParams) UnmarshalJSON(b []byte) error {
	type plain PrepareParams
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	if raw, ok := fields["expected_revision"]; !ok || string(raw) == "null" {
		return fmt.Errorf("expected_revision is required")
	}
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	// The desired settings are decoded strictly: a field this porter does not
	// know must be refused, never silently dropped from an accepted plan.
	if raw, ok := fields["desired"]; ok {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var d Desired
		if err := dec.Decode(&d); err != nil {
			return fmt.Errorf("desired: %w", err)
		}
		if dec.More() {
			return fmt.Errorf("desired: trailing data")
		}
	}
	*p = PrepareParams(v)
	return nil
}

// FrozenChange is an inert DTO. It never carries confirmation or authority.
type FrozenChange struct {
	Before              BeforeSettings `json:"before"`
	PlanID              string         `json:"plan_id"`
	Kind                string         `json:"kind"`
	CreatedAt           time.Time      `json:"created_at"`
	ExpiresAt           time.Time      `json:"expires_at"`
	Request             PrepareParams  `json:"request"`
	PreconditionsDigest string         `json:"preconditions_digest"`
	CalendarPreview     string         `json:"calendar_preview,omitempty"`
	PlanDigest          string         `json:"plan_digest"`
}
type ApplyParams struct {
	Plan     FrozenChange `json:"plan"`
	ChangeID string       `json:"change_id"`
}
type StatusParams struct {
	AgentRef identity.AgentRef `json:"agent_ref"`
	ChangeID string            `json:"change_id"`
}

// ReconcileParams selects one fixed recovery branch, requiring a fresh owner interaction.
type ReconcileParams struct {
	AgentRef identity.AgentRef `json:"agent_ref"`
	ChangeID string            `json:"change_id"`
	Action   string            `json:"action"`
}
type TimerObservation struct {
	Status          string `json:"status"`
	ActiveState     string `json:"active_state,omitempty"`
	UnitFileState   string `json:"unit_file_state,omitempty"`
	LastTrigger     string `json:"last_trigger,omitempty"`
	NextTrigger     string `json:"next_trigger,omitempty"`
	Linger          bool   `json:"linger"`
	CalendarPreview string `json:"calendar_preview,omitempty"`
}
type LegacyCopy struct {
	TargetRef LocationRef `json:"target_ref"`
	Enabled   bool        `json:"enabled"`
	Every     string      `json:"every,omitempty"`
}
type LegacySettings struct {
	Scope           Scope        `json:"scope"`
	Period          Period       `json:"period"`
	TimerConfigured bool         `json:"timer_configured"`
	Every           string       `json:"every,omitempty"`
	Calendar        string       `json:"calendar,omitempty"`
	OnActive        string       `json:"on_active,omitempty"`
	Copies          []LegacyCopy `json:"copies"`
}
type BeforeSettings struct {
	Mode           string          `json:"mode"`
	ServerPlan     *ServerPlan     `json:"server_plan,omitempty"`
	LegacySettings *LegacySettings `json:"legacy_settings,omitempty"`
}

type View struct {
	LegacySettings *LegacySettings  `json:"legacy_settings,omitempty"`
	Mode           string           `json:"mode"`
	ServerPlan     *ServerPlan      `json:"server_plan,omitempty"`
	Timer          TimerObservation `json:"timer"`
	Warnings       []string         `json:"warnings"`
	// MissedRuns (protection-plan/2) is computed read-only from the accepted
	// schedule and timer run records, by the server clock in ServerNow.
	MissedRuns []MissedRun `json:"missed_runs"`
	ServerNow  time.Time   `json:"server_now"`
	// PendingChanges lists this instance's plan changes that a crash or a
	// failed verification left for the person to settle with Reconcile.
	PendingChanges []PendingChange `json:"pending_changes,omitempty"`
}

// PendingChange is one unsettled plan change. Committed means the new
// revision is already published: only verify can complete it.
type PendingChange struct {
	ChangeID    string   `json:"change_id"`
	State       string   `json:"state"`
	Committed   bool     `json:"committed"`
	OldRevision Revision `json:"old_revision"`
	NewRevision Revision `json:"new_revision"`
	ErrorCode   string   `json:"error_code,omitempty"`
}
type Change struct {
	Schema            string            `json:"schema"`
	ChangeID          string            `json:"change_id"`
	AgentRef          identity.AgentRef `json:"agent_ref"`
	RequestDigest     string            `json:"request_digest"`
	State             string            `json:"state"`
	OldRevision       Revision          `json:"old_revision"`
	NewRevision       Revision          `json:"new_revision"`
	Committed         bool              `json:"committed"`
	ReconcileRequired bool              `json:"reconcile_required"`
	RollbackSucceeded bool              `json:"rollback_succeeded"`
	// ErrorCode says why a recorded change failed or awaits reconciliation;
	// the change itself is the result, not an RPC error.
	ErrorCode string    `json:"error_code,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Digest hashes a deterministic JSON projection, never an authorization proof.
func Digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), nil
}
func (p FrozenChange) Digest() (string, error) { p.PlanDigest = ""; return Digest(p) }

// NewDefault supplies only the agreed scope default. The caller must choose daily time,
// both timezones and the period explicitly before preparing a change.
func NewDefault() Desired {
	enabled := false
	return Desired{Scope: Scope{Kind: "preset", Tier: "full"}, Schedule: Schedule{Enabled: &enabled}, ServerCopies: []ServerCopy{}}
}
