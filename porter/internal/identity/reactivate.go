package identity

import (
	"errors"
	"path/filepath"
	"time"

	"valise/core/enroll"
	model "valise/core/identity"
	"valise/core/valise"
)

// Reactivate issues a fresh active generation for an already-bound enrolled
// instance on this server. When mode is "switch_back" and SwitchFrom is set,
// it also commits a completed operation record and a reverse
// migration_completed relation so cross-server listings annotate the previous
// target as MigratedTo this server.
func Reactivate(home string, p enroll.ReactivateParams) (res *enroll.ReactivateResult, err error) {
	if err := enroll.ValidInstanceID(p.InstanceID); err != nil {
		return nil, valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	switch p.Mode {
	case enroll.ReactivateModeSwitchBack:
		if p.SwitchFrom == nil || p.SwitchFrom.ServerUID == "" || p.SwitchFrom.InstallID == "" ||
			p.SwitchFrom.InstanceID == "" || p.SwitchFrom.Generation == "" {
			return nil, valise.New("switch_from_required", "切回模式必须提供当前活跃目标的完整 AgentRef", "刷新 Agent 列表后重试")
		}
		if p.SourceDisposition == "" {
			p.SourceDisposition = model.StoppedVerified
		}
		if p.SourceDisposition != model.StoppedVerified {
			return nil, valise.New("source_disposition_invalid", "切回模式要求先核实新机服务已停止（stopped_verified）", "确认新机服务已停止后重试")
		}
	case enroll.ReactivateModeCoexist:
		// No reverse migration edge; SwitchFrom is ignored.
	default:
		return nil, valise.New("reactivate_mode_invalid", "不支持的恢复模式："+p.Mode, "只支持 switch_back 或 coexist")
	}

	err = WithLock(home, func() error {
		if e := checkCommit(home); e != nil {
			return e
		}
		s, e := server(home)
		if e != nil {
			return e
		}
		var prior model.Agent
		found, e := readJSON(agentPath(home, p.InstanceID), &prior)
		if e != nil {
			return e
		}
		if !found || s == nil || prior.Schema != model.Schema ||
			prior.Ref.ServerUID != s.ServerUID || prior.Ref.InstanceID != p.InstanceID ||
			prior.Ref.InstallID == "" || prior.Ref.Generation == "" || prior.CreatedAt.IsZero() {
			return valise.New("instance_not_bound", "该实例尚未绑定有效的 Valise 身份记录", "请先重新接入该实例后再试")
		}
		if p.Mode == enroll.ReactivateModeSwitchBack &&
			p.SwitchFrom.ServerUID == s.ServerUID && p.SwitchFrom.InstanceID == p.InstanceID {
			return valise.New("switch_from_same_instance", "不能从同一个实例切回自身", "选择迁移后的目标实例进行切回")
		}

		gen, e := model.NewID()
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		newAgent := model.Agent{
			Schema: model.Schema,
			Ref: model.AgentRef{
				ServerUID:  s.ServerUID,
				InstallID:  prior.Ref.InstallID,
				InstanceID: prior.Ref.InstanceID,
				Generation: gen,
			},
			Roots:         prior.Roots,
			ProfileDigest: prior.ProfileDigest,
			State:         "active",
			CreatedAt:     now,
			AcceptedAt:    now,
		}

		var rel *model.Relation
		if p.Mode == enroll.ReactivateModeSwitchBack && p.SwitchFrom != nil {
			opSuffix, e := model.NewID()
			if e != nil {
				return e
			}
			opID := "reactivate-" + opSuffix
			relID, e := model.NewID()
			if e != nil {
				return e
			}
			fromRef := *p.SwitchFrom
			rel = &model.Relation{
				Schema:            model.Schema,
				RelationID:        relID,
				Kind:              model.MigrationCompleted,
				SourceAgent:       &fromRef,
				TargetAgent:       newAgent.Ref,
				OperationRef:      opID,
				VerifiedAt:        now,
				SourceDisposition: model.StoppedVerified,
			}
			newAgent.RelationRefs = []string{relID}

			if e := privateDir(filepath.Join(Dir(home), "operations")); e != nil {
				return e
			}
			if e := privateDir(filepath.Join(Dir(home), "relations")); e != nil {
				return e
			}
			opCommit := &recoveryCommit{
				Schema: model.Schema,
				State:  "complete",
				Input: RecoveryInput{
					OperationRef: opID,
					Context: model.RecoveryContext{
						Mode:        enroll.ReactivateModeSwitchBack,
						SourceAgent: &fromRef,
					},
					SourceDisposition: model.StoppedVerified,
					InstallID:         newAgent.Ref.InstallID,
					InstanceID:        newAgent.Ref.InstanceID,
					Roots:             newAgent.Roots,
					ProfileDigest:     newAgent.ProfileDigest,
				},
				Server:   *s,
				Agent:    newAgent,
				Relation: rel,
			}
			if e := writeJSON(operationPath(home, opID), opCommit); e != nil {
				return e
			}
			if e := writeJSON(relationPath(home, relID), rel); e != nil {
				return e
			}
		}

		if e := writeJSON(agentPath(home, p.InstanceID), &newAgent); e != nil {
			return e
		}
		if newAgent.Ref.Generation == prior.Ref.Generation {
			return errors.New("generation did not advance")
		}
		res = &enroll.ReactivateResult{
			InstanceID: p.InstanceID,
			Mode:       p.Mode,
			Agent:      newAgent,
			Relation:   rel,
		}
		return nil
	})
	return res, err
}
