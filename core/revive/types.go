// Package revive defines the swap journal, its state machine and the RPC
// messages of reviving (docs/ARCHITECTURE.md section 8).
package revive

import (
	"fmt"
	"regexp"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
)

// Modes.
const (
	ModeRollback = "rollback"
	ModeMigrate  = "migrate"
	ModeClone    = "clone"
)

// Journal states (docs/ARCHITECTURE.md section 8.2).
const (
	StatePlanned      = "PLANNED"
	StateStopped      = "STOPPED"
	StateLiveMoved    = "LIVE_MOVED"
	StageMoved        = "STAGE_MOVED"
	StateBodySwitched = "BODY_SWITCHED"
	StateStarted      = "STARTED"
	StateHealthy      = "HEALTHY"
	StateRollingBack  = "ROLLING_BACK"
	StateRolledBack   = "ROLLED_BACK"
	StateRestored     = "RESTORED_NOT_STARTED" // instances without a service
)

// Final reports whether a state ends the operation.
func Final(s string) bool { return s == StateHealthy || s == StateRolledBack || s == StateRestored }

// RPC method names.
const (
	MethodInspect     = "revive_inspect"
	MethodStageBegin  = "stage_begin"
	MethodStageVerify = "stage_verify"
	MethodSwap        = "swap"
	MethodResume      = "revive_resume"
	MethodJournal     = "journal_get"
	MethodPrevList    = "prev_list"
	MethodPrevDrop    = "prev_drop"
	MethodOldList     = "old_list"
	MethodOldCleanup  = "old_cleanup"
	MethodService     = "service_control"
	MethodRecipientRm = "recipient_remove"
)

// Fault injection (drills and tests only), passed to porter as the
// VALISE_FAULT environment variable: "health" or "crash-at=<STATE>".
const FaultEnv = "VALISE_FAULT"

var opRe = regexp.MustCompile(`^op_[0-9A-HJKMNP-TV-Z]{26}$`)

// ValidOpID reports whether id is an operation id.
func ValidOpID(id string) bool { return opRe.MatchString(id) }

// RootPlan maps one snapshot root to the target.
type RootPlan struct {
	ID string `json:"id"`
	// Path is the target path of this root.
	Path string `json:"path"`
	// SwapRoot is the root this one is nested in ("" when it is swapped
	// itself); Rel is its path inside SwapRoot.
	SwapRoot string `json:"swap_root,omitempty"`
	Rel      string `json:"rel,omitempty"`
}

// InspectParams asks the target about a planned revive.
type InspectParams struct {
	Profile    profile.Profile   `json:"profile"`
	InstanceID string            `json:"instance_id"`
	Roots      []RootPlan        `json:"roots"`
	Provenance enroll.Provenance `json:"provenance"`
	PlainBytes int64             `json:"plain_bytes"`
}

// RootFacts describes one target root.
type RootFacts struct {
	Identity  string `json:"identity,omitempty"`
	ID        string `json:"id"`
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Symlinked bool   `json:"symlinked"`
	SameFS    bool   `json:"same_fs"`
	FreeBytes uint64 `json:"free_bytes"`
	UsedBytes int64  `json:"used_bytes"`
}

// InspectResult is what the target reports for planning.
type InspectResult struct {
	Home        string                `json:"home"`
	OS          string                `json:"os"`
	Arch        string                `json:"arch"`
	StateDir    string                `json:"state_dir"` // resolved by detect candidates on the target
	BinaryFound bool                  `json:"binary_found,omitempty"`
	Roots       []RootFacts           `json:"roots"`
	Tools       map[string]bool       `json:"tools"`
	Provenance  enroll.Provenance     `json:"provenance"` // body currently installed on the target
	Service     []enroll.ServiceState `json:"service"`
	Listening   []enroll.EntryState   `json:"entries"`
	Record      *enroll.Record        `json:"record,omitempty"`
	PendingOps  []string              `json:"pending_ops"`
	Busy        []Process             `json:"busy,omitempty"`
	// Overlapping lists other enrolled instances whose roots equal or lie
	// inside the planned roots (they would be superseded by a migrate or
	// clone).
	Overlapping []string `json:"overlapping,omitempty"`
	// Inside lists enrolled instances one of whose roots holds a planned
	// root, outside the profile's named-instance locations: the plan is
	// refused (docs/ARCHITECTURE.md 8.2).
	Inside        []string `json:"inside,omitempty"`
	RegistryCheck string   `json:"registry_integrity,omitempty"`
	// Nested lists other agents inside the target state dir (named
	// instances, other enrolled instances). A swap would carry them off,
	// so it is refused while there are any.
	Nested []string `json:"nested,omitempty"`
	// NestedShared: every nested agent is a named instance of this profile
	// running under the same service, so stopping that service stops them
	// and a swap may carry their directories over (Plan.Keep).
	NestedShared bool `json:"nested_shared,omitempty"`
	// NestedRel are Nested relative to the (resolved) state dir.
	NestedRel []string `json:"nested_rel,omitempty"`
}

