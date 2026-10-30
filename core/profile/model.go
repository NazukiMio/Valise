// Package profile defines the Harness Profile data model (valise-profile/1),
// its strict YAML loader and profilelint. See docs/PROFILES.md.
package profile

// Schema is the only profile schema this package understands.
const Schema = "valise-profile/1"

// StateRoot is the implicit root every profile has: the resolved state dir.
const StateRoot = "state"

// Profile status values.
const (
	StatusDraft    = "draft"
	StatusVerified = "verified"
)

// Profile mirrors the schema in docs/PROFILES.md section 2.
type Profile struct {
	Schema                string       `yaml:"schema" json:"schema"`
	ID                    string       `yaml:"id" json:"id"`
	DisplayName           string       `yaml:"display_name" json:"display_name"`
	ProfileVersion        int          `yaml:"profile_version" json:"profile_version"`
	Status                string       `yaml:"status" json:"status"`
	HarnessVersionsTested []string     `yaml:"harness_versions_tested" json:"harness_versions_tested,omitempty"`
	Docs                  string       `yaml:"docs" json:"docs,omitempty"`
	Extends               string       `yaml:"extends" json:"extends,omitempty"`
	When                  *When        `yaml:"when" json:"when,omitempty"`
	Detect                *Detect      `yaml:"detect" json:"detect,omitempty"`
	Roots                 []Root       `yaml:"roots" json:"roots,omitempty"`
	Paths                 []PathRule   `yaml:"paths" json:"paths,omitempty"`
	Consistency           *Consistency `yaml:"consistency" json:"consistency,omitempty"`
	Provenance            *Provenance  `yaml:"provenance" json:"provenance,omitempty"`
	Body                  *Body        `yaml:"body" json:"body,omitempty"`
	Relocation            *Relocation  `yaml:"relocation" json:"relocation,omitempty"`
	Service               *Service     `yaml:"service" json:"service,omitempty"`
	Entries               []Entry      `yaml:"entries" json:"entries,omitempty"`
	Health                *Health      `yaml:"health" json:"health,omitempty"`
	Login                 []Login      `yaml:"login" json:"login,omitempty"`
	ExclusiveSecrets      []PathRef    `yaml:"exclusive_secrets" json:"exclusive_secrets,omitempty"`
	Native                *Native      `yaml:"native" json:"native,omitempty"`
	Checkup               *Checkup     `yaml:"checkup" json:"checkup,omitempty"`
	Handoff               *Handoff     `yaml:"handoff" json:"handoff,omitempty"`
	AgentKit              *AgentKit    `yaml:"agent_kit" json:"agent_kit,omitempty"`
}

// When is the activation condition of a child profile.
type When struct {
	Exists *PathRef `yaml:"exists" json:"exists,omitempty"`
}

// Detect finds candidate instances.
type Detect struct {
	Binaries           []string `yaml:"binaries" json:"binaries,omitempty"`
	VersionArgv        Argv     `yaml:"version_argv" json:"version_argv,omitempty"`
	StateDirCandidates []string `yaml:"state_dir_candidates" json:"state_dir_candidates,omitempty"`
	MarkersAny         []string `yaml:"markers_any" json:"markers_any,omitempty"`
}

// Root is an additional root besides state.
type Root struct {
	ID           string `yaml:"id" json:"id"`
	DiscoverArgv Argv   `yaml:"discover_argv" json:"discover_argv,omitempty"`
	Default      string `yaml:"default" json:"default"`
}

// PathRule assigns a category to the files a glob matches under a root.
type PathRule struct {
	Root     string `yaml:"root" json:"root"`
	Glob     string `yaml:"glob" json:"glob"`
	Category string `yaml:"category" json:"category"`
	Note     string `yaml:"note" json:"note,omitempty"`
	Verify   string `yaml:"verify" json:"verify,omitempty"`
}

// PathRef is a glob under a root without a category.
type PathRef struct {
	Root string `yaml:"root" json:"root"`
	Glob string `yaml:"glob" json:"glob"`
}

// Consistency declares how files are captured consistently.
type Consistency struct {
	SQLite  []PathRef `yaml:"sqlite" json:"sqlite,omitempty"`
	Quiesce string    `yaml:"quiesce" json:"quiesce,omitempty"`
}

// Provenance kinds.
const (
	ProvenanceNPMGlobal = "npm_global"
	ProvenanceGit       = "git"
	ProvenanceUVTool    = "uv_tool"
	ProvenancePipx      = "pipx"
	ProvenanceBinary    = "binary"
)

// Provenance tells where the body was installed from.
type Provenance struct {
	Kind string         `yaml:"kind" json:"kind"`
	NPM  *NPMProvenance `yaml:"npm" json:"npm,omitempty"`
	Git  *GitProvenance `yaml:"git" json:"git,omitempty"`
}

// NPMProvenance names the npm package of the body.
type NPMProvenance struct {
	Package string `yaml:"package" json:"package"`
}

// GitProvenance locates the git work tree of the body.
type GitProvenance struct {
	Root string `yaml:"root" json:"root"`
	Path string `yaml:"path" json:"path"`
}

// Body describes the rebuildable part inside the roots.
type Body struct {
	Paths           []PathRef `yaml:"paths" json:"paths,omitempty"`
	BootstrapArgv   Argv      `yaml:"bootstrap_argv" json:"bootstrap_argv,omitempty"`
	FreshInstallDoc string    `yaml:"fresh_install_doc" json:"fresh_install_doc,omitempty"`
}

