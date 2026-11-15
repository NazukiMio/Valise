package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/client/ssh"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/rpc"
)

// This fixture supplies wire observations only; Linux tests exercise the real
// controller's service stop/start, source verification and data exchange.
type switchWireState struct {
	Review    environment.SwitchReview
	Calls     []rpc.Request
	LoseReply bool
	Cleanup   *environment.OldCleanupReview
}

func preparationSwitchWire(state *preparationWireState, req rpc.Request) (any, *rpc.Error) {
	w := state.Switch
	switch req.Method {
	case environment.MethodSwitchReview:
		return w.Review, nil
	case environment.MethodSwitchStatus:
		return w.Review.Status, nil
	case environment.MethodOldCleanupReview:
		if w.Cleanup == nil {
			os.Exit(28)
		}
		return w.Cleanup, nil
	case environment.MethodCurrentOldCleanup:
		var p environment.SettingsParams
		if w.Cleanup == nil || json.Unmarshal(req.Params, &p) != nil || p.Agent != w.Cleanup.Agent {
			os.Exit(30)
		}
		return w.Cleanup, nil
	case environment.MethodOldCleanup:
		var p environment.ReviewedOperationParams
		if w.Cleanup == nil || json.Unmarshal(req.Params, &p) != nil || p.PreparationParams != w.Cleanup.PreparationParams || p.Digest != w.Cleanup.Digest || w.Cleanup.State == "cleaned" {
			os.Exit(29)
		}
		w.Calls = append(w.Calls, req)
		w.Cleanup.State = "cleaned"
		for i := range w.Cleanup.Items {
			w.Cleanup.Items[i].Present = false
			w.Cleanup.Items[i].Files = 0
			w.Cleanup.Items[i].Bytes = 0
		}
		w.Review.Status.CleanupState, w.Review.Status.OriginalCleaned, w.Review.Status.CanReviewOldCleanup = "cleaned", true, false
	case environment.MethodSwitch:
		var p environment.SwitchParams
		if json.Unmarshal(req.Params, &p) != nil || p.PreparationParams != w.Review.Status.PreparationParams || p.Digest != w.Review.Digest || !p.AllowUntested || !w.Review.CanSwitch {
			os.Exit(25)
		}
		w.Calls = append(w.Calls, req)
		w.Review.CanSwitch, w.Review.CanReturn = false, true
		w.Review.CanConfirm = true
		w.Review.Status.Current = &w.Review.Status.Candidate
		w.Review.Status.State, w.Review.Status.Phase = "awaiting_human", environment.AwaitingHuman
		w.Review.Digest = strings.Repeat("e", 64)
		w.Review.ConfirmationDigest = strings.Repeat("c", 64)
	case environment.MethodConfirm:
		var p environment.ReviewedOperationParams
		if json.Unmarshal(req.Params, &p) != nil || p.PreparationParams != w.Review.Status.PreparationParams || p.Digest != w.Review.ConfirmationDigest || !w.Review.CanConfirm {
			os.Exit(27)
		}
		w.Calls = append(w.Calls, req)
		w.Review.CanConfirm = false
		w.Review.Status.Phase, w.Review.Status.ConversationKnown = environment.Accepted, true
		w.Review.Digest = strings.Repeat("f", 64)
	case environment.MethodReturn:
		var p environment.ReviewedOperationParams
		if json.Unmarshal(req.Params, &p) != nil || p.PreparationParams != w.Review.Status.PreparationParams || p.Digest != w.Review.Digest || !w.Review.CanReturn {
			os.Exit(26)
		}
		w.Calls = append(w.Calls, req)
		w.Review.CanReturn = false
		w.Review.CanConfirm, w.Review.Status.ConversationKnown = false, false
		w.Review.Status.Current = &w.Review.Status.Original
		w.Review.Status.State, w.Review.Status.Phase = "rolled_back", environment.RolledBack
	}
	return w.Review.Status, nil
}

