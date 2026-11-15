package vault

import (
	"time"

	"valise/core/enroll"
	"valise/core/profile"
)

const FormatRuntime = "valise-runtime/1"

// RuntimeRecord captures source facts only. Full preparation must separately
// verify every dependency and source; absent/unknown records remain usable for
// file recovery without guessing a source HOME or a named instance's runtime.
type RuntimeRecord struct {
	Format       string                 `json:"format"`
	CapturedAt   time.Time              `json:"captured_at"`
	OS           string                 `json:"os"`
	Arch         string                 `json:"arch"`
	Home         RuntimeRoot            `json:"home"`
	Roots        map[string]RuntimeRoot `json:"roots"`
	Installation *RuntimeInstallation   `json:"installation,omitempty"`
	Dependencies []RuntimeDependency    `json:"dependencies,omitempty"`
	Unresolved   []string               `json:"unresolved"`
}

type RuntimeRoot struct {
	Path     string `json:"path"`
	Resolved string `json:"resolved,omitempty"`
}

// RuntimeInstallation preserves the registration accepted for the installation
// a named Agent belongs to. It deliberately does not claim current observation.
type RuntimeInstallation struct {
	InstallID            string                 `json:"install_id"`
	DefaultInstanceID    string                 `json:"default_instance_id"`
	AcceptedAt           time.Time              `json:"accepted_at"`
	ProfileDoc           *profile.Profile       `json:"profile_doc"`
	Roots                map[string]RuntimeRoot `json:"roots"`
	RegisteredProvenance enroll.Provenance      `json:"registered_provenance"`
}

// RuntimeDependency may be populated only by an explicit source adapter. A
// login PATH lookup or a parent Agent's current version is not such evidence.
type RuntimeDependency struct {
	Role       string             `json:"role"`
	Path       string             `json:"path"`
	Resolved   string             `json:"resolved,omitempty"`
	Size       int64              `json:"size,omitempty"`
	SHA256     string             `json:"sha256,omitempty"`
	Version    string             `json:"version,omitempty"`
	Provenance *enroll.Provenance `json:"provenance,omitempty"`
}

func SupportedSnapshotFormat(format string) bool {
	return format == FormatSnapshotLegacy || format == FormatSnapshot
}
