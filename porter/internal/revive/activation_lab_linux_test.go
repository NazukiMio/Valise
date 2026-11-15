//go:build linux

package revive

import (
	"context"
	"flag"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
)

var activationLab = flag.Bool("activation-lab", false, "synthetic persistent activation transaction with systemd")

type activationLabFixture struct {
	w                   *managedWorld
	a                   *ActivationRunner
	plan                ActivationPlan
	original, candidate managedruntime.Definition
	programs            [2]string
	before              managedruntime.Observation
	native              *managedruntime.NativeRef
}

func newActivationLab(t *testing.T, candidateStatus int, startup ...bool) *activationLabFixture {
	return newActivationLabKind(t, candidateStatus, false, startup...)
}

func newActivationLabKind(t *testing.T, candidateStatus int, native bool, startup ...bool) *activationLabFixture {
	t.Helper()
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	nativeUnit := profile.ServiceUnit{Scope: "user", Unit: "fixture-native-" + id + ".service"}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	w := newManagedWorld(t, false, func(p *profile.Profile) {
		p.Entries = []profile.Entry{{ID: "gateway", Kind: "web", Host: "127.0.0.1", Port: port, Path: "/"}}
		p.Health = &profile.Health{Checks: []profile.HealthCheck{{Kind: "http", Entry: "gateway", ExpectStatus: []int{200}}}}
		p.Environments = &profile.Environments{GatewayEntry: "gateway"}
		if native {
			p.Service = &profile.Service{Candidates: []profile.ServiceUnit{nativeUnit}}
		}
	})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	f := &activationLabFixture{w: w}
	definitions := [2]managedruntime.Definition{}
	for i, status := range []int{200, candidateStatus} {
		workspace, err := isolated.NewWorkspaceForHome(t.TempDir(), "/home/fixture")
		if err != nil {
			t.Fatal(err)
		}
		if err := workspace.SetProgramPaths([]string{"app"}); err != nil {
			t.Fatal(err)
		}
		f.programs[i] = filepath.Join(workspace.ProgramDir(), "app/gateway")
		if err := os.WriteFile(f.programs[i], program, 0700); err != nil {
			t.Fatal(err)
		}
		spec := isolated.Spec{Home: "/home/fixture", WorkingDir: "/home/fixture/state", Path: []string{"/home/fixture/app"}, Argv: []string{
			"/home/fixture/app/gateway", "-test.run=TestManagedDataGuest", "-managed-data-guest", "-managed-data-port=" + strconv.Itoa(port), "-managed-data-status=" + strconv.Itoa(status)}}
		definitions[i], err = managedruntime.Compile(w.scope.Agent, workspace, spec, []isolated.DataBinding{{Host: w.state, Guest: "/home/fixture/state"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.original, f.candidate = definitions[0], definitions[1]
	hash := vault.SHA256Hex(program)
	f.a = NewActivationRunner(w.r, func(ctx context.Context, agent identity.AgentRef, ref environment.Ref, expected string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if agent != w.scope.Agent || expected != hash {
			return managedruntime.ErrDefinition
		}
		i := 0
		if ref == w.scope.Candidate {
			i = 1
		} else if ref != w.scope.Original {
			return managedruntime.ErrDefinition
		}
		b, err := os.ReadFile(f.programs[i])
		if err != nil || vault.SHA256Hex(b) != expected {
			return managedruntime.ErrDefinition
		}
		return ctx.Err()
	})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	f.a.runtime = managedruntime.Controller{Home: home}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if !native {
		if err := f.a.runtime.Install(ctx, f.original, ""); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var current managedruntime.Definition
		for _, d := range definitions {
			if _, err := f.a.runtime.Load(w.scope.Agent, d.Digest()); err == nil {
				current = d
				break
			}
		}
		if current.Digest() == "" {
			if native && f.a.runtime.RequireAbsent(ctx, f.original) == nil {
				return
			}
			t.Error("synthetic definition unknown; kept", f.original.Name())
			return
		}
		if _, err := f.a.runtime.DisableStartup(ctx, current); err != nil {
			t.Error(err)
			return
		}
		if result, err := f.a.runtime.Stop(ctx, current); err != nil || !result.Observed.Stopped {
			t.Error("synthetic stop", result, err)
			return
		}
		if err := f.a.runtime.Remove(ctx, current); err != nil {
			t.Error(err)
			return
		}
		if err := os.Remove(filepath.Join(home, ".local/state/valise/managed-runtime", current.Name()+".lock")); err != nil {
			t.Error(err)
		}
	})
	if native {
		ref, ob := startNativeActivationFixture(t, f.a.runtime, w.p.Profile, nativeUnit, program, w.state, port, len(startup) == 0 || startup[0])
		f.native, f.before = &ref, ob
	} else {
		if _, err := f.a.runtime.Start(ctx, f.original); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 40; i++ {
			proof, err := f.a.runtime.CheckHTTP(ctx, f.original, w.p.Profile)
			if err == nil && proof.GatewayPassed {
				break
			}
			if i == 39 {
				t.Fatal("original synthetic gateway unavailable", proof, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(startup) == 0 || startup[0] {
			if _, err := f.a.runtime.EnableStartup(ctx, f.original, w.p.Profile); err != nil {
				t.Fatal(err)
			}
		}
		f.before, err = f.a.runtime.Read(ctx, f.original)
		if err != nil {
			t.Fatal(err)
		}
	}
	w.begin(t)
	w.transition(t, environment.Registry.Prepared)
	f.plan = ActivationPlan{Agent: w.scope.Agent, OperationID: w.scope.OperationID, Snapshot: w.scope.Snapshot, Original: w.scope.Original, Candidate: w.scope.Candidate,
		DataOpID: w.p.OpID, OriginalMaterials: hash, CandidateMaterials: hash, OriginalProfile: w.p.Profile, CandidateProfile: w.p.Profile}
	f.plan.NativeOriginal = f.native
	review, err := f.a.Prepare(ctx, f.plan, f.original, f.candidate)
	if err != nil || review.Record.State != "ready" || review.Current == nil || review.Current.MainPID != f.before.MainPID || review.Current.KernelStart != f.before.KernelStart {
		t.Fatal("preparation interrupted original", review, err)
	}
	return f
}

func TestManagedActivationTransactionLab(t *testing.T) {
	if !*activationLab {
		t.Skip("requires explicit real Linux activation lab")
	}
	for _, test := range []string{"gateway_failure", "human_acceptance_pending", "cancel_preparation", "changed_candidate", "unknown_reconnect", "disabled_startup", "unknown_candidate_stop", "original_materials_refused"} {
		t.Run(test, func(t *testing.T) {
			status := 200
			if test == "gateway_failure" || test == "original_materials_refused" {
				status = 503
			}
			f := newActivationLab(t, status, test != "disabled_startup")
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			agent, op := f.plan.Agent, f.plan.OperationID
			originalVerifier := f.a.verify
			if test == "original_materials_refused" {
				f.a.verify = func(ctx context.Context, agent identity.AgentRef, ref environment.Ref, hash string) error {
					r, err := f.a.registry(&ActivationRecord{Plan: f.plan})
					if err != nil {
						return err
					}
					if ref == f.plan.Original && r.Operation.Phase == environment.CandidateDown {
						return managedruntime.ErrDefinition
					}
					return originalVerifier(ctx, agent, ref, hash)
				}
			}
			if test == "unknown_reconnect" {
				rec, _ := loadActivation(f.w.home, agent, op)
				rec.State = "running"
				rec.Facts["disable_original"] = ActivationFact{IntentAt: time.Now().UTC()}
				if err := saveActivation(f.w.home, rec); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					review, err := f.a.Activate(ctx, agent, op)
					if err != nil || review.Record.State != "running" || review.Current.MainPID != f.before.MainPID || !review.Current.Running {
						t.Fatal("reconnect replayed intent", review, err)
					}
				}
				return
			}
			if test == "cancel_preparation" {
				review, err := f.a.Return(ctx, agent, op)
				if err != nil || review.Phase != environment.RolledBack || review.Current.MainPID != f.before.MainPID || review.Current.KernelStart != f.before.KernelStart || review.Current.UnitFileState != "enabled" {
					t.Fatal("preparation cancellation interrupted A", review, err)
				}
				return
			}
			if test == "changed_candidate" {
				if err := os.WriteFile(f.programs[1], []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
				review, err := f.a.Activate(ctx, agent, op)
				if err != nil || review.Record.State != "needs_review" || review.Record.FailureCode != "activation_materials_changed" || review.Current.MainPID != f.before.MainPID || review.Current.UnitFileState != "enabled" {
					t.Fatal("changed candidate disturbed A", review, err)
				}
				return
			}
			review, err := f.a.Activate(ctx, agent, op)
			if err != nil {
				t.Fatal(err)
			}
			if test == "original_materials_refused" {
				if review.Record.State != "needs_review" || review.Phase != environment.CandidateDown || review.Current == nil || !review.Current.Stopped || review.Record.Facts["start_original"].Command != nil || read(t, filepath.Join(f.w.state, "SOUL.md")) != "current soul" {
					t.Fatal("unverified original programme started", review)
				}
				f.a.verify = originalVerifier
				review, err = f.a.Return(ctx, agent, op)
				if err != nil {
					t.Fatal(err)
				}
			}
			if test == "human_acceptance_pending" || test == "disabled_startup" || test == "unknown_candidate_stop" {
				if review.Record.State != "awaiting_human" || review.Phase != environment.AwaitingHuman || review.Current == nil || !review.Current.Running || review.Current.UnitFileState != f.before.UnitFileState || !review.Gateway.GatewayPassed {
					t.Fatal("successful trial not awaiting human", review)
				}
				pid := review.Current.MainPID
				again, err := f.a.Activate(ctx, agent, op)
				if err != nil || again.Current.MainPID != pid || again.Record.State != "awaiting_human" {
					t.Fatal("repeated activation restarted B", again, err)
				}
				if read(t, filepath.Join(f.w.state, "SOUL.md")) != "restored" {
					t.Fatal("selected data absent")
				}
				if test == "unknown_candidate_stop" {
					path := filepath.Join(f.a.runtime.Home, ".config/systemd/user", f.candidate.Name())
					if err := os.Chmod(path, 0644); err != nil {
						t.Fatal(err)
					}
					unknown, err := f.a.Return(ctx, agent, op)
					if restoreErr := os.Chmod(path, 0600); restoreErr != nil {
						t.Fatal(restoreErr)
					}
					if err != nil || unknown.Record.State != "needs_review" || unknown.Phase != environment.RollingBack || unknown.Current != nil || unknown.Record.Facts["start_original"].Command != nil || read(t, filepath.Join(f.w.state, "SOUL.md")) != "restored" {
						t.Fatal("unknown candidate authorized starting A", unknown, err)
					}
				}
				review, err = f.a.Return(ctx, agent, op)
				if err != nil {
					t.Fatal(err)
				}
			}
			if review.Record.State != "rolled_back" || review.Phase != environment.RolledBack || review.Current == nil || !review.Current.Running || review.Current.UnitFileState != f.before.UnitFileState || review.Record.FailureCode == "" {
				t.Fatal("rollback lost running proof or failure", review)
			}
			j, _ := f.w.r.Store.Load(f.w.p.OpID)
			if read(t, filepath.Join(f.w.state, "SOUL.md")) != "current soul" || read(t, filepath.Join(j.Roots[0].Failed, "service-write")) != strconv.Itoa(status) {
				t.Fatal("original or failed candidate lost")
			}
			pid := review.Current.MainPID
			if again, err := f.a.Activate(ctx, agent, op); err != nil || again.Current.MainPID != pid || again.Record.State != "rolled_back" {
				t.Fatal("repeat replayed failed operation", again, err)
			}
		})
	}
}
