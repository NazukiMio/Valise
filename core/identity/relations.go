package identity

import "time"

const (
	MigrationCompleted = "migration_completed"
	CloneFrom          = "clone_from"
	RestoredFrom       = "restored_from"
	StoppedVerified    = "stopped_verified"
	ExclusiveOmitted   = "exclusive_omitted"
	Unresolved         = "unresolved"
)

// RecoveryContext is explicit provenance bound to one confirmed recovery mode.
// Missing SourceAgent means the source instance's identity is unknown; its
// snapshot may still be identified independently without inventing an AgentRef.
type RecoveryContext struct {
	Mode           string      `json:"mode"`
	SourceAgent    *AgentRef   `json:"source_agent,omitempty"`
	SourceSnapshot SnapshotRef `json:"source_snapshot"`
}

// Relation is an authoritative target-side record outside the public vault.
// Only migration_completed continues an existing journey; clone_from starts an
// independent journey, and restored_from retains the target generation.
type Relation struct {
	Schema            string      `json:"schema"`
	RelationID        string      `json:"relation_id"`
	Kind              string      `json:"kind"`
	SourceAgent       *AgentRef   `json:"source_agent,omitempty"`
	SourceSnapshot    SnapshotRef `json:"source_snapshot"`
	TargetAgent       AgentRef    `json:"target_agent"`
	OperationRef      string      `json:"operation_ref"`
	VerifiedAt        time.Time   `json:"verified_at"`
	SourceDisposition string      `json:"source_disposition"`
}
