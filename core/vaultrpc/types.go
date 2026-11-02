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
	// Vault tree and cloud backup (docs/VAULT_FORMAT.md section 10,
	// docs/ARCHITECTURE.md section 7).
	MethodServerNameSet = "server_name_set"
	MethodPeriodSet     = "period_set"
	MethodLeafDelete    = "leaf_delete"
	MethodTargetList    = "target_list"
	MethodTargetLeafDel = "target_leaf_delete"
	MethodTimerSet      = "timer_set"
	MethodTimerStatus   = "timer_status"
	MethodSealLog       = "seal_log"
)

// Initiators recorded in the audit log.
const (
	InitiatorClient   = "client"
	InitiatorAgentMCP = "agent-mcp"
	InitiatorTimer    = "timer"
	InitiatorAuto     = "auto"
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
	SnapshotID string `json:"snapshot_id"`
	InstanceID string `json:"instance_id"`
	// Leaf is the period the snapshot was written to.
	Leaf string `json:"leaf"`
	// AutoSync lists the targets an automatic cloud backup was started for.
	AutoSync          []string           `json:"auto_sync,omitempty"`
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
	// Station is set for a station Valise manages (docs/ARCHITECTURE.md
	// section 7): porter connects with these and its own station key, not
	// the server's SSH configuration. Dest is then "<address>:<path>".
	Station *StationConn `json:"station,omitempty"`
	// Auto turns on syncing after seals, at most once per Every.
	Auto *AutoSync `json:"auto,omitempty"`
}

// StationConn is how porter reaches a Valise-managed station; no secrets.
type StationConn struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	// KnownHosts are the confirmed known_hosts lines of the station.
	KnownHosts []string `json:"known_hosts"`
	// Rooted: the station anchors this server's paths at its own folder
	// (rrsync), so Dest already is the server's folder.
	Rooted bool `json:"rooted,omitempty"`
}

// Station certificate methods (owner tier): the client signs the server's
// station key for a while (docs/ARCHITECTURE.md section 7).
const (
	MethodStationCert  = "station_cert"
	MethodStationProbe = "station_probe"
)

// StationCertParams installs a certificate for the server's station key.
type StationCertParams struct {
	Name string `json:"name"`
	Cert string `json:"cert"`
}

// StationProbeResult is what the station's forced probe command printed.
type StationProbeResult struct {
	Output string `json:"output"`
}

// Station key and record methods (owner tier).
const (
	MethodStationKey = "station_key"
	MethodSyncRecord = "sync_record"
)

// StationKeyParams names the station whose key porter makes, shows,
// probes with.
type StationKeyParams struct {
	Name string `json:"name"`
}

// StationKeyResult is the public key of the server's station key.
type StationKeyResult struct {
	PublicKey string `json:"public_key"`
}

// SyncRecordParams records a relayed copy in the target's sync record.
type SyncRecordParams struct {
	Target    string   `json:"target"`
	Snapshots []string `json:"snapshots"`
	// Forget drops a period the client deleted from the station.
	Forget string `json:"forget,omitempty"`
}

// AutoSync is a target's automatic backup setting.
type AutoSync struct {
	Every string `json:"every"`
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
	ServerName string        `json:"server_name,omitempty"`
	Recipients []string      `json:"recipient_fingerprints"`
	Snapshots  []vault.Meta  `json:"snapshots"`
	Leaves     []LeafState   `json:"leaves"`
	Targets    []TargetState `json:"targets"`
}

// LeafState is one period vault on the server.
type LeafState struct {
	Leaf     string        `json:"leaf"`
	Period   *vault.Period `json:"period,omitempty"`
	VaultID  string        `json:"vault_id,omitempty"`
	Epochs   int           `json:"epochs"`
	Bytes    int64         `json:"bytes"`
	Deleting bool          `json:"deleting,omitempty"`
	// Current is the leaf the instance writes to today.
	Current   bool     `json:"current,omitempty"`
	Snapshots []string `json:"snapshots"`
}

// LeafOf returns the leaf holding a snapshot, "" when unknown.
func (s *StatusResult) LeafOf(snapshotID string) string {
	for _, l := range s.Leaves {
		for _, id := range l.Snapshots {
			if id == snapshotID {
				return l.Leaf
			}
		}
	}
	return ""
}

// VerifyResult is a no-key verification.
type VerifyResult struct {
	Problems []vault.Problem `json:"problems"`
}

// FilesParams names the leaf to list.
type FilesParams struct {
	Leaf string `json:"leaf"`
}

// FilesResult lists every file of a leaf (relative path and size), for pull.
type FilesResult struct {
	Leaf    string      `json:"leaf"`
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
	Leaves    []string `json:"leaves"`
	Snapshots []string `json:"snapshots"`
	// Method is how the files went (rsync or sftp); Note explains a
	// fallback to sftp.
	Method string `json:"method,omitempty"`
	Note   string `json:"note,omitempty"`
}

// ServerNameSetParams names the server (once).
type ServerNameSetParams struct {
	Name string `json:"name"`
}

// PeriodSetParams sets an instance's period unit and time zone.
type PeriodSetParams struct {
	InstanceID string              `json:"instance_id"`
	Setting    vault.PeriodSetting `json:"setting"`
}

// LeafDeleteParams deletes one whole leaf on the server.
type LeafDeleteParams struct {
	Leaf string `json:"leaf"`
}

// LeafDeleteResult reports a deletion.
type LeafDeleteResult struct {
	Leaf  string `json:"leaf"`
	Bytes int64  `json:"bytes"`
}

// TargetListParams names the target to list.
type TargetListParams struct {
	Target string `json:"target"`
}

// TargetListResult is what a target holds for this server.
type TargetListResult struct {
	Target string       `json:"target"`
	Dest   string       `json:"dest"`
	Leaves []RemoteLeaf `json:"leaves"`
}

// RemoteLeaf is one leaf found on a target.
type RemoteLeaf struct {
	Leaf      string   `json:"leaf"`
	Bytes     int64    `json:"bytes"`
	Deleting  bool     `json:"deleting,omitempty"`
	Snapshots []string `json:"snapshots"`
	Packs     []string `json:"packs"`
}

// TargetLeafDeleteParams deletes one leaf on a target.
type TargetLeafDeleteParams struct {
	Target string `json:"target"`
	Leaf   string `json:"leaf"`
}

// TimerSetParams turns the timed seal of an instance on or off.
type TimerSetParams struct {
	InstanceID string `json:"instance_id"`
	Enabled    bool   `json:"enabled"`
	Every      string `json:"every,omitempty"`
}

// TimerStatusParams names an instance.
type TimerStatusParams struct {
	InstanceID string `json:"instance_id"`
}

// TimerStatus describes the timed seal of an instance.
type TimerStatus struct {
	Enabled bool     `json:"enabled"`
	Every   string   `json:"every,omitempty"`
	Units   []string `json:"units,omitempty"`
	// Linger reports whether the user's systemd instance runs without a
	// login session; without it the timer only fires while logged in.
	Linger bool `json:"linger"`
}

// SealLogParams names an instance.
type SealLogParams struct {
	InstanceID string `json:"instance_id"`
}

// SealLogEntry says who started a snapshot and why (from the audit log;
// snapshot metadata holds neither).
type SealLogEntry struct {
	SnapshotID string    `json:"snapshot_id"`
	Time       time.Time `json:"time"`
	Initiator  string    `json:"initiator"`
	Reason     string    `json:"reason,omitempty"`
}

// SealLogResult lists an instance's successful seals, oldest first.
type SealLogResult struct {
	Entries []SealLogEntry `json:"entries"`
}
