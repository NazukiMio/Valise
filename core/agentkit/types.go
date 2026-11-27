// Package agentkit holds the types shared by the MCP server, the client and
// porter's agent-kit RPCs (docs/AGENT_KIT.md).
package agentkit

import (
	"time"

	"valise/core/profile"
)

// RPC methods (client <-> porter).
const (
	MethodCheckup         = "checkup"
	MethodProposalsList   = "proposals_list"
	MethodProposalsDecide = "proposals_decide"
	MethodKitInstall      = "kit_install"
	MethodKitStatus       = "kit_status"
	MethodKitToolsCheck   = "kit_tools_check"
	MethodKitToolBegin    = "kit_tool_begin"
	MethodKitToolRemove   = "kit_tool_remove"
	MethodKitUninstall    = "kit_uninstall"
)

// Finding ids (docs/AGENT_KIT.md section 5).
const (
	FindingLongSession       = "long_session"
	FindingCostGrowth        = "cost_growth"
	FindingMemoryBloat       = "memory_bloat"
	FindingSessionPileup     = "session_pileup"
	FindingNoMemoryMechanism = "no_memory_mechanism"
)

// Default checkup thresholds (docs/AGENT_KIT.md 8.4).
const (
	DefaultLongSessionDays   = 14
	DefaultLongSessionTokens = 500_000
	DefaultCostGrowthMin     = 50_000
	DefaultPileupBytes       = 100 << 20
	DefaultColdDays          = 90
)

// SessionStat describes one session.
type SessionStat struct {
	ID        string    `json:"id"`
	Bytes     int64     `json:"bytes"`
	Tokens    int64     `json:"tokens"`
	Estimated bool      `json:"tokens_estimated"`
	Events    int64     `json:"events"`
	First     time.Time `json:"first,omitzero"`
	Last      time.Time `json:"last,omitzero"`
	Days      float64   `json:"days"`
}

// MemoryStat is one memory file against its threshold.
type MemoryStat struct {
	Root      string `json:"root"`
	Path      string `json:"path"`
	Lines     int    `json:"lines"`
	WarnLines int    `json:"warn_lines"`
}

// DayTokens is the token volume of one UTC day.
type DayTokens struct {
	Day    string `json:"day"` // YYYY-MM-DD
	Tokens int64  `json:"tokens"`
}

// Finding is a checkup result id with the numbers behind it.
type Finding struct {
	ID     string `json:"id"`
	Detail string `json:"detail"`
}

// CheckupResult is computed by code only (docs/ARCHITECTURE.md section 9).
type CheckupResult struct {
	InstanceID       string        `json:"instance_id"`
	At               time.Time     `json:"at"`
	SessionsDeclared bool          `json:"sessions_declared"`
	Sessions         []SessionStat `json:"sessions"` // largest first, at most 20
	SessionCount     int           `json:"session_count"`
	TotalBytes       int64         `json:"total_bytes"`
	ColdBytes        int64         `json:"cold_bytes"`
	Current          *SessionStat  `json:"current,omitempty"` // most recently active
	Daily            []DayTokens   `json:"daily_tokens"`      // last 7 UTC days, oldest first
	Memory           []MemoryStat  `json:"memory_files"`
	MemoryMechanism  bool          `json:"memory_mechanism"`
	Findings         []Finding     `json:"findings"`
	Notes            []string      `json:"notes,omitempty"`
	NativeOptionsDoc string        `json:"native_options_doc,omitempty"`
}

// CheckupParams names the instance.
type CheckupParams struct {
	InstanceID string `json:"instance_id"`
}

// Proposal kinds (docs/AGENT_KIT.md section 3 & 8.2).
const (
	ProposalKindClassify    = "classify"
	ProposalKindSQLite      = "sqlite"
	ProposalKindExternalRef = "external_ref"
	ProposalKindToolDep     = "tool_dep"
	ProposalKindWebEntry    = "web_entry"

	ExternalRefDir     = "external_dir"
	ExternalRefSymlink = "symlink_target"
)

// WebEntrySpec declares a read-only loopback HTTP entry reported by an agent.
type WebEntrySpec struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Port        int    `json:"port"`
	Path        string `json:"path"`
	Hint        string `json:"hint,omitempty"`
}

