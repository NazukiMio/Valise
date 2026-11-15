package service

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/client/ssh"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// A fake-ssh wire fixture, not a Linux readiness or production-source proof.
// The real controller/kernel is tested separately. This guest verifies the
// client's decrypted tar, parent/child addressing, exactly-once calls and lost
// response handling without adding any execution hook to production code.
type preparationWireState struct {
	Status    environment.PreparationStatus
	Receipt   environment.PreparationReceipt
	Manifest  []revive.ManifestEntry
	Calls     []string
	Uploaded  bool
	LoseReply bool
	Switch    *switchWireState `json:"switch,omitempty"`
}

func TestParallelPreparationWireGuest(t *testing.T) {
	if os.Getenv("VALISE_PREPARATION_WIRE_GUEST") != "1" {
		return
	}
	args := flag.Args()
	path := filepath.Join(os.Getenv("HOME"), "preparation-wire.json")
	var state preparationWireState
	if b, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(b, &state) != nil {
			os.Exit(11)
		}
	}
	save := func() {
		b, _ := json.Marshal(state)
		if os.WriteFile(path, b, 0600) != nil {
			os.Exit(12)
		}
	}
	if len(args) == 3 && args[0] == "stage-write" && args[1] == "--op" {
		if args[2] != state.Receipt.DataOperation || state.Uploaded {
			os.Exit(13)
		}
		want := map[string]revive.ManifestEntry{}
		for _, item := range state.Manifest {
			want[item.Path] = item
		}
		tr := tar.NewReader(os.Stdin)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				os.Exit(14)
			}
			item, ok := want[h.Name]
			hash := sha256.New()
			n, err := io.Copy(hash, tr)
			if !ok || err != nil || n != item.Size || hex.EncodeToString(hash.Sum(nil)) != item.SHA256 || h.Typeflag != tar.TypeReg {
				os.Exit(15)
			}
			delete(want, h.Name)
		}
		if len(want) != 0 {
			os.Exit(16)
		}
		state.Uploaded = true
		state.Calls = append(state.Calls, "stage-write")
		save()
		os.Exit(0)
	}
	if len(args) != 1 || args[0] != "rpc" {
		os.Exit(17)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		var req rpc.Request
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(18)
		}
		var out any
		var failure *rpc.Error
		switch req.Method {
		case rpc.MethodHello:
			caps := []string{environment.PreparationCapability}
			if state.Switch != nil {
				caps = append(caps, environment.SwitchCapability)
			}
			out = rpc.HelloResult{Protocol: rpc.ProtocolVersion, Home: os.Getenv("HOME"), OS: runtime.GOOS, Arch: runtime.GOARCH, Capabilities: caps}
		case environment.MethodSwitchReview, environment.MethodSwitchStatus, environment.MethodSwitch, environment.MethodReturn, environment.MethodConfirm, environment.MethodOldCleanupReview, environment.MethodOldCleanup, environment.MethodCurrentOldCleanup:
			if state.Switch == nil {
				os.Exit(24)
			}
			out, failure = preparationSwitchWire(&state, req)
			if req.Method == environment.MethodSwitch || req.Method == environment.MethodReturn || req.Method == environment.MethodConfirm || req.Method == environment.MethodOldCleanup {
				save()
				if state.Switch.LoseReply {
					os.Exit(0) // Commit, then drop the RPC stream before a reply.
				}
			}
		case environment.MethodPreparationBegin:
			var p revive.EnvironmentPreparationParams
			if json.Unmarshal(req.Params, &p) != nil || state.Receipt.OperationID != "" {
				os.Exit(19)
			}
			state.Receipt = environment.PreparationReceipt{Agent: p.Agent, OperationID: p.OperationID, Snapshot: identity.SnapshotRef{VaultID: p.Input.VaultID, SnapshotID: p.Input.Header.SnapshotID}, State: "data_ready", DataOperation: revive.NewOpID(time.Now()), BindingDigest: strings.Repeat("a", 64)}
			state.Receipt.ManifestDigest, _ = protection.Digest(state.Manifest)
			state.Status = environment.PreparationStatus{Agent: p.Agent, OperationID: p.OperationID, Snapshot: state.Receipt.Snapshot, State: "data_ready", Mode: environment.Parallel, Phase: environment.Building, Materials: "retained", Probe: "not_started", OriginalObserved: true, OriginalRunning: true, CanCancel: true}
			state.Calls = append(state.Calls, req.Method)
			save()
			out = state.Receipt
		case environment.MethodPreparationFinish:
			if !state.Uploaded || state.Status.State != "data_ready" {
				os.Exit(20)
			}
			state.Status.State, state.Status.Phase, state.Status.Probe = "prepared", environment.Prepared, "stopped"
			state.Status.Check = &environment.ProbeResult{Completed: true, ProcessStarted: true, PortListening: true, HTTPResponded: true, GatewayPassed: true, ProcessesStopped: true}
			state.Calls = append(state.Calls, req.Method)
			save()
			out = state.Status
			if state.LoseReply {
				failure = &rpc.Error{Code: rpc.CodeUserError, Message: "fixture lost response", Data: valise.New("fixture_lost_response", "合成丢失响应", "查询原操作")}
			}
		case environment.MethodPreparationStatus:
			out = state.Status
		case environment.MethodPreparationCancel:
			state.Status.State, state.Status.CanCancel, state.Status.CanReviewCleanup = "cancelled", false, true
			state.Calls = append(state.Calls, req.Method)
			save()
			out = state.Status
		case environment.MethodPreparationCleanupReview:
			out = environment.PreparationCleanupReview{PreparationParams: environment.PreparationParams{Agent: state.Status.Agent, OperationID: state.Status.OperationID}, Snapshot: state.Status.Snapshot, State: "cancelled", Digest: strings.Repeat("c", 64), Items: []environment.PreparationCleanupItem{{Kind: "workspace", Present: true}}}
		case environment.MethodPreparationCleanup:
			var p environment.PreparationCleanupParams
			if json.Unmarshal(req.Params, &p) != nil || p.Digest != strings.Repeat("c", 64) || state.Status.State != "cancelled" {
				os.Exit(21)
			}
			state.Status.State, state.Status.Materials, state.Status.Retired, state.Status.CanReviewCleanup = "cleaned", "cleared", true, false
			state.Calls = append(state.Calls, req.Method)
			save()
			out = state.Status
		default:
			os.Exit(22) // No legacy swap/resume or generic execution is allowed.
		}
		b, _ := json.Marshal(out)
		if failure != nil {
			b = nil
		}
		if json.NewEncoder(os.Stdout).Encode(rpc.Response{JSONRPC: rpc.JSONRPCVersion, ID: req.ID, Result: b, Error: failure}) != nil {
			os.Exit(23)
		}
	}
	os.Exit(0)
}

