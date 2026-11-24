package gui

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

type restoreSwapReply struct {
	approve  bool
	deadline time.Time
}

type restoreFlow struct {
	point                                    journey.TimelinePoint
	sources                                  []service.RestoreSource
	source                                   int
	origin, target, mode, id, roots          string
	step                                     string
	plan                                     *service.PreparedRestore
	task                                     service.RestoreTask
	review                                   *revive.ReviewResult
	j                                        job
	progress                                 service.Progress
	credential, draft, snapshotProfile, swap bool
	allowVersionMismatch, skipService        bool
	envNeeded, envAutoSkipped                bool
	sourceChoice                             string
	backupCurrent                            bool
	verify                                   *revive.StageVerifyResult
	deadline                                 time.Time
	reply                                    chan restoreSwapReply
	stopConfirm                              bool
	newIntent                                bool
	recoverAction                            string
	exit                                     func()

	placement      *service.TargetPlacement
	placeLoading   bool
	placeErr       error
	placeKey       string
	placeMode      string
	customStateDir string
	extraRoots     map[string]string
	inlineInstall  *restoreInstallState
}

func (a *App) openRestore(p journey.TimelinePoint, origin string) {
	if a.restore != nil && (a.restore.j.busy || a.restore.task.State == "needs_recovery" || a.restore.task.State == "reconciling") {
		a.navigate("/restore")
		return
	}
	sources := a.svc.RestoreSources(p)
	if len(sources) == 0 {
		return
	}
	origin = a.resolveOriginHost(origin, sources[0])
	r := &restoreFlow{point: p, sources: sources, origin: origin, target: origin, mode: revive.ModeRollback, id: p.RecordedInstanceID + "-copy", step: "setup"}
	a.restore = r
	a.navigate("/restore")
}

func (a *App) restorePage(c *ui.Context) {
	r := a.restore
	if r == nil {
		pageHead(c, T("restore.title"), T("restore.choose_point"))
		return
	}
	backLink(c, T("nav.agents"), "/agents")
	pageHead(c, T("restore.title"), T("restore.step."+r.step))
	envSkipped := r.step != "setup" && r.step != "env" && !r.envNeeded
	restoreStepper(c, r.step, envSkipped)
	if !r.point.CreatedAt.IsZero() {
		subPanel(ui.Row(c)).Gap(spSection).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.clock).FontSize(fsTitle3).TextColor(palette.Brand)
			pointFact(c, T("restore.snapshot_time"), r.point.CreatedAt.Local().Format("2006-01-02 15:04"))
			pointFact(c, T("point.scope"), tierText(r.point.Scope.Tier))
		})
	}
	if r.j.err != nil {
		errorBox(c, r.j.err, a.fold("restore-error"))
	}
	switch r.step {
	case "setup":
		a.restoreSetup(c, r)
	case "env":
		a.restoreEnv(c, r)
	case "plan":
		a.restorePlan(c, r)
	case "prepare", "swap":
		a.restoreProgress(c, r)
	case "confirm":
		a.restoreConfirm(c, r)
	case "result", "reconcile":
		a.restoreResult(c, r)
	}
	if r.newIntent {
		a.restoreNewDialog(c, r)
	}
	if r.stopConfirm {
		a.restoreStopDialog(c, r)
	}
	if r.recoverAction != "" {
		a.restoreRecoveryDialog(c, r)
	}
}

func (a *App) prepareRestore(r *restoreFlow) {
	if r.j.busy {
		return
	}
	roots := map[string]string{}
	if r.mode == revive.ModeRollback {
		r.target = r.origin
	} else {
		r.roots = r.resolvedRoots()
		var err error
		roots, err = parseRestoreRoots(r.roots)
		if err != nil {
			r.j.err = err
			return
		}
	}
	if r.origin == "" || r.target == "" {
		r.j.err = valise.New("target_missing", T("restore.error.target"), T("restore.error.target_hint"))
		return
	}
	req := service.RestoreRequest{Options: service.ReviveOptions{Target: r.target, From: r.origin, Mode: r.mode, SnapshotID: r.point.SnapshotRef.SnapshotID, InstanceID: r.id, Roots: roots, AllowDraft: r.draft, AllowSnapshotProfile: r.snapshotProfile, AllowVersionMismatch: r.allowVersionMismatch, SkipService: r.skipService}, Snapshot: r.point.SnapshotRef, Source: r.sources[r.source]}
	if r.plan != nil {
		r.plan.Revive.Close()
		r.plan = nil
	}
	r.credential, r.swap = false, false
	r.j = job{busy: true, doing: T("restore.reading"), doingAt: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	r.j.cancel = cancel
	ctx = a.restoreContext(ctx, r)
	fn := a.prepareRestoreFn
	if fn == nil {
		fn = a.svc.PrepareRestore
	}
	a.run(func() {
		defer cancel()
		p, err := fn(ctx, req)
		a.update(func() {
			r.j.busy, r.j.err, r.j.cancel = false, err, nil
			if err == nil {
				r.plan = p
				needEnv := hasEnvBlocker(p.Revive)
				wasInEnv := r.step == "env"
				r.envNeeded = needEnv
				if needEnv {
					r.step = "env"
					r.envAutoSkipped = false
				} else {
					r.step = "plan"
					r.envAutoSkipped = wasInEnv
				}
			}
			a.restoreSafeExit(r)
		})
	})
}

func parseRestoreRoots(text string) (map[string]string, error) {
	roots := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, path, ok := strings.Cut(line, "=")
		if !ok || id == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(id, " /\\") || roots[id] != "" {
			return nil, valise.New("root_invalid", T("restore.error.roots"), T("restore.error.roots_hint"))
		}
		roots[id] = path
	}
	return roots, nil
}

