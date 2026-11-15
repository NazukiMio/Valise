//go:build linux

package revive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
	"valise/porter/internal/managedruntime"
)

func nativeFixtureCommand(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, "/usr/bin/systemctl", append([]string{"--user"}, args...)...).Run()
}

func startNativeActivationFixture(t *testing.T, c managedruntime.Controller, p profile.Profile, unit profile.ServiceUnit, program []byte, state string, port int, enabled bool, sourceExecutable ...string) (managedruntime.NativeRef, managedruntime.Observation) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "native-gateway")
	if err := os.WriteFile(exe, program, 0700); err != nil {
		t.Fatal(err)
	}
	if len(sourceExecutable) > 0 {
		exe = sourceExecutable[0]
	}
	base := filepath.Join(c.Home, ".config/systemd/user")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, unit.Unit)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	nestedArg := ""
	for _, r := range p.Roots {
		if r.ID == "workspace" {
			nestedArg = " -managed-data-nested"
		}
	}
	content := "[Unit]\nDescription=Owned native activation fixture\n[Service]\nType=exec\nExecStart=" + strconv.Quote(exe) + " -test.run=TestManagedDataGuest -managed-data-guest -managed-data-port=" + strconv.Itoa(port) + nestedArg + "\nWorkingDirectory=" + state + "\nStandardOutput=null\nStandardError=null\nNoNewPrivileges=yes\nKillMode=control-group\n[Install]\nWantedBy=default.target\n"
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	savedRef := managedruntime.NativeRef{Unit: unit.Unit, Definition: vault.SHA256Hex([]byte(content)), Executable: exe}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		if err := nativeFixtureCommand(cleanup, "disable", "--", unit.Unit); err != nil {
			t.Error(err)
			return
		}
		if err := nativeFixtureCommand(cleanup, "stop", "--", unit.Unit); err != nil {
			t.Error(err)
			return
		}
		stopped, err := c.ReadNative(cleanup, savedRef)
		if err != nil || !stopped.Stopped || stopped.UnitFileState != "disabled" {
			t.Error("native stop unverified; retained", unit.Unit, stopped, err)
			return
		}
		if err := os.Remove(path); err != nil {
			t.Error(err)
			return
		}
		if err := nativeFixtureCommand(cleanup, "daemon-reload"); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := nativeFixtureCommand(ctx, "daemon-reload"); err != nil {
		t.Fatal("native fixture reload", err)
	}
	if err := nativeFixtureCommand(ctx, "start", "--", unit.Unit); err != nil {
		t.Fatal("native fixture start", err)
	}
	if enabled {
		if err := nativeFixtureCommand(ctx, "enable", "--", unit.Unit); err != nil {
			t.Fatal(err)
		}
	}
	ref, ob, err := c.InspectNative(ctx, p, unit)
	if err != nil {
		t.Fatalf("native fixture retained for inspection: %s: %v", path, err)
	}

	savedRef = ref
	for i := 0; i < 40; i++ {
		proof, err := c.CheckNativeHTTP(ctx, ref, p)
		if err == nil && proof.GatewayPassed {
			return ref, ob
		}
		if i == 39 {
			t.Fatal("native gateway not ready", proof, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ref, ob
}

func TestNativeFirstActivationTransactionLab(t *testing.T) {
	if !*activationLab {
		t.Skip("requires explicitly authorized real Linux native activation lab")
	}
	for _, scenario := range []string{"success-and-return", "gateway-failure", "cancel-before-stop", "source-changed-after-stop", "changed-boot-policy", "unknown-intent-reconnect", "disabled-startup", "native-restarted-before-activate", "native-restarted-before-candidate"} {
		t.Run(scenario, func(t *testing.T) {
			status := 200
			if scenario == "gateway-failure" || scenario == "source-changed-after-stop" {
				status = 503
			}
			f := newActivationLabKind(t, status, true, scenario != "disabled-startup")
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			agent, op := f.plan.Agent, f.plan.OperationID
			if scenario == "native-restarted-before-activate" {
				if err := nativeFixtureCommand(ctx, "restart", "--", f.native.Unit); err != nil {
					t.Fatal(err)
				}
				review, err := f.a.Activate(ctx, agent, op)
				if err != nil || review.Record.State != "needs_review" || review.Phase != environment.Prepared || review.Native == nil || !review.Native.Running || review.Native.MainPID == f.before.MainPID || len(review.Record.Facts) != 0 {
					t.Fatal("changed native process used old activation", review, err)
				}
				return
			}
			if scenario == "cancel-before-stop" {
				review, err := f.a.Return(ctx, agent, op)
				if err != nil || review.Phase != environment.RolledBack || review.Current == nil || review.Current.MainPID != f.before.MainPID || review.Current.KernelStart != f.before.KernelStart || review.Current.UnitFileState != "enabled" {
					t.Fatal("native preparation cancellation interrupted A", review, err)
				}
				if err := f.a.runtime.RequireAbsent(ctx, f.original); err != nil {
					t.Fatal("cancel installed an unnecessary managed A", err)
				}
				return
			}
			if scenario == "changed-boot-policy" {
				if _, err := f.a.runtime.DisableNativeStartup(ctx, *f.native); err != nil {
					t.Fatal(err)
				}
				review, err := f.a.Activate(ctx, agent, op)
				if err != nil || review.Record.State != "needs_review" || review.Phase != environment.Prepared || review.Current == nil || review.Current.MainPID != f.before.MainPID || len(review.Record.Facts) != 0 {
					t.Fatal("changed native facts executed an old plan", review, err)
				}
				return
			}
			if scenario == "unknown-intent-reconnect" {
				rec, err := loadActivation(f.w.home, agent, op)
				if err != nil {
					t.Fatal(err)
				}
				rec.State = "running"
				rec.Facts["disable_original"] = ActivationFact{IntentAt: time.Now().UTC()}
				if err := saveActivation(f.w.home, rec); err != nil {
					t.Fatal(err)
				}
				review, err := f.a.Activate(ctx, agent, op)
				if err != nil || review.Record.State != "running" || review.Current == nil || review.Current.MainPID != f.before.MainPID || review.Current.UnitFileState != "enabled" {
					t.Fatal("reconnect replayed native intent", review, err)
				}
				return
			}
			if scenario == "source-changed-after-stop" || scenario == "native-restarted-before-candidate" {
				verify := f.a.verify
				changed := false
				f.a.verify = func(ctx context.Context, agent identity.AgentRef, ref environment.Ref, digest string) error {
					if ref == f.plan.Candidate && !changed {
						r, err := f.a.registry(&ActivationRecord{Plan: f.plan})
						if err != nil {
							return err
						}
						if r.Operation.Phase == environment.OriginalDown {
							if scenario == "native-restarted-before-candidate" {
								if err := nativeFixtureCommand(ctx, "start", "--", f.native.Unit); err != nil {
									return err
								}
							} else if err := os.WriteFile(f.native.Executable, []byte("source is no longer runnable"), 0700); err != nil {
								return err
							}
							changed = true
						}
					}
					return verify(ctx, agent, ref, digest)
				}
			}
			review, err := f.a.Activate(ctx, agent, op)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "native-restarted-before-candidate" {
				if review.Record.State != "needs_review" || review.Native == nil || !review.Native.Running || review.Current == nil || review.Current.Running {
					t.Fatal("restarted native allowed B to run", review)
				}
				if _, ok := review.Record.Facts["start_candidate"]; ok {
					t.Fatal("B start dispatched without native stop proof")
				}
				if err := nativeFixtureCommand(ctx, "stop", "--", f.native.Unit); err != nil {
					t.Fatal(err)
				}
				review, err = f.a.Return(ctx, agent, op)
				if err != nil {
					t.Fatal("explicit retry after native stop failed", err)
				}
			}
			if review.Native == nil || !review.Native.Stopped || review.Native.UnitFileState != "disabled" {
				t.Fatal("native entry not verified stopped", review)
			}
			if status == 200 && scenario != "native-restarted-before-candidate" {
				if review.Phase != environment.AwaitingHuman || review.Record.State != "awaiting_human" || review.Current == nil || !review.Current.Running {
					t.Fatal("native to B activation failed", review)
				}
				review, err = f.a.Return(ctx, agent, op)
				if err != nil {
					t.Fatal(err)
				}
			}
			if review.Phase != environment.RolledBack || review.Record.State != "rolled_back" || review.Current == nil || !review.Current.Running || review.Current.Unit == f.native.Unit || review.Gateway == nil || !review.Gateway.GatewayPassed {
				t.Fatal("independent A did not recover", review)
			}
			if read(t, filepath.Join(f.w.state, "SOUL.md")) != "current soul" {
				t.Fatal("original data not recovered")
			}
			if review.Current.UnitFileState != f.before.UnitFileState {
				t.Fatal("original startup policy changed", review.Current)
			}
		})
	}
}
