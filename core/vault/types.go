// Package vault defines the data structures of the public vault format
// valise-vault/1 (docs/VAULT_FORMAT.md). Reading and writing come in M3.
package vault

import "time"

// Format identifiers.
const (
	FormatVault    = "valise-vault/1"
	FormatIndex    = "valise-index/1"
	FormatSnapshot = "valise-snapshot/1"
)

// Seal tiers.
const (
	TierSoul        = "soul"
	TierSoulSecrets = "soul-secrets"
	TierFull        = "full"
)

// Descriptor is the plaintext VAULT file.
type Descriptor struct {
	Format    string    `json:"format"`
	VaultID   string    `json:"vault_id"`
	CreatedAt time.Time `json:"created_at"`
	Chunker   Chunker   `json:"chunker"`
	Epochs    []Epoch   `json:"epochs"`
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

// Epoch is one recipient set. Dedupe only happens within an epoch.
type Epoch struct {
	Epoch                 int       `json:"epoch"`
	Since                 time.Time `json:"since"`
	RecipientFingerprints []string  `json:"recipient_fingerprints"`
}

// Index lists the blobs a seal added.
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

// Snapshot is the age-encrypted snapshot manifest.
type Snapshot struct {
	Format     string            `json:"format"`
	SnapshotID string            `json:"snapshot_id"`
	InstanceID string            `json:"instance_id"`
	Tier       string            `json:"tier"`
	Profile    ProfileRef        `json:"profile"`
	Provenance map[string]any    `json:"provenance"`
	CreatedAt  time.Time         `json:"created_at"`
	Roots      map[string]string `json:"roots"`
	Files      []File            `json:"files"`
	Symlinks   []Symlink         `json:"symlinks"`
	Notes      Notes             `json:"notes"`
}

// ProfileRef records which profile a snapshot was sealed with.
type ProfileRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Status  string `json:"status"`
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

// Notes records what happened while sealing.
type Notes struct {
	SQLiteVacuumed    []string `json:"sqlite_vacuumed"`
	Unstable          []string `json:"unstable"`
	UnclassifiedCount int      `json:"unclassified_count"`
}

// Meta is the plaintext <snapshot_id>.meta.json. It never contains paths or
// file names.
type Meta struct {
	InstanceID string    `json:"instance_id"`
	Tier       string    `json:"tier"`
	CreatedAt  time.Time `json:"created_at"`
	FileCount  int       `json:"file_count"`
	PlainBytes int64     `json:"plain_bytes"`
	AddedBytes int64     `json:"added_bytes"`
	Epoch      int       `json:"epoch"`
	ProfileID  string    `json:"profile_id"`
}