// BodyAction is how the body is handled.
const (
	BodyKeep       = "keep"        // same source and version already present
	BodyMoveDirs   = "move_dirs"   // body dirs inside the state dir carried across the swap
	BodyNPMInstall = "npm_install" // install the recorded exact version in the swap window
)

// BodyDirsCapability: the porter also carries the target's own body
// directories (Dirs) into the new live directory for keep and npm_install,
// skipping those the target does not have (docs/ARCHITECTURE.md 8.2.1).
const BodyDirsCapability = "revive-body-dirs/1"

// BodyPlan says what happens to the body.
type BodyPlan struct {
	Action    string   `json:"action"`
	Dirs      []string `json:"dirs,omitempty"` // swap-root-relative body dirs carried across the swap
	Package   string   `json:"package,omitempty"`
	Version   string   `json:"version,omitempty"`
	Registry  string   `json:"registry,omitempty"`
	Integrity string   `json:"integrity,omitempty"`
}

// Plan is the confirmed plan the target executes.
type Plan struct {
	TestOnly   bool               `json:"test_only,omitempty"`
	OpID       string             `json:"op_id"`
	Mode       string             `json:"mode"`
	InstanceID string             `json:"instance_id"`
	SnapshotID string             `json:"snapshot_id"`
	Tier       string             `json:"tier"`
	Profile    profile.Profile    `json:"profile"`
	Overrides  []profile.Override `json:"overrides,omitempty"`
	Roots      []RootPlan         `json:"roots"`
	// Carry copies live content outside the tier into the stage (rollback).
	Carry bool     `json:"carry"`
	Body  BodyPlan `json:"body"`
	// Keep are other agents' directories inside a root ("<root>/<rel>"):
	// moved into the new live directory in the swap window like body
	// directories, never copied or replaced (InspectResult.NestedShared).
	Keep []string `json:"keep,omitempty"`
	// Unit is the service unit to stop/start ("" for foreground harnesses).
	Unit string `json:"unit,omitempty"`
	// InstallService runs profile.service.install_argv after the body.
	InstallService bool `json:"install_service"`
	// Record is written on the target when the swap succeeds (migrate, clone).
	Record *enroll.Record `json:"record,omitempty"`
	// Supersedes are instances marked superseded when the swap succeeds.
	Supersedes []string `json:"supersedes,omitempty"`
	// Identity is optional provenance supported by identity/1 peers only.
	Identity *identity.RecoveryContext `json:"identity,omitempty"`
}

// BodyMove records a body directory carried across the swap.
type BodyMove struct {
	Root  string `json:"root"`
	Rel   string `json:"rel"`
	Moved bool   `json:"moved"`
	// Keep marks another agent's directory (Plan.Keep), not the body.
	Keep bool `json:"keep,omitempty"`
}

// JournalRoot are the paths of one swapped root.
type JournalRoot struct {
	ID          string `json:"id"`
	Live        string `json:"live"`
	Stage       string `json:"stage"`
	Prev        string `json:"prev"`
	Failed      string `json:"failed"`
	LiveExisted bool   `json:"live_existed"`
}

