package environment

import "valise/core/identity"

// This capability prepares a first native environment. It does not advertise
// activation, conversation acceptance, inactive-slot reuse or arbitrary sources.
const PreparationCapability = "environment-restore-prepare/1"

const (
	MethodPreparationBegin         = "environment_restore_prepare_begin"
	MethodPreparationFinish        = "environment_restore_prepare_finish"
	MethodPreparationStatus        = "environment_restore_prepare_status"
	MethodPreparationCancel        = "environment_restore_prepare_cancel"
	MethodPreparationCleanupReview = "environment_restore_prepare_cleanup_review"
	MethodPreparationCleanup       = "environment_restore_prepare_cleanup"
)

type PreparationParams struct {
	Agent       identity.AgentRef `json:"agent"`
	OperationID string            `json:"operation_id"`
}

// PreparationReceipt exposes only the fixed upload target, never the private
// intent, profile, programme material or decrypted file manifest.
type PreparationReceipt struct {
	Agent          identity.AgentRef    `json:"agent"`
	OperationID    string               `json:"operation_id"`
	Snapshot       identity.SnapshotRef `json:"snapshot"`
	State          string               `json:"state"`
	DataOperation  string               `json:"data_operation"`
	BindingDigest  string               `json:"binding_digest"`
	ManifestDigest string               `json:"manifest_digest"`
}

// PreparationStatus contains observations and conditions, not permissions.
type PreparationStatus struct {
	Agent            identity.AgentRef    `json:"agent"`
	OperationID      string               `json:"operation_id"`
	Snapshot         identity.SnapshotRef `json:"snapshot"`
	State            string               `json:"state"`
	Mode             Mode                 `json:"mode"`
	Phase            Phase                `json:"phase,omitempty"`
	FailureCode      string               `json:"failure_code,omitempty"`
	IssueCode        string               `json:"issue_code,omitempty"`
	Materials        string               `json:"materials"`
	Probe            string               `json:"probe"`
	Check            *ProbeResult         `json:"check,omitempty"`
	OriginalObserved bool                 `json:"original_observed"`
	OriginalRunning  bool                 `json:"original_running"`
	OriginalStopped  bool                 `json:"original_stopped"`
	CanCancel        bool                 `json:"can_cancel"`
	CanReviewCleanup bool                 `json:"can_review_cleanup"`
	Retired          bool                 `json:"retired"`
}

type PreparationCleanupItem struct {
	Kind    string `json:"kind"`
	Root    string `json:"root,omitempty"`
	Present bool   `json:"present"`
}

type PreparationCleanupReview struct {
	PreparationParams
	Snapshot identity.SnapshotRef     `json:"snapshot"`
	State    string                   `json:"state"`
	Items    []PreparationCleanupItem `json:"items"`
	Digest   string                   `json:"digest"`
}

type PreparationCleanupParams struct {
	PreparationParams
	Digest string `json:"digest"`
}
