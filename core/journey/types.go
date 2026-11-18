// Package journey reads metadata without requiring an original server or keys.
// It makes no claim about dependency completeness, decryption, or restorability.
package journey

import (
	"time"
	"valise/core/vault"
)

type SnapshotRef struct {
	VaultID    string `json:"vault_id"`
	SnapshotID string `json:"snapshot_id"`
}

type LocationRef struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	AuthorityID string `json:"authority_id"`
}

// Source must come from a registered repository or an explicit local selection.
// Root can be a vault leaf, a server tree, or a repository of server trees.
type Source struct {
	LocationRef LocationRef `json:"location_ref"`
	Root        string      `json:"-"`
}

type Observation struct {
	LocationRef   LocationRef `json:"location_ref"`
	Status        string      `json:"status"`
	ObservedAt    time.Time   `json:"observed_at"`
	LastAttemptAt time.Time   `json:"last_attempt_at"`
	Source        string      `json:"source"`
	Error         string      `json:"error,omitempty"`
}

type Scope struct {
	Kind string `json:"kind"`
	Tier string `json:"tier"`
}

type Copy struct {
	LocationRef    LocationRef `json:"location_ref"`
	SourceLocator  string      `json:"source_locator"`
	MetadataState  string      `json:"metadata_state"`
	HeaderDigest   string      `json:"header_digest"`
	MetadataDigest string      `json:"metadata_digest"`
	// Metadata preserves each variant when the same stable reference conflicts.
	Metadata vault.Meta `json:"metadata"`
	// MaterialStatus is always unknown until a separate material verifier runs.
	MaterialStatus string      `json:"material_status"`
	Observation    Observation `json:"observation"`
}

type TimelinePoint struct {
	SnapshotRef        SnapshotRef `json:"snapshot_ref"`
	CreatedAt          time.Time   `json:"created_at"`
	RecordedInstanceID string      `json:"recorded_instance_id"`
	Scope              Scope       `json:"scope"`
	Kind               string      `json:"kind,omitempty"`
	Note               string      `json:"note,omitempty"`
	MetadataState      string      `json:"metadata_state"`
	PlainBytes         int64       `json:"plain_bytes"`
	FileCount          int         `json:"file_count"`
	Copies             []Copy      `json:"copies"`
	Warnings           []string    `json:"warnings,omitempty"`
}

// Gap reports a missing/invalid published pair without inventing a saved point.
type Gap struct {
	LocationRef   LocationRef `json:"location_ref"`
	SourceLocator string      `json:"source_locator"`
	Code          string      `json:"code"`
}

type Result struct {
	Points       []TimelinePoint `json:"points"`
	Observations []Observation   `json:"observations"`
	Gaps         []Gap           `json:"gaps"`
	Partial      bool            `json:"partial"`
}
