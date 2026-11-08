// Package identity defines identities outside the public vault and dossier formats.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const Schema = "valise-identity/1"
const Capability = "identity/1"

// ListCapability (identity/2) means instance_list carries each instance's
// Binding, and bindings carry relation summaries; reading never initializes.
const ListCapability = "identity/2"

// LostSourceCapability: the porter accepts a migration whose source could
// not be reached, with exclusive secrets left out (exclusive_omitted on
// migrate, docs/ARCHITECTURE.md 8.2.3).
const LostSourceCapability = "identity-lost-source/1"
const MethodGet = "identity_get"

type GetParams struct {
	InstanceID string `json:"instance_id"`
}

const (
	LegacyUnbound       = "legacy_unbound"
	Bound               = "bound"
	NeedsReconciliation = "needs_reconciliation"
	// Unreadable is reported in listings when a record exists but could not
	// be read or validated; it is never treated as legacy or bound.
	Unreadable = "unreadable"
)

type ServerRef struct {
	ServerUID    string `json:"server_uid"`
	ConnectionID string `json:"connection_id,omitempty"`
}
type InstallRef struct {
	ServerUID string `json:"server_uid"`
	InstallID string `json:"install_id"`
}
type AgentRef struct {
	ServerUID  string `json:"server_uid"`
	InstallID  string `json:"install_id"`
	InstanceID string `json:"instance_id"`
	Generation string `json:"generation"`
}
type SnapshotRef struct {
	VaultID    string `json:"vault_id"`
	SnapshotID string `json:"snapshot_id"`
}
type Server struct {
	Schema    string    `json:"schema"`
	ServerUID string    `json:"server_uid"`
	CreatedAt time.Time `json:"created_at"`
}
type Agent struct {
	Schema        string            `json:"schema"`
	Ref           AgentRef          `json:"agent_ref"`
	Roots         map[string]string `json:"roots"`
	ProfileDigest string            `json:"profile_digest"`
	State         string            `json:"state"`
	CreatedAt     time.Time         `json:"created_at"`
	AcceptedAt    time.Time         `json:"accepted_at"`
	RelationRefs  []string          `json:"relation_refs,omitempty"`
}
type Binding struct {
	IdentityState string `json:"identity_state"`
	Agent         *Agent `json:"agent,omitempty"`
	// Relations summarize committed relations whose target is Agent. They
	// carry identities only: no roots, paths, dispositions or secrets.
	Relations []RelationSummary `json:"relations,omitempty"`
}

// NewID generates an opaque 128-bit identifier using the system RNG.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
