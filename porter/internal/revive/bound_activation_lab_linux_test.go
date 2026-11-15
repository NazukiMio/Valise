//go:build linux

package revive

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
)

func boundProgramArchive(t *testing.T) (environments.ProgramMaterial, []byte) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	file := environments.ProgramFile{Path: ".demo/program/gateway", Size: int64(len(program)), SHA256: vault.SHA256Hex(program), Mode: 0700}
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	if err := w.WriteHeader(&tar.Header{Name: file.Path, Typeflag: tar.TypeReg, Mode: 0700, Size: file.Size}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(program); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return environments.ProgramMaterial{SourceDigest: file.SHA256, ArchiveSHA: vault.SHA256Hex(data.Bytes()), ArchiveBytes: int64(data.Len()), OS: "linux", Arch: goruntime.GOARCH, Files: []environments.ProgramFile{file}}, data.Bytes()
}

func TestBoundActivationReopensMaterialsAndUsesConcreteVerifierLab(t *testing.T) {
	if !*activationLab {
		t.Skip("requires explicitly authorized real Linux activation lab")
	}
	for _, scenario := range []struct {
		name                 string
		fail, native, nested bool
	}{
		{"success-and-reconnect", false, false, false}, {"formal-gateway-failure", true, false, false},
		{"native-success-and-reconnect", false, true, false}, {"native-formal-gateway-failure", true, true, false},
		{"nested-native-success", false, true, true}, {"nested-native-failure", true, true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fail, native := scenario.fail, scenario.native
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			w := newWorld(t)
			if err := os.Chmod(w.home, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(w.state, 0700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			w.p.Schema, w.p.DisplayName, w.p.Status = profile.Schema, "Fixture", profile.StatusDraft
			w.p.Provenance = &profile.Provenance{Kind: profile.ProvenanceBinary, Binary: &profile.BinaryProvenance{Name: "fixture-program"}}
			w.p.Detect = &profile.Detect{StateDirCandidates: []string{"${HOME}/.demo"}}
			w.p.Entries = []profile.Entry{{ID: "gateway", Kind: "web", Host: "127.0.0.1", Port: port, Path: "/"}}
			w.p.Health = &profile.Health{Checks: []profile.HealthCheck{{Kind: "http", Entry: "gateway", ExpectStatus: []int{200}}}}
			w.p.Paths = append(w.p.Paths, profile.PathRule{Root: "state", Glob: "formal-failure", Category: "state"})
			w.p.Environments = &profile.Environments{Engine: "linux-bwrap", StartArgv: profile.Argv{"${STATE_DIR}/program/gateway", "-test.run=TestManagedDataGuest", "-managed-data-guest", "-managed-data-port=" + strconv.Itoa(port)},
				WorkingDir: "${STATE_DIR}", Path: []string{"${HOME}/.demo/program"}, ProgramPaths: []profile.PathRef{{Root: "state", Glob: "program"}}, GatewayEntry: "gateway", Verification: profile.Capability{Status: "untested"}}
			registeredRoots := map[string]string{"state": w.state}
			guestRoots := map[string]string{"state": "/home/fixture/.demo"}
			if scenario.nested {
				w.p.Roots = []profile.Root{{ID: "workspace", Default: "${STATE_DIR}/workspace"}}
				w.p.Paths = append(w.p.Paths, profile.PathRule{Root: "workspace", Glob: "**", Category: "memory"})
				w.p.Environments.StartArgv = append(w.p.Environments.StartArgv, "-managed-data-nested")
				registeredRoots["workspace"] = filepath.Join(w.state, "workspace")
				guestRoots["workspace"] = "/home/fixture/.demo/workspace"
				if err := os.Mkdir(registeredRoots["workspace"], 0700); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(registeredRoots["workspace"], "original.md"), "original workspace", 0600)
			}
			var nativeUnit profile.ServiceUnit
			if native {
				id, err := identity.NewID()
				if err != nil {
					t.Fatal(err)
				}
				nativeUnit = profile.ServiceUnit{Scope: "user", Unit: "fixture-native-" + id + ".service"}
				w.p.Service = &profile.Service{Candidates: []profile.ServiceUnit{nativeUnit}}
			}
			_, agent, err := porterenroll.AcceptRecord(w.home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: w.p.ProfileVersion, Roots: registeredRoots, EnrolledAt: time.Now().UTC()}, w.p)
			if err != nil {
				t.Fatal(err)
			}
			material, archive := boundProgramArchive(t)
			layout := environments.Layout{Home: "/home/fixture", Roots: guestRoots}
			programs := environments.ProgramStore{Home: w.home}
			oldWorkspace, oldBinding, err := programs.BuildOriginal(ctx, agent.Ref, environments.OriginalProgramInput{Profile: w.p, Layout: layout, Program: material, Archive: bytes.NewReader(archive), MaxBytes: material.ArchiveBytes + 1<<20})
			if err != nil {
				t.Fatal(err)
			}
			if oldBinding.Source != nil || oldBinding.Receipt != nil {
				t.Fatal("original imported as a fake backup")
			}
			registryStore := environments.Store{Home: w.home}
			registry, err := registryStore.Update(agent.Ref, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(agent.Ref) })
			if err != nil {
				t.Fatal(err)
			}
			snapshot := identity.SnapshotRef{VaultID: vault.NewID("v", time.Now()), SnapshotID: vault.NewID("s", time.Now())}
			op := strings.Repeat("a", 32)
			update := func(fn func(environment.Registry, string) (environment.Registry, error)) {
				t.Helper()
				var err error
				registry, err = registryStore.Update(agent.Ref, registry.Revision, func(r environment.Registry) (environment.Registry, error) { return fn(r, op) })
				if err != nil {
					t.Fatal(err)
				}
			}
			registry, err = registryStore.Update(agent.Ref, registry.Revision, func(r environment.Registry) (environment.Registry, error) { return r.Begin(op, snapshot) })
			if err != nil {
				t.Fatal(err)
			}
			original, candidateRef := registry.Operation.Original, registry.Operation.Candidate
			data := map[string]string{"SOUL.md": "restored"}
			if scenario.nested {
				data["workspace:restored.md"] = "restored workspace"
			}
			if fail {
				data["formal-failure"] = "1"
			}
			files := vault.Files{Format: vault.FormatFiles}
			for path, content := range data {
				root, rel := "state", path
				if r, p, ok := strings.Cut(path, ":"); ok {
					root, rel = r, p
				}
				category := profile.NewClassifier(&w.p, nil).Classify(root, rel).Category
				files.Files = append(files.Files, vault.File{Root: root, Path: rel, Size: int64(len(content)), SHA256: vault.SHA256Hex([]byte(content)), Mode: 0600, Category: category})
			}
			source := environment.Source{Agent: agent.Ref, Snapshot: snapshot, Time: time.Now().UTC(), Tier: vault.TierFull, ProfileDigest: environments.Digest(w.p), ProgramDigest: material.SourceDigest, LayoutDigest: environments.LayoutDigest(layout, nil), Verified: true}
			candidate, binding, err := programs.BuildBound(ctx, agent.Ref, candidateRef, environments.BuildInput{Profile: w.p, Layout: layout, Program: material, Archive: bytes.NewReader(archive), MaxBytes: material.ArchiveBytes + 1<<20, Target: environment.Material{Source: source, Files: files},
				OpenData: func(_ context.Context, ref identity.SnapshotRef, root, path string) (io.ReadCloser, error) {
					if ref != snapshot || registeredRoots[root] == "" {
						return nil, environments.ErrData
					}
					key := path
					if root != "state" {
						key = root + ":" + path
					}
					content, ok := data[key]
					if !ok {
						return nil, environments.ErrData
					}
					return io.NopCloser(strings.NewReader(content)), nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			// Full isolation still uses a separate network and finite probe.
			check, err := isolated.CheckFor(w.p)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := isolated.GatewayProbe(ctx, candidate.Workspace, candidate.Spec, check)
			if err != nil || !proof.Verified() {
				t.Fatal("isolated binding did not reach gateway", proof, err)
			}
			mapped, err := revive.CanonicalRoots(registeredRoots)
			if err != nil {
				t.Fatal(err)
			}
			bindings, err := ManagedBindings(revive.Plan{Roots: mapped}, layout)
			if err != nil {
				t.Fatal(err)
			}
			oldDef, err := managedruntime.Compile(agent.Ref, oldWorkspace, oldBinding.Spec, bindings)
			if err != nil {
				t.Fatal(err)
			}
			newDef, err := managedruntime.Compile(agent.Ref, candidate.Workspace, candidate.Spec, bindings)
			if err != nil {
				t.Fatal(err)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			runtime := managedruntime.Controller{Home: home}
			var nativeRef *managedruntime.NativeRef
			if native {
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				program, err := os.ReadFile(exe)
				if err != nil {
					t.Fatal(err)
				}
				ref, _ := startNativeActivationFixture(t, runtime, w.p, nativeUnit, program, w.state, port, false)
				nativeRef = &ref
			} else {
				if err := runtime.Install(ctx, oldDef, ""); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				for _, d := range []managedruntime.Definition{oldDef, newDef} {
					if _, err := runtime.Load(agent.Ref, d.Digest()); err != nil {
						continue
					}
					if _, err := runtime.DisableStartup(cleanup, d); err != nil {
						t.Error(err)
						return
					}
					if result, err := runtime.Stop(cleanup, d); err != nil || !result.Observed.Stopped {
						t.Error(result, err)
						return
					}
					if err := runtime.Remove(cleanup, d); err != nil {
						t.Error(err)
						return
					}
					os.Remove(filepath.Join(home, ".local/state/valise/managed-runtime", d.Name()+".lock"))
					return
				}
				if !native || runtime.RequireAbsent(cleanup, oldDef) != nil {
					t.Error("owned synthetic unit unknown")
				}
			})
			if !native {
				if _, err := runtime.Start(ctx, oldDef); err != nil {
					t.Fatal(err)
				}
			}
			plan := w.plan()
			plan.Roots = mapped
			plan.SnapshotID, plan.Tier, plan.Carry, plan.Unit = snapshot.SnapshotID, vault.TierFull, false, ""
			plan.Body = revive.BodyPlan{Action: revive.BodyKeep}
			scope := revive.ManagedDataRef{Agent: agent.Ref, OperationID: op, Snapshot: snapshot, Original: original, Candidate: candidateRef}
			var manifest []revive.ManifestEntry
			var tarData bytes.Buffer
			tw := tar.NewWriter(&tarData)
			for path, content := range data {
				name := "state/" + path
				logicalRoot, logicalPath := "state", path
				if root, rel, ok := strings.Cut(path, ":"); ok {
					logicalRoot, logicalPath = root, rel
					for _, r := range mapped {
						if r.ID == root {
							name = r.SwapRoot + "/" + r.Rel + "/" + rel
						}
					}
				}
				category := profile.NewClassifier(&w.p, nil).Classify(logicalRoot, logicalPath).Category
				manifest = append(manifest, revive.ManifestEntry{Path: name, Size: int64(len(content)), SHA256: vault.SHA256Hex([]byte(content)), Category: category})
				tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(content)), Mode: 0600})
				tw.Write([]byte(content))
			}
			tw.Close()
			if _, err := BeginManagedData(ctx, w.r.Env, w.r.Store, plan, scope, manifest); err != nil {
				t.Fatal(err)
			}
			if _, err := StageWrite(w.r.Store, plan.OpID, &tarData); err != nil {
				t.Fatal(err)
			}
			if result, err := StageVerify(w.r.Store, revive.StageVerifyParams{OpID: plan.OpID, Manifest: manifest}); err != nil || len(result.Problems) != 0 {
				t.Fatal(result, err)
			}
			update(environment.Registry.Prepared)
			a := NewBoundActivationRunner(w.r)
			a.runtime = runtime
			activationPlan := ActivationPlan{Agent: agent.Ref, OperationID: op, Snapshot: snapshot, Original: original, Candidate: candidateRef, DataOpID: plan.OpID, OriginalMaterials: environments.Digest(oldBinding), CandidateMaterials: environments.Digest(binding), OriginalProfile: w.p, CandidateProfile: w.p}
			activationPlan.NativeOriginal = nativeRef
			if _, err := a.Prepare(ctx, activationPlan, oldDef, newDef); err != nil {
				t.Fatal(err)
			}
			if !native {
				assertActiveRead(t, ctx, w.home, runtime, agent.Ref, original, false)
			} // Legacy binary fixture has no full native source binding; the
			// production NPM preparer covers the native daily-reader path.
			// Discard all material-provider closures; recreate using disk only.
			a = NewBoundActivationRunner(w.r)
			a.runtime = runtime
			review, err := a.Activate(ctx, agent.Ref, op)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if review.Phase != environment.RolledBack || review.Record.FailureCode == "" || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
					t.Fatal("persistent verifier rollback failed", review)
				}
			} else {
				if review.Phase != environment.AwaitingHuman || !review.Current.Running || !review.Gateway.GatewayPassed {
					t.Fatal(review)
				}
				assertActiveRead(t, ctx, w.home, runtime, agent.Ref, candidateRef, false)
				a = NewBoundActivationRunner(w.r)
				a.runtime = runtime
				if review, err = a.Return(ctx, agent.Ref, op); err != nil || review.Phase != environment.RolledBack {
					t.Fatal(review, err)
				}
			}
			assertActiveRead(t, ctx, w.home, runtime, agent.Ref, original, false)
		})
	}
}
