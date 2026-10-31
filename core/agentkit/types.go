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

// Proposal is an agent's classification proposal awaiting the user.
type Proposal struct {
	ID       string           `json:"id"`
	Rule     profile.Override `json:"rule"`
	Reason   string           `json:"reason"`
	Demotion bool             `json:"demotion"` // moves files out of the soul tier
	// Unclassified counts files now unclassified (sealed with the secrets
	// layer) that the rule would move into the soul tier.
	Unclassified int `json:"unclassified,omitempty"`
	Created  time.Time        `json:"created"`
}

// ProposalsParams names the instance.
type ProposalsParams struct {
	InstanceID string `json:"instance_id"`
}

// ProposalsResult lists pending proposals.
type ProposalsResult struct {
	Proposals []Proposal `json:"proposals"`
}

// DecideParams accepts or rejects proposals. Demotions are accepted only
// when listed in AcceptDemotions (confirmed separately by the user).
type DecideParams struct {
	InstanceID      string   `json:"instance_id"`
	Accept          []string `json:"accept,omitempty"`
	AcceptDemotions []string `json:"accept_demotions,omitempty"`
	Reject          []string `json:"reject,omitempty"`
}

// DecideResult reports what changed.
type DecideResult struct {
	Accepted  []Proposal `json:"accepted"`
	Rejected  []string   `json:"rejected"`
	Remaining int        `json:"remaining"`
}

// SkillFile is one file of an agent-kit skill.
type SkillFile struct {
	Skill   string `json:"skill"` // directory name, e.g. valise-upkeep
	Name    string `json:"name"`  // file name, e.g. SKILL.md
	Content []byte `json:"content"`
}

// KitInstallParams installs skills into the instance's skills_dir.
type KitInstallParams struct {
	InstanceID string      `json:"instance_id"`
	Files      []SkillFile `json:"files"`
	DryRun     bool        `json:"dry_run"`
}

// KitInstallResult lists what was (or would be) written.
type KitInstallResult struct {
	Dir     string   `json:"dir"`
	Written []string `json:"written"`
	Backups []string `json:"backups,omitempty"`
	Same    []string `json:"unchanged,omitempty"`
	Porter  string   `json:"porter"` // absolute porter path, for MCP config
}

// IsDemotion reports whether a category takes files out of the soul tier.
func IsDemotion(category string) bool {
	return category == "ignore" || category == "cache" || category == "body"
}
