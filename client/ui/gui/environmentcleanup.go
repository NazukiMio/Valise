package gui

import (
	"context"
	"time"

	"valise/core/environment"
	"valise/ui/service"
)

func (a *App) reviewSettingsOldCleanup(host string, ps *protState) {
	m := ps.restoreMode
	if m == nil || m.view == nil || m.j.busy || !m.cleanupSupported || !m.view.Managed || m.view.Mode != environment.Direct {
		return
	}
	if a.restore != nil && (a.restore.j.busy || oldCleanupPending(a.restore.task) || a.restore.task.State == "needs_recovery" || a.restore.task.State == "reconciling") {
		a.navigate("/restore")
		return
	}
	agent := m.view.Agent
	var plan *service.PreparedOldCleanup
	a.start(&m.j, 4*time.Minute, func(ctx context.Context) (err error) {
		plan, err = a.svc.PrepareCurrentOldCleanup(ctx, host, agent)
		return err
	}, func(err error) {
		if err == nil {
			r := reopenRestoreTask(plan.Task())
			available := true
			r.oldCleanupPlan, r.step, r.parallelRead, r.parallelSwitchCapability = plan, "parallel_old_cleanup", true, &available
			a.restore = r
			a.navigate("/restore")
		}
	})
}
