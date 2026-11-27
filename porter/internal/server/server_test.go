package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/rpc"
	porterenroll "valise/porter/internal/enroll"
	porteridentity "valise/porter/internal/identity"
	porterrevive "valise/porter/internal/revive"
	"valise/porter/internal/seal"
)

func serve(t *testing.T, home string, lines ...string) []rpc.Response {
	t.Helper()
	var out bytes.Buffer
	cfg := Config{Version: "test", Home: home, DetectTools: func(context.Context) []rpc.Tool {
		return []rpc.Tool{{Name: rpc.ToolGit, Available: true, Path: "/usr/bin/git"}}
	}}
	if err := Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, cfg); err != nil {
		t.Fatal(err)
	}
	var resps []rpc.Response
	dec := json.NewDecoder(&out)
	for dec.More() {
		var r rpc.Response
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		resps = append(resps, r)
	}
	return resps
}

const helloReq = `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"client_version":"t","protocol":"1.0"}}`

func TestHello(t *testing.T) {
	home := t.TempDir()
	st := porterrevive.Store{Home: home}
	a, b := revive.NewOpID(time.UnixMilli(1)), revive.NewOpID(time.UnixMilli(2))
	for _, op := range []string{b, a} {
		if err := st.Save(&revive.Journal{Plan: revive.Plan{OpID: op, InstanceID: "demo", Mode: revive.ModeRollback}, State: revive.StateLiveMoved}); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(JournalDir(home), "notes.txt"), []byte("x"), 0o600)
	resps := serve(t, home, helloReq)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("%+v", resps)
	}
	var h rpc.HelloResult
	if err := json.Unmarshal(resps[0].Result, &h); err != nil {
		t.Fatal(err)
	}
	if h.Home != home || h.Protocol != rpc.ProtocolVersion || h.PorterVersion != "test" || h.Arch == "" {
		t.Fatalf("%+v", h)
	}
	if len(h.PendingOps) != 2 || h.PendingOps[0].OpID != a || h.PendingOps[1].State != revive.StateLiveMoved {
		t.Fatalf("pending = %+v", h.PendingOps)
	}
}

func TestHelloNoJournalDir(t *testing.T) {
	resps := serve(t, t.TempDir(), helloReq)
	var h rpc.HelloResult
	if err := json.Unmarshal(resps[0].Result, &h); err != nil || h.PendingOps == nil || len(h.PendingOps) != 0 {
		t.Fatalf("%s %v", resps[0].Result, err)
	}
}

func TestHelloRequiredFirst(t *testing.T) {
	resps := serve(t, t.TempDir(),
		`{"jsonrpc":"2.0","id":1,"method":"status"}`,
		helloReq,
		`{"jsonrpc":"2.0","id":3,"method":"status"}`,
	)
	if resps[0].Error == nil || resps[0].Error.Code != rpc.CodeHelloRequired {
		t.Fatalf("%+v", resps[0])
	}
	if resps[1].Error != nil {
		t.Fatalf("%+v", resps[1])
	}
	if resps[2].Error == nil || resps[2].Error.Code != rpc.CodeMethodNotFound || string(resps[2].ID) != "3" {
		t.Fatalf("%+v", resps[2])
	}
}

func TestProtocolMismatch(t *testing.T) {
	resps := serve(t, t.TempDir(), `{"jsonrpc":"2.0","id":"a","method":"hello","params":{"protocol":"2.0"}}`)
	e := resps[0].Error
	if e == nil || e.Code != rpc.CodeUserError || e.Data == nil || e.Data.Code != "protocol_mismatch" {
		t.Fatalf("%+v", resps[0])
	}
}

func TestBadInput(t *testing.T) {
	resps := serve(t, t.TempDir(), `not json`, `{"jsonrpc":"1.0","id":1,"method":"hello"}`)
	if resps[0].Error.Code != rpc.CodeParseError || resps[1].Error.Code != rpc.CodeInvalidRequest {
		t.Fatalf("%+v", resps)
	}
}

// Every porter method must be classified into a tier
// (docs/ARCHITECTURE.md 2.2).
func TestMethodsClassified(t *testing.T) {
	for m := range methods {
		if !rpc.Classified(m) {
			t.Errorf("method %q has no tier in core/rpc/tier.go", m)
		}
	}
}

