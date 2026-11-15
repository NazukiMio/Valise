package gui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/identity"
	"valise/core/protection"
	"valise/ui/service"
)

func TestBackupClockSurvivesIdleAndPageChange(t *testing.T) {
	a, tt := world(t)
	ref := identity.AgentRef{InstanceID: "xiaoman"}
	a.agents.value.Items[0].AgentRef = &ref
	at := tt.Now().Add(-65 * time.Second)
	run := &protection.BackupRun{AcceptedAt: at, UpdatedAt: at, State: "running", Components: []protection.BackupComponent{
		{Kind: "server_backup", State: "running", StartedAt: &at},
	}}
	br := &backupRun{j: job{busy: true}, task: &service.BackupTask{State: "running", Run: run}}
	ps := &protState{ref: &ref, view: &protection.View{}, backup: br}
	a.prots["home/xiaoman"] = ps
	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)
	tt.Frame()
	hasAll(t, tt.Texts(), "已用 1 分 6 秒", "进行中 · 1 分 6 秒")
	// No input or manual Frame: the view itself asks for a clock update.
	tt.Advance(2100 * time.Millisecond)
	hasAll(t, tt.Texts(), "已用 1 分 8 秒")
	shot(t, tt, "backup-running")
	a.agentTab = tabOverview
	tt.Frame()
	tt.Advance(30 * time.Second)
	a.agentTab = tabManage
	tt.Frame()
	hasAll(t, tt.Texts(), "已用 1 分 38 秒")
	if ps.backup != br {
		t.Fatal("page change lost the task")
	}
	end := tt.Now()
	br.j.busy, br.task.State = false, "partial"
	run.State, run.UpdatedAt = "partial", end
	run.Components[0].State, run.Components[0].CompletedAt = "succeeded", &end
	run.Components = append(run.Components, protection.BackupComponent{Kind: "server_copy", TargetRef: &protection.LocationRef{ID: "nas"},
		State: "failed", FailureCause: "connect_timeout"})
	tt.Frame()
	hasAll(t, tt.Texts(), "用了 1 分 38 秒", "已完成 · 1 分 38 秒", "复制到 nas", "连接这个位置超时", "经这台电脑中转")
	shot(t, tt, "backup-partial")
	tt.Advance(time.Minute)
	hasAll(t, tt.Texts(), "用了 1 分 38 秒")
}

func TestUnknownBackupHasQueryAndNoLiveClock(t *testing.T) {
	a, tt := world(t)
	ref := identity.AgentRef{InstanceID: "xiaoman", Generation: "current"}
	a.agents.value.Items[0].AgentRef = &ref
	at, observed := tt.Now().Add(-5*time.Minute), tt.Now().Add(-2*time.Minute)
	br := &backupRun{task: &service.BackupTask{State: "reconciling", Run: &protection.BackupRun{
		AcceptedAt: at, UpdatedAt: observed, Components: []protection.BackupComponent{{Kind: "server_backup", State: "running", StartedAt: &at}},
	}}}
	ps := &protState{ref: &ref, backup: br, view: &protection.View{ServerPlan: &protection.ServerPlan{}}}
	a.prots["home/xiaoman"] = ps
	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "最后核实到已用 3 分 0 秒", "结果需要核实", "核实结果", "不会重新备份")
	shot(t, tt, "backup-unknown")
	if tt.HasText("现在备份") || tt.HasText("进行中") {
		t.Fatalf("unknown result looked live or could be repeated: %q", tt.Texts())
	}
	tt.Advance(time.Minute)
	hasAll(t, tt.Texts(), "最后核实到已用 3 分 0 秒")
	click(t, tt, "开始另一次备份…")
	hasAll(t, tt.Texts(), "可能多出一个时间点", "我知道，开始另一次备份")
	click(t, tt, "取消")
	if ps.backup != br || a.keyFlow != nil {
		t.Fatal("dismissing the new-intent confirmation started a backup")
	}
}

func TestRestoreBackupCardsKeepsEveryUnresolvedTaskAndExactGeneration(t *testing.T) {
	a, _ := world(t)
	ref := identity.AgentRef{InstanceID: "demo", Generation: "current"}
	old := ref
	old.Generation = "old"
	ps := &protState{ref: &ref}
	tasks := []service.BackupTask{
		{Host: "box", AgentRef: ref, State: "partial", TaskRef: service.ProtectionTaskRef{TaskID: "latest"}},
		{Host: "box", AgentRef: old, State: "reconciling", TaskRef: service.ProtectionTaskRef{TaskID: "old-generation"}},
		{Host: "other", AgentRef: ref, State: "running", TaskRef: service.ProtectionTaskRef{TaskID: "other-host"}},
		{Host: "box", AgentRef: ref, State: "reconciling", TaskRef: service.ProtectionTaskRef{TaskID: "unknown"}},
		{Host: "box", AgentRef: ref, State: "running", TaskRef: service.ProtectionTaskRef{TaskID: "live"}},
	}
	a.restoreBackupCards(ps, "box", tasks)
	a.restoreBackupCards(ps, "box", tasks)
	if ps.backup.task.TaskRef.TaskID != "latest" || len(ps.otherBackups) != 2 || !ps.backupBlocked() {
		t.Fatalf("lost or duplicated pending tasks: %+v", ps)
	}
	for _, br := range ps.otherBackups {
		br.task.State = "succeeded"
	}
	if ps.backupBlocked() {
		t.Fatal("known final results still block the next backup")
	}
}

func TestOldBackupDoesNotInventComponentDuration(t *testing.T) {
	a, tt := world(t)
	row := &agentRow{Name: "Demo"}
	end := tt.Now()
	br := &backupRun{task: &service.BackupTask{State: "partial", Run: &protection.BackupRun{
		AcceptedAt: end.Add(-5 * time.Minute), UpdatedAt: end, Components: []protection.BackupComponent{
			{Kind: "server_copy", State: "failed", TargetRef: &protection.LocationRef{ID: "nas"}, CompletedAt: &end},
		},
	}}}
	// Old records have no started_at; even a known completion cannot give
	// a per-component elapsed time.
	tt = ui.NewTester(func(c *ui.Context) { a.backupCard(c, row, br) }, 1000, 600)
	hasAll(t, tt.Texts(), "用了 5 分 0 秒", "没有完成")
	if strings.Contains(strings.Join(tt.Texts(), " "), "没有完成 ·") {
		t.Fatal("invented duration for an old component")
	}
}
