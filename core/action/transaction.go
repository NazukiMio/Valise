package action

import (
	"regexp"
	"time"

	"valise/core/profile"
)

const (
	MethodPlan             = "action_plan"
	MethodStatus           = "action_status"
	MethodResume           = "action_resume"
	MethodCleanup          = "action_cleanup"
	OperationFormat        = "valise-action-operation/1"
	CapabilityTransactions = "action-transactions/1"
	ResumeContinue         = "continue"
	ResumeRestore          = "restore-services"
)

var operationID = regexp.MustCompile(`^act_[a-f0-9]{32}$`)

func ValidOperationID(id string) bool { return operationID.MatchString(id) }

type PlanParams struct {
	InstanceID string `json:"instance_id"`
	ActionID   string `json:"action_id"`
}
type OperationParams struct {
	OperationID string `json:"operation_id"`
}
type ResumeParams struct {
	OperationID string `json:"operation_id"`
	Choice      string `json:"choice"`
}
type CleanupParams struct {
	OperationID string `json:"operation_id"`
	Confirm     bool   `json:"confirm"`
}

// Identity identifies a directory across rename windows. Paths alone do not.
type Identity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}
type Service struct {
	Unit     string `json:"unit"`
	Scope    string `json:"scope"`
	Original string `json:"original"`
	Identity string `json:"identity"`
}
type StepResult struct {
	ID        string `json:"id"`
	Started   bool   `json:"started"`
	Completed bool   `json:"completed"`
	Exit      *int   `json:"exit,omitempty"`
}

// Plan contains only profile declarations and observed non-secret facts.
// No login environment, configuration contents or subprocess output belongs here.
type Plan struct {
	InstanceID      string                `json:"instance_id"`
	Affected        []string              `json:"affected"`
	ProfileDigest   string                `json:"profile_digest"`
	ActionDigest    string                `json:"action_digest"`
	Digest          string                `json:"digest"`
	Version         string                `json:"version"`
	BinDirs         []string              `json:"bin_dirs"`
	SelectionDigest string                `json:"selection_digest"`
	Definition      profile.Action        `json:"definition"`
	Approval        profile.ApprovalCheck `json:"approval"`
	Live            string                `json:"live"`
	LiveIdentity    Identity              `json:"live_identity"`
	Bytes           int64                 `json:"bytes"`
	Services        []Service             `json:"services"`
	Health          *profile.Health       `json:"health,omitempty"`
	Entries         []profile.Entry       `json:"entries,omitempty"`
	// Programs are the recorded program locations links may lead into;
	// they are part of the confirmation (INSTALL.md 5.2.1).
	Programs []string `json:"programs,omitempty"`
	// Bytes (above), Links and LinkBytes are readings of a state directory
	// a running instance keeps writing: shown at confirmation, outside the
	// confirmation digest, read again after the stop and frozen into the
	// operation record.
	Links     []Link `json:"links,omitempty"`
	LinkBytes int64  `json:"link_bytes,omitempty"`
}

// Link classes (INSTALL.md 5.2.1).
const (
	LinkInside = "inside" // absolute, resolving inside the state root
	LinkBody   = "body"   // resolving inside a recorded program location
)

// Link is one state symlink and how the transaction stages it. Rel is
// slash-separated under the state root; Text is the original link text;
// Target is the fully resolved target. For a body link, Digest is the
// target tree digest taken at planning and checked after the steps.
type Link struct {
	Rel    string `json:"rel"`
	Text   string `json:"text"`
	Target string `json:"target"`
	Class  string `json:"class"`
	Dir    bool   `json:"dir"`
	Digest string `json:"digest,omitempty"`
}

// Operation is the independent persistent transaction record.
type Operation struct {
	Format          string          `json:"format"`
	ID              string          `json:"id"`
	Plan            Plan            `json:"plan"`
	BackupID        string          `json:"backup_id,omitempty"`
	Stage           string          `json:"stage"`
	Previous        string          `json:"previous"`
	StageIdentity   Identity        `json:"stage_identity"`
	Phase           string          `json:"phase"`
	Steps           []StepResult    `json:"steps"`
	Stopped         map[string]bool `json:"stopped"`
	Restored        map[string]bool `json:"restored"`
	StopIntent      string          `json:"stop_intent,omitempty"`
	RestoreIntent   string          `json:"restore_intent,omitempty"`
	Published       bool            `json:"published"`
	StageVerified   bool            `json:"stage_verified"`
	StageTreeDigest string          `json:"stage_tree_digest"`
	// WriteLinks are the link texts the steps left in declared write areas
	// and the checks accepted (INSTALL.md 5.2.1), by slash path.
	WriteLinks      map[string]string `json:"write_links,omitempty"`
	LiveTreeDigest  string            `json:"live_tree_digest"`
	Approval        string            `json:"approval"`
	Services        string            `json:"services"`
	Health          string            `json:"health"`
	Failure         string            `json:"failure,omitempty"`
	RecoveryFailure string            `json:"recovery_failure,omitempty"`
	Created         time.Time         `json:"created"`
	Updated         time.Time         `json:"updated"`
	Done            bool              `json:"done"`
	CleanupStarted  bool              `json:"cleanup_started,omitempty"`
	Cleaned         bool              `json:"cleaned"`
}

type PendingAction struct {
	OperationID string `json:"operation_id"`
	InstanceID  string `json:"instance_id"`
	Phase       string `json:"phase"`
	Published   bool   `json:"published"`
}
