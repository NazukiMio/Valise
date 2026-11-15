package revive

import (
	"valise/core/revive"
	"valise/core/valise"
)

func rollbackEvidence(j *revive.Journal) *revive.RollbackEvidence {
	if j.Rollback == nil {
		j.Rollback = &revive.RollbackEvidence{Directories: "unknown", Program: "not_needed", Service: "not_started", Health: "not_checked"}
	}
	return j.Rollback
}
func (r *Runner) rollbackFailed(j *revive.Journal, code, reason string, cause error) (*revive.SwapResult, error) {
	evidence := rollbackEvidence(j)
	evidence.Problem = code
	if err := r.Store.Save(j); err != nil {
		return nil, err
	}
	userErr := valise.New(code, reason, "检查原操作结果，处理失败的项目后继续退回；不要重新发起恢复")
	if cause != nil {
		userErr = userErr.WithDetail(cause.Error())
	}
	return &revive.SwapResult{OpID: j.Plan.OpID, State: j.State, Rollback: evidence}, userErr
}
