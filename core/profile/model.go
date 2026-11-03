// Package profile defines the Harness Profile data model (valise-profile/1),
// its strict YAML loader and profilelint. See docs/PROFILES.md.
package profile

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

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
	// NativeHints are shown before the native CLI opens.
	NativeHints []string `yaml:"native_hints" json:"native_hints,omitempty"`
	// Actions are managed actions (docs/INSTALL.md section 5).
	Actions []Action `yaml:"actions" json:"actions,omitempty"`
	// Terms are the harness's own words, shown instead of Valise's.
	Terms *Terms `yaml:"terms" json:"terms,omitempty"`
	// ApprovalCheck reads whether the harness asks before risky commands
	// (docs/PROFILES.md 1, 审批状态检查).
	ApprovalCheck *ApprovalCheck `yaml:"approval_check" json:"approval_check,omitempty"`
	// Identity reads the name the harness gives its agent (UI.md 9.5 "名字").
	Identity *Identity `yaml:"identity" json:"identity,omitempty"`
	// Capabilities record what was verified, per capability
	// (docs/PROFILES.md 1.2).
	Capabilities map[string]Capability `yaml:"capabilities" json:"capabilities,omitempty"`
}

// Display is how the instance card looks in the UI (docs/PROFILES.md).
type Display struct {
	Color string `yaml:"color" json:"color,omitempty"`
	// Icon is character art taken from the harness's own TUI.
	Icon string `yaml:"icon" json:"icon,omitempty"`
	// IconCompact is the optional small icon (docs/UI.md 8.1).
	IconCompact string `yaml:"icon_compact" json:"icon_compact,omitempty"`
	IconSource  string `yaml:"icon_source" json:"icon_source,omitempty"`
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
	// HomeEnv names the harness's own home variable: when set on the
	// server, it stands for ${HOME} in the candidates and named globs.
	HomeEnv string `yaml:"home_env" json:"home_env,omitempty"`
	// Named are further instances of one installation (docs/PROFILES.md
	// 2, 多实例): each directory a glob matches that holds a marker is an
	// instance named by what the * matched.
	Named []Named `yaml:"named" json:"named,omitempty"`
}

// Named is a glob whose last segment holds exactly one *.
type Named struct {
	Glob string `yaml:"glob" json:"glob"`
	// Env selects the instance for the harness's own commands: values
	// may use ${NAME} (what the * matched) and ${STATE_DIR}.
	Env map[string]string `yaml:"env" json:"env,omitempty"`
	// Service "shared": the instance runs under the default instance's
	// service (one gateway for all); "" means its service is unknown.
	Service string `yaml:"service" json:"service,omitempty"`
}

// ForAgent is the effective profile of one agent of an installation
// (docs/ARCHITECTURE.md 4.1): a named agent shares the installation (no
// body or provenance of its own) and has no verified entries; its service
// is kept only when named instances share the default's.
func (p Profile) ForAgent(name string) Profile {
	if name == "" {
		return p
	}
	if !p.NamedSharesService() {
		p.Service = nil
	}
	p.Entries, p.Body, p.Provenance = nil, nil, nil
	return p
}

// AgentTerm is what the harness calls one agent, "" when the profile does
// not say (docs/PROFILES.md 1.3).
func (p *Profile) AgentTerm() string {
	if p.Terms == nil {
		return ""
	}
	return p.Terms.Agent
}

// NamedSharesService reports whether p's named instances run under the
// default instance's service.
func (p *Profile) NamedSharesService() bool {
	if p.Detect == nil || len(p.Detect.Named) == 0 {
		return false
	}
	for _, n := range p.Detect.Named {
		if n.Service != "shared" {
			return false
		}
	}
	return true
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
	// Roots are node_modules directories (globs, ${HOME} / ${STATE_DIR})
	// a private installer keeps the package in, behind a launcher script;
	// checked before the global root of the npm on PATH.
	Roots []string `yaml:"roots" json:"roots,omitempty"`
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
	ID      string `yaml:"id" json:"id"`
	Summary string `yaml:"summary" json:"summary"`
	// Where, Takes and Notes make the plan's short summary (docs/UI.md
	// 9.6): where it installs, about how long it takes, and what the user
	// should know before (e.g. the harness's default approval setting).
	Where    string        `yaml:"where" json:"where,omitempty"`
	Takes    string        `yaml:"takes" json:"takes,omitempty"`
	Notes    []string      `yaml:"notes" json:"notes,omitempty"`
	Requires []Require     `yaml:"requires" json:"requires,omitempty"`
	Steps    []InstallStep `yaml:"steps" json:"steps"`
}

