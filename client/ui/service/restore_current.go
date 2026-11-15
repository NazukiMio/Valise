package service

import (
	"context"
	"maps"
	"path/filepath"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
)

// The desktop's current-instance restore targets the registered directories,
// including after a move. It never revives into a path from the old snapshot
// or from an input retained while switching modes.
func (s *Service) currentRestoreRoots(ctx context.Context, o *ReviveOptions, instance string) error {
	if instance == "" {
		return restoreError("source_invalid", "备份记录没有实例信息，请重新读取这个时间点")
	}
	var result enroll.InstanceGetResult
	if err := s.call(ctx, o.Target, enroll.MethodInstanceGet, enroll.InstanceGetParams{InstanceID: instance}, &result); err != nil {
		return err
	}
	if result.Record == nil || result.Record.Superseded != nil {
		return restoreError("not_enrolled", "这里没有可恢复的当前 Agent，请先检查接入状态")
	}
	if result.Record.Roots[profile.StateRoot] == "" {
		return restoreError("root_invalid", "当前 Agent 的保存位置不完整，请重新接入")
	}
	for _, path := range result.Record.Roots {
		if !filepath.IsAbs(path) {
			return restoreError("root_invalid", "当前 Agent 的保存位置不完整，请重新接入")
		}
	}
	o.Roots = maps.Clone(result.Record.Roots)
	return nil
}

func currentRestorePlan(p *RevivePlan, instance string, roots map[string]string) error {
	if p.Plan.Mode == revive.ModeRollback && p.Plan.InstanceID != instance {
		return restoreError("source_conflict", "备份的实例信息与文件记录不一致，请重新选择时间点")
	}
	if p.Plan.Mode == revive.ModeRollback {
		for _, root := range p.Plan.Roots {
			if roots[root.ID] == "" || filepath.Clean(roots[root.ID]) != filepath.Clean(root.Path) {
				return restoreError("root_invalid", "这个备份中的保存位置尚未登记，请先检查 Agent 的接入信息")
			}
		}
	}
	return nil
}