func TestInstanceListMissingRootsAndInstanceForget(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, ".hermes")
	wsDir := filepath.Join(home, "workspace")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{
		ID: "hermes", ProfileVersion: 14, Status: "draft",
		Roots: []profile.Root{{ID: "workspace", Default: wsDir}},
	}
	rec := enroll.Record{
		InstanceID: "hermes", ProfileID: "hermes", ProfileVersion: 14,
		Roots: map[string]string{"state": stateDir, "workspace": wsDir},
	}
	if _, _, err := porterenroll.AcceptRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}

	// Simulate protection plan, pending change, timer units, proposals, declaration, vault & prev dir.
	protFile := filepath.Join(home, ".config", "valise", "protection", "hermes.json")
	protChangeFile := filepath.Join(home, ".local", "state", "valise", "protection-changes", "hermes.json")
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	tmrFile := filepath.Join(unitDir, "valise-seal@hermes.timer")
	svcFile := filepath.Join(unitDir, "valise-seal@hermes.service")
	propFile := filepath.Join(porterenroll.InstancesDir(home), "hermes.proposals.json")
	declFile := filepath.Join(porterenroll.InstancesDir(home), "hermes.declaration.json")
	vaultDir := filepath.Join(home, ".local", "share", "valise", "vaults", "hermes", "2026-11")
	prevDir := filepath.Join(home, ".valise-prev-op1-state")

	for _, f := range []string{protFile, protChangeFile, tmrFile, svcFile, propFile, declFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{vaultDir, prevDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "keep.txt"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Both roots exist -> MissingRoots is empty.
	resps := serve(t, home, helloReq, `{"jsonrpc":"2.0","id":2,"method":"instance_list","params":{}}`)
	var h rpc.HelloResult
	if err := json.Unmarshal(resps[0].Result, &h); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.Capabilities, enroll.ForgetCapability) {
		t.Fatalf("hello capabilities missing %q: %v", enroll.ForgetCapability, h.Capabilities)
	}
	var list1 enroll.InstanceListResult
	if err := json.Unmarshal(resps[1].Result, &list1); err != nil {
		t.Fatal(err)
	}
	if len(list1.Instances) != 1 || len(list1.Instances[0].MissingRoots) != 0 || list1.Instances[0].StateMissing() {
		t.Fatalf("unexpected MissingRoots when state exists: %+v", list1.Instances)
	}

	// 2. Remove stateDir outside Valise -> MissingRoots contains "state", StateMissing() is true.
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}
	resps = serve(t, home, helloReq, `{"jsonrpc":"2.0","id":2,"method":"instance_list","params":{}}`)
	var list2 enroll.InstanceListResult
	if err := json.Unmarshal(resps[1].Result, &list2); err != nil {
		t.Fatal(err)
	}
	if len(list2.Instances) != 1 || !slices.Equal(list2.Instances[0].MissingRoots, []string{"state"}) || !list2.Instances[0].StateMissing() {
		t.Fatalf("expected MissingRoots=[state], got %+v", list2.Instances)
	}

	// 3. Daily connection without owner proof must be rejected with owner_required.
	var dailyOut bytes.Buffer
	dailyCfg := Config{
		Version: "test", Home: home, Tier: rpc.TierDaily,
		DetectTools: func(context.Context) []rpc.Tool { return nil },
	}
	if err := Serve(context.Background(), strings.NewReader(helloReq+"\n"+`{"jsonrpc":"2.0","id":2,"method":"instance_forget","params":{"instance_id":"hermes"}}`+"\n"), &dailyOut, dailyCfg); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&dailyOut)
	var r1, r2 rpc.Response
	_ = dec.Decode(&r1)
	_ = dec.Decode(&r2)
	if r2.Error == nil || r2.Error.Data == nil || r2.Error.Data.Code != rpc.OwnerRequiredCode {
		t.Fatalf("expected owner_required on unproved daily tier, got %+v", r2)
	}

	// 4. Call instance_forget -> removes dossier, protection, timer, proposals, declaration, identity; keeps vault, wsDir, prevDir.
	resps = serve(t, home, helloReq, `{"jsonrpc":"2.0","id":2,"method":"instance_forget","params":{"instance_id":"hermes"}}`)
	if resps[1].Error != nil {
		t.Fatalf("instance_forget error: %+v", resps[1].Error)
	}
	var forgetRes enroll.InstanceForgetResult
	if err := json.Unmarshal(resps[1].Result, &forgetRes); err != nil {
		t.Fatal(err)
	}
	if forgetRes.InstanceID != "hermes" || len(forgetRes.Removed) == 0 {
		t.Fatalf("unexpected forget result: %+v", forgetRes)
	}

	for _, gone := range []string{
		protFile, protChangeFile, tmrFile, svcFile, propFile, declFile,
		porterenroll.DossierPath(home, "hermes"),
		filepath.Join(porterenroll.DossiersDir(home), "hermes.profile.json"),
		filepath.Join(porteridentity.Dir(home), "agents", "hermes.json"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat err = %v", gone, err)
		}
	}
	if b := porteridentity.ListBinding(home, "hermes"); b.IdentityState != identity.LegacyUnbound {
		t.Fatalf("expected identity binding removed (legacy_unbound), got %+v", b)
	}

	// HARD INVARIANT: vault, remaining live root (wsDir), and .valise-prev-* must stay intact.
	for _, kept := range []string{
		filepath.Join(vaultDir, "keep.txt"),
		filepath.Join(prevDir, "keep.txt"),
		wsDir,
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("hard invariant violated: %s was touched: %v", kept, err)
		}
	}
	auditBytes, err := os.ReadFile(seal.Paths{Home: home}.Audit())
	if err != nil || !strings.Contains(string(auditBytes), `"op":"instance_forget"`) {
		t.Fatalf("expected instance_forget in audit log, got %q (err=%v)", string(auditBytes), err)
	}
}

