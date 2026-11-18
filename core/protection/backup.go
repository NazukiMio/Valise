package protection

import (
	"strings"
	"time"

	"valise/core/identity"
	"valise/core/valise"
	"valise/core/vault"
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
	BackupCapabilityV3 = "backup-runs/3"
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
	Detach    bool            `json:"detach,omitempty"`
	Selection BackupSelection `json:"selection,omitzero"`
}

// An empty selection uses the accepted plan. A nonempty tier applies only
// to this run; the accepted plan and its copies are never rewritten.
type BackupSelection struct {
	Tier       string `json:"tier"`
	SkipCopies bool   `json:"skip_copies,omitempty"`
	Note       string `json:"note,omitempty"`
}

func (s BackupSelection) Validate() error {
	if _, err := vault.NormalizeNote(s.Note); err != nil {
		return err
	}
	if s.Tier == "" && !s.SkipCopies {
		return nil
	}
	switch s.Tier {
	case "soul", "soul-secrets", "full", "raw":
		return nil
	}
	return valise.New("unsupported_scope", "本次备份范围不受支持", "选择现有备份档位")
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
	StartedAt   *time.Time   `json:"started_at,omitempty"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
	ErrorCode   string       `json:"error_code,omitempty"`
	// FailureCause (contracts 16.G1) classifies a failed copy: host_unknown,
	// connect_refused, connect_timeout, auth_denied, host_key_unverified,
	// not_writable, tool_missing or other.
	FailureCause string `json:"failure_cause,omitempty"`
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
	if p.Selection.Tier != "" {
		switch p.Selection.Tier {
		case "soul", "soul-secrets", "full", "raw":
		default:
			return p, valise.New("unsupported_scope", "本次备份范围不受支持", "选择现有备份档位")
		}
		if p.Reason != "manual" && strings.TrimSpace(p.Reason) != "" {
			return p, valise.New("request_invalid", "一次性范围只能由手动备份选择", "定时备份沿用保护计划")
		}
	} else if p.Selection.SkipCopies {
		return p, valise.New("request_invalid", "本次复制选择缺少备份范围", "重新准备一次性备份")
	}
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
	note, err := vault.NormalizeNote(p.Selection.Note)
	if err != nil {
		return p, err
	}
	if p.Reason != "manual" && note != "" {
		return p, valise.New("request_invalid", "定时备份不接受备注", "手动建立存档点时填写备注")
	}
	p.Selection.Note = note
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
