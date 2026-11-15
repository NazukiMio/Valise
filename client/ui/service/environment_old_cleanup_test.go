package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/ssh"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/vault"
)

func TestParallelOldCleanupRequiresPrivateFreshSingleUseReview(t *testing.T) {
	s, _ := enrollWorld(t)
	p := &PreparedOldCleanup{Expires: time.Now().Add(time.Minute)}
	p.View.State = "ready"
	if _, _, err := s.RunOldCleanup(context.Background(), p, nil); journeyCode(err) != "confirmation_required" || p.consumed {
		t.Fatal("display projection authorized deletion", err)
	}
	p.allowed, p.Expires = true, time.Now().Add(-time.Second)
	if _, _, err := s.RunOldCleanup(context.Background(), p, nil); journeyCode(err) != "confirmation_expired" {
		t.Fatal("expired review submitted deletion", err)
	}
	p.Expires = time.Now().Add(time.Minute)
	encoded, _ := json.Marshal(p)
	var copied PreparedOldCleanup
	if json.Unmarshal(encoded, &copied) != nil {
		t.Fatal("cannot decode public projection")
	}
	if _, _, err := s.RunOldCleanup(context.Background(), &copied, nil); journeyCode(err) != "confirmation_required" {
		t.Fatal("serialized projection reconstructed deletion consent", err)
	}
}