func TestServiceControlRestart(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(home, "systemctl.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/systemctl.log\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := &porterenroll.Env{Home: home, Vars: []string{"PATH=" + binDir + ":/usr/bin:/bin", "HOME=" + home}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer inR.Close()
	defer outR.Close()
	cfg := Config{
		Version: "test", Home: home,
		DetectTools: func(context.Context) []rpc.Tool { return nil },
		LoadEnv: func(_ context.Context, _ string) *porterenroll.Env {
			return env
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), inR, outW, cfg)
		outW.Close()
	}()
	dec := json.NewDecoder(outR)
	for _, line := range []string{
		helloReq,
		`{"jsonrpc":"2.0","id":2,"method":"service_control","params":{"unit":"hermes-gateway.service","action":"restart"}}`,
	} {
		if _, err := io.WriteString(inW, line+"\n"); err != nil {
			t.Fatal(err)
		}
		var resp rpc.Response
		if err := dec.Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.Error != nil {
			t.Fatalf("request %s failed: %+v", line, resp.Error)
		}
	}
	_ = inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "--user reset-failed hermes-gateway.service") ||
		!strings.Contains(string(logged), "--user restart hermes-gateway.service") {
		t.Fatalf("unexpected systemctl invocations:\n%s", string(logged))
	}
}