// Require is a program the plan needs, with an optional minimum version and
// how to install it (run only in advanced mode, docs/INSTALL.md 4.1).
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
	StepPathAdd   = "path_add"
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
	// path_add: a directory under ${HOME} put on the login shell's PATH.
	Dir string `yaml:"dir" json:"dir,omitempty"`
	// Note explains the step to the user.
	Note string `yaml:"note" json:"note,omitempty"`
	// Optional steps run only when the user opts in; they need an ID and
	// SkipEffect (what happens without them).
	Optional   bool   `yaml:"optional" json:"optional,omitempty"`
	ID         string `yaml:"id" json:"id,omitempty"`
	SkipEffect string `yaml:"skip_effect" json:"skip_effect,omitempty"`
}

// Action risk levels and output kinds (docs/INSTALL.md section 5).
const (
	RiskRead   = "read"
	RiskManage = "manage"
	RiskConfig = "config"
	RiskDanger = "danger"

	OutputText   = "text"
	OutputNone   = "none"
	OutputSecret = "secret"
)

// Action is one managed action: a fixed command with a risk level.
type Action struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Explain string `yaml:"explain" json:"explain"`
	// Undo says whether and how the action can be undone.
	Undo   string `yaml:"undo" json:"undo,omitempty"`
	Risk   string `yaml:"risk" json:"risk"`
	Argv   Argv   `yaml:"argv" json:"argv"`
	Output string `yaml:"output" json:"output"`
	// Secret picks the secret out of the output; nil takes the first line.
	Secret *SecretPick `yaml:"secret" json:"secret,omitempty"`
	// Terminal actions run in their own interactive terminal; their output
	// never passes through Valise.
	Terminal bool `yaml:"terminal" json:"terminal,omitempty"`
}

// SecretPick extracts a secret from JSON output: the string at JSONField
// and, when URLParam is set, that parameter of the URL's query or fragment.
type SecretPick struct {
	JSONField string `yaml:"json_field" json:"json_field,omitempty"`
	URLParam  string `yaml:"url_param" json:"url_param,omitempty"`
}

// NeedsAdmin reports whether running a needs more than the daily key.
func (a Action) NeedsAdmin() bool {
	return a.AccessTier() != "daily"
}

// AccessTier is the access level running a needs (docs/ARCHITECTURE.md
// 2.2; the values of core/rpc's Tier): read actions without secret output
// run on the daily key, manage actions and secret reads need the owner
// proof, config and danger actions and anything in a terminal need
// developer mode.
func (a Action) AccessTier() string {
	switch {
	case a.Terminal || a.Risk == RiskConfig || a.Risk == RiskDanger:
		return "admin"
	case a.Risk == RiskManage || a.Output == OutputSecret:
		return "owner"
	}
	return "daily"
}

