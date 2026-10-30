// Package enroll defines the data exchanged during enrollment
// (docs/ARCHITECTURE.md section 4): detection candidates, the enrollment
// report, provenance records and the instance record stored on the server.
package enroll

import (
	"fmt"
	"regexp"
	"time"

	"valise/core/profile"
)

// RPC method names.
const (
	MethodDetect        = "detect"
	MethodScan          = "enroll_scan"
	MethodInstanceGet   = "instance_get"
	MethodInstanceWrite = "instance_write"
)

var idRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidInstanceID reports whether id follows the profile id naming rule.
func ValidInstanceID(id string) error {
	if !idRe.MatchString(id) || len(id) > 64 {
		return fmt.Errorf("实例 id 只能包含小写字母、数字和连字符（最长 64 个字符）：%q", id)
	}
	return nil
}

// DetectParams carries the effective profiles; porter has none of its own.
type DetectParams struct {
	Profiles []profile.Profile `json:"profiles"`
}

// DetectResult lists candidate instances.
type DetectResult struct {
	Candidates []Candidate `json:"candidates"`
}

// Candidate is one detected instance.
type Candidate struct {
	ProfileID string `json:"profile_id"`
	StateDir  string `json:"state_dir"`
	// Markers are the markers_any entries present in StateDir.
	Markers []string `json:"markers"`
	// Binary is the first detect.binaries entry found on the login PATH.
	Binary  string `json:"binary,omitempty"`
	Version string `json:"version,omitempty"`
	// Children are child profiles whose when condition holds here.
	Children []string `json:"children,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ScanParams asks porter to resolve and classify one candidate.
type ScanParams struct {
	Profile    profile.Profile    `json:"profile"`
	StateDir   string             `json:"state_dir"`
	InstanceID string             `json:"instance_id"`
	Overrides  []profile.Override `json:"overrides,omitempty"`
}

// Root is a resolved root.
type Root struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// Source is "detect", "default" or "discover".
	Source string `json:"source"`
	Exists bool   `json:"exists"`
	// Resolved is set when Path is (or passes through) a symlink: the real
	// directory that is scanned.
	Resolved string `json:"resolved,omitempty"`
}

// Dir returns the directory actually scanned.
func (r Root) Dir() string {
	if r.Resolved != "" {
		return r.Resolved
	}
	return r.Path
}

// Tally is a file count and byte total.
type Tally struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// FileRef is a root-relative path. Reports carry paths only, never contents.
type FileRef struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

// SQLiteFile is a declared database found on disk.
type SQLiteFile struct {
	FileRef
	Category     string   `json:"category"`
	SecretTables []string `json:"secret_tables,omitempty"`
	Sidecars     []string `json:"sidecars,omitempty"`
}

// UnclassifiedGroup aggregates unclassified files by their first two path
// segments so the report stays readable.
type UnclassifiedGroup struct {
	Root   string `json:"root"`
	Prefix string `json:"prefix"`
	Tally
}

// ServiceState is the observed state of a service candidate.
type ServiceState struct {
	Scope     string `json:"scope"`
	Unit      string `json:"unit"`
	LoadState string `json:"load_state"`
	Active    string `json:"active_state"`
	Enabled   string `json:"unit_file_state"`
}

// Exists reports whether systemd knows the unit.
func (s ServiceState) Exists() bool { return s.LoadState == "loaded" }

// EntryState is the observed state of an entry port on the server.
type EntryState struct {
	ID        string `json:"id"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Listening bool   `json:"listening"`
}

