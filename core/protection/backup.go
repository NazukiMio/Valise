package protection

import (
	"strings"
	"time"

	"valise/core/identity"
	"valise/core/valise"
)

const (
	MethodBackup          = "protection_backup"
	MethodRunStatus       = "backup_run_status"
	MethodSnapshotBinding = "backup_snapshot_binding"
	MethodRunCancel       = "backup_run_cancel"
	BackupCapability      = "backup-runs/1"
	// BackupCapabilityV2 adds detached execution in a transient user unit,
	// recorded stages and backup_run_cancel. A client without it in hello
	// keeps the synchronous backup-runs/1 behaviour.
	BackupCapabilityV2 = "backup-runs/2"
	BackupRunSchema    = "valise-backup-run/1"
)

type BackupParams struct {
	AgentRef         identity.AgentRef `json:"agent_ref"`
	ExpectedRevision Revision          `json:"expected_revision"`
	RunID            string            `json:"run_id"`
	Reason           string            `json:"reason"`
	// Detach (backup-runs/2) lets the porter hand the accepted run to a
	// transient user unit and return at once. Only a client that follows
	// the run by status sends it; without it the call stays synchronous.
	Detach bool `json:"detach,omitempty"`
}
type SnapshotBindingParams struct {
	SnapshotRef identity.SnapshotRef `json:"snapshot_ref"`
}
type SnapshotBinding struct {
	AgentRef *identity.AgentRef `json:"agent_ref,omitempty"`
	State    string             `json:"state"`
}

// BackupCancelParams asks the fixed executor to stop at its next safe point.
type BackupCancelParams struct {
	AgentRef identity.AgentRef `json:"agent_ref"`
	RunID    string            `json:"run_id"`
}

// Execution modes of a run. Detached runs in a transient systemd user unit
// that outlives the RPC connection; attached runs inside the connection or
// the timer process, so closing the connection interrupts it.
const (
	ExecutionAttached = "attached"
	ExecutionDetached = "detached"
)

// Stages are the executor's last recorded phase, not progress percentages.
const (
	StageQueued  = "queued"
	StageSealing = "sealing"
	StageCopying = "copying"
)

type BackupStatusParams struct {
	AgentRef identity.AgentRef `json:"agent_ref"`
	RunID    string            `json:"run_id"`
}
type BackupComponent struct {
	Kind        string       `json:"kind"`
	TargetRef   *LocationRef `json:"target_ref,omitempty"`
	State       string       `json:"state"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
	ErrorCode   string       `json:"error_code,omitempty"`
}
type BackupRun struct {
	Schema        string                `json:"schema"`
	RunID         string                `json:"run_id"`
	AgentRef      identity.AgentRef     `json:"agent_ref"`
	Request       BackupParams          `json:"request"`
	RequestDigest string                `json:"request_digest"`
	Plan          ServerPlan            `json:"plan"`
	State         string                `json:"state"`
	Snapshot      *identity.SnapshotRef `json:"snapshot_ref,omitempty"`
	Components    []BackupComponent     `json:"components"`
	AcceptedAt    time.Time             `json:"accepted_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	ErrorCode     string                `json:"error_code,omitempty"`
	// Execution, Stage and StageTarget (backup-runs/2) are written by the
	// executor; they are empty in records of older porters.
	Execution   string       `json:"execution,omitempty"`
	Stage       string       `json:"stage,omitempty"`
	StageTarget *LocationRef `json:"stage_target,omitempty"`
	// CancelRequestedAt is projected from the separate cancel request; the
	// run stays nonterminal until the executor confirms it stopped.
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
}

// Normalize accepts only fixed reason classes; free-form native output cannot
// enter durable history through this field. A new key denotes a new intent.
func (p BackupParams) Normalize() (BackupParams, error) {
	p.Reason = strings.TrimSpace(p.Reason)
	if p.Reason == "" {
		p.Reason = "manual"
	}
	if err := ValidateAgent(p.AgentRef); err != nil {
		return p, err
	}
	if !ValidID(p.RunID) || p.ExpectedRevision == 0 {
		return p, valise.New("request_invalid", "备份运行标识或计划版本无效", "使用随机 128 位十六进制运行标识并重新读取保护计划")
	}
	if p.Detach && p.Reason != "manual" {
		return p, valise.New("request_invalid", "只有手动备份可以交给一次性单元执行", "定时备份在自己的进程内执行")
	}
	switch p.Reason {
	case "manual", "timer":
	default:
		return p, valise.New("request_invalid", "备份原因分类无效", "选择 manual 或 timer；受管方 MCP 使用原有限频备份入口")
	}
	return p, nil
}