// Relocation names the env var that moves the state dir.
type Relocation struct {
	Env string `yaml:"env" json:"env"`
}

// Service lists candidate service units.
type Service struct {
	Candidates []ServiceUnit `yaml:"candidates" json:"candidates,omitempty"`
}

// ServiceUnit is one systemd unit candidate.
type ServiceUnit struct {
	Scope string `yaml:"scope" json:"scope"`
	Unit  string `yaml:"unit" json:"unit"`
}

// Entry is a web entry of the instance.
type Entry struct {
	ID   string `yaml:"id" json:"id"`
	Kind string `yaml:"kind" json:"kind"`
	Host string `yaml:"host" json:"host"`
	Port int    `yaml:"port" json:"port"`
	Path string `yaml:"path" json:"path"`
}

// Health declares how to tell the instance is healthy.
type Health struct {
	GraceSeconds int           `yaml:"grace_seconds" json:"grace_seconds"`
	Checks       []HealthCheck `yaml:"checks" json:"checks,omitempty"`
}

// HealthCheck is one check; which fields apply depends on Kind.
type HealthCheck struct {
	Kind         string `yaml:"kind" json:"kind"`
	Entry        string `yaml:"entry" json:"entry,omitempty"`
	ExpectStatus []int  `yaml:"expect_status" json:"expect_status,omitempty"`
	Argv         Argv   `yaml:"argv" json:"argv,omitempty"`
	OKExit       []int  `yaml:"ok_exit" json:"ok_exit,omitempty"`
}

// Login is a login method, see docs/LOGIN_RELAY.md.
type Login struct {
	ID             string `yaml:"id" json:"id"`
	DisplayName    string `yaml:"display_name" json:"display_name"`
	Kind           string `yaml:"kind" json:"kind"`
	Argv           Argv   `yaml:"argv" json:"argv"`
	URLPattern     string `yaml:"url_pattern" json:"url_pattern,omitempty"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds,omitempty"`
}

// Native declares the harness's own export command.
type Native struct {
	ExportArgv Argv `yaml:"export_argv" json:"export_argv,omitempty"`
}

// Checkup declares what the code checkup measures.
type Checkup struct {
	Sessions               *SessionsSpec `yaml:"sessions" json:"sessions,omitempty"`
	MemoryFiles            []MemoryFile  `yaml:"memory_files" json:"memory_files,omitempty"`
	MemoryMechanismMarkers []PathRef     `yaml:"memory_mechanism_markers" json:"memory_mechanism_markers,omitempty"`
	NativeOptionsDoc       string        `yaml:"native_options_doc" json:"native_options_doc,omitempty"`
}

// SessionsSpec locates session logs. UsageFields is defined in M6.
type SessionsSpec struct {
	Root        string `yaml:"root" json:"root"`
	Glob        string `yaml:"glob" json:"glob"`
	Format      string `yaml:"format" json:"format"`
	UsageFields any    `yaml:"usage_fields" json:"usage_fields,omitempty"`
}

// MemoryFile is a memory file with a line threshold.
type MemoryFile struct {
	Root      string `yaml:"root" json:"root"`
	Glob      string `yaml:"glob" json:"glob"`
	WarnLines int    `yaml:"warn_lines" json:"warn_lines"`
}

// Handoff declares where handoff content goes.
type Handoff struct {
	Targets        []HandoffTarget `yaml:"targets" json:"targets,omitempty"`
	NewSessionHint string          `yaml:"new_session_hint" json:"new_session_hint,omitempty"`
}

// HandoffTarget is one native memory file handoff writes to.
type HandoffTarget struct {
	Root     string `yaml:"root" json:"root"`
	Glob     string `yaml:"glob" json:"glob"`
	Mode     string `yaml:"mode" json:"mode"`
	MaxLines int    `yaml:"max_lines" json:"max_lines"`
}

// AgentKit declares where agent-kit skills are installed.
type AgentKit struct {
	SkillsDir    *RootPath `yaml:"skills_dir" json:"skills_dir,omitempty"`
	MCPConfigDoc string    `yaml:"mcp_config_doc" json:"mcp_config_doc,omitempty"`
}

// RootPath is a plain relative path (not a glob) under a root.
type RootPath struct {
	Root string `yaml:"root" json:"root"`
	Path string `yaml:"path" json:"path"`
}

// Categories, see docs/ARCHITECTURE.md section 5.
const (
	CategoryIdentity  = "identity"
	CategoryMemory    = "memory"
	CategorySkills    = "skills"
	CategoryConfig    = "config"
	CategorySessions  = "sessions"
	CategoryState     = "state"
	CategoryBodyPatch = "body_patch"
	CategorySecrets   = "secrets"
	CategoryWorkspace = "workspace"
	CategoryBody      = "body"
	CategoryCache     = "cache"
	CategoryIgnore    = "ignore"
)

// Categories lists every valid category.
var Categories = []string{
	CategoryIdentity, CategoryMemory, CategorySkills, CategoryConfig,
	CategorySessions, CategoryState, CategoryBodyPatch, CategorySecrets,
	CategoryWorkspace, CategoryBody, CategoryCache, CategoryIgnore,
}

// IsCategory reports whether c is a valid category.
func IsCategory(c string) bool {
	for _, k := range Categories {
		if c == k {
			return true
		}
	}
	return false
}