func TestParallelSwitchRequiresFreshSeparateSingleUseReview(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()
	for _, method := range []string{environment.MethodSwitchReview, environment.MethodSwitchStatus, environment.MethodSwitch, environment.MethodReturn, environment.MethodConfirm, environment.MethodOldCleanupReview, environment.MethodOldCleanup, environment.MethodCurrentOldCleanup} {
		if n2Compatibility(&rpc.HelloResult{}, method, nil) == nil {
			t.Fatal("old porter gained formal switch", method)
		}
	}
	p := &PreparedParallelSwitch{Expires: time.Now().Add(time.Minute), allowed: true, untested: true}
	if _, _, err := s.RunParallelSwitch(ctx, p, false, nil); journeyCode(err) != "confirmation_required" || p.consumed {
		t.Fatal("unreviewed capability consumed plan", err)
	}
	p.Expires = time.Now().Add(-time.Second)
	if _, _, err := s.RunParallelSwitch(ctx, p, true, nil); journeyCode(err) != "confirmation_expired" {
		t.Fatal("expired switch was submitted", err)
	}
	p.Expires, p.allowed = time.Now().Add(time.Minute), false
	p.View.CanSwitch = true // Public display fields cannot authorize execution.
	if _, _, err := s.RunParallelSwitch(ctx, p, true, nil); journeyCode(err) != "confirmation_required" || p.consumed {
		t.Fatal("display projection authorized switch", err)
	}
}

