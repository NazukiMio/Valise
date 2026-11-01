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
	Display               *Display     `yaml:"display" json:"display,omitempty"`
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
	// Install is the official install plan (docs/INSTALL.md section 3).
	Install *Install `yaml:"install" json:"install,omitempty"`
	// NativeCLI is the harness's own interactive CLI (docs/INSTALL.md
	// section 2).
	NativeCLI Argv `yaml:"native_cli" json:"native_cli,omitempty"`
	// Actions are managed actions; the schema is settled in M12, until then
	// only an empty list is accepted.
	Actions []any `yaml:"actions" json:"actions,omitempty"`
	// Capabilities record what was verified, per capability
	// (docs/PROFILES.md 1.2).
	Capabilities map[string]Capability `yaml:"capabilities" json:"capabilities,omitempty"`
}

// Display is how the instance card looks in the UI (docs/PROFILES.md).
type Display struct {
	Color string `yaml:"color" json:"color,omitempty"`
	// Icon is character art taken from the harness's own TUI.
	Icon       string `yaml:"icon" json:"icon,omitempty"`
	IconSource string `yaml:"icon_source" json:"icon_source,omitempty"`
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
	// BinDirs are the harness's own executable directories (for example a
	// private prefix its installer uses), put in front of the login PATH
	// whenever Valise runs this harness's commands.
	BinDirs []string `yaml:"bin_dirs" json:"bin_dirs,omitempty"`
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
	SQLite  []SQLiteRef `yaml:"sqlite" json:"sqlite,omitempty"`
	Quiesce string      `yaml:"quiesce" json:"quiesce,omitempty"`
}

// SQLiteRef declares SQLite databases. SecretTables are tables holding
// credentials; the soul tier clears them in the sealed copy.
type SQLiteRef struct {
	Root         string   `yaml:"root" json:"root"`
	Glob         string   `yaml:"glob" json:"glob"`
	SecretTables []string `yaml:"secret_tables" json:"secret_tables,omitempty"`
}

// Ref returns the root and glob.
func (s SQLiteRef) Ref() PathRef { return PathRef{s.Root, s.Glob} }

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
	Kind   string            `yaml:"kind" json:"kind"`
	NPM    *NPMProvenance    `yaml:"npm" json:"npm,omitempty"`
	Git    *GitProvenance    `yaml:"git" json:"git,omitempty"`
	UV     *PackageRef       `yaml:"uv" json:"uv,omitempty"`
	Pipx   *PackageRef       `yaml:"pipx" json:"pipx,omitempty"`
	Binary *BinaryProvenance `yaml:"binary" json:"binary,omitempty"`
}

// PackageRef names a uv tool or pipx package.
type PackageRef struct {
	Package string `yaml:"package" json:"package"`
}

// BinaryProvenance names the command whose file is hashed.
type BinaryProvenance struct {
	Name string `yaml:"name" json:"name"`
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
	// InstallArgv is the harness's own command that installs the service;
	// run on a revive target lacking the unit, after the user confirms.
	InstallArgv Argv `yaml:"install_argv" json:"install_argv,omitempty"`
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
	// Auth says how the harness's own command yields the entry's token
	// (docs/LOGIN_RELAY.md 4.1).
	Auth *EntryAuth `yaml:"auth" json:"auth,omitempty"`
	// PortChange moves the entry to another port (docs/LOGIN_RELAY.md 4.2).
	PortChange *PortChange `yaml:"port_change" json:"port_change,omitempty"`
}

// PortChange is the harness's own commands that move an entry to another
// port (docs/LOGIN_RELAY.md 4.2), run in order; they may only use ${PORT}.
type PortChange struct {
	Steps          []Argv `yaml:"steps" json:"steps"`
	RestartService bool   `yaml:"restart_service" json:"restart_service,omitempty"`
}

// Install is an official install plan: what must already be there, and the
// steps. Only verified versions and methods belong here.
type Install struct {
	ID       string        `yaml:"id" json:"id"`
	Summary  string        `yaml:"summary" json:"summary"`
	Requires []Require     `yaml:"requires" json:"requires,omitempty"`
	Steps    []InstallStep `yaml:"steps" json:"steps"`
}

