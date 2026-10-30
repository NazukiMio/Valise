// Package vaultrpc defines the RPC messages for keys, sealing, vault status,
// verification and sync.
package vaultrpc

import (
	"time"

	"valise/core/vault"
)

// Method names.
const (
	MethodRecipientAdd = "recipient_add"
	MethodSeal         = "seal"
	MethodStatus       = "vault_status"
	MethodVerify       = "vault_verify"
	MethodFiles        = "vault_files"
	MethodTargetSet    = "target_set"
	MethodSync         = "sync"
)

// Initiators recorded in the audit log.
const (
	InitiatorClient   = "client"
	InitiatorAgentMCP = "agent-mcp"
	InitiatorTimer    = "timer"
)

// RecipientAddParams registers the client's public key.
type RecipientAddParams struct {
	Recipient string `json:"recipient"`
	// Label is written as a comment above the key in recipients.txt.
	Label string `json:"label,omitempty"`
}

// RecipientAddResult lists the server's recipients afterwards.
type RecipientAddResult struct {
	Added        bool     `json:"added"`
	Fingerprints []string `json:"fingerprints"`
}

// SealParams asks for one seal.
type SealParams struct {
	InstanceID string `json:"instance_id"`
	Tier       string `json:"tier"`
	Reason     string `json:"reason,omitempty"`
}

// Tally is a file count and byte total.
type Tally struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// SealReport is the result of a seal (docs/ARCHITECTURE.md section 6).
type SealReport struct {
	SnapshotID        string             `json:"snapshot_id"`
	InstanceID        string             `json:"instance_id"`
	Tier              string             `json:"tier"`
	Epoch             int                `json:"epoch"`
	NewEpoch          bool               `json:"new_epoch"`
	Categories        map[string]Tally   `json:"categories"`
	Files             int                `json:"files"`
	PlainBytes        int64              `json:"plain_bytes"`
	AddedBytes        int64              `json:"added_bytes"`
	UnclassifiedCount int                `json:"unclassified_count"`
	Unstable          []string           `json:"unstable"`
	SQLite            []vault.SQLiteNote `json:"sqlite"`
	BodyPatch         bool               `json:"body_patch"`
	Warnings          []string           `json:"warnings,omitempty"`
	DurationMS        int64              `json:"duration_ms"`
}

// Target is one entry of targets.json.
type Target struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Dest string `json:"dest"`
}

// TargetSetParams adds or replaces a target.
type TargetSetParams struct {
	Target Target `json:"target"`
}

// TargetState is the last sync of a target.
type TargetState struct {
	Target
	LastOK     *time.Time `json:"last_ok,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
	LastSynced []string   `json:"last_synced_snapshots,omitempty"`
}

// StatusResult describes the server vault without any key.
type StatusResult struct {
	Exists     bool          `json:"exists"`
	VaultID    string        `json:"vault_id,omitempty"`
	Epochs     int           `json:"epochs"`
	Recipients []string      `json:"recipient_fingerprints"`
	Snapshots  []vault.Meta  `json:"snapshots"`
	Targets    []TargetState `json:"targets"`
}

// VerifyResult is a no-key verification.
type VerifyResult struct {
	Problems []vault.Problem `json:"problems"`
}

// FilesResult lists every vault file (relative path and size), for pull.
type FilesResult struct {
	VaultID string      `json:"vault_id"`
	Files   []VaultFile `json:"files"`
}

// VaultFile is one file of the vault.
type VaultFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// SyncParams names the target.
type SyncParams struct {
	Target string `json:"target"`
}

// SyncResult reports a finished sync.
type SyncResult struct {
	Target    string   `json:"target"`
	Dest      string   `json:"dest"`
	Snapshots []string `json:"snapshots"`
}
