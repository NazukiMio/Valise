// Package vault implements the public vault format valise-vault/1
// (docs/VAULT_FORMAT.md): types, the append-only writer used by porter, the
// reader used by the client, and verification.
package vault

import (
	"time"

	"valise/core/enroll"
	"valise/core/profile"
)

// Format identifiers.
const (
	FormatVault          = "valise-vault/1"
	FormatDedupe         = "valise-dedupe/1"
	FormatIndex          = "valise-index/1"
	FormatSnapshot       = "valise-snapshot/2"
	FormatSnapshotLegacy = "valise-snapshot/1"
	FormatFiles          = "valise-files/1"
	FormatMeta           = "valise-meta/1"
)

// Seal tiers.
const (
	TierSoul        = "soul"
	TierSoulSecrets = "soul-secrets"
	TierFull        = "full"
	TierRaw         = "raw"
)

// Tiers lists every tier.
var Tiers = []string{TierSoul, TierSoulSecrets, TierFull, TierRaw}

// CategoryUnclassified marks files no rule matched. It cannot be declared
// in a profile; such files are treated like secrets ("unknown means
// sensitive") and never enter the soul tier.
const CategoryUnclassified = "unclassified"

// Sensitive reports whether files of a category, or a whole snapshot of a
// tier, belong to the secrets layer.
func Sensitive(tier, category string) bool {
	return tier == TierRaw || category == "secrets" || category == CategoryUnclassified
}

// BodyPatchRoot and BodyPatchPath name the virtual body_patch file.
const (
	BodyPatchRoot = "valise"
	BodyPatchPath = "body.patch"
)

// Descriptor is the plaintext VAULT file. It is written once.
type Descriptor struct {
	Format    string    `json:"format"`
	VaultID   string    `json:"vault_id"`
	CreatedAt time.Time `json:"created_at"`
	Chunker   Chunker   `json:"chunker"`
}

// Chunker holds the content-defined chunking parameters.
type Chunker struct {
	Algo         string `json:"algo"`
	Min          int64  `json:"min"`
	Avg          int64  `json:"avg"`
	Max          int64  `json:"max"`
	SmallFileMax int64  `json:"small_file_max"`
}

// DefaultChunker is the chunker of new vaults.
var DefaultChunker = Chunker{Algo: "restic-chunker", Min: 512 << 10, Avg: 1 << 20, Max: 8 << 20, SmallFileMax: 1 << 20}

// Epoch is epochs/<n>.json: one recipient set. Dedupe only happens within
// an epoch.
type Epoch struct {
	Epoch                 int       `json:"epoch"`
	Since                 time.Time `json:"since"`
	RecipientFingerprints []string  `json:"recipient_fingerprints"`
}

// DedupeParams is the plaintext of keys/dedupe.<n>.age and of the server's
// dedupe/<vault_id>.key.
type DedupeParams struct {
	Format     string `json:"format"`
	VaultID    string `json:"vault_id"`
	Epoch      int    `json:"epoch"`
	HMACKey    string `json:"hmac_key"`
	ChunkerPol string `json:"chunker_pol"`
}

// Index lists new blobs, or the full dependency set for an automatic snapshot.
type Index struct {
	Format     string      `json:"format"`
	SnapshotID string      `json:"snapshot_id"`
	Epoch      int         `json:"epoch"`
	Blobs      []BlobEntry `json:"blobs"`
}

// BlobEntry locates one blob inside a pack.
type BlobEntry struct {
	ID        string `json:"id"`
	Pack      string `json:"pack"`
	Offset    int64  `json:"offset"`
	Length    int64  `json:"length"`
	PlainSize int64  `json:"plain_size"`
}

