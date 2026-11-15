package gui

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

type restoreTestFlow struct {
	server, instance string
	confirm          bool
	selected         *journey.TimelinePoint
	sources          []service.RestoreSource
	source           int
	depth            string
	materialReview   *service.TestMaterialReview
	reviewReply      chan bool
	request          *service.SelectedRestoreTest
	j                job
	progress         service.Progress
	result           *service.RestoreTest
	exit             func()
}

func (a *App) showRestoreTest(t *restoreTestFlow) {
	a.openAgent(t.server, t.instance)
	a.agentTab = tabHealth
}

func (a *App) openRestoreTest(row *agentRow) {
	if t := a.restoreTest; t != nil && (t.j.busy || t.result != nil && (t.result.State == "running" || t.result.State == "needs_cleanup")) {
		a.showRestoreTest(t)
		return
	}
	if a.restore != nil && a.restore.j.busy {
		a.navigate("/restore")
		return
	}
	a.restoreTest = &restoreTestFlow{server: row.Server, instance: row.Instance, confirm: true}
	a.defaultTestPoint(a.restoreTest)
}

func (a *App) startRestoreTest(t *restoreTestFlow, cleanup bool) {
	if t.j.busy {
		return
	}
	if !cleanup && t.request == nil && (t.selected == nil || len(t.sources) == 0 || t.source < 0 || t.source >= len(t.sources)) {
		return
	}
	var selection service.SelectedRestoreTest
	if !cleanup {
		if t.request != nil {
			selection = *t.request
		} else {
			selection = service.SelectedRestoreTest{Depth: t.depth, Target: t.server, Instance: t.instance, Snapshot: t.selected.SnapshotRef, Source: t.sources[t.source]}
		}
	}
	t.confirm = false
	ctx, cancel := context.WithCancel(context.Background())
	t.j = job{busy: true, doingAt: time.Now(), cancel: cancel}
	t.progress = service.Progress{}
	if cleanup {
		t.progress.Stage = "test_cleanup"
	}
	var mu sync.Mutex
	var last time.Time
	stage := ""
	ctx = service.WithProgress(ctx, func(p service.Progress) {
		mu.Lock()
		defer mu.Unlock()
		if p.Stage == stage && time.Since(last) < 150*time.Millisecond && p.Done != p.Total {
			return
		}
		stage, last = p.Stage, time.Now()
		a.update(func() { t.progress = p })
	})
	if !cleanup && selection.Depth == "isolated" {
		selection.ReviewMaterials = func(review service.TestMaterialReview) bool { return a.reviewTestMaterials(ctx, t, review) }
	}
	a.run(func() {
		defer cancel()
		var result *service.RestoreTest
		var err error
		if cleanup {
			result, err = a.svc.CleanupRestoreTest(ctx, t.server, t.instance, t.result.OpID)
		} else {
			result, err = a.runRestoreTest(ctx, selection)
		}
		if errors.Is(err, context.Canceled) {
			key := "restoretest.stopped_hint"
			if result != nil && (result.State == "running" || result.State == "needs_cleanup") {
				key = "restoretest.cleanup_retry"
			}
			err = valise.New("cancelled", T("restoretest.stopped"), T(key))
		}
		a.update(func() {
			t.j.busy, t.j.err, t.j.cancel = false, err, nil
			t.result = result
			a.readHealth(t.server, t.instance)
			if t.exit != nil {
				exit := t.exit
				t.exit = nil
				exit()
			}
		})
	})
}

func (a *App) cancelRestoreTest(t *restoreTestFlow) {
	if t.j.cancel != nil {
		t.j.cancel()
	}
	for _, p := range a.asks {
		p.value = ""
		select {
		case p.reply <- passReply{err: errStopped}:
		default:
		}
	}
	a.asks = nil
}