func TestParallelOldCleanupLostReplyOnlyQueriesAndPreservesAcceptedRestoreOverFakeSSH(t *testing.T) {
	for _, mode := range []environment.Mode{environment.Compact, environment.Parallel} {
		t.Run(string(mode), func(t *testing.T) {
			s, home := environmentServiceWorld(t)
			ctx := context.Background()
			settings, err := s.InstanceEnvironmentSettings(ctx, "box", "demo")
			if err != nil {
				t.Fatal(err)
			}
			trust, err := s.restoreTrust("box")
			if err != nil {
				t.Fatal(err)
			}
			original, candidate := environment.Ref{Slot: environment.A, Revision: 1}, environment.Ref{Slot: environment.B, Revision: 1}
			task := RestoreTask{Kind: RestoreKindEnvironment, TargetAgent: &settings.Agent, OpID: strings.Repeat("a", 32), Target: "box", From: "box", Instance: "demo", Mode: revive.ModeRollback, EnvironmentMode: mode,
				TrustDigest: trust, Snapshot: journey.SnapshotRef{VaultID: vault.NewID("v", time.Now()), SnapshotID: vault.NewID("s", time.Now())}, State: "succeeded", RuntimeRequested: true, OldCleanupState: "retained"}
			status := environment.SwitchStatus{PreparationParams: parallelTaskParams(task), Snapshot: identity.SnapshotRef{VaultID: task.Snapshot.VaultID, SnapshotID: task.Snapshot.SnapshotID}, Mode: task.EnvironmentMode,
				Original: original, Candidate: candidate, Current: &candidate, Phase: environment.Accepted, CurrentObserved: true, CurrentRunning: true, ConversationKnown: true, CleanupState: "retained", CanReviewOldCleanup: true}
			state := preparationWireState{Switch: &switchWireState{LoseReply: true, Review: environment.SwitchReview{Status: status}, Cleanup: &environment.OldCleanupReview{
				PreparationParams: parallelTaskParams(task), Snapshot: status.Snapshot, Mode: task.EnvironmentMode, Target: original, Current: candidate, State: "ready", Digest: strings.Repeat("d", 64),
				Items: []environment.OldCleanupItem{{Kind: "data", Root: "state", Present: true, Files: 2, Bytes: 20}, {Kind: "program", Present: true, Files: 3, Bytes: 30}, {Kind: "preparation", Present: true, Files: 4, Bytes: 40}},
			}}}
			state.Switch.Cleanup.PolicyMode = mode
			if mode == environment.Parallel {
				state.Switch.Cleanup.PolicyMode = environment.Direct
			}
			statePath := filepath.Join(home, "preparation-wire.json")
			writeState := func() {
				t.Helper()
				b, _ := json.Marshal(state)
				if err := os.WriteFile(statePath, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			readState := func() {
				t.Helper()
				b, err := os.ReadFile(statePath)
				if err != nil || json.Unmarshal(b, &state) != nil {
					t.Fatal("missing wire state", err)
				}
			}
			writeState()
			porterPath := filepath.Join(home, ".local/bin/valise-porter")
			if err := os.Rename(porterPath, porterPath+".original"); err != nil {
				t.Fatal(err)
			}
			exe, _ := os.Executable()
			wrapper := "#!/bin/sh\ncase \"$1\" in\nrpc) VALISE_PREPARATION_WIRE_GUEST=1 exec " + ssh.Quote(exe) + " -test.run=^TestParallelPreparationWireGuest$ -- \"$@\" ;;\n*) exec " + ssh.Quote(porterPath+".original") + " \"$@\" ;;\nesac\n"
			if err := os.WriteFile(porterPath, []byte(wrapper), 0700); err != nil {
				t.Fatal(err)
			}
			prepare := func() (*PreparedOldCleanup, error) {
				if mode == environment.Parallel {
					return s.PrepareCurrentOldCleanup(ctx, task.Target, *task.TargetAgent)
				}
				return s.PrepareOldCleanup(ctx, task)
			}
			p, err := prepare()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(s.restoreTaskPath(task.OpID)); !os.IsNotExist(err) {
				t.Fatal("read-only review created local execution index", err)
			}
			if p.Task().OpID != task.OpID || p.Task().Snapshot != task.Snapshot {
				t.Fatal("settings discovery lost exact operation or backup")
			}
			copy := p.Task()
			copy.TargetAgent.Generation = "another"
			if p.Task().TargetAgent.Generation != task.TargetAgent.Generation {
				t.Fatal("public task accessor could alter private execution identity")
			}
			if _, _, err := s.RunOldCleanup(ctx, p, func(current RestoreTask) {
				if current.Stage == "environment_old_cleanup_login" {
					p.Expires = time.Now().Add(-time.Second)
				}
			}); journeyCode(err) != "confirmation_expired" {
				t.Fatal("management login extended deletion confirmation", err)
			}
			readState()
			if len(state.Switch.Calls) != 0 {
				t.Fatal("expired login sent deletion")
			}
			// A pending cleanup remains a separate fact even if the accepted service
			// is deliberately stopped; no HTTP or fresh conversation is required.
			state.Switch.Review.Status.Phase = environment.Cleaning
			state.Switch.Review.Status.CurrentRunning, state.Switch.Review.Status.CurrentStopped = false, true
			state.Switch.Review.Status.CleanupState, state.Switch.Review.Status.CleanupIssueCode = "pending", "environment_cleanup_interrupted"
			state.Switch.Cleanup.State = "pending"
			writeState()
			observed, _, err := s.InspectParallelSwitch(ctx, task)
			if err != nil || observed.State != "succeeded" || observed.OldCleanupState != "pending" {
				t.Fatal("partial cleanup or stopped service erased accepted restoration", observed, err)
			}
			task = observed
			// Existing local display metadata is retained, but never acts as consent.
			task.SnapshotTime, task.SnapshotTier = time.Now().UTC().Truncate(time.Second), "full"
			if err := s.saveRestore(task); err != nil {
				t.Fatal(err)
			}
			p, err = prepare()
			if err != nil {
				t.Fatal(err)
			}
			if mode == environment.Parallel && (!p.Task().SnapshotTime.Equal(task.SnapshotTime) || p.Task().SnapshotTier != "full") {
				t.Fatal("settings cleanup erased existing snapshot display metadata")
			}
			if p.Task().OldCleanupState != "pending" {
				t.Fatal("reopening pending scope lost cleanup reminder")
			}
			task, actual, err := s.RunOldCleanup(ctx, p, nil)
			if err == nil || actual == nil || !actual.OriginalCleaned || task.State != "succeeded" || task.OldCleanupState != "cleaned" || !actual.ConversationKnown || !actual.CurrentStopped {
				t.Fatal("lost deletion reply was confused with failed restoration", task, actual, err)
			}
			if _, _, err := s.RunOldCleanup(ctx, p, nil); journeyCode(err) != "confirmation_expired" {
				t.Fatal("deletion resent after dropped reply", err)
			}
			before, _ := os.ReadFile(statePath)
			for range 2 {
				if _, _, err := s.InspectParallelSwitch(ctx, task); err != nil {
					t.Fatal(err)
				}
			}
			after, _ := os.ReadFile(statePath)
			if string(before) != string(after) {
				t.Fatal("query replayed cleanup or wrote server metadata")
			}
			readState()
			if len(state.Switch.Calls) != 1 || state.Switch.Calls[0].Method != environment.MethodOldCleanup {
				t.Fatal("unexpected cleanup execution", state.Switch.Calls)
			}
			var fields map[string]any
			if json.Unmarshal(state.Switch.Calls[0].Params, &fields) != nil || len(fields) != 3 || fields["agent"] == nil || fields["operation_id"] == nil || fields["digest"] == nil {
				t.Fatal("cleanup sent paths or claimed evidence", fields)
			}
			finished, err := prepare()
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.RunOldCleanup(ctx, finished, nil); journeyCode(err) != "confirmation_required" {
				t.Fatal("already cleaned review authorized deletion", err)
			}
			raw, err := os.ReadFile(s.restoreTaskPath(task.OpID))
			if err != nil || strings.Contains(string(raw), state.Switch.Cleanup.Digest) || strings.Contains(string(raw), "confirmed") || strings.Contains(string(raw), "cleanup_items") {
				t.Fatal("task stored cleanup consent or private range", err)
			}
			state.Switch.Cleanup.Snapshot.SnapshotID = "another-point"
			writeState()
			if _, err := prepare(); journeyCode(err) != "source_conflict" {
				t.Fatal("cleanup silently changed selected backup", err)
			}
		})
	}
}