func TestDossierUpdateRPC(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, ".demo")
	wsDir := filepath.Join(home, "workspace")
	extraDir := filepath.Join(home, "notes")
	for _, d := range []string{stateDir, wsDir, extraDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := profile.Profile{
		ID: "demo", ProfileVersion: 1, Status: "draft",
		Roots: []profile.Root{{ID: "workspace", Default: wsDir}},
	}
	rec := enroll.Record{
		InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1,
		Roots:        map[string]string{"state": stateDir, "workspace": wsDir},
		ExternalRefs: []enroll.ExternalRef{{Path: extraDir, Kind: "external_dir", Reason: "notes"}},
	}
	_, boundAgent, err := porterenroll.AcceptRecord(home, rec, p)
	if err != nil || boundAgent == nil {
		t.Fatalf("AcceptRecord: agent=%+v err=%v", boundAgent, err)
	}

	// 1. Hello advertises dossier-roots/1.
	resps := serve(t, home, helloReq)
	var h rpc.HelloResult
	if err := json.Unmarshal(resps[0].Result, &h); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.Capabilities, enroll.DossierCapability) {
		t.Fatalf("hello missing %q: %v", enroll.DossierCapability, h.Capabilities)
	}

	// 2. Daily tier without owner proof is refused.
	var dailyOut bytes.Buffer
	dailyCfg := Config{
		Version: "test", Home: home, Tier: rpc.TierDaily,
		DetectTools: func(context.Context) []rpc.Tool { return nil },
	}
	if err := Serve(context.Background(), strings.NewReader(helloReq+"\n"+`{"jsonrpc":"2.0","id":2,"method":"dossier_update","params":{"instance_id":"demo"}}`+"\n"), &dailyOut, dailyCfg); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&dailyOut)
	var r1, r2 rpc.Response
	_ = dec.Decode(&r1)
	_ = dec.Decode(&r2)
	if r2.Error == nil || r2.Error.Data == nil || r2.Error.Data.Code != rpc.OwnerRequiredCode {
		t.Fatalf("expected owner_required on unproved daily tier, got %+v", r2)
	}

	call := func(params enroll.DossierUpdateParams) rpc.Response {
		b, _ := json.Marshal(params)
		req := `{"jsonrpc":"2.0","id":2,"method":"dossier_update","params":` + string(b) + `}`
		rs := serve(t, home, helloReq, req)
		return rs[1]
	}

	// 3. Reject banned paths, forbidden categories, and removing protected roots.
	for _, bad := range []enroll.DossierUpdateParams{
		{InstanceID: "demo", RemoveRootIDs: []string{"state"}},
		{InstanceID: "demo", RemoveRootIDs: []string{"workspace"}},
		{InstanceID: "demo", AddRoots: []enroll.LocalRoot{{ID: "ext-home", Path: home, Category: "memory"}}},
		{InstanceID: "demo", AddRoots: []enroll.LocalRoot{{ID: "ext-etc", Path: "/etc", Category: "config"}}},
		{InstanceID: "demo", AddRoots: []enroll.LocalRoot{{ID: "ext-badcat", Path: extraDir, Category: "cache"}}},
		{InstanceID: "demo", AddRoots: []enroll.LocalRoot{{ID: "ext-overlap", Path: stateDir, Category: "memory"}}},
		{InstanceID: "demo", AddToolDeps: []enroll.ToolDep{{Name: "/bin/sh"}}},
	} {
		if resp := call(bad); resp.Error == nil {
			t.Errorf("dossier_update unexpectedly succeeded for %+v", bad)
		}
	}

	// 4. Add a valid local_root, tool_dep, and local_entry.
	resp := call(enroll.DossierUpdateParams{
		InstanceID:  "demo",
		AddRoots:    []enroll.LocalRoot{{ID: "ext-notes", Path: extraDir, Category: "memory", Reason: "personal notes"}},
		AddToolDeps: []enroll.ToolDep{{Name: "ffmpeg", Purpose: "audio", Reason: "voice"}},
		AddEntries:  []enroll.LocalEntry{{ID: "webapp", DisplayName: "WebApp", Port: 7860, Path: "/ui", Hint: "run demo ui"}},
	})
	if resp.Error != nil {
		t.Fatalf("dossier_update add failed: %+v", resp.Error)
	}
	var addRes enroll.DossierUpdateResult
	if err := json.Unmarshal(resp.Result, &addRes); err != nil {
		t.Fatal(err)
	}
	if len(addRes.Record.LocalRoots) != 1 || addRes.Record.Roots["ext-notes"] != extraDir ||
		len(addRes.Record.ToolDeps) != 1 || addRes.Record.ToolDeps[0].Name != "ffmpeg" ||
		len(addRes.Record.LocalEntries) != 1 || addRes.Record.LocalEntries[0].ID != "webapp" ||
		len(addRes.Record.ExternalRefs) != 0 {
		t.Fatalf("unexpected add result: %+v", addRes.Record)
	}
	// Identity binding must stay valid with the same AgentRef & Generation after adding a local_root.
	if err := porterenroll.ValidateIdentity(context.Background(), home, boundAgent.Ref); err != nil {
		t.Fatalf("ValidateIdentity failed after dossier_update add: %v", err)
	}

	// 5. Remove the local_root, tool_dep, and local_entry.
	resp = call(enroll.DossierUpdateParams{
		InstanceID:      "demo",
		RemoveRootIDs:   []string{"ext-notes"},
		RemoveToolNames: []string{"ffmpeg"},
		RemoveEntryIDs:  []string{"webapp"},
	})
	if resp.Error != nil {
		t.Fatalf("dossier_update remove failed: %+v", resp.Error)
	}
	var rmRes enroll.DossierUpdateResult
	if err := json.Unmarshal(resp.Result, &rmRes); err != nil {
		t.Fatal(err)
	}
	if len(rmRes.Record.LocalRoots) != 0 || rmRes.Record.Roots["ext-notes"] != "" ||
		len(rmRes.Record.ToolDeps) != 0 || len(rmRes.Record.LocalEntries) != 0 {
		t.Fatalf("unexpected remove result: %+v", rmRes.Record)
	}
	if err := porterenroll.ValidateIdentity(context.Background(), home, boundAgent.Ref); err != nil {
		t.Fatalf("ValidateIdentity failed after dossier_update remove: %v", err)
	}
	auditBytes, err := os.ReadFile(seal.Paths{Home: home}.Audit())
	if err != nil || !strings.Contains(string(auditBytes), `"op":"dossier_update"`) {
		t.Fatalf("expected dossier_update in audit log, got %q (err=%v)", string(auditBytes), err)
	}

	// 6. Legacy unbound instance: dossier_update succeeds without creating an identity binding.
	unboundHome := t.TempDir()
	uState := filepath.Join(unboundHome, ".legacy")
	uExtra := filepath.Join(unboundHome, "extra")
	os.MkdirAll(uState, 0o755)
	os.MkdirAll(uExtra, 0o755)
	up := profile.Profile{ID: "legacy", ProfileVersion: 1, Status: "draft"}
	urec := enroll.Record{InstanceID: "legacy", ProfileID: "legacy", ProfileVersion: 1, Roots: map[string]string{"state": uState}}
	if _, err := porterenroll.WriteRecord(unboundHome, urec, up); err != nil {
		t.Fatal(err)
	}
	ub, _ := json.Marshal(enroll.DossierUpdateParams{
		InstanceID: "legacy",
		AddRoots:   []enroll.LocalRoot{{ID: "ext-extra", Path: uExtra, Category: "workspace"}},
	})
	uResps := serve(t, unboundHome, helloReq, `{"jsonrpc":"2.0","id":2,"method":"dossier_update","params":`+string(ub)+`}`)
	if uResps[1].Error != nil {
		t.Fatalf("dossier_update on legacy unbound instance failed: %+v", uResps[1].Error)
	}
	if b := porteridentity.ListBinding(unboundHome, "legacy"); b.IdentityState != identity.LegacyUnbound {
		t.Fatalf("legacy unbound instance should remain unbound, got %+v", b)
	}
}

func TestMethodReactivateRejectsSuperseded(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, ".demo")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{ID: "demo", ProfileVersion: 1, Status: "draft"}
	rec := enroll.Record{
		InstanceID:     "demo",
		ProfileID:      "demo",
		ProfileVersion: 1,
		Roots:          map[string]string{"state": stateDir},
		Superseded: &enroll.Supersession{
			At:   time.Now().UTC(),
			By:   "demo-new",
			OpID: "op-1",
		},
	}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(enroll.ReactivateParams{
		InstanceID: "demo",
		Mode:       enroll.ReactivateModeCoexist,
	})
	resps := serve(t, home, helloReq, `{"jsonrpc":"2.0","id":2,"method":"`+enroll.MethodReactivate+`","params":`+string(params)+`}`)
	if len(resps) < 2 || resps[1].Error == nil || resps[1].Error.Data == nil || resps[1].Error.Data.Code != "instance_superseded" {
		t.Fatalf("expected instance_superseded, got %+v", resps)
	}
}