func (a *App) restoreTestReport(c *ui.Context, t *restoreTestFlow) {
	if t.confirm {
		a.restoreTestSelection(c, t)
		return
	}
	if !t.j.busy && t.result == nil && t.j.err == nil {
		return
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			title := "restoretest.title"
			if t.depth == "isolated" || t.result != nil && t.result.Depth == "isolated" {
				title = "restoretest.environment.title"
			}
			ui.Text(c, T(title)).Bold().Grow(1)
			pending := t.result != nil && (t.result.State == "running" || t.result.State == "needs_cleanup")
			if st := a.health[t.server+"/"+t.instance]; !t.j.busy && !pending && st != nil && st.canTest {
				ui.Button(c, T("restoretest.open")).OnClick(func() { a.openRestoreTest(&agentRow{Server: t.server, Instance: t.instance}) })
			}
		})
		if t.materialReview != nil {
			a.restoreTestMaterialReview(c, t)
			return
		}
		if t.j.busy {
			c.After(time.Second)
			ui.Row(c).Gap(8).Children(func() { ui.Spinner(c); ui.Text(c, T("restoretest.progress."+testStage(t.progress.Stage))).Wrap() })
			muted(ui.Text(c, T("restore.elapsed", "duration", backupDuration(c.Now().Sub(t.j.doingAt))))).Wrap()
			if p := t.progress; p.Total > 0 {
				ui.Progress(c, float64(p.Done)/float64(p.Total))
			}
			ui.Button(c, T("common.stop")).OnClick(func() { a.cancelRestoreTest(t) })
			return
		}
		if t.j.err != nil {
			errorBox(c, t.j.err, a.fold("restore-test-error"))
		}
		if r := t.result; r != nil {
			state := r.State
			if state == "" {
				if r.OK {
					state = "passed"
				} else {
					state = "failed"
				}
			}
			stateKey := "restoretest.state." + state
			if r.Depth == "isolated" && state == "passed" {
				stateKey = "restoretest.environment.passed"
			}
			ui.Text(c, T(stateKey)).Bold().Wrap()
			if !r.Created.IsZero() {
				fact(c, T("restore.snapshot_time"), r.Created.Local().Format("2006-01-02 15:04:05"))
			}
			fact(c, T("point.scope"), tierText(r.Tier))
			if r.SourceLocation != nil {
				fact(c, T("restore.read_from"), journeyLocation(*r.SourceLocation))
			}
			fact(c, T("restoretest.file_scope"), T("restoretest.file_count", "n", r.Files, "size", size(r.Bytes)))
			local, server := T("restore.not_checked"), T("restore.not_checked")
			if r.LocalVerified || r.OK {
				local = T("restore.checked")
			}
			if r.ServerVerified || r.OK {
				server = T("restore.checked")
			}
			fact(c, T("restoretest.local_result"), local)
			fact(c, T("restoretest.server_result"), server)
			if r.Depth == "isolated" {
				a.environmentTestReport(c, r)
			} else {
				muted(ui.Text(c, T("restoretest.result_hint"))).Wrap()
			}
			if r.AgentRef == nil && r.State == "" {
				muted(ui.Text(c, T("restoretest.legacy"))).Wrap()
			}
			if r.Problem != "" {
				ui.Text(c, r.Problem).TextColor(palette.Warn).Wrap()
			}
			if r.State == "running" || r.State == "needs_cleanup" {
				ui.Button(c, T("restoretest.cleanup")).OnClick(func() { a.startRestoreTest(t, true) })
			}
		}
	})
}

func testStage(stage string) string {
	switch stage {
	case service.StagePull, service.StagePullMeta, service.StagePullPacks, service.StageOpen, service.StageInspect, service.StageStage, service.StageVerify, service.TestLocal, service.TestServer, "test_cleanup", "test_environment":
		return stage
	}
	return "reading"
}

func (a *App) restoreTestBanner(c *ui.Context) {
	if t := a.restoreTest; t != nil && (t.j.busy || t.result != nil && (t.result.State == "running" || t.result.State == "needs_cleanup")) {
		ui.Row(c).Padding(8, 16).Gap(10).Children(func() {
			ui.Text(c, T("restoretest.banner", "server", t.server, "instance", t.instance)).Grow(1).Wrap()
			ui.Button(c, T("restoretest.view")).OnClick(func() { a.showRestoreTest(t) })
		})
	}
}

func (a *App) actualRestoreEvidence(c *ui.Context, row *agentRow) {
	var latest *service.RestoreTask
	var current *identity.AgentRef
	for _, agent := range a.agents.value.Items {
		if agent.ConnectionID == row.Server && agent.Instance.InstanceID == row.Instance {
			current = agent.AgentRef
		}
	}
	legacy := false
	for i, task := range a.restoreTasks {
		if task.Target != row.Server || task.Instance != row.Instance || task.State != "succeeded" || task.SwapState != revive.StateHealthy && task.SwapState != revive.StateRestored {
			continue
		}
		if current == nil || task.TargetAgent == nil {
			legacy = true
			continue
		}
		if *current != *task.TargetAgent {
			continue
		}
		if latest == nil || task.Updated.After(latest.Updated) {
			latest = &a.restoreTasks[i]
		}
	}
	card(ui.Column(c)).Gap(6).Children(func() {
		ui.Text(c, T("restoretest.actual_title")).Bold()
		if latest == nil {
			if legacy {
				muted(ui.Text(c, T("restoretest.actual_legacy"))).Wrap()
				ui.Button(c, T("restore.history")).OnClick(func() { a.readRestoreTasks(); a.navigate("/restore/tasks") })
			} else {
				muted(ui.Text(c, T("restoretest.actual_none"))).Wrap()
			}
			return
		}
		key := "restoretest.actual_files"
		if latest.SwapState == revive.StateHealthy {
			key = "restoretest.actual_healthy"
		}
		ui.Text(c, T(key, "time", latest.Updated.Local().Format("2006-01-02 15:04:05"))).Wrap()
		ui.Button(c, T("restore.history")).OnClick(func() { a.readRestoreTasks(); a.navigate("/restore/tasks") })
	})
}