// Require is a program the plan needs, with an optional minimum version and
// how to install it (run only in full access mode, docs/INSTALL.md 4.1).
type Require struct {
	Binary  string           `yaml:"binary" json:"binary"`
	Version string           `yaml:"version" json:"version,omitempty"` // ">=x.y.z"
	Why     string           `yaml:"why" json:"why,omitempty"`
	Install []RequireInstall `yaml:"install" json:"install,omitempty"`
}

// RequireInstall installs a requirement on the listed distributions
// (os-release ID).
type RequireInstall struct {
	OSID []string `yaml:"os_id" json:"os_id"`
	Argv Argv     `yaml:"argv" json:"argv"`
}

// Install step kinds.
const (
	StepNPMGlobal = "npm_global"
	StepScript    = "script"
	StepArgv      = "argv"
)

// InstallStep is one step; which fields apply depends on Kind.
type InstallStep struct {
	Kind string `yaml:"kind" json:"kind"`
	// npm_global
	Package   string `yaml:"package" json:"package,omitempty"`
	Version   string `yaml:"version" json:"version,omitempty"`
	Integrity string `yaml:"integrity" json:"integrity,omitempty"`
	// script: downloaded by the client, checked against SHA256, shown,
	// then run as Interpreter + file + Args.
	URL         string   `yaml:"url" json:"url,omitempty"`
	SHA256      string   `yaml:"sha256" json:"sha256,omitempty"`
	Interpreter Argv     `yaml:"interpreter" json:"interpreter,omitempty"`
	Args        []string `yaml:"args" json:"args,omitempty"`
	// RuntimeDownloads says what the script itself downloads; "" means
	// verified to download nothing.
	RuntimeDownloads *string `yaml:"runtime_downloads" json:"runtime_downloads,omitempty"`
	// argv
	Argv Argv `yaml:"argv" json:"argv,omitempty"`
	// Note explains the step to the user.
	Note string `yaml:"note" json:"note,omitempty"`
}

// Capability is the verification status of one capability.
type Capability struct {
	Status   string   `yaml:"status" json:"status"`
	Versions []string `yaml:"versions" json:"versions,omitempty"`
	Env      string   `yaml:"env" json:"env,omitempty"`
	Date     string   `yaml:"date" json:"date,omitempty"`
	Note     string   `yaml:"note" json:"note,omitempty"`
}

// Capability names and statuses (docs/PROFILES.md 1.2).
var (
	CapabilityNames    = []string{"install", "manage", "backup", "restore", "login", "checkup", "kit"}
	CapabilityStatuses = []string{"tested", "partial", "untested", "failed"}
)

// EntryAuth obtains an entry's token with a harness command. At most one of
// URLArgv and TokenArgv is set; URLSuffix goes with TokenArgv.
type EntryAuth struct {
	URLArgv Argv `yaml:"url_argv" json:"url_argv,omitempty"`
	// URLField is a dotted JSON field of url_argv's output holding the URL;
	// without it the first URL on the entry's port is taken.
	URLField  string `yaml:"url_field" json:"url_field,omitempty"`
	TokenArgv Argv   `yaml:"token_argv" json:"token_argv,omitempty"`
	URLSuffix string `yaml:"url_suffix" json:"url_suffix,omitempty"`
	Hint      string `yaml:"hint" json:"hint,omitempty"`
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
	// Thresholds override the checkup defaults (docs/AGENT_KIT.md 8.4).
	Thresholds *Thresholds `yaml:"thresholds" json:"thresholds,omitempty"`
	// Remedies are the harness's own ways to handle each finding, keyed by
	// finding code; shown to the user, never executed (docs/AGENT_KIT.md 8.6).
	Remedies map[string][]string `yaml:"remedies" json:"remedies,omitempty"`
}

