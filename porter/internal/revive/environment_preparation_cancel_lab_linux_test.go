//go:build linux

package revive

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
)

var preparationWorker = flag.String("preparation-worker", "", "private cancellation lab worker metadata")

type preparationWorkerInput struct {
	Home, Phase, Preview, Operation, Expected string
	Params                                    revive.EnvironmentPlanParams
}

func TestPreparationCancellationWorker(t *testing.T) {
	if *preparationWorker == "" {
		t.Skip("owned subprocess worker only")
	}
	raw, err := os.ReadFile(*preparationWorker)
	if err != nil {
		t.Fatal(err)
	}
	var in preparationWorkerInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{Store: Store{Home: in.Home}}
	// Source preparation needs the authenticated registration environment only;
	// finish reads the already fixed stage and never invokes a host command.
	r.Env = &porterenroll.Env{Home: in.Home}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := managedruntime.Controller{Home: home}
	if in.Phase == "begin" {
		_, err = r.beginEnvironmentPreparation(ctx, in.Params, in.Preview, in.Operation, c, true)
	} else if in.Phase == "cleanup" {
		_, err = r.cleanupEnvironmentPreparation(ctx, in.Params.Agent, in.Operation, in.Expected, c)
	} else if in.Phase == "old-cleanup" {
		a := NewBoundActivationRunner(r)
		a.runtime = c
		_, err = a.CleanupOldReviewed(ctx, in.Params.Agent, in.Operation, in.Expected)
	} else {
		_, err = r.finishEnvironmentPreparation(ctx, in.Params.Agent, in.Operation, c)
	}
	if err != nil {
		t.Fatal(err)
	}
}

type cancellationWorld struct {
	*world
	params      revive.EnvironmentPlanParams
	preview, op string
	control     managedruntime.Controller
	native      managedruntime.NativeRef
}