// Report is the enrollment report.
type Report struct {
	InstanceID     string              `json:"instance_id"`
	ProfileID      string              `json:"profile_id"`
	ProfileVersion int                 `json:"profile_version"`
	ProfileStatus  string              `json:"profile_status"`
	Roots          []Root              `json:"roots"`
	Categories     map[string]Tally    `json:"categories"`
	Secrets        []FileRef           `json:"secrets"`
	SQLite         []SQLiteFile        `json:"sqlite"`
	Unclassified   Tally               `json:"unclassified"`
	UnclassGroups  []UnclassifiedGroup `json:"unclassified_groups"`
	Symlinks       int                 `json:"symlinks"`
	Provenance     Provenance          `json:"provenance"`
	Service        []ServiceState      `json:"service"`
	Entries        []EntryState        `json:"entries"`
	Pending        []profile.Pending   `json:"pending,omitempty"`
	Overrides      []profile.Override  `json:"local_overrides,omitempty"`
	Warnings       []string            `json:"warnings,omitempty"`
	Previous       *Record             `json:"previous,omitempty"`
	Changes        []Change            `json:"changes,omitempty"`
	ScannedAt      time.Time           `json:"scanned_at"`
	Unreadable     []FileRef           `json:"unreadable,omitempty"`
}

// Provenance records where the body came from.
type Provenance struct {
	Kind     string        `json:"kind"`
	NPM      *NPMRecord    `json:"npm,omitempty"`
	Git      *GitRecord    `json:"git,omitempty"`
	Tool     *ToolRecord   `json:"tool,omitempty"`
	Binary   *BinaryRecord `json:"binary,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
}

// NPMRecord is an npm global install.
type NPMRecord struct {
	Package   string `json:"package"`
	Version   string `json:"version"`
	Registry  string `json:"registry,omitempty"`
	Integrity string `json:"integrity,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
}

// GitRecord is a git work tree.
type GitRecord struct {
	Path        string `json:"path"`
	Remote      string `json:"remote"`
	Commit      string `json:"commit"`
	Dirty       bool   `json:"dirty"`
	PatchBytes  int64  `json:"patch_bytes,omitempty"`
	PatchSHA256 string `json:"patch_sha256,omitempty"`
	Untracked   int    `json:"untracked,omitempty"`
}

// ToolRecord is a uv tool or pipx package.
type ToolRecord struct {
	Package string `json:"package"`
	Version string `json:"version"`
}

// BinaryRecord is a hashed executable.
type BinaryRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Record is the instance record at ~/.config/valise/instances/<id>.json.
type Record struct {
	InstanceID     string             `json:"instance_id"`
	ProfileID      string             `json:"profile_id"`
	ProfileVersion int                `json:"profile_version"`
	Roots          map[string]string  `json:"roots"`
	Provenance     Provenance         `json:"provenance"`
	Service        []ServiceState     `json:"service"`
	Entries        []EntryState       `json:"entries"`
	LocalOverrides []profile.Override `json:"local_overrides"`
	EnrolledAt     time.Time          `json:"enrolled_at"`
	// Superseded is set when a migrate or clone replaced this instance's
	// directories with another instance (ARCHITECTURE.md 8.2.1).
	Superseded *Supersession `json:"superseded,omitempty"`
}

// Supersession says which operation replaced an instance.
type Supersession struct {
	By   string    `json:"by"`
	OpID string    `json:"op_id"`
	At   time.Time `json:"at"`
}

// RecordFrom builds the record the user accepts from a report.
func RecordFrom(r *Report, now time.Time) Record {
	roots := map[string]string{}
	for _, rt := range r.Roots {
		roots[rt.ID] = rt.Path
	}
	ov := r.Overrides
	if ov == nil {
		ov = []profile.Override{}
	}
	return Record{
		InstanceID: r.InstanceID, ProfileID: r.ProfileID, ProfileVersion: r.ProfileVersion,
		Roots: roots, Provenance: r.Provenance, Service: r.Service, Entries: r.Entries,
		LocalOverrides: ov, EnrolledAt: now.UTC(),
	}
}

// Change is one difference from the previous enrollment.
type Change struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// InstanceGetParams names an instance.
type InstanceGetParams struct {
	InstanceID string `json:"instance_id"`
}

// InstanceGetResult is nil Record when not enrolled.
type InstanceGetResult struct {
	Record *Record `json:"record"`
}

// InstanceWriteParams writes an accepted record and the effective profile
// it was enrolled with (used for sealing while the client is offline).
type InstanceWriteParams struct {
	Record  Record          `json:"record"`
	Profile profile.Profile `json:"profile"`
}

// InstanceWriteResult reports where the record was written.
type InstanceWriteResult struct {
	Path string `json:"path"`
}
