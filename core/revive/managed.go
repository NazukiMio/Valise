package revive

import (
	"valise/core/environment"
	"valise/core/identity"
)

// ManagedDataRef binds a file-only directory journal to its parent environment
// transaction. Only the trusted executor writes it, in the initial journal;
// Plan deliberately has no corresponding request field. RESTORED_NOT_STARTED
// in this journal means data placed, never a successful environment activation.
type ManagedDataRef struct {
	Agent          identity.AgentRef    `json:"agent"`
	OperationID    string               `json:"operation_id"`
	Snapshot       identity.SnapshotRef `json:"snapshot"`
	Original       environment.Ref      `json:"original"`
	Candidate      environment.Ref      `json:"candidate"`
	PlanDigest     string               `json:"plan_digest"`
	ManifestDigest string               `json:"manifest_digest"`
	OriginalRoots  []ManagedRoot        `json:"original_roots"`
	CandidateRoots []ManagedRoot        `json:"candidate_roots"`
	// LogicalRoots is required for nested layouts and locates the preserved
	// original logical directories within their one physical exchange root.
	LogicalRoots []ManagedRoot `json:"logical_roots,omitempty"`
}

// ManagedRoot records directory identity without reading any file content.
type ManagedRoot struct {
	ID     string `json:"id"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}