// FindAction returns the action with id, or nil.
func (p *Profile) FindAction(id string) *Action {
	for i := range p.Actions {
		if p.Actions[i].ID == id {
			return &p.Actions[i]
		}
	}
	return nil
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

// ApprovalCheck runs a read-only command and matches fields of its JSON
// output by equality: all of BypassWhen means bypass, any of AskWhen means
// the harness asks; anything else is unknown. With Each, every element of
// that array is checked and the worst result wins.
type ApprovalCheck struct {
	Argv       Argv                `yaml:"argv" json:"argv"`
	Each       string              `yaml:"each" json:"each,omitempty"`
	BypassWhen map[string]string   `yaml:"bypass_when" json:"bypass_when"`
	AskWhen    []map[string]string `yaml:"ask_when" json:"ask_when,omitempty"`
	// DefaultWhen marks a bypass that is the harness's factory setting:
	// every bypass item matches one of these groups.
	DefaultWhen []map[string]string `yaml:"default_when" json:"default_when,omitempty"`
	// Remedy are the steps the user takes to turn approval on, shown as
	// text; Valise never runs them (docs/UI.md 9.5).
	Remedy []string `yaml:"remedy" json:"remedy,omitempty"`
	Verify string   `yaml:"verify" json:"verify,omitempty"`
}

// Identity reads agent names from a read-only command's JSON output: Each
// is the list ("" when the output is the list), Pick the entry that is this
// instance's agent (equality), Name the name field. A state directory may
// hold several of the harness's agents: all their names are listed.
type Identity struct {
	Argv   Argv              `yaml:"argv" json:"argv"`
	Each   string            `yaml:"each" json:"each,omitempty"`
	Pick   map[string]string `yaml:"pick" json:"pick,omitempty"`
	Name   string            `yaml:"name" json:"name"`
	Verify string            `yaml:"verify" json:"verify,omitempty"`
}

// EvalIdentity returns the picked agent's name and every listed name.
func EvalIdentity(c *Identity, out []byte) (name string, all []string) {
	var doc any
	if json.Unmarshal(out, &doc) != nil {
		return "", nil
	}
	list, ok := doc.([]any)
	if c.Each != "" {
		list, ok = field(doc, c.Each).([]any)
	}
	if !ok {
		return "", nil
	}
	for _, it := range list {
		n, ok := field(it, c.Name).(string)
		if !ok || strings.TrimSpace(n) == "" {
			continue
		}
		all = append(all, n)
		if name == "" && matches(it, c.Pick) {
			name = n
		}
	}
	return name, all
}

// Approval modes read by ApprovalCheck.
const (
	ApprovalAsk     = "ask"
	ApprovalBypass  = "bypass"
	ApprovalUnknown = "unknown"
)

// EvalApproval matches a check against the command's JSON output and
// returns the mode with the matched values as a short detail.
func EvalApproval(c *ApprovalCheck, out []byte) (mode, detail string) {
	var doc any
	if json.Unmarshal(out, &doc) != nil {
		return ApprovalUnknown, "输出不是 JSON"
	}
	items := []any{doc}
	if c.Each != "" {
		arr, ok := field(doc, c.Each).([]any)
		if !ok || len(arr) == 0 {
			return ApprovalUnknown, c.Each + " 不是非空数组"
		}
		items = arr
	}
	mode = ApprovalAsk
	for _, it := range items {
		m, d := evalOne(c, it)
		switch {
		case m == ApprovalBypass:
			return m, d
		case m == ApprovalUnknown:
			mode, detail = m, d
		case mode == ApprovalAsk:
			detail = d
		}
	}
	return mode, detail
}

// ApprovalDefault reports whether a bypass is the harness's factory setting
// (every bypass item matches a default_when group).
func ApprovalDefault(c *ApprovalCheck, out []byte) bool {
	var doc any
	if len(c.DefaultWhen) == 0 || json.Unmarshal(out, &doc) != nil {
		return false
	}
	items := []any{doc}
	if c.Each != "" {
		arr, ok := field(doc, c.Each).([]any)
		if !ok {
			return false
		}
		items = arr
	}
	bypass := false
	for _, it := range items {
		if m, _ := evalOne(c, it); m != ApprovalBypass {
			continue
		}
		bypass = true
		if !slices.ContainsFunc(c.DefaultWhen, func(w map[string]string) bool { return matches(it, w) }) {
			return false
		}
	}
	return bypass
}

func matches(it any, want map[string]string) bool {
	for k, v := range want {
		if fmt.Sprint(field(it, k)) != v {
			return false
		}
	}
	return true
}

func evalOne(c *ApprovalCheck, it any) (string, string) {
	match := func(want map[string]string) (bool, string) {
		keys := make([]string, 0, len(want))
		for k := range want {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			got := fmt.Sprint(field(it, k))
			parts = append(parts, k+"="+got)
			if got != want[k] {
				return false, ""
			}
		}
		return true, strings.Join(parts, " ")
	}
	if ok, d := match(c.BypassWhen); ok {
		return ApprovalBypass, d
	}
	for _, w := range c.AskWhen {
		if ok, d := match(w); ok {
			return ApprovalAsk, d
		}
	}
	return ApprovalUnknown, "没有匹配的规则"
}

// field follows a dotted path through JSON objects.
func field(v any, path string) any {
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// Terms are the harness's own words for Valise's concepts.
type Terms struct {
	// Agent is what the harness calls one agent (Hermes: profile).
	Agent string `yaml:"agent" json:"agent,omitempty"`
}
