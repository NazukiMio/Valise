//go:build linux

package revive

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
	"valise/porter/internal/activeview"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/managedruntime"
)

func TestTrustedNativePreparationUsesSelectedDataAndNeverStopsSourceLab(t *testing.T) {
	if !*activationLab {
		t.Skip("requires explicitly authorized real Linux preparation lab")
	}
	for _, scenario := range []string{"prepared-and-reopen", "accepted-cleanup", "accepted-cleanup-nested", "accepted-cleanup-direct", "accepted-cleanup-repeat", "accepted-reuse-repeat", "accepted-reuse-return", "incomplete-upload", "changed-preview", "unreviewed-profile", "changed-source-before-finish", "changed-source-before-activation", "failed-probe", "native-restart-read-only"} {
		t.Run(scenario, func(t *testing.T) {
			cleanupCase := strings.HasPrefix(scenario, "accepted-cleanup")
			exerciseActivation := scenario == "prepared-and-reopen" || cleanupCase || strings.HasPrefix(scenario, "accepted-reuse")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
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
			buildWorld := environmentWorldProfile
			if scenario == "accepted-cleanup-nested" {
				buildWorld = func(t *testing.T, executable []byte, change func(*profile.Profile)) (*world, revive.Plan) {
					return environmentWorldProfileRoots(t, executable, func(p *profile.Profile) {
						change(p)
						p.Roots = []profile.Root{{ID: "workspace", Default: "${STATE_DIR}/workspace"}}
						p.Paths = append(p.Paths, profile.PathRule{Root: "workspace", Glob: "**", Category: "workspace"})
					}, func(w *world) map[string]string {
						write(t, filepath.Join(w.state, "workspace/original.md"), "original workspace", 0600)
						return map[string]string{"state": w.state, "workspace": filepath.Join(w.state, "workspace")}
					})
				}
			}
			w, p := buildWorld(t, program, func(p *profile.Profile) {
				p.Service = &profile.Service{Candidates: []profile.ServiceUnit{unit}}
				p.Environments.StartArgv = append(p.Environments.StartArgv, "-environment-mutate", "-environment-accept-original")
				nativeFixtureDeclarations(p)
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
			ref, before := startNativeActivationFixture(t, control, w.p, unit, program, w.state, 18391, false, filepath.Join(w.state, "bin/gateway"))
			bound, err := porteridentity.Read(w.home, "demo")
			if err != nil {
				t.Fatal(err)
			}
			agent := bound.Agent.Ref
			settings, err := (environments.Store{Home: w.home}).Settings(agent)
			if err != nil {
				t.Fatal(err)
			}
			input := *p.IsolatedTest
			if cleanupCase && scenario != "accepted-cleanup-direct" {
				settings, err = (environments.Store{Home: w.home}).SetSettings(environment.SetSettingsParams{Agent: agent, Expected: settings.Digest, Mode: environment.Compact})
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "failed-probe" {
				input.Files.Files[0].Size = int64(len("not snapshot"))
				input.Files.Files[0].SHA256 = vault.SHA256Hex([]byte("not snapshot"))
			}
			req := revive.EnvironmentPlanParams{Agent: agent, SettingsDigest: settings.Digest, Input: input}
			view, err := w.r.planEnvironment(ctx, req, control)
			if err != nil {
				t.Fatal(err)
			}
			preview := view.Digest
			if scenario == "unreviewed-profile" {
				rec, err := w.r.beginEnvironmentPreparation(ctx, req, preview, id, control, false)
				if err == nil || rec != nil {
					t.Fatal("unreviewed profile began capture", rec, err)
				}
				if _, err := os.Lstat(filepath.Join(w.home, ".local/state/valise/environment-preparations")); !os.IsNotExist(err) {
					t.Fatal("refused profile wrote intent", err)
				}
				return
			}
			if scenario == "changed-preview" {
				preview = strings.Repeat("f", 64)
			}
			rec, err := w.r.beginEnvironmentPreparation(ctx, req, preview, id, control, true)
			if scenario == "changed-preview" {
				if err == nil || rec != nil {
					t.Fatal("changed preview accepted", rec, err)
				}
				if _, err := os.Lstat(filepath.Join(w.home, ".local/state/valise/environment-preparations")); !os.IsNotExist(err) {
					t.Fatal("rejected preview wrote intent", err)
				}
				return
			}
			if err != nil || rec == nil || rec.State != "data_ready" || rec.Native != ref || rec.Mode != settings.Mode {
				t.Fatal(rec, err)
			}
			if rec.Plan.TestOnly || rec.Plan.IsolatedTest != nil || rec.Plan.Carry || rec.Native.MainPID != before.MainPID {
				t.Fatal("formal child reused test/active data")
			}
			assertActiveDataRead(t, ctx, w.home, agent, rec.Original, true)
			if exerciseActivation {
				assertAttachedNative(t, ctx, w, control)
			}
			j, err := w.r.Store.Load(rec.Plan.OpID)
			if err != nil || j.ManagedData == nil || j.ManagedData.ManifestDigest != environments.Digest(rec.Manifest) {
				t.Fatal(j, err)
			}
			original, err := (environments.ProgramStore{Home: w.home}).Load(agent, rec.Original)
			if err != nil || original.Runtime == nil || original.Source != nil {
				t.Fatal(original, err)
			}
			if scenario == "changed-source-before-finish" {
				write(t, filepath.Join(w.state, "tools/npm/lib/node_modules/fixture/new.js"), "new source", 0600)
			}
			if scenario == "incomplete-upload" {
				if got, err := w.r.finishEnvironmentPreparation(ctx, agent, id, control); err == nil || got.State != "data_ready" {
					t.Fatal("incomplete data consumed intent", got, err)
				}
			}
			var data bytes.Buffer
			tw := tar.NewWriter(&data)
			content := "snapshot"
			if scenario == "failed-probe" {
				content = "not snapshot"
			}
			if err := tw.WriteHeader(&tar.Header{Name: "state/SOUL.md", Size: int64(len(content)), Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := StageWrite(w.r.Store, rec.Plan.OpID, &data); err != nil {
				t.Fatal(err)
			}
			rec, err = w.r.finishEnvironmentPreparation(ctx, agent, id, control)
			if scenario == "changed-source-before-finish" {
				if err == nil || rec.State != "data_ready" {
					t.Fatal("changed live programme consumed build intent", rec, err)
				}
			} else if scenario == "failed-probe" {
				if err == nil || rec.State != "failed" || rec.Probe == nil || !rec.Probe.ProcessesStopped || rec.Probe.Verified() {
					t.Fatal("failed probe counted ready", rec, err)
				}
			} else {
				if err != nil || rec.State != "prepared" || rec.Probe == nil || !rec.Probe.Verified() {
					t.Fatal("trusted preparation failed", rec, err)
				}
				programs := environments.ProgramStore{Home: w.home}
				candidate, _, err := programs.Open(ctx, agent, rec.Candidate, rec.CandidateMaterials)
				if err != nil {
					t.Fatal(err)
				}
				if read(t, filepath.Join(candidate.HomeDir(), ".demo/SOUL.md")) != "snapshot" {
					t.Fatal("probe writes entered formal candidate")
				}
				j, err := w.r.Store.Load(rec.Plan.OpID)
				if err != nil || read(t, filepath.Join(j.Roots[0].Stage, "SOUL.md")) != "snapshot" {
					t.Fatal("probe writes entered formal stage", err)
				}
				a := NewBoundActivationRunner(w.r)
				a.runtime = control
				prepared, err := a.Query(ctx, agent, id)
				if err != nil || prepared.Record.State != "ready" || prepared.Record.Plan.NativeSourceMaterials != rec.OriginalMaterials {
					t.Fatal(prepared, err)
				}
				assertActiveRead(t, ctx, w.home, control, agent, rec.Original, true)
				if scenario == "native-restart-read-only" {
					if err := nativeFixtureCommand(ctx, "restart", "--", unit.Unit); err != nil {
						t.Fatal(err)
					}
					reader := activeview.Reader{Home: w.home, Control: control}
					ready := false
					for i := 0; i < 30; i++ {
						current, e := reader.Read(ctx, agent)
						if e == nil && current != nil && current.Gateway.GatewayPassed && current.Current.MainPID != before.MainPID {
							ready = true
							break
						}
						time.Sleep(50 * time.Millisecond)
					}
					if !ready {
						t.Fatal("daily query did not identify restarted native source")
					}
					assertActiveRead(t, ctx, w.home, control, agent, rec.Original, true)
					frozen, e := loadActivation(w.home, agent, id)
					if e != nil || frozen.Plan.NativeOriginal.MainPID != before.MainPID || frozen.State != "ready" || len(frozen.Facts) != 0 {
						t.Fatal("query rewrote frozen cutover", frozen, e)
					}
					result, e := a.Activate(ctx, agent, id)
					if e != nil || result.Record.State != "needs_review" || len(result.Record.Facts) != 0 {
						t.Fatal("old cutover accepted new native PID", result, e)
					}
					if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
						t.Fatal("query or stale cutover changed live data")
					}
					return
				}
				if scenario == "changed-source-before-activation" {
					write(t, filepath.Join(w.state, "tools/npm/lib/node_modules/fixture/new.js"), "new source", 0600)
					if current, err := (activeview.Reader{Home: w.home, Control: control}).Read(ctx, agent); err == nil || current != nil {
						t.Fatal("daily reader trusted changed native source", current, err)
					}
					result, err := a.Activate(ctx, agent, id)
					if err != nil || result.Record.State != "needs_review" || result.Record.FailureCode != "activation_native_source_changed" || len(result.Record.Facts) != 0 {
						t.Fatal("changed JS stopped native Agent", result, err)
					}
				}
			}
			assertActiveDataRead(t, ctx, w.home, agent, rec.Original, true)
			after, err := control.ReadNative(ctx, ref)
			if err != nil || !after.Running || after.MainPID != before.MainPID || after.KernelStart != before.KernelStart || after.UnitFileState != before.UnitFileState {
				t.Fatal("preparation disturbed native source", after, err)
			}
			if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
				t.Fatal("preparation changed active data")
			}
			// Recreate the controller. Neither a repeat begin nor a repeat finish may
			// build/probe again, including after an interrupted or failed intent.
			recordDir, _ := preparationDir(w.home, agent, false)
			path := filepath.Join(recordDir, id+".json")
			saved, _ := os.ReadFile(path)
			fresh := &Runner{Env: w.r.Env, Store: w.r.Store}
			reopened, err := fresh.QueryEnvironmentPreparation(ctx, agent, id)
			if err != nil || environments.Digest(reopened) != environments.Digest(rec) {
				t.Fatal(reopened, err)
			}
			again, err := fresh.beginEnvironmentPreparation(ctx, req, view.Digest, id, control, true)
			if err != nil || environments.Digest(again) != environments.Digest(rec) {
				t.Fatal("begin replayed", again, err)
			}
			if rec.State != "data_ready" {
				if _, err := fresh.finishEnvironmentPreparation(ctx, agent, id, control); err != nil {
					t.Fatal(err)
				}
			}
			unchanged, _ := os.ReadFile(path)
			if !bytes.Equal(saved, unchanged) {
				t.Fatal("reopening rewrote preparation")
			}
			if exerciseActivation {
				a := NewBoundActivationRunner(fresh)
				a.runtime = control
				record, err := loadActivation(w.home, agent, id)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					cleanup, done := context.WithTimeout(context.Background(), time.Minute)
					defer done()
					for _, hash := range []string{record.CandidateDefinition, record.OriginalDefinition} {
						d, err := control.Load(agent, hash)
						if err != nil {
							continue
						}
						if _, err := control.DisableStartup(cleanup, d); err != nil {
							t.Error(err)
							return
						}
						if result, err := control.Stop(cleanup, d); err != nil || !result.Observed.Stopped {
							t.Error(result, err)
							return
						}
						if err := control.Remove(cleanup, d); err != nil {
							t.Error(err)
							return
						}
						os.Remove(filepath.Join(home, ".local/state/valise/managed-runtime", d.Name()+".lock"))
						return
					}
				})
				beforeReview := privateReadFingerprint(t, w.home)
				review, err := a.ReviewSwitch(ctx, agent, id)
				if err != nil || !review.CanSwitch || !review.StageChecked || !review.SourceChecked || !review.ProgramsChecked || len(review.Digest) != 64 {
					t.Fatal("fresh production cutover review failed", review, err)
				}
				if privateReadFingerprint(t, w.home) != beforeReview {
					t.Fatal("cutover review modified private state or material")
				}
				if _, err := a.ConfirmReviewed(ctx, agent, id, review.Digest); err == nil {
					t.Fatal("original gateway or preparation review substituted for candidate conversation")
				}
				// Agent writes remain legal while a human considers the second
				// confirmation. These bytes must be preserved when A is sealed.
				write(t, filepath.Join(w.state, "continued-use"), "A stayed active during review", 0600)
				againReview, err := a.ReviewSwitch(ctx, agent, id)
				if err != nil || againReview.Digest != review.Digest {
					t.Fatal("active data writes invalidated immutable candidate review", againReview, err)
				}
				if _, err := a.ActivateReviewed(ctx, agent, id, strings.Repeat("f", 64), true); err == nil {
					t.Fatal("wrong cutover review stopped native A")
				}
				if _, err := a.ActivateReviewed(ctx, agent, id, review.Digest, false); err == nil {
					t.Fatal("cutover skipped untested-version allowance")
				}
				// A stage changed after the human's review must be rejected before
				// stopping the native source, even when its size is unchanged.
				child, err := fresh.Store.Load(record.Plan.DataOpID)
				if err != nil {
					t.Fatal(err)
				}
				stagePath := filepath.Join(child.Roots[0].Stage, "SOUL.md")
				write(t, stagePath, "tampered", 0600)
				if _, err := a.ActivateReviewed(ctx, agent, id, review.Digest, true); err == nil {
					t.Fatal("changed candidate accepted old human review")
				}
				write(t, stagePath, "snapshot", 0600)
				if _, err := a.ActivateReviewed(ctx, agent, id, review.Digest, true); err == nil {
					t.Fatal("restored bytes with different identity metadata reused old review")
				}
				review, err = a.ReviewSwitch(ctx, agent, id)
				if err != nil || !review.CanSwitch {
					t.Fatal("repaired stage cannot be reviewed anew", review, err)
				}
				if native, err := control.ReadNative(ctx, ref); err != nil || native.MainPID != before.MainPID || !native.Running {
					t.Fatal("rejected review disturbed original", native, err)
				}
				switched, err := a.ActivateReviewed(ctx, agent, id, review.Digest, true)
				if err != nil || switched.Phase != environment.AwaitingHuman || switched.Record.State != "awaiting_human" || switched.Current == nil || !switched.Current.Running || switched.Native == nil || !switched.Native.Stopped || switched.Gateway == nil || !switched.Gateway.GatewayPassed {
					t.Fatal("production preparation did not activate", switched, err)
				}
				assertActiveRead(t, ctx, w.home, control, agent, rec.Candidate, false)
				assertActiveDataRead(t, ctx, w.home, agent, rec.Candidate, false)
				assertAttachedNative(t, ctx, w, control)
				if _, err := fresh.cancelEnvironmentPreparation(ctx, agent, id, control); err == nil {
					t.Fatal("formally switched environment cancelled as preparation")
				}
				same, err := control.Read(ctx, func() managedruntime.Definition {
					d, e := control.Load(agent, record.CandidateDefinition)
					if e != nil {
						t.Fatal(e)
					}
					return d
				}())
				if err != nil || !same.Running || same.KernelStart != switched.Current.KernelStart {
					t.Fatal("failed preparation cancel disturbed candidate", same, err)
				}
				// Discard the controller again: return uses only durable source bindings.
				if cleanupCase {
					if _, err := a.ReviewOldCleanup(ctx, agent, id); err == nil {
						t.Fatal("gateway substituted for cleanup acceptance")
					}
				}
				assertExplicitHumanAcceptanceRetainsOriginal(t, ctx, a, record, w.state)
				if scenario == "accepted-reuse-repeat" {
					assertManagedReusePreparation(t, ctx, a, w, rec.Params, record)
					return
				}
				if scenario == "accepted-reuse-return" {
					assertManagedPreparationSequence(t, ctx, a, w, rec.Params, record, true, true)
					return
				}
				if cleanupCase {
					assertOldCleanupKeepsCandidateAndBackups(t, ctx, a, w, record, scenario == "accepted-cleanup")
					if scenario == "accepted-cleanup-repeat" {
						assertManagedRepeatPreparation(t, ctx, a, w, rec.Params, record)
					}
					return
				}
				a = NewBoundActivationRunner(fresh)
				a.runtime = control
				returnReview, err := a.ReviewSwitch(ctx, agent, id)
				if err != nil || !returnReview.CanReturn || returnReview.CanConfirm || returnReview.CanSwitch || !returnReview.Status.ConversationKnown || returnReview.Status.Phase != environment.Accepted || returnReview.Status.Current == nil || *returnReview.Status.Current != rec.Candidate {
					t.Fatal("running candidate could not be reviewed for explicit return", returnReview, err)
				}
				returned, err := a.ReturnReviewed(ctx, agent, id, returnReview.Digest)
				if err != nil || returned.Phase != environment.RolledBack || returned.Gateway == nil || !returned.Gateway.GatewayPassed || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
					t.Fatal("prepared operation did not return original", returned, err)
				}
				assertActiveDataRead(t, ctx, w.home, agent, rec.Original, false)
				status, err := a.SwitchStatus(ctx, agent, id)
				if err != nil || status.ConversationKnown || status.Current == nil || *status.Current != rec.Original {
					t.Fatal("returned A borrowed candidate's conversation confirmation", status, err)
				}
				assertAttachedNative(t, ctx, w, control)
				if read(t, filepath.Join(w.state, "continued-use")) != "A stayed active during review" {
					t.Fatal("cutover lost original writes made during confirmation")
				}
				child, err = fresh.Store.Load(rec.Plan.OpID)
				if err != nil || read(t, filepath.Join(child.Roots[0].Failed, "dialogue-writes")) != "synthetic interaction writes" || returned.Record.Facts["human_acceptance"].FinishedAt.IsZero() {
					t.Fatal("return discarded accepted candidate writes or its historical human action", child, err)
				}
			}

		})
	}
}