// ExternalRefSpec identifies an out-of-root directory or symlink target.
type ExternalRefSpec struct {
	Path              string `json:"path"`
	RefKind           string `json:"ref_kind"` // external_dir | symlink_target
	SuggestedCategory string `json:"suggested_category,omitempty"`
	EstimatedFiles    int    `json:"estimated_files,omitempty"`
	EstimatedBytes    int64  `json:"estimated_bytes,omitempty"`
}

// ToolDepSpec identifies a companion CLI executable checked read-only during
// migration.
type ToolDepSpec struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose,omitempty"`
}

// Proposal is an agent's dossier proposal awaiting user confirmation.
type Proposal struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind,omitempty"` // "" or "classify" | "sqlite" | "external_ref" | "tool_dep" | "web_entry"
	Rule        profile.Override   `json:"rule,omitempty"`
	SQLite      *profile.SQLiteRef `json:"sqlite,omitempty"`
	ExternalRef *ExternalRefSpec   `json:"external_ref,omitempty"`
	ToolDep     *ToolDepSpec       `json:"tool_dep,omitempty"`
	WebEntry    *WebEntrySpec      `json:"web_entry,omitempty"`
	Reason      string             `json:"reason"`
	Demotion    bool               `json:"demotion"` // moves files out of the soul tier
	// Unclassified counts files now unclassified (sealed with the secrets
	// layer) that the rule would move into the soul tier.
	Unclassified int       `json:"unclassified,omitempty"`
	Created      time.Time `json:"created"`
}

// EffectiveKind returns the proposal kind, defaulting legacy empty values to classify.
func (p Proposal) EffectiveKind() string {
	if p.Kind == "" {
		return ProposalKindClassify
	}
	return p.Kind
}

// ProposalsParams names the instance.
type ProposalsParams struct {
	InstanceID string `json:"instance_id"`
}

// ProposalsResult lists pending proposals.
type ProposalsResult struct {
	Proposals []Proposal `json:"proposals"`
}

// ExternalRootPromotion promotes an external_ref proposal into a backed-up
// local_root on the instance dossier.
type ExternalRootPromotion struct {
	ProposalID string `json:"proposal_id"`
	RootID     string `json:"root_id,omitempty"`
	Category   string `json:"category"`
}

// DecideParams accepts or rejects proposals. Demotions are accepted only
// when listed in AcceptDemotions (confirmed separately by the user).
type DecideParams struct {
	InstanceID           string                  `json:"instance_id"`
	Accept               []string                `json:"accept,omitempty"`
	AcceptDemotions      []string                `json:"accept_demotions,omitempty"`
	Reject               []string                `json:"reject,omitempty"`
	PromoteExternalRoots []ExternalRootPromotion `json:"promote_external_roots,omitempty"`
}

// DecideResult reports what changed.
type DecideResult struct {
	Accepted  []Proposal `json:"accepted"`
	Rejected  []string   `json:"rejected"`
	Remaining int        `json:"remaining"`
}

// SkillFile is one file of an agent-kit skill.
type SkillFile struct {
	Skill   string `json:"skill"` // directory name, e.g. valise-onboard
	Name    string `json:"name"`  // file name, e.g. SKILL.md
	Content []byte `json:"content"`
}

// KitInstallParams installs skills into the instance's skills_dir.
type KitInstallParams struct {
	InstanceID string      `json:"instance_id"`
	Files      []SkillFile `json:"files"`
	DryRun     bool        `json:"dry_run"`
}

// KitInstallResult lists what was (or would be) written and any unmodified
// legacy skills removed on cleanup.
type KitInstallResult struct {
	Dir           string   `json:"dir"`
	Written       []string `json:"written"`
	Backups       []string `json:"backups,omitempty"`
	Same          []string `json:"unchanged,omitempty"`
	LegacyRemoved []string `json:"legacy_removed,omitempty"`
	LegacyKept    []string `json:"legacy_kept,omitempty"`
	Porter        string   `json:"porter"` // absolute porter path, for MCP config
}