// Snapshot is the age-encrypted snapshot header.
type Snapshot struct {
	Format     string            `json:"format"`
	SnapshotID string            `json:"snapshot_id"`
	InstanceID string            `json:"instance_id"`
	Tier       string            `json:"tier"`
	Profile    ProfileRef        `json:"profile"`
	Provenance enroll.Provenance `json:"provenance"`
	// Runtime is encrypted source evidence, never permission to execute a
	// recorded path or proof that all dependencies can be rebuilt.
	Runtime *RuntimeRecord `json:"runtime,omitempty"`
	// ProfileDoc is the effective profile the snapshot was sealed with; a
	// client without that profile version may revive with it explicitly.
	ProfileDoc *profile.Profile `json:"profile_doc,omitempty"`
	// LocalOverrides, LocalSQLite and ExternalRefs record the instance's
	// dossier extensions at seal time.
	LocalOverrides []profile.Override   `json:"local_overrides,omitempty"`
	LocalSQLite    []profile.SQLiteRef  `json:"local_sqlite,omitempty"`
	ExternalRefs   []enroll.ExternalRef `json:"external_refs,omitempty"`
	// Name and Env: a named instance (docs/PROFILES.md 2, 多实例).
	Name        string            `json:"name,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	Epoch       int               `json:"epoch"`
	Roots       map[string]string `json:"roots"`
	FilesBlobs  []string          `json:"files_blobs"`
	FilesSHA256 string            `json:"files_sha256"`
	SQLite      []SQLiteNote      `json:"sqlite"`
	Notes       Notes             `json:"notes"`
}

// ProfileRef records which profile a snapshot was sealed with.
type ProfileRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Status  string `json:"status"`
}

// SQLiteNote records a database sealed from a VACUUM INTO copy.
type SQLiteNote struct {
	Root           string   `json:"root"`
	Path           string   `json:"path"`
	StrippedTables []string `json:"stripped_tables,omitempty"`
}

// Notes records what happened while sealing.
type Notes struct {
	Unstable          []string `json:"unstable"`
	UnclassifiedCount int      `json:"unclassified_count"`
}

// Files is the content-addressed file list (valise-files/1).
type Files struct {
	Format   string    `json:"format"`
	Files    []File    `json:"files"`
	Symlinks []Symlink `json:"symlinks"`
}

// File is one regular file in a snapshot.
type File struct {
	Root     string    `json:"root"`
	Path     string    `json:"path"`
	Category string    `json:"category"`
	Mode     uint32    `json:"mode"`
	MTime    time.Time `json:"mtime"`
	Size     int64     `json:"size"`
	SHA256   string    `json:"sha256"`
	Blobs    []string  `json:"blobs"`
}

// Symlink is one symbolic link in a snapshot.
type Symlink struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Target string `json:"target"`
}

const (
	KindAuto      = "auto"
	KindSavepoint = "savepoint"
)

// Meta is the plaintext <snapshot_id>.meta.json. It never contains paths or
// file names.
type Meta struct {
	Format       string    `json:"format"`
	SnapshotID   string    `json:"snapshot_id"`
	InstanceID   string    `json:"instance_id"`
	Tier         string    `json:"tier"`
	CreatedAt    time.Time `json:"created_at"`
	FileCount    int       `json:"file_count"`
	PlainBytes   int64     `json:"plain_bytes"`
	AddedBytes   int64     `json:"added_bytes"`
	Epoch        int       `json:"epoch"`
	ProfileID    string    `json:"profile_id"`
	Kind         string    `json:"kind,omitempty"`
	Note         string    `json:"note,omitempty"`
	PromotedFrom string    `json:"promoted_from,omitempty"`
}

// TierCategories are the categories each tier seals
// (docs/ARCHITECTURE.md section 5).
var TierCategories = map[string][]string{
	TierSoul:        {"identity", "memory", "skills", "config", "sessions", "state", "body_patch"},
	TierSoulSecrets: {"identity", "memory", "skills", "config", "sessions", "state", "body_patch", "secrets", CategoryUnclassified},
	TierFull:        {"identity", "memory", "skills", "config", "sessions", "state", "body_patch", "secrets", CategoryUnclassified, "workspace"},
	TierRaw: {"identity", "memory", "skills", "config", "sessions", "state", "body_patch", "secrets", CategoryUnclassified,
		"workspace", "body", "cache", "ignore"},
}

// InTier reports whether category is sealed by tier.
func InTier(tier, category string) bool {
	for _, c := range TierCategories[tier] {
		if c == category {
			return true
		}
	}
	return false
}
