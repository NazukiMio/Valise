package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/testbed/fakesshenv"
)

func agentFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func agentFixture(t *testing.T, home, uid, generation string) identity.AgentRef {
	t.Helper()
	at := time.Now().UTC()
	ref := identity.AgentRef{ServerUID: uid, InstallID: "demo", InstanceID: "demo", Generation: generation}
	root := filepath.Join(home, "state")
	d := enroll.Dossier{Schema: enroll.DossierSchema, InstallID: "demo", ProfileID: "demo", Version: 1, Agents: []enroll.DossierAgent{{InstanceID: "demo", Roots: map[string]string{"state": root}, DisplayName: "Same display name"}}}
	agentFixtureJSON(t, filepath.Join(home, ".config/valise/dossiers/demo.json"), d)
	agentFixtureJSON(t, filepath.Join(home, ".config/valise/dossiers/demo.profile.json"), map[string]any{"id": "demo", "profile_version": 1})
	dir := filepath.Join(home, ".config/valise/identity")
	agentFixtureJSON(t, filepath.Join(dir, "server.json"), identity.Server{Schema: identity.Schema, ServerUID: uid, CreatedAt: at})
	agentFixtureJSON(t, filepath.Join(dir, "agents/demo.json"), identity.Agent{Schema: identity.Schema, Ref: ref, Roots: map[string]string{"state": root}, State: "active", CreatedAt: at, AcceptedAt: at, ProfileDigest: "fixture-digest"})
	if err := os.WriteFile(filepath.Join(dir, "lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return ref
}

func agentObservation(t *testing.T, result AgentListResult, id string) AgentServerObservation {
	t.Helper()
	for _, obs := range result.Observations {
		if obs.ConnectionID == id {
			return obs
		}
	}
	t.Fatalf("no observation for %s: %+v", id, result)
	return AgentServerObservation{}
}

func TestAgentReaderFakeSSHPartialFailureAndStale(t *testing.T) {
	e := fakesshenv.New(t)
	alpha := e.AddHost("alpha")
	beta := e.AddHost("beta")
	e.AddHost("empty")
	for _, host := range []string{"alpha", "beta", "empty"} {
		e.InstallPorter(host)
	}
	aRef := agentFixture(t, alpha, "server-alpha", "generation-alpha")
	agentFixture(t, beta, "server-beta", "generation-beta")
	s := newService(t, e.SSH)
	if _, err := s.AddServers("alpha", "beta", "empty"); err != nil {
		t.Fatal(err)
	}
	r := NewAgentReader(s)
	at := time.Now().UTC()
	r.now = func() time.Time { return at }
	first, err := r.ListAgents(context.Background(), AgentListRequest{})
	if err != nil || first.Partial || len(first.Items) != 2 {
		t.Fatalf("first: %+v %v", first, err)
	}
	empty := agentObservation(t, first, "empty")
	if empty.Status != "fresh" || empty.Value == nil || len(*empty.Value) != 0 {
		t.Fatalf("empty list not authoritative: %+v", empty)
	}
	if first.Items[0].AgentRef == nil || *first.Items[0].AgentRef != aRef {
		t.Fatalf("missing identity: %+v", first.Items)
	}
	// Caller mutation cannot change the cache used after a subsequent failure.
	first.Items[0].Instance.Roots["state"] = "mutated"
	first.Items[0].AgentRef.Generation = "mutated"
	if _, err := s.AddServers("offline"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKESSH_FAIL_CONNECT", "alpha")
	at = at.Add(time.Minute)
	next, err := r.ListAgents(context.Background(), AgentListRequest{})
	if err != nil || !next.Partial || len(next.Items) != 2 {
		t.Fatalf("partial: %+v %v", next, err)
	}
	a := agentObservation(t, next, "alpha")
	if a.Status != "stale" || a.Value == nil || a.ObservedAt == nil || !a.LastAttemptAt.After(*a.ObservedAt) || a.Error == nil {
		t.Fatalf("stale: %+v", a)
	}
	if (*a.Value)[0].AgentRef.Generation != aRef.Generation || (*a.Value)[0].Instance.Roots["state"] == "mutated" {
		t.Fatal("caller changed cached value")
	}
	if agentObservation(t, next, "beta").Status != "fresh" {
		t.Fatal("other server success discarded")
	}
	unknown := agentObservation(t, next, "offline")
	if unknown.Status != "unknown" || unknown.Value != nil || unknown.ObservedAt != nil || unknown.Error == nil {
		t.Fatalf("unknown was an empty list: %+v", unknown)
	}
	got, err := r.GetAgent(context.Background(), AgentGetRequest{ConnectionID: "alpha", AgentRef: &aRef})
	if err != nil || got.Status != "stale" || got.Value == nil {
		t.Fatalf("stale detail: %+v %v", got, err)
	}
	// Changing SSH configuration invalidates the old connection's cache.
	if err := os.WriteFile(s.SSHConfigPath, []byte("Host alpha\n HostName changed.example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := r.ListAgents(context.Background(), AgentListRequest{ConnectionIDs: []string{"alpha"}})
	if err != nil || agentObservation(t, changed, "alpha").Status != "unknown" {
		t.Fatalf("old identity reused after connection change: %+v %v", changed, err)
	}
}

func TestAgentReaderFakeSSHConflictAndExactGeneration(t *testing.T) {
	e := fakesshenv.New(t)
	alpha := e.AddHost("alpha")
	beta := e.AddHost("beta")
	e.InstallPorter("alpha")
	e.InstallPorter("beta")
	ref := agentFixture(t, alpha, "duplicated-server-uid", "generation-a")
	agentFixture(t, beta, "duplicated-server-uid", "generation-b")
	s := newService(t, e.SSH)
	if _, err := s.AddServers("alpha", "beta"); err != nil {
		t.Fatal(err)
	}
	r := NewAgentReader(s)
	result, err := r.ListAgents(context.Background(), AgentListRequest{})
	if err != nil || !result.Partial || len(result.Items) != 2 {
		t.Fatalf("merged cloned server: %+v %v", result, err)
	}
	for _, item := range result.Items {
		if item.IdentityState != "identity_conflict" {
			t.Fatalf("no conflict: %+v", item)
		}
	}
	for _, obs := range result.Observations {
		if obs.Error == nil || obs.Error.Code != "identity_conflict" {
			t.Fatalf("no server conflict: %+v", obs)
		}
	}
	_, err = r.GetAgent(context.Background(), AgentGetRequest{AgentRef: &ref})
	wantCode(t, err, "identity_conflict")
	// An explicitly selected connection can be read, but generation is exact.
	ref.Generation = "previous-generation"
	_, err = r.GetAgent(context.Background(), AgentGetRequest{ConnectionID: "alpha", AgentRef: &ref})
	wantCode(t, err, "agent_not_found")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.ListAgents(cancelled, AgentListRequest{}); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	_, err = r.ListAgents(context.Background(), AgentListRequest{ConnectionIDs: []string{"unregistered"}})
	wantCode(t, err, "server_unknown")
}

// A standalone stdlib-only legacy peer exercises fake-ssh and the real RPC
// transport without depending on a particular historical porter binary.
const legacyAgentPorter = `package main
import("bufio";"encoding/json";"fmt";"os";"path/filepath")
func main(){
 if len(os.Args)>1&&os.Args[1]=="version"{fmt.Println("{\"name\":\"valise-porter\",\"protocol\":\"1.3\"}");return}
 scan:=bufio.NewScanner(os.Stdin);enc:=json.NewEncoder(os.Stdout)
 for scan.Scan(){
  var req struct{ID json.RawMessage ` + "`json:\"id\"`" + `;Method string ` + "`json:\"method\"`" + `};if json.Unmarshal(scan.Bytes(),&req)!=nil{return}
  var result any
  switch req.Method{
  case "hello":result=map[string]any{"protocol":"1.3","home":os.Getenv("HOME"),"tools":[]any{},"pending_ops":[]any{}}
  case "instance_list":result=map[string]any{"instances":[]any{map[string]any{"instance_id":"legacy","profile_id":"demo","roots":map[string]string{"state":"/fixture"}}}}
  default:os.WriteFile(filepath.Join(os.Getenv("HOME"),"unexpected-identity-call"),[]byte(req.Method),0600);return
  }
  enc.Encode(map[string]any{"jsonrpc":"2.0","id":req.ID,"result":result})
 }
}
`

func TestAgentReaderLegacyCapabilityFakeSSH(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("old")
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(legacyAgentPorter), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local/bin/valise-porter")
	if err := os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, source)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build legacy peer: %v\n%s", err, out)
	}
	s := newService(t, e.SSH)
	if _, err := s.AddServers("old"); err != nil {
		t.Fatal(err)
	}
	got, err := NewAgentReader(s).ListAgents(context.Background(), AgentListRequest{})
	if err != nil || got.Partial || len(got.Items) != 1 || got.Items[0].IdentityState != identity.LegacyUnbound || got.Items[0].AgentRef != nil {
		t.Fatalf("legacy: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, "unexpected-identity-call")); !os.IsNotExist(err) {
		t.Fatal("queried unsupported identity RPC")
	}
	if _, err := os.Stat(filepath.Join(home, ".config/valise/identity")); !os.IsNotExist(err) {
		t.Fatal("read initialized legacy identity")
	}
}