// IsDemotion reports whether a category takes files out of the soul tier.
func IsDemotion(category string) bool {
	return category == "ignore" || category == "cache" || category == "body"
}

// Skill states in kit status.
const (
	SkillCurrent  = "current"  // same content as this client ships
	SkillModified = "modified" // present with other content
	SkillMissing  = "missing"
)

// KitStatusParams compares the installed skills with the shipped ones.
type KitStatusParams struct {
	InstanceID string      `json:"instance_id"`
	Files      []SkillFile `json:"files"`
}

// SkillState is one skill in the skills directory.
type SkillState struct {
	Skill   string `json:"skill"`
	Path    string `json:"path"`
	State   string `json:"state"`
	Backups int    `json:"backups"`

	ChangeKind      string     `json:"change_kind,omitempty"`
	InstalledSHA256 string     `json:"installed_sha256,omitempty"`
	DiskSHA256      string     `json:"disk_sha256,omitempty"`
	InstalledAt     *time.Time `json:"installed_at,omitempty"`
	DiskMTime       *time.Time `json:"disk_mtime,omitempty"`
	DiskContent     string     `json:"disk_content,omitempty"`    // <= 64 KiB UTF-8, only when modified
	ShippedContent  string     `json:"shipped_content,omitempty"` // filled by the client
}

// Four-way skill change kinds (SkillState.ChangeKind).
const (
	SkillChangeCurrent        = "current"
	SkillChangeOfficialUpdate = "official_update"
	SkillChangeLocalModified  = "local_modified"
	SkillChangeBothChanged    = "both_changed"
	SkillChangeMissing        = "missing"
)

// KitStatusResult describes what agent-kit left on the instance.
type KitStatusResult struct {
	Dir      string       `json:"dir,omitempty"` // empty when the profile has no skills_dir
	Skills   []SkillState `json:"skills"`
	LastMCP  *time.Time   `json:"last_mcp_call,omitempty"`
	MCPCalls int          `json:"mcp_calls"`
}

// Approval modes an agent declares (docs/AGENT_KIT.md section 3).
const (
	ApprovalAsk     = "ask"
	ApprovalBypass  = "bypass"
	ApprovalUnknown = "unknown"
)

// Declaration is what an agent says about its own safety: the security
// skills it has and whether risky commands need the user's approval.
type Declaration struct {
	SecuritySkills []SecuritySkill `json:"security_skills"`
	ApprovalMode   string          `json:"approval_mode"`
	At             time.Time       `json:"at"`
}

// SecuritySkill is one declared security skill.
type SecuritySkill struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
}

// KitUninstallParams removes installed skills. Only files equal to the
// shipped content (and Valise's own backups) go, unless Force is set.
type KitUninstallParams struct {
	InstanceID string      `json:"instance_id"`
	Files      []SkillFile `json:"files"`
	Force      bool        `json:"force"`
	DryRun     bool        `json:"dry_run"`
}

// KitUninstallResult lists removed (or, in a dry run, removable) paths and
// the modified files that were kept.
type KitUninstallResult struct {
	Removed []string `json:"removed"`
	Kept    []string `json:"kept,omitempty"`
}

// KitToolBeginParams starts writing an enhanced Kit tool's binary; the
// daily key then runs `valise-porter tool-put <name> <sha256>` with the
// returned grant (docs/AGENT_KIT.md 8.4.1).
type KitToolBeginParams struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// KitToolBeginResult carries the grant (daily sessions only).
type KitToolBeginResult struct {
	Grant string `json:"grant,omitempty"`
}

// KitToolRemoveParams removes a tool binary Valise installed.
type KitToolRemoveParams struct {
	Name string `json:"name"`
}

// KitToolsCheckParams names the programs to look for (names only; nothing
// is run).
type KitToolsCheckParams struct {
	Binaries []string `json:"binaries"`
}

// KitToolsCheckResult is what the server has for the Kit tools.
type KitToolsCheckResult struct {
	OSID   string            `json:"os_id"`
	Arch   string            `json:"arch"`
	Found  map[string]string `json:"found"` // binary → path
	Linger bool              `json:"linger"`
	// Ours lists the tool binaries Valise installed (name → SHA-256).
	Ours map[string]string `json:"ours"`
}
