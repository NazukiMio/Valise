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
	"valise/core/revive"
	"valise/porter/internal/activeview"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

func assertManagedRepeatPreparation(t *testing.T, ctx context.Context, a *ActivationRunner, w *world, input revive.EnvironmentPlanParams, first *ActivationRecord) {
	assertManagedPreparationSequence(t, ctx, a, w, input, first, false, false)
}

func assertManagedReusePreparation(t *testing.T, ctx context.Context, a *ActivationRunner, w *world, input revive.EnvironmentPlanParams, first *ActivationRecord) {
	assertManagedPreparationSequence(t, ctx, a, w, input, first, true, false)
}

func assertManagedPreparationSequence(t *testing.T, ctx context.Context, a *ActivationRunner, w *world, input revive.EnvironmentPlanParams, first *ActivationRecord, retain, returnFirst bool) {
	t.Helper()
	agent := first.Plan.Agent
	control := a.runtime
	reader := activeview.Reader{Home: w.home, Control: control}
	currentRef := first.Plan.Candidate
	phases := []string{"cancel-before-bind", "cancel-after-bind"}
	if returnFirst {
		phases = append(phases, "return-A")
	}
	phases = append(phases, "activate-A", "activate-B")
	for _, phase := range phases {
		settings, err := (environments.Store{Home: w.home}).Settings(agent)
		if err != nil {
			t.Fatal(err)
		}
		input.SettingsDigest = settings.Digest
		before, err := reader.Read(ctx, agent)
		if err != nil || before.Ref != currentRef || !before.Current.Running || !before.ConversationConfirmed {
			t.Fatal("lost source before next preparation", before, err)
		}
		programPath, _ := (environments.ProgramStore{Home: w.home}).RevisionPath(agent, currentRef)
		programBefore, _, _, err := oldCleanupTree(ctx, programPath, false)
		if err != nil {
			t.Fatal(err)
		}
		view, err := w.r.planEnvironment(ctx, input, control)
		if err != nil || !view.ManagedSource || view.Original == nil || *view.Original != currentRef || view.Candidate == nil || view.Candidate.Slot == currentRef.Slot {
			t.Fatal("managed next plan missing", phase, view, err)
		}
		if retain && view.Reuse == nil {
			t.Fatal("normal plan omitted retained environment or cancelled materials", phase)
		}
		op, _ := identity.NewID()
		prepared, err := w.r.beginEnvironmentPreparation(ctx, input, view.Digest, op, control, true)
		if err != nil || prepared.State != "data_ready" || prepared.ManagedSource == nil || prepared.Original != currentRef || prepared.Candidate != *view.Candidate {
			facts, inspectErr := w.r.inspectEnvironmentPlan(ctx, input, control)
			if inspectErr == nil {
				next := &EnvironmentPreparation{ManagedSource: facts.Managed, Reuse: facts.Reuse, Params: input}
				dir, _ := preparationDir(w.home, agent, false)
				entries, _ := os.ReadDir(dir)
				for _, entry := range entries {
					old, loadErr := loadPreparation(w.home, agent, strings.TrimSuffix(entry.Name(), ".json"))
					if loadErr != nil {
						t.Log("prior preparation unreadable", entry.Name(), loadErr)
					} else {
						t.Log("prior preparation", old.State, old.Original, old.Candidate, "allowed", completedPreparationForManagedSource(w.home, next, old))
					}
				}
			} else {
				t.Log("repeat inspection failed", inspectErr)
			}
			t.Fatal("managed next prepare failed", phase, prepared, err)
		}
		t.Log("repeat checkpoint", phase)
		assertHistoricalRestoreRead(t, ctx, a, w, first)
		assertManagedAcceptedRead(t, ctx, w.home, control, agent, currentRef, phase)
		assertActiveDataRead(t, ctx, w.home, agent, currentRef, false)
		for _, claim := range prepared.Claims {
			if claim.Kind == "program-original" {
				t.Fatal("borrowed active programme claimed as new material")
			}
		}
		if phase != "cancel-before-bind" {
			var data bytes.Buffer
			tw := tar.NewWriter(&data)
			for _, item := range prepared.Manifest {
				if item.Path != "state/SOUL.md" {
					t.Fatal("unexpected synthetic snapshot scope", item.Path)
				}
				content := "snapshot"
				if err := tw.WriteHeader(&tar.Header{Name: item.Path, Size: int64(len(content)), Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write([]byte(content)); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := StageWrite(w.r.Store, prepared.Plan.OpID, &data); err != nil {
				t.Fatal(err)
			}
			prepared, err = w.r.finishEnvironmentPreparation(ctx, agent, op, control)
			if err != nil || prepared.State != "prepared" {
				t.Fatal("managed next finish failed", phase, prepared, err)
			}
		}
		after, err := reader.Read(ctx, agent)
		programAfter, _, _, treeErr := oldCleanupTree(ctx, programPath, false)
		if err != nil || after.Ref != currentRef || after.Current.MainPID != before.Current.MainPID || after.Current.KernelStart != before.Current.KernelStart || !after.ConversationConfirmed || treeErr != nil || programAfter != programBefore {
			t.Fatal("preparation touched active programme/process or lost acceptance", phase, err)
		}
		if phase == "cancel-before-bind" || phase == "cancel-after-bind" {
			cancelled, err := w.r.cancelEnvironmentPreparation(ctx, agent, op, control)
			if err != nil || cancelled.State != "cancelled" {
				t.Fatal("managed cancellation failed", phase, cancelled, err)
			}
			t.Log("repeat checkpoint", phase)
			assertManagedAcceptedRead(t, ctx, w.home, control, agent, currentRef, phase)
			assertActiveDataRead(t, ctx, w.home, agent, currentRef, false)
			if retain {
				continue
			}
			cleanup, _, leases, err := w.r.reviewPreparationCleanup(ctx, agent, op, control)
			closePreparationLeases(leases)
			if err != nil {
				t.Fatal("managed cancellation cleanup missing", phase, err)
			}
			for _, item := range cleanup.Items {
				if item.Kind == "program-original" {
					t.Fatal("cleanup offered borrowed active programme")
				}
			}
			if result, err := w.r.cleanupEnvironmentPreparation(ctx, agent, op, cleanup.Digest, control); err != nil || result.State != "cleaned" {
				t.Fatal("managed cancellation cleanup failed", phase, result, err)
			}
			t.Log("repeat checkpoint", phase)
			assertManagedAcceptedRead(t, ctx, w.home, control, agent, currentRef, phase)
			programAfter, _, _, treeErr := oldCleanupTree(ctx, programPath, false)
			if treeErr != nil || programAfter != programBefore {
				t.Fatal("cleanup deleted or rewrote source programme")
			}
			continue
		}
		parent, err := loadActivation(w.home, agent, op)
		if err != nil || parent.Plan.NativeOriginal != nil || parent.Plan.ManagedSourceDigest == "" {
			t.Fatal("next activation lost managed source binding", err)
		}
		// Every later definition is owned by this test. Cleanup always uses
		// the exact definition currently installed, never a service-name scan.
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			for _, hash := range []string{parent.CandidateDefinition, parent.OriginalDefinition} {
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
				os.Remove(filepath.Join(control.Home, ".local/state/valise/managed-runtime", d.Name()+".lock"))
				return
			}
		})
		review, err := a.ReviewSwitch(ctx, agent, op)
		if err != nil || !review.CanSwitch {
			t.Fatal("next switch not ready", phase, review, err)
		}
		status, err := a.ActivateReviewed(ctx, agent, op, review.Digest, true)
		if err != nil || status.Current == nil || !status.Current.Running || status.Phase != environment.AwaitingHuman {
			t.Fatal("next activation failed", phase, status, err)
		}
		review, err = a.ReviewSwitch(ctx, agent, op)
		if phase == "return-A" {
			if err != nil || !review.CanReturn {
				t.Fatal("candidate cannot return before reuse", err)
			}
			returned, err := a.ReturnReviewed(ctx, agent, op, review.Digest)
			if err != nil || returned.Phase != environment.RolledBack || returned.Current == nil || !returned.Current.Running {
				t.Fatal("return before reuse failed", returned, err)
			}
			assertManagedAcceptedRead(t, ctx, w.home, control, agent, currentRef, phase)
			assertActiveDataRead(t, ctx, w.home, agent, currentRef, false)
			continue
		}
		if err != nil || !review.CanConfirm {
			t.Fatal("next candidate cannot be separately accepted", err)
		}
		status, err = a.ConfirmReviewed(ctx, agent, op, review.ConfirmationDigest)
		if err != nil || status.Phase != environment.Accepted {
			t.Fatal("synthetic human acceptance failed", err)
		}
		if !retain {
			old, err := a.ReviewOldCleanup(ctx, agent, op)
			if err != nil {
				t.Fatal("next old cleanup unavailable", err)
			}
			if _, err := a.CleanupOldReviewed(ctx, agent, op, old.Digest); err != nil {
				t.Fatal("next old cleanup failed", err)
			}
		}
		currentRef = prepared.Candidate
		t.Log("repeat checkpoint", phase)
		assertManagedAcceptedRead(t, ctx, w.home, control, agent, currentRef, phase)
		assertActiveDataRead(t, ctx, w.home, agent, currentRef, false)
	}
	if currentRef.Slot != environment.B || currentRef.Revision != 2 {
		t.Fatal("later preparations did not alternate revisions", currentRef)
	}
}

func assertManagedAcceptedRead(t *testing.T, ctx context.Context, home string, control managedruntime.Controller, agent identity.AgentRef, ref environment.Ref, phase string) {
	t.Helper()
	before := privateReadFingerprint(t, home)
	reader := activeview.Reader{Home: home, Control: control}
	current, err := reader.Read(ctx, agent)
	if err != nil || current == nil || current.Ref != ref || current.Native != nil || !current.Current.Running || !current.Gateway.GatewayPassed || !current.ConversationConfirmed {
		data, dataErr := reader.ReadData(ctx, agent)
		registry, _ := (environments.Store{Home: home}).Load(agent)
		t.Fatal("accepted current environment lost during later preparation", phase, ref, err, "data", data != nil, dataErr, "phase", registry.Operation.Phase)
	}
	summary, units, entries := reader.Summary(ctx, agent)
	if summary == nil || !summary.Known || !summary.Running || !summary.ConversationConfirmed || len(units) != 1 || len(entries) != 1 {
		t.Fatal("daily summary lost accepted source", summary)
	}
	if privateReadFingerprint(t, home) != before {
		t.Fatal("query changed private state")
	}
}
