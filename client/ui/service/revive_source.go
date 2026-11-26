package service

import (
	"context"
	"time"

	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// UnverifiedSourceNotice is shown with every snapshot whose source
// generation has no backup_snapshot_binding evidence.
const UnverifiedSourceNotice = "来源无法核实"

// UnverifiedSource lists what the snapshot itself records when its source
// instance generation cannot be verified (contracts 16.D). The values come
// from the decrypted snapshot header and the vault it was read from; none of
// them is evidence of identity.
type UnverifiedSource struct {
	Notice string `json:"notice"`
	// InstanceID and Name are recorded in the snapshot (Name: a named
	// instance inside its installation, "" for the default one).
	InstanceID     string `json:"instance_id"`
	Name           string `json:"name,omitempty"`
	ProfileID      string `json:"profile_id"`
	ProfileVersion int    `json:"profile_version"`
	// SourceServer is the connection whose vault holds the snapshot, Period
	// its period there.
	SourceServer string    `json:"source_server"`
	Period       string    `json:"period"`
	CreatedAt    time.Time `json:"created_at"`
	Tier         string    `json:"tier"`
	// BackupFirst offers "先备份当前状态再恢复": the target has a current,
	// enrolled instance BackupInstance whose state this restore replaces.
	BackupFirst    bool   `json:"backup_first_available"`
	BackupInstance string `json:"backup_instance,omitempty"`
}

// SourceDecision is the person's answer to an unverified source. It only
// ever comes from an in-process callback (ReviveConfirm.UnverifiedSource),
// never from a request field.
type SourceDecision string

const (
	// SourceCancel stops before anything is changed.
	SourceCancel SourceDecision = ""
	// SourceProceed is "我清楚，继续".
	SourceProceed SourceDecision = "proceed"
	// SourceBackupFirst is "先备份当前状态再恢复": one explicit full backup of
	// the target's current instance must succeed before staging.
	SourceBackupFirst SourceDecision = "backup_first"
)

// ReviveConfirm are the human confirmations of one revive, asked in order.
type ReviveConfirm struct {
	// BackupCurrent is an explicit human choice for current-instance restore.
	BackupCurrent bool
	// CarryProtection is an explicit human choice for migration to carry over
	// the source instance's protection plan onto the migrated target.
	CarryProtection bool
	// RemapHomeSymlinks rewrites symlink targets under the source $HOME to
	// the target $HOME when the home differs; RewriteConfigHomePaths does so
	// for allowlisted config files (originals kept as .valise-home-bak);
	// CarryMCP re-runs the harness's MCP add command after a migration.
	RemapHomeSymlinks      bool
	RewriteConfigHomePaths bool
	CarryMCP               bool
	// UnverifiedSource is asked before anything is staged, only when the
	// plan's source is unverified. Nil refuses such a plan.
	UnverifiedSource func(UnverifiedSource) SourceDecision
	// Swap is the second confirmation, with the stage verification result.
	Swap func(*revive.StageVerifyResult) bool
}

// describeUnverifiedSource fills rp.UnverifiedSource unless a bound snapshot
// binding identified the source generation.
func describeUnverifiedSource(rp *RevivePlan, head *vault.Snapshot, period string, facts revive.InspectResult, instance string) {
	if rp.sourceAgent != nil {
		return
	}
	u := &UnverifiedSource{Notice: UnverifiedSourceNotice, InstanceID: head.InstanceID, Name: head.Name,
		ProfileID: head.Profile.ID, ProfileVersion: head.Profile.Version, SourceServer: rp.From, Period: period,
		CreatedAt: head.CreatedAt, Tier: head.Tier}
	// Only the same, still current instance on the target has a state this
	// restore replaces; a new copy has nothing to back up.
	if facts.Record != nil && facts.Record.InstanceID == instance && facts.Record.Superseded == nil {
		u.BackupFirst, u.BackupInstance = true, instance
	}
	rp.UnverifiedSource = u
}

// periodOf names the period a leaf holds, for display.
func periodOf(st *vaultrpc.StatusResult, leaf string) string {
	for _, l := range st.Leaves {
		if l.Leaf == leaf && l.Period != nil {
			return l.Period.Name()
		}
	}
	return leaf
}

// settleUnverifiedSource asks the person and, for SourceBackupFirst, seals
// the target's current instance at full before anything is staged.
func (s *Service) settleUnverifiedSource(ctx context.Context, rp *RevivePlan, ask func(UnverifiedSource) SourceDecision) error {
	u := rp.UnverifiedSource
	if u == nil {
		return nil
	}
	if ask == nil {
		return valise.New("source_unverified", "这个快照的来源实例无法核实，需要你亲自确认后才能恢复",
			"查看快照自带的实例、profile、来源服务器与时间后，选择“我清楚，继续”或“先备份当前状态再恢复”")
	}
	switch ask(*u) {
	case SourceProceed:
		return nil
	case SourceBackupFirst:
		if !u.BackupFirst {
			return valise.New("backup_first_unavailable", "目标上没有正在使用的同一实例，没有当前状态可以先备份",
				"确认快照信息后选择“我清楚，继续”，或取消")
		}
		return s.backupBeforeRestore(ctx, rp, u.BackupInstance)
	}
	return valise.New("cancelled", "已取消，没有做任何改动", "")
}

func (s *Service) backupBeforeRestore(ctx context.Context, rp *RevivePlan, instance string) error {
	rep, err := s.Seal(ctx, rp.Target, vaultrpc.SealParams{InstanceID: instance, Tier: vault.TierFull,
		Reason: "恢复时间点 " + rp.Plan.SnapshotID + " 之前，先备份当前状态（操作 " + rp.Plan.OpID + "）"})
	if err != nil {
		e := valise.New("pre_restore_backup_failed", "恢复前的完整备份没有完成，恢复尚未开始",
			"检查服务器与备份库后重试；或重新查看计划，取消先备份的选择")
		if ue, ok := valise.As(err); ok {
			return e.WithDetail(ue.Code)
		}
		return e
	}
	rp.PreRestoreBackup = rep
	return ctx.Err()
}