func (a *App) runRestore(r *restoreFlow) {
	if r.j.busy || r.plan == nil {
		return
	}
	r.j = job{busy: true, doingAt: time.Now()}
	r.step = "prepare"
	ctx, cancel := context.WithCancel(context.Background())
	r.j.cancel = cancel
	ctx = a.restoreContext(ctx, r)
	decision := service.SourceDecision(r.sourceChoice)
	backupCurrent := r.mode == revive.ModeRollback && r.backupCurrent
	p := r.plan
	a.run(func() {
		defer cancel()
		t, err := a.svc.RunRestore(ctx, p, service.ReviveConfirm{
			BackupCurrent:    backupCurrent,
			UnverifiedSource: func(service.UnverifiedSource) service.SourceDecision { return decision },
			Swap: func(ver *revive.StageVerifyResult) bool {
				reply := make(chan restoreSwapReply, 1)
				a.update(func() {
					r.verify = ver
					r.reply = reply
					r.deadline = time.Now().Add(10 * time.Minute)
					r.swap = false
					r.step = "confirm"
				})
				for {
					select {
					case decision := <-reply:
						if !decision.approve {
							return false
						}
						if time.Now().Before(decision.deadline) {
							return true
						}
						a.update(func() {
							r.swap = false
							r.j.err = valise.New("confirmation_expired", T("restore.expired"), T("restore.expired_hint"))
						})
					case <-ctx.Done():
						return false
					}
				}
			},
		}, func(t service.RestoreTask) {
			a.update(func() {
				r.task = t
				if t.Stage == service.StageSwap {
					r.step = "swap"
				}
			})
		})
		a.update(func() {
			r.task = t
			r.j.busy, r.j.err, r.j.cancel = false, err, nil
			r.reply = nil
			r.plan = nil
			p.Revive.Close()
			r.step = "result"
			if t.State == "reconciling" || t.State == "needs_recovery" {
				r.step = "reconcile"
			}
			a.refreshTimeline()
			a.refreshAgents()
			a.readRestoreTasks()
			a.restoreSafeExit(r)
		})
	})
}

func (a *App) restoreSafeExit(r *restoreFlow) {
	if !r.j.busy && r.exit != nil {
		exit := r.exit
		r.exit = nil
		exit()
	}
}

// dropAllAsks closes the window's management-login prompts that may still
// be waiting; a cancelled context cannot answer that UI channel.
func (a *App) dropAllAsks() {
	for _, p := range a.asks {
		p.value = ""
		select {
		case p.reply <- passReply{err: errStopped}:
		default:
		}
	}
	a.asks = nil
}

// RequestClose coordinates a normal close with this process's recovery work.
func (a *App) RequestClose(exit func()) bool {
	a.clearCloudSecret()
	if r := a.serverShell; r != nil && r.running {
		r.exit = func() {
			if a.RequestClose(exit) {
				exit()
			}
		}
		a.dropAllAsks()
		a.stopServerShell(r)
		return false
	}
	if n := a.native; n != nil && n.running {
		n.exit = func() {
			if a.RequestClose(exit) {
				exit()
			}
		}
		a.dropAllAsks()
		a.stopNative(n)
		return false
	}
	if t := a.restoreTest; t != nil && t.j.busy {
		t.exit = exit
		a.showRestoreTest(t)
		a.cancelRestoreTest(t)
		return false
	}
	r := a.restore
	if r == nil || !r.j.busy {
		return true
	}
	r.exit = exit
	a.navigate("/restore")
	if r.step != "swap" {
		a.stopRestore(r)
	}
	return false
}
func (a *App) stopRestore(r *restoreFlow) {
	if r.step == "swap" {
		return
	}
	if r.j.cancel != nil {
		r.j.cancel()
	}
	for _, p := range a.asks {
		p.value = ""
		select {
		case p.reply <- passReply{err: errStopped}:
		default:
		}
	}
	a.asks = nil
	r.stopConfirm = false
}

func (a *App) restoreContext(ctx context.Context, r *restoreFlow) context.Context {
	var mu sync.Mutex
	lastStage := ""
	lastAt := time.Time{}
	return service.WithProgress(ctx, func(p service.Progress) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if p.Stage != lastStage || now.Sub(lastAt) >= 100*time.Millisecond || p.Total > 0 && p.Done >= p.Total {
			lastStage, lastAt = p.Stage, now
			a.update(func() { r.progress = p })
		}
	})
}
