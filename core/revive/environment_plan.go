package revive

import (
	"time"
	"valise/core/environment"
	"valise/core/identity"
)

type EnvironmentPlanParams struct {
	Agent          identity.AgentRef     `json:"agent"`
	SettingsDigest string                `json:"settings_digest"`
	Input          environment.TestInput `json:"input"`
}

type EnvironmentSpace struct {
	Roots      []string `json:"roots"`
	Programmes bool     `json:"programmes"`
	NeedBytes  uint64   `json:"need_bytes"`
	FreeBytes  uint64   `json:"free_bytes"`
}

// EnvironmentPlan reports read-only observations, never a preparation task,
// readiness proof, execution permission or human approval.
type EnvironmentPlan struct {
	Reuse           *EnvironmentReuse       `json:"reuse,omitempty"`
	ManagedSource   bool                    `json:"managed_source,omitempty"`
	Original        *environment.Ref        `json:"original,omitempty"`
	Candidate       *environment.Ref        `json:"candidate,omitempty"`
	Agent           identity.AgentRef       `json:"agent"`
	Snapshot        identity.SnapshotRef    `json:"snapshot"`
	CreatedAt       time.Time               `json:"created_at"`
	Tier            string                  `json:"tier"`
	Mode            environment.Mode        `json:"mode"`
	InputDigest     string                  `json:"input_digest"`
	Digest          string                  `json:"digest"`
	ProfileUntested bool                    `json:"profile_untested"`
	NativeRunning   bool                    `json:"native_running"`
	NativeUnit      string                  `json:"native_unit"`
	EnableOnBoot    bool                    `json:"enable_on_boot"`
	ProgramMatches  bool                    `json:"program_matches"`
	ProgramVersion  string                  `json:"program_version"`
	ProgramFiles    int                     `json:"program_files"`
	ProgramBytes    int64                   `json:"program_bytes"`
	Files           int                     `json:"files"`
	Bytes           int64                   `json:"bytes"`
	LogicalRoots    int                     `json:"logical_roots"`
	PhysicalRoots   int                     `json:"physical_roots"`
	Baseline        *environment.DataOrigin `json:"baseline,omitempty"`
	Space           []EnvironmentSpace      `json:"space"`
}

type EnvironmentReuse struct {
	Target environment.Ref              `json:"target"`
	Items  []environment.OldCleanupItem `json:"items"`
}