func TestParallelSwitchOnlyQueriesLostResponseAndRequiresNewReturnReviewOverFakeSSH(t *testing.T) {
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
	task := RestoreTask{Kind: RestoreKindEnvironment, TargetAgent: &settings.Agent, OpID: strings.Repeat("a", 32), Target: "box", From: "box", Instance: "demo", Mode: revive.ModeRollback, EnvironmentMode: environment.Parallel,
		TrustDigest: trust, Snapshot: journey.SnapshotRef{VaultID: "selected-vault", SnapshotID: "selected-point"}, State: "prepared"}
	state := preparationWireState{Switch: &switchWireState{LoseReply: true, Review: environment.SwitchReview{Digest: strings.Repeat("d", 64), CanSwitch: true, ProgramsChecked: true, OriginalProgramChecked: true, CandidateProgramChecked: true, StageChecked: true, SourceChecked: true, ProfileUntested: true,
		Status: environment.SwitchStatus{PreparationParams: parallelTaskParams(task), Snapshot: identity.SnapshotRef{VaultID: task.Snapshot.VaultID, SnapshotID: task.Snapshot.SnapshotID}, Mode: task.EnvironmentMode,
			Original: original, Candidate: candidate, Current: &original, State: "ready", Phase: environment.Prepared, CurrentObserved: true, CurrentRunning: true, GatewayObserved: true, PortOwned: true, HTTPResponded: true, GatewayPassed: true}}}}
	statePath := filepath.Join(home, "preparation-wire.json")
	writeState := func() {
		b, _ := json.Marshal(state)
		if err := os.WriteFile(statePath, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	readState := func() {
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
	if err := s.saveRestore(task); err != nil {
		t.Fatal(err)
	}
	p, err := s.PrepareParallelSwitch(ctx, task, "switch")
	if err != nil {
		t.Fatal(err)
	}
	// Waiting for management login cannot extend the human review. Expire it
	// at the real client's waiting callback, before its separate connection.
	if _, _, err := s.RunParallelSwitch(ctx, p, true, func(current RestoreTask) {
		if current.Stage == "environment_switch_login" {
			p.Expires = time.Now().Add(-time.Second)
		}
	}); journeyCode(err) != "confirmation_expired" {
		t.Fatal("management login extended expired review", err)
	}
	readState()
	if len(state.Switch.Calls) != 0 {
		t.Fatal("expired review sent an operation", state.Switch.Calls)
	}
	p, err = s.PrepareParallelSwitch(ctx, task, "switch")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RunParallelSwitch(ctx, p, false, nil); journeyCode(err) != "confirmation_required" {
		t.Fatal(err)
	}
	task, status, err := s.RunParallelSwitch(ctx, p, true, nil)
	if err == nil || status == nil || task.State != "awaiting_human" || !task.RuntimeRequested || status.ConversationKnown {
		t.Fatal("lost response confused current runtime or dialogue", task, status, err)
	}
	if _, _, err := s.RunParallelSwitch(ctx, p, true, nil); journeyCode(err) != "confirmation_expired" {
		t.Fatal("switch resent", err)
	}
	before, _ := os.ReadFile(statePath)
	for range 2 {
		if _, _, err := s.InspectParallelSwitch(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(statePath)
	if string(before) != string(after) {
		t.Fatal("query changed operation or replayed switch")
	}
	confirmed, err := s.PrepareParallelSwitch(ctx, task, "confirm")
	if err != nil {
		t.Fatal(err)
	}
	task, status, err = s.RunParallelSwitch(ctx, confirmed, false, nil)
	if err == nil || status == nil || !status.ConversationKnown || task.State != "succeeded" || status.Phase != environment.Accepted || status.OriginalCleaned {
		t.Fatal("explicit acceptance confused with gateway, cleanup or lost response", task, status, err)
	}
	if _, _, err := s.RunParallelSwitch(ctx, confirmed, false, nil); journeyCode(err) != "confirmation_expired" {
		t.Fatal("human acceptance resent after response loss", err)
	}
	returnPlan, err := s.PrepareParallelSwitch(ctx, task, "return")
	if err != nil {
		t.Fatal(err)
	}
	task, status, err = s.RunParallelSwitch(ctx, returnPlan, false, nil)
	if err == nil || status == nil || task.State != "failed" || status.Current == nil || *status.Current != original {
		t.Fatal("return response was replayed or candidate became current", task, status, err)
	}
	readState()
	if len(state.Switch.Calls) != 3 || state.Switch.Calls[0].Method != environment.MethodSwitch || state.Switch.Calls[1].Method != environment.MethodConfirm || state.Switch.Calls[2].Method != environment.MethodReturn {
		t.Fatal("unexpected execution", state.Switch.Calls)
	}
	for _, call := range state.Switch.Calls {
		var fields map[string]any
		if json.Unmarshal(call.Params, &fields) != nil {
			t.Fatal("bad params")
		}
		for key := range fields {
			if !slices.Contains([]string{"agent", "operation_id", "digest", "allow_untested"}, key) {
				t.Fatal("unexpected execution input", key)
			}
		}
	}
	raw, err := os.ReadFile(s.restoreTaskPath(task.OpID))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"allow_untested", "confirmed", "gateway_passed", "original_program_checked", state.Switch.Review.Digest} {
		if strings.Contains(string(raw), private) {
			t.Fatal("task persisted execution review or consent", private)
		}
	}
	// No human confirmation may be inferred from a working HTTP gateway.
	state.Switch.Review.Status.Phase = environment.Accepted
	state.Switch.Review.Status.Current = &candidate
	writeState()
	if observed, _, err := s.InspectParallelSwitch(ctx, task); err != nil || observed.State == "succeeded" {
		t.Fatal("gateway substituted for dialogue", observed, err)
	}
	// A copied/different point must fail even if all observation booleans pass.
	state.Switch.Review.Status.Snapshot.SnapshotID = "another-point"
	writeState()
	if _, _, err := s.InspectParallelSwitch(ctx, task); journeyCode(err) != "source_conflict" {
		t.Fatal("different backup accepted", err)
	}
	state.Switch.Review.Status.Snapshot.SnapshotID = task.Snapshot.SnapshotID
	state.Switch.Review.Status.Phase = environment.AwaitingHuman
	state.Switch.Review.Status.Current = &original
	state.Switch.Review.CanConfirm = true
	writeState()
	if _, err := s.PrepareParallelSwitch(ctx, task, "confirm"); journeyCode(err) != "source_conflict" {
		t.Fatal("a boolean without actual candidate identity could confirm", err)
	}
}