func newCancellationWorld(t *testing.T, hold bool) *cancellationWorld {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	unit := profile.ServiceUnit{Scope: "user", Unit: "fixture-native-" + id + ".service"}
	w, p := environmentWorldProfile(t, program, func(p *profile.Profile) {
		p.Service = &profile.Service{Candidates: []profile.ServiceUnit{unit}}
		p.Environments.StartArgv = append(p.Environments.StartArgv, "-environment-accept-original")
		if hold {
			p.Environments.StartArgv = append(p.Environments.StartArgv, "-environment-hold")
		}
	})
	for _, path := range []string{w.home, w.state} {
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	control := managedruntime.Controller{Home: home}
	native, _ := startNativeActivationFixture(t, control, w.p, unit, program, w.state, 18391, false, filepath.Join(w.state, "bin/gateway"))
	binding, err := porteridentity.Read(w.home, "demo")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := (environments.Store{Home: w.home}).Settings(binding.Agent.Ref)
	if err != nil {
		t.Fatal(err)
	}
	params := revive.EnvironmentPlanParams{Agent: binding.Agent.Ref, SettingsDigest: settings.Digest, Input: *p.IsolatedTest}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	view, err := w.r.planEnvironment(ctx, params, control)
	if err != nil {
		t.Fatal(err)
	}
	return &cancellationWorld{w, params, view.Digest, id, control, native}
}
func (w *cancellationWorld) begin(t *testing.T) *EnvironmentPreparation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := w.r.beginEnvironmentPreparation(ctx, w.params, w.preview, w.op, w.control, true)
	if err != nil || p.State != "data_ready" {
		t.Fatal(p, err)
	}
	return p
}
func (w *cancellationWorld) upload(t *testing.T, p *EnvironmentPreparation) {
	t.Helper()
	var data bytes.Buffer
	tw := tar.NewWriter(&data)
	tw.WriteHeader(&tar.Header{Name: "state/SOUL.md", Typeflag: tar.TypeReg, Mode: 0600, Size: 8})
	tw.Write([]byte("snapshot"))
	tw.Close()
	if _, err := StageWrite(w.r.Store, p.Plan.OpID, &data); err != nil {
		t.Fatal(err)
	}
}
func (w *cancellationWorld) assertOriginal(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	after, err := w.control.ReadNative(ctx, w.native)
	if err != nil || !after.Running || after.MainPID != w.native.MainPID || after.KernelStart != w.native.KernelStart || after.UnitFileState != "disabled" || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("cancellation disturbed original", after, err)
	}
}
func (w *cancellationWorld) worker(t *testing.T, phase string, expected ...string) *exec.Cmd {
	t.Helper()
	input := preparationWorkerInput{Home: w.home, Phase: phase, Preview: w.preview, Operation: w.op, Params: w.params}
	if len(expected) > 0 {
		input.Expected = expected[0]
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.home, "worker.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestPreparationCancellationWorker$", "-preparation-worker="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return cmd
}
func TestPreparationCancellationAndExplicitCleanupLab(t *testing.T) {
	if !*activationLab {
		t.Skip("requires explicitly authorized real Linux cancellation lab")
	}
	for _, scenario := range []string{"data-ready-and-retry", "prepared", "interrupted-copy", "interrupted-probe", "changed-directory", "stale-review", "interrupted-cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			w := newCancellationWorld(t, scenario == "interrupted-probe")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			var p *EnvironmentPreparation
			if scenario == "interrupted-copy" {
				cmd := w.worker(t, "begin")
				until := time.Now().Add(10 * time.Second)
				for time.Now().Before(until) {
					p, _ = loadPreparation(w.home, w.params.Agent, w.op)
					if p != nil && p.State == "capturing" {
						break
					}
					time.Sleep(time.Millisecond)
				}
				if p == nil || p.State != "capturing" {
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatal("did not observe real copy intent", p)
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				cmd.Wait()
				p, _ = loadPreparation(w.home, w.params.Agent, w.op)
			} else {
				p = w.begin(t)
			}
			if scenario == "prepared" {
				w.upload(t, p)
				var err error
				p, err = w.r.finishEnvironmentPreparation(ctx, w.params.Agent, w.op, w.control)
				if err != nil || p.State != "prepared" {
					t.Fatal(p, err)
				}
			}
			if scenario == "interrupted-probe" {
				w.upload(t, p)
				cmd := w.worker(t, "finish")
				until := time.Now().Add(15 * time.Second)
				started := false
				for time.Now().Before(until) {
					p, _ = loadPreparation(w.home, w.params.Agent, w.op)
					if p != nil && p.ProbeWorkspace != "" && p.ProbeLease.Inode != 0 {
						dir, _ := preparationWorkspace(w.home, p, false)
						marker := filepath.Join(dir, p.ProbeWorkspace, "home/.demo/probe-started")
						if _, err := os.Stat(marker); err == nil {
							started = true
							break
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !started {
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatal("no actual private probe observed", p)
				}
				dir, _ := preparationWorkspace(w.home, p, false)
				if lease, err := isolated.HoldProbeStopped(filepath.Join(dir, p.ProbeWorkspace)); err == nil {
					if lease != nil {
						lease.Close()
					}
					t.Fatal("running namespace did not retain lifetime lock")
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				cmd.Wait()
				// No replay after the parent dies. Wait only for the specific observed
				// namespace's inherited lease, which cannot be released by the harness.
				stopped := false
				until = time.Now().Add(5 * time.Second)
				for time.Now().Before(until) {
					lease, err := isolated.HoldProbeStopped(filepath.Join(dir, p.ProbeWorkspace))
					if err == nil {
						lease.Close()
						stopped = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !stopped {
					t.Fatal("orphaned namespace lifetime remained live")
				}
				fresh, err := w.r.QueryEnvironmentPreparation(ctx, w.params.Agent, w.op)
				if err != nil || fresh.State != "probing" || fresh.Probe != nil {
					t.Fatal("reopen fabricated completion", fresh, err)
				}
			}
			cancelled, err := w.r.cancelEnvironmentPreparation(ctx, w.params.Agent, w.op, w.control)
			if err != nil || cancelled.State != "cancelled" {
				t.Fatal("cancel preparation failed", cancelled, err)
			}
			w.assertOriginal(t)
			recordDir, _ := preparationDir(w.home, w.params.Agent, false)
			recordPath := filepath.Join(recordDir, w.op+".json")
			beforeRead, _ := os.ReadFile(recordPath)
			status, err := w.r.reviewEnvironmentPreparation(ctx, w.params.Agent, w.op, w.control)
			if err != nil || status.State != "cancelled" || status.CanCancel || !status.CanReviewCleanup || status.Native == nil || !status.Native.Running {
				t.Fatal("cancelled projection did not report actual conditions", status, err)
			}
			afterRead, _ := os.ReadFile(recordPath)
			if !bytes.Equal(beforeRead, afterRead) {
				t.Fatal("read-only review changed the preparation")
			}
			// Cancellation preserves every existing claimed directory.
			for _, claim := range cancelled.Claims {
				path, _ := preparationResource(w.home, cancelled, claim.Kind, claim.Root)
				if _, err := os.Stat(path); err != nil {
					t.Fatal("cancel deleted material", claim.Kind, err)
				}
			}
			view, _, leases, err := w.r.reviewPreparationCleanup(ctx, w.params.Agent, w.op, w.control)
			closePreparationLeases(leases)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "changed-directory" {
				var stage string
				for _, item := range view.Items {
					if item.Kind == "stage" {
						stage, _ = preparationResource(w.home, cancelled, item.Kind, item.Root)
					}
				}
				if err := os.Rename(stage, stage+".owned"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(stage, 0700); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(stage, "foreign"), "keep", 0600)
				if _, err := w.r.cleanupEnvironmentPreparation(ctx, w.params.Agent, w.op, view.Digest, w.control); err == nil {
					t.Fatal("changed directory removed")
				}
				if read(t, filepath.Join(stage, "foreign")) != "keep" {
					t.Fatal("foreign data removed")
				}
				w.assertOriginal(t)
				return
			}
			if scenario == "stale-review" {
				store := environments.Store{Home: w.home}
				settings, _ := store.Settings(w.params.Agent)
				if _, err := store.SetSettings(environment.SetSettingsParams{Agent: w.params.Agent, Expected: settings.Digest, Mode: environment.Compact}); err != nil {
					t.Fatal(err)
				}
				if _, err := w.r.cleanupEnvironmentPreparation(ctx, w.params.Agent, w.op, view.Digest, w.control); err == nil {
					t.Fatal("stale scope accepted")
				}
				view, _, leases, err = w.r.reviewPreparationCleanup(ctx, w.params.Agent, w.op, w.control)
				closePreparationLeases(leases)
				if err != nil {
					t.Fatal(err)
				}
			}

			if scenario == "interrupted-cleanup" {
				dir, err := preparationWorkspace(w.home, cancelled, false)
				if err != nil {
					t.Fatal(err)
				}
				// Keep actual deletion observable long enough to kill this one worker.
				for i := 0; i < 10000; i++ {
					path := filepath.Join(dir, fmt.Sprintf("owned-cleanup-%05d", i))
					if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				cmd := w.worker(t, "cleanup", view.Digest)
				observed := false
				until := time.Now().Add(10 * time.Second)
				for time.Now().Before(until) {
					saved, _ := loadPreparation(w.home, w.params.Agent, w.op)
					if saved != nil && saved.State == "cleaning" {
						for _, claim := range saved.Claims {
							if claim.Kind == "program-original" && claim.Removed {
								observed = true
								break
							}
						}
					}
					if observed {
						break
					}
					time.Sleep(time.Millisecond)
				}
				if !observed {
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatal("did not observe partial real cleanup")
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				cmd.Wait()
				saved, err := loadPreparation(w.home, w.params.Agent, w.op)
				if err != nil || saved.State != "cleaning" {
					t.Fatal("cleanup not interrupted", saved, err)
				}
				w.assertOriginal(t)
				status, err := w.r.reviewEnvironmentPreparation(ctx, w.params.Agent, w.op, w.control)
				if err != nil || status.State != "cleaning" || !status.CanReviewCleanup || status.Native == nil || !status.Native.Running {
					t.Fatal("partial cleanup projection lost facts", status, err)
				}
			}
			cleaned, err := w.r.cleanupEnvironmentPreparation(ctx, w.params.Agent, w.op, view.Digest, w.control)
			if err != nil || cleaned.State != "cleaned" {
				t.Fatal("cleanup failed", cleaned, err)
			}
			w.assertOriginal(t)
			settings, err := (environments.Store{Home: w.home}).Settings(w.params.Agent)
			expectedMode := environment.Parallel
			if scenario == "stale-review" {
				expectedMode = environment.Compact
			}
			if err != nil || settings.Managed || settings.Busy || settings.Mode != expectedMode || view.RegistryRevision != 0 && settings.Digest == w.params.SettingsDigest {
				t.Fatal("initial registration or mode not released correctly", settings, err)
			}
			pending, err := w.r.Store.Pending()
			if err != nil || len(pending) != 0 {
				t.Fatal("cancelled child still pending", pending, err)
			}
			for _, item := range view.Items {
				path, _ := preparationResource(w.home, cleaned, item.Kind, item.Root)
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("owned material left", item.Kind, err)
				}
			}
			if scenario == "data-ready-and-retry" || scenario == "interrupted-copy" {
				if scenario == "interrupted-copy" {
					dir, _ := preparationDir(w.home, w.params.Agent, false)
					if err := os.WriteFile(filepath.Join(dir, ".preparation-interrupted-metadata"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				w.params.SettingsDigest = settings.Digest
				newView, err := w.r.planEnvironment(ctx, w.params, w.control)
				if err != nil {
					t.Fatal(err)
				}
				nextID, err := identity.NewID()
				if err != nil {
					t.Fatal(err)
				}
				next, err := w.r.beginEnvironmentPreparation(ctx, w.params, newView.Digest, nextID, w.control, true)
				if err != nil || next.State != "data_ready" {
					t.Fatal("cleaned first attempt could not retry", next, err)
				}
				if _, err := w.r.cleanupEnvironmentPreparation(ctx, cleaned.Params.Agent, cleaned.OperationID, view.Digest, w.control); err != nil {
					t.Fatal(err)
				}
				if err := (environments.ProgramStore{Home: w.home}).Verify(ctx, w.params.Agent, next.Original, next.OriginalMaterials); err != nil {
					t.Fatal("old cleanup deleted reused A", err)
				}
			}
		})
	}
}
