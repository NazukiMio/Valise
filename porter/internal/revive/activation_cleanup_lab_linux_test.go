//go:build linux

package revive

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"filippo.io/age"
	"valise/core/environment"
	"valise/core/vault"
	"valise/porter/internal/activeview"
	"valise/porter/internal/environments"
	"valise/porter/internal/seal"
)

func cleanupVaultFingerprint(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = vault.SHA256Hex(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertOldCleanupKeepsCandidateAndBackups(t *testing.T, ctx context.Context, a *ActivationRunner, w *world, rec *ActivationRecord, interrupt bool) {
	t.Helper()
	agent, op := rec.Plan.Agent, rec.Plan.OperationID
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := seal.AddRecipient(seal.Paths{Home: w.home}, id.Recipient().String(), "cleanup-fixture"); err != nil {
		t.Fatal(err)
	}
	beforeBackup, files := sealActiveFixture(t, &managedWorld{world: w}, id, vault.TierFull)
	if files["state:dialogue-writes"] != "synthetic interaction writes" || beforeBackup.Runtime == nil {
		t.Fatal("missing current backup before cleanup")
	}
	backups := cleanupVaultFingerprint(t, (seal.Paths{Home: w.home}).Vaults())
	store := environments.Store{Home: w.home}
	policy, err := store.Settings(agent)
	if err != nil {
		t.Fatal(err)
	}
	directCleanup := policy.Mode == environment.Parallel
	if directCleanup {
		if _, err := a.ReviewOldCleanup(ctx, agent, op); err == nil {
			t.Fatal("retaining mode allowed old deletion")
		}
		if _, err := a.ReviewCurrentOldCleanup(ctx, agent); err == nil {
			t.Fatal("settings cleanup did not require saved direct policy")
		}
		beforeCurrent, err := (activeview.Reader{Home: w.home, Control: a.runtime}).Read(ctx, agent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetSettings(environment.SetSettingsParams{Agent: agent, Expected: policy.Digest, Mode: environment.Direct}); err != nil {
			t.Fatal(err)
		}
		afterCurrent, err := (activeview.Reader{Home: w.home, Control: a.runtime}).Read(ctx, agent)
		if err != nil || afterCurrent.Current.MainPID != beforeCurrent.Current.MainPID || afterCurrent.Current.KernelStart != beforeCurrent.Current.KernelStart || afterCurrent.Ref != beforeCurrent.Ref || !afterCurrent.ConversationConfirmed {
			t.Fatal("saving direct policy restarted or replaced current environment", err)
		}
	}
	before := privateReadFingerprint(t, w.home)
	view, err := a.ReviewOldCleanup(ctx, agent, op)
	if err != nil || view.State != "ready" || len(view.Items) != 3 || view.Target != rec.Plan.Original || view.Current != rec.Plan.Candidate {
		t.Fatal("missing fixed old scope", view, err)
	}
	if privateReadFingerprint(t, w.home) != before {
		t.Fatal("cleanup review wrote state")
	}
	if directCleanup {
		settingsView, err := a.ReviewCurrentOldCleanup(ctx, agent)
		if err != nil || settingsView.Digest != view.Digest || settingsView.Mode != environment.Parallel || settingsView.PolicyMode != environment.Direct {
			t.Fatal("settings discovery lost original operation or policy", settingsView, err)
		}
		currentPolicy, _ := store.Settings(agent)
		if _, err := store.SetSettings(environment.SetSettingsParams{Agent: agent, Expected: currentPolicy.Digest, Mode: environment.Parallel}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.CleanupOldReviewed(ctx, agent, op, view.Digest); err == nil {
			t.Fatal("stale direct policy authorized deletion")
		}
		currentPolicy, _ = store.Settings(agent)
		if _, err := store.SetSettings(environment.SetSettingsParams{Agent: agent, Expected: currentPolicy.Digest, Mode: environment.Direct}); err != nil {
			t.Fatal(err)
		}
		view, err = a.ReviewCurrentOldCleanup(ctx, agent)
		if err != nil {
			t.Fatal(err)
		}
	}
	j, err := w.r.Store.Load(rec.Plan.DataOpID)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(w.state, "still-active"), "B continued running", 0600)
	again, err := a.ReviewOldCleanup(ctx, agent, op)
	if err != nil || again.Digest != view.Digest {
		t.Fatal("active candidate writes changed deletion consent", again, err)
	}
	write(t, filepath.Join(j.Roots[0].Prev, "late-file"), "new old data", 0600)
	if _, err := a.CleanupOldReviewed(ctx, agent, op, view.Digest); err == nil {
		t.Fatal("changed old tree accepted stale review")
	}
	view, err = a.ReviewOldCleanup(ctx, agent, op)
	if err != nil {
		t.Fatal(err)
	}
	owned := j.Roots[0].Prev + ".owned"
	if err := os.Rename(j.Roots[0].Prev, owned); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(j.Roots[0].Prev, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(j.Roots[0].Prev, "foreign"), "preserve", 0600)
	if _, err := a.CleanupOldReviewed(ctx, agent, op, view.Digest); err == nil {
		t.Fatal("replacement directory deleted")
	}
	foreign := filepath.Join(t.TempDir(), "foreign-directory")
	if err := os.Rename(j.Roots[0].Prev, foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(owned, j.Roots[0].Prev); err != nil {
		t.Fatal(err)
	}
	// Even an otherwise valid, unregistered backup descriptor must be retained.
	marker := filepath.Join(j.Roots[0].Prev, "nested-backup", vault.DescriptorFile)
	write(t, marker, `{"format":"valise-vault/1"}`, 0600)
	if _, err := a.ReviewOldCleanup(ctx, agent, op); err == nil {
		t.Fatal("nested backup became a deletion target")
	}
	if err := os.Rename(filepath.Dir(marker), filepath.Join(t.TempDir(), "retained-backup")); err != nil {
		t.Fatal(err)
	}
	if interrupt {
		p, err := loadPreparation(w.home, agent, op)
		if err != nil {
			t.Fatal(err)
		}
		dir, err := preparationWorkspace(w.home, p, false)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 10000; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("cleanup-%05d", i)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		view, err = a.ReviewOldCleanup(ctx, agent, op)
		if err != nil {
			t.Fatal(err)
		}
		cw := &cancellationWorld{world: w, params: p.Params, op: op, control: a.runtime}
		cmd := cw.worker(t, "old-cleanup", view.Digest)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		waited := false
		defer func() {
			if !waited {
				_ = cmd.Process.Kill()
				<-done
			}
		}()
		until, observed := time.Now().Add(15*time.Second), false
		for time.Now().Before(until) {
			select {
			case err := <-done:
				waited = true
				t.Fatalf("cleanup worker exited before interruption: %v", err)
			default:
			}
			saved, _ := loadActivation(w.home, agent, op)
			if saved != nil && saved.Cleanup != nil && saved.Cleanup.Items[1].Removed && !saved.Cleanup.Complete {
				observed = true
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !observed {
			t.Fatal("did not observe partial real deletion")
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		<-done
		waited = true
		before = privateReadFingerprint(t, w.home)
		status, err := a.SwitchStatus(ctx, agent, op)
		if err != nil || status.Phase != environment.Cleaning || !status.ConversationKnown || !status.CurrentRunning || status.CleanupState != "pending" {
			t.Fatal("partial cleanup rewrote accepted runtime", status, err)
		}
		if privateReadFingerprint(t, w.home) != before {
			t.Fatal("query resumed deletion")
		}
	}
	view, err = a.ReviewOldCleanup(ctx, agent, op)
	if err != nil {
		t.Fatal("remaining fixed scope cannot be reviewed", err)
	}
	status, err := a.CleanupOldReviewed(ctx, agent, op, view.Digest)
	if err != nil || status.CleanupState != "cleaned" || !status.OriginalCleaned || !status.ConversationKnown || !status.CurrentRunning || status.Current == nil || *status.Current != rec.Plan.Candidate {
		t.Fatal("old cleanup disturbed current candidate", status, err)
	}
	if read(t, filepath.Join(w.state, "still-active")) != "B continued running" || read(t, filepath.Join(foreign, "foreign")) != "preserve" {
		t.Fatal("cleanup deleted unrelated or current data")
	}
	for _, path := range []string{j.Roots[0].Prev} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("old root retained", err)
		}
	}
	if !reflect.DeepEqual(backups, cleanupVaultFingerprint(t, (seal.Paths{Home: w.home}).Vaults())) {
		t.Fatal("cleanup changed backup bytes")
	}
	assertActiveDataRead(t, ctx, w.home, agent, rec.Plan.Candidate, false)
	assertAttachedNative(t, ctx, w, a.runtime)
	afterBackup, data := sealActiveFixture(t, &managedWorld{world: w}, id, vault.TierFull)
	if data["state:still-active"] != "B continued running" || afterBackup.Runtime == nil || len(afterBackup.Runtime.Dependencies) != len(beforeBackup.Runtime.Dependencies) {
		t.Fatal("current backup lost data or independent source after cleanup")
	}
	before = privateReadFingerprint(t, w.home)
	if _, err := a.CleanupOldReviewed(ctx, agent, op, view.Digest); err != nil {
		t.Fatal(err)
	}
	if privateReadFingerprint(t, w.home) != before {
		t.Fatal("repeated cleaned operation wrote state")
	}
	checked, err := a.ReviewSwitch(ctx, agent, op)
	if err != nil || checked.CanReturn || checked.Status.IssueCode != "" {
		t.Fatal("expected deleted programme rendered damaged or returnable", checked, err)
	}
	if _, err := a.ReturnReviewed(ctx, agent, op, view.Digest); err == nil {
		t.Fatal("cleared original could be restored")
	}
	current, err := (activeview.Reader{Home: w.home, Control: a.runtime}).Read(ctx, agent)
	if err != nil || current == nil || !current.Current.Running || !current.ConversationConfirmed {
		t.Fatal("cleanup lost current binding", current, err)
	}
	if directCleanup {
		settings, err := store.Settings(agent)
		if err != nil || settings.Mode != environment.Direct || settings.Busy || !settings.Managed {
			t.Fatal("cleanup changed direct policy or removed active management", settings, err)
		}
	}
}