// Thresholds are checkup limits; zero means the default.
type Thresholds struct {
	LongSessionDays   int   `yaml:"long_session_days" json:"long_session_days,omitempty"`
	LongSessionTokens int64 `yaml:"long_session_tokens" json:"long_session_tokens,omitempty"`
	CostGrowthMin     int64 `yaml:"cost_growth_min_tokens" json:"cost_growth_min_tokens,omitempty"`
	PileupBytes       int64 `yaml:"session_pileup_bytes" json:"session_pileup_bytes,omitempty"`
	ColdDays          int   `yaml:"cold_days" json:"cold_days,omitempty"`
}

// SessionsSpec locates session logs: one JSONL file per session, or rows
// of a SQLite table.
type SessionsSpec struct {
	Root   string `yaml:"root" json:"root"`
	Glob   string `yaml:"glob" json:"glob"`
	Format string `yaml:"format" json:"format"`
	// JSONL: dotted paths of token counts to sum, and the event time field.
	UsageFields []string `yaml:"usage_fields" json:"usage_fields,omitempty"`
	TimeField   string   `yaml:"time_field" json:"time_field,omitempty"`
	// SQLite: where the events are.
	SQLite *SessionTable `yaml:"sqlite" json:"sqlite,omitempty"`
}

// SessionTable describes session events stored as table rows.
type SessionTable struct {
	Table         string `yaml:"table" json:"table"`
	SessionColumn string `yaml:"session_column" json:"session_column"`
	TimeColumn    string `yaml:"time_column" json:"time_column"`
	TimeUnit      string `yaml:"time_unit" json:"time_unit"` // s or ms
	TokensColumn  string `yaml:"tokens_column" json:"tokens_column,omitempty"`
	// TokensJSON reads the token count from a JSON column (json_extract).
	TokensJSON   *JSONField `yaml:"tokens_json" json:"tokens_json,omitempty"`
	SizeColumn   string     `yaml:"size_column" json:"size_column,omitempty"`
	BytesColumns []string   `yaml:"bytes_columns" json:"bytes_columns,omitempty"`
	// Usage holds per-session token totals kept in another table; when set
	// it replaces the per-event tokens column and the byte estimate.
	Usage *UsageTable `yaml:"usage" json:"usage,omitempty"`
}

// JSONField is a dotted path inside a JSON text column.
type JSONField struct {
	Column string `yaml:"column" json:"column"`
	Path   string `yaml:"path" json:"path"`
}

// UsageTable is a table of token totals per session.
type UsageTable struct {
	Table         string   `yaml:"table" json:"table"`
	SessionColumn string   `yaml:"session_column" json:"session_column"`
	TokensColumns []string `yaml:"tokens_columns" json:"tokens_columns"`
	TimeColumn    string   `yaml:"time_column" json:"time_column,omitempty"` // when the usage was last updated, for daily totals
	TimeUnit      string   `yaml:"time_unit" json:"time_unit,omitempty"`
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
	// MCPAddArgv is the harness's own command for adding an MCP server,
	// shown to the user (never run by Valise).
	MCPAddArgv Argv `yaml:"mcp_add_argv" json:"mcp_add_argv,omitempty"`
	// MCPConfigSnippet is a configuration fragment to paste when the
	// harness has no command for it (shown only; ${PORTER}, ${INSTANCE}).
	MCPConfigSnippet string `yaml:"mcp_config_snippet" json:"mcp_config_snippet,omitempty"`
	// MCPRemoveArgv is the harness's own command for removing the server,
	// shown by kit uninstall (never run by Valise).
	MCPRemoveArgv Argv `yaml:"mcp_remove_argv" json:"mcp_remove_argv,omitempty"`
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

// CapabilityStatus is the verification status of a capability. Profiles
// without capabilities fall back to their status: verified means tested,
// draft means untested (docs/PROFILES.md 1.2).
func (p *Profile) CapabilityStatus(name string) string {
	if c, ok := p.Capabilities[name]; ok {
		return c.Status
	}
	if p.Status == "verified" {
		return "tested"
	}
	return "untested"
}