func TestParallelPreparationTransfersChosenDataAndOnlyQueriesLostResponseOverFakeSSH(t *testing.T) {
	s, home := environmentServiceWorld(t)
	ctx := context.Background()
	sealed, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: "full"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListTimeline(ctx, JourneyListRequest{AllLocations: true, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("missing selected backup", page, err)
	}
	ref := page.Items[0].SnapshotRef
	rp, err := s.planRevive(ctx, ReviveOptions{Target: "box", From: "box", Mode: revive.ModeRollback, SnapshotID: sealed.SnapshotID, testOnly: true, AllowDraft: true, AllowSnapshotProfile: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	head, files, err := rp.vault.Snapshot(sealed.SnapshotID, rp.ident)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	trust, err := s.restoreTrust("box")
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(home, "preparation-wire.json")
	state := preparationWireState{Manifest: parallelManifest(rp), LoseReply: true}
	encoded, _ := json.Marshal(state)
	if err := os.WriteFile(statePath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	porterPath := filepath.Join(home, ".local/bin/valise-porter")
	if err := os.Rename(porterPath, porterPath+".original"); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	wrapper := "#!/bin/sh\ncase \"$1\" in\nrpc|stage-write) VALISE_PREPARATION_WIRE_GUEST=1 exec " + ssh.Quote(exe) + " -test.run=^TestParallelPreparationWireGuest$ -- \"$@\" ;;\n*) exec " + ssh.Quote(porterPath+".original") + " \"$@\" ;;\nesac\n"
	if err := os.WriteFile(porterPath, []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	p := &PreparedParallelRestore{View: revive.EnvironmentPlan{Mode: environment.Parallel, Digest: strings.Repeat("b", 64)}, plan: rp, trust: trust, Expires: time.Now().Add(time.Minute),
		request: RestoreRequest{Snapshot: ref}, params: revive.EnvironmentPlanParams{Agent: settings.Agent, SettingsDigest: settings.Digest, Input: environment.TestInput{VaultID: ref.VaultID, HeaderDigest: strings.Repeat("d", 64), Header: *head, Files: *files}}}
	task, err := s.RunParallelPreparation(ctx, p, ParallelPrepareChoice{Credentials: true}, nil)
	if journeyCode(err) != "fixture_lost_response" || task.State != "prepared" || !task.Verified {
		t.Fatal("lost finish response was replayed or not reconciled", task, err)
	}
	before, _ := os.ReadFile(statePath)
	for range 2 {
		if _, _, err := s.InspectParallelPreparation(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(statePath)
	if string(before) != string(after) {
		t.Fatal("query changed or replayed fixture operation")
	}
	if _, err := s.RunParallelPreparation(ctx, p, ParallelPrepareChoice{}, nil); journeyCode(err) != "confirmation_expired" {
		t.Fatal("consumed human plan replayed")
	}
	if _, _, err := s.CancelParallelPreparation(ctx, task); err != nil {
		t.Fatal(err)
	}
	review, err := s.ReviewParallelPreparationCleanup(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	cleaned, status, err := s.CleanupParallelPreparation(ctx, task, *review)
	if err != nil || cleaned.State != "cleaned" || status == nil || !status.Retired {
		t.Fatal("cleaned preparation still claims retained materials", cleaned, status, err)
	}
	encoded, _ = os.ReadFile(statePath)
	if json.Unmarshal(encoded, &state) != nil || !slices.Equal(state.Calls, []string{environment.MethodPreparationBegin, "stage-write", environment.MethodPreparationFinish, environment.MethodPreparationCancel, environment.MethodPreparationCleanup}) {
		t.Fatal("unexpected execution or retry", state.Calls)
	}
	if readFile(t, filepath.Join(home, ".demo/memory/a.md")) != "m" {
		t.Fatal("client transfer modified original data")
	}
}