// Transition is one state change.
type Transition struct {
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

// Journal is ~/.local/state/valise/journal/<op_id>.json.
type Journal struct {
	Rollback         *RollbackEvidence `json:"rollback,omitempty"`
	Plan             Plan              `json:"plan"`
	State            string            `json:"state"`
	History          []Transition      `json:"history"`
	Roots            []JournalRoot     `json:"roots"`
	BodyMoves        []BodyMove        `json:"body_moves,omitempty"`
	NPMOldVersion    string            `json:"npm_old_version,omitempty"`
	NPMInstalled     bool              `json:"npm_installed,omitempty"`
	ServiceWasActive bool              `json:"service_was_active"`
	ServiceInstalled bool              `json:"service_installed,omitempty"`
	Manifest         []ManifestEntry   `json:"stage_manifest,omitempty"`
	ManifestSaved    bool              `json:"stage_manifest_saved,omitempty"`
	StageVerified    bool              `json:"stage_verified"`
	// RecoveryDisposition is recorded only by the owner swap execution path.
	RecoveryDisposition string          `json:"recovery_disposition,omitempty"`
	RecoveryTarget      *identity.Agent `json:"recovery_target,omitempty"`
	// Intruders are live paths recreated by other processes during the
	// swap, moved aside and kept.
	Intruders []string `json:"intruders,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// ManifestEntry is one staged file the target must verify.
type ManifestEntry struct {
	Path     string `json:"path"` // "<swap root>/<rel>"
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Category string `json:"category"`
	SQLite   bool   `json:"sqlite,omitempty"`
}

// StageBeginParams creates the journal and stage directories.
type StageBeginParams struct {
	Plan Plan `json:"plan"`
}

// StageVerifyParams lists what the stage must contain.
type StageVerifyParams struct {
	OpID     string          `json:"op_id"`
	Manifest []ManifestEntry `json:"manifest"`
}

// StageVerifyResult reports the stage check.
type StageVerifyResult struct {
	Files    int      `json:"files"`
	Bytes    int64    `json:"bytes"`
	Problems []string `json:"problems"`
}

// OpParams names an operation.
type OpParams struct {
	OpID string `json:"op_id"`
	// SourceDisposition reports a trusted client's actual execution result;
	// it is not a user approval, grant, or source-identity inference.
	SourceDisposition string `json:"source_disposition,omitempty"`
}

// ResumeParams resolves an unfinished operation.
type ResumeParams struct {
	OpID   string `json:"op_id"`
	Action string `json:"action"` // "continue" or "back"
}

// SwapResult is the end of a swap.
type SwapResult struct {
	Rollback  *RollbackEvidence `json:"rollback,omitempty"`
	OpID      string            `json:"op_id"`
	State     string            `json:"state"`
	Prev      []string          `json:"prev"`
	Logs      string            `json:"logs,omitempty"` // service logs on rollback; shown, never audited
	Message   string            `json:"message"`
	Carried   int               `json:"carried_files"`
	Intruders []string          `json:"intruders,omitempty"`
	HealthLog []string          `json:"health_log,omitempty"`
}

// PrevEntry is one kept pre-swap state.
type PrevEntry struct {
	OpID    string    `json:"op_id"`
	Path    string    `json:"path"`
	Created time.Time `json:"created"`
	Bytes   int64     `json:"bytes"`
}

// PrevListResult lists kept states.
type PrevListResult struct {
	Entries []PrevEntry `json:"entries"`
}

// Process is another process using a root (blocks a swap).
type Process struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Path    string `json:"path"`
}

// ServiceParams starts or stops a user unit (used to stop the source of a
// migration and to restart it after a failed one).
type ServiceParams struct {
	Unit   string `json:"unit"`
	Action string `json:"action"` // "stop" or "start"
}

// RecipientRemoveParams removes a recipient.
type RecipientRemoveParams struct {
	Recipient string `json:"recipient"`
}

// NewOpID returns op_<ULID>.
func NewOpID(now time.Time) string { return vault.NewID("op", now) }

// Describe is a short human description of a state.
func Describe(s string) string {
	switch s {
	case StatePlanned:
		return "已计划，尚未改动活动目录"
	case StateStopped:
		return "服务已停止，活动目录尚未改动"
	case StateLiveMoved:
		return "活动目录已移走，新状态尚未就位（此时实例不可用）"
	case StageMoved:
		return "新状态已就位，身体尚未切换"
	case StateBodySwitched:
		return "身体已切换，服务尚未启动"
	case StateStarted:
		return "服务已启动，健康检查未完成"
	case StateRollingBack:
		return "正在退回"
	}
	return fmt.Sprintf("状态 %s", s)
}

// OldEntry is one superseded instance and what cleaning it up removes.
type OldEntry struct {
	InstanceID string              `json:"instance_id"`
	Superseded enroll.Supersession `json:"superseded"`
	Roots      map[string]string   `json:"roots"`
	// Leftovers are the directories the superseding operation kept.
	Leftovers []PrevEntry `json:"leftovers"`
}

// OldListResult lists superseded instances.
type OldListResult struct {
	Entries []OldEntry `json:"entries"`
}

// OldCleanupParams names the superseded instance to remove.
type OldCleanupParams struct {
	InstanceID string `json:"instance_id"`
}
