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
	BackupCapability      = "backup-runs/1"
	BackupRunSchema       = "valise-backup-run/1"
)

type BackupParams struct {
	AgentRef         identity.AgentRef `json:"agent_ref"`
	ExpectedRevision Revision          `json:"expected_revision"`
	RunID            string            `json:"run_id"`
	Reason           string            `json:"reason"`
}
type SnapshotBindingParams struct {
	SnapshotRef identity.SnapshotRef `json:"snapshot_ref"`
}
type SnapshotBinding struct {
	AgentRef *identity.AgentRef `json:"agent_ref,omitempty"`
	State    string             `json:"state"`
}

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
	switch p.Reason {
	case "manual", "timer":
	default:
		return p, valise.New("request_invalid", "备份原因分类无效", "选择 manual 或 timer；受管方 MCP 使用原有限频备份入口")
	}
	return p, nil
}
