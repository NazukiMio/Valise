package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// scriptedAgentPorter answers hello, instance_list and identity_get from
// $HOME/stub.json and appends every method it serves to $HOME/calls.
const scriptedAgentPorter = `package main
import("bufio";"encoding/json";"fmt";"os";"path/filepath")
func main(){
 if len(os.Args)>1&&os.Args[1]=="version"{fmt.Println("{\"name\":\"valise-porter\",\"protocol\":\"1.3\"}");return}
 var stub struct{Capabilities []string ` + "`json:\"capabilities\"`" + `;Instances json.RawMessage ` + "`json:\"instances\"`" + `;Identity map[string]json.RawMessage ` + "`json:\"identity\"`" + `}
 b,_:=os.ReadFile(filepath.Join(os.Getenv("HOME"),"stub.json"));if json.Unmarshal(b,&stub)!=nil{return}
 log,_:=os.OpenFile(filepath.Join(os.Getenv("HOME"),"calls"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);defer log.Close()
 scan:=bufio.NewScanner(os.Stdin);enc:=json.NewEncoder(os.Stdout)
 for scan.Scan(){
  var req struct{ID json.RawMessage ` + "`json:\"id\"`" + `;Method string ` + "`json:\"method\"`" + `;Params struct{InstanceID string ` + "`json:\"instance_id\"`" + `} ` + "`json:\"params\"`" + `};if json.Unmarshal(scan.Bytes(),&req)!=nil{return}
  fmt.Fprintln(log,req.Method)
  var result any
  switch req.Method{
  case "hello":result=map[string]any{"protocol":"1.3","home":os.Getenv("HOME"),"tools":[]any{},"pending_ops":[]any{},"capabilities":stub.Capabilities}
  case "instance_list":result=map[string]any{"instances":stub.Instances}
  case "identity_get":r,ok:=stub.Identity[req.Params.InstanceID];if !ok{return};result=r
  default:return
  }
  enc.Encode(map[string]any{"jsonrpc":"2.0","id":req.ID,"result":result})
 }
}
`

func installScriptedAgentPorter(t *testing.T, e *fakesshenv.Env, host string, stub any) string {
	t.Helper()
	home := e.AddHost(host)
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(scriptedAgentPorter), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local/bin/valise-porter")
	if err := os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, source)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build scripted peer: %v\n%s", err, out)
	}
	agentFixtureJSON(t, filepath.Join(home, "stub.json"), stub)
	return home
}

func scriptedCalls(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "calls"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentReaderIdentityListCapabilityFakeSSH(t *testing.T) {
	e := fakesshenv.New(t)
	at := time.Now().UTC()
	listedRef := identity.AgentRef{ServerUID: "uid-new", InstallID: "demo", InstanceID: "demo", Generation: "listed"}
	queriedRef := identity.AgentRef{ServerUID: "uid-old", InstallID: "demo", InstanceID: "demo", Generation: "queried"}
	binding := func(ref identity.AgentRef) identity.Binding {
		return identity.Binding{IdentityState: identity.Bound, Agent: &identity.Agent{Schema: identity.Schema, Ref: ref, State: "active", CreatedAt: at}}
	}
	instance := func(b *identity.Binding) map[string]any {
		m := map[string]any{"instance_id": "demo", "install_id": "demo", "profile_id": "demo", "roots": map[string]string{"state": "/fixture"}}
		if b != nil {
			m["identity"] = b
		}
		return m
	}
	listed := binding(listedRef)
	legacy := identity.Binding{IdentityState: identity.LegacyUnbound}
	newHome := installScriptedAgentPorter(t, e, "new", map[string]any{
		"capabilities": []string{identity.Capability, identity.ListCapability},
		"instances":    []any{instance(&listed), map[string]any{"instance_id": "plain", "profile_id": "demo", "roots": map[string]string{}, "identity": legacy}},
	})
	// An identity/1 peer: anything it may put in instance_list is ignored and
	// identity_get stays the source of truth.
	stray := binding(identity.AgentRef{ServerUID: "uid-old", InstallID: "demo", InstanceID: "demo", Generation: "stray"})
	oldHome := installScriptedAgentPorter(t, e, "old", map[string]any{
		"capabilities": []string{identity.Capability},
		"instances":    []any{instance(&stray)},
		"identity":     map[string]any{"demo": binding(queriedRef)},
	})
	s := newService(t, e.SSH)
	if _, err := s.AddServers("new", "old"); err != nil {
		t.Fatal(err)
	}
	got, err := NewAgentReader(s).ListAgents(context.Background(), AgentListRequest{})
	if err != nil || got.Partial || len(got.Items) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	byConn := map[string][]AgentSummary{}
	for _, item := range got.Items {
		byConn[item.ConnectionID] = append(byConn[item.ConnectionID], item)
		if item.Instance.Identity != nil {
			t.Fatalf("raw listed binding leaked into summary: %+v", item)
		}
	}
	n := byConn["new"]
	if len(n) != 2 || n[0].AgentRef == nil || *n[0].AgentRef != listedRef || n[1].IdentityState != identity.LegacyUnbound || n[1].AgentRef != nil {
		t.Fatalf("identity/2 read: %+v", n)
	}
	if calls := scriptedCalls(t, newHome); strings.Contains(calls, "identity_get") {
		t.Fatalf("identity/2 peer was queried per instance: %q", calls)
	}
	o := byConn["old"]
	if len(o) != 1 || o[0].AgentRef == nil || *o[0].AgentRef != queriedRef {
		t.Fatalf("identity/1 read: %+v", o)
	}
	if calls := scriptedCalls(t, oldHome); strings.Count(calls, "identity_get") != 1 {
		t.Fatalf("identity/1 peer not queried per instance: %q", calls)
	}

	// An identity/2 peer that omits a binding is not silently legacy.
	agentFixtureJSON(t, filepath.Join(newHome, "stub.json"), map[string]any{
		"capabilities": []string{identity.Capability, identity.ListCapability},
		"instances":    []any{instance(nil)},
	})
	bad, err := NewAgentReader(s).ListAgents(context.Background(), AgentListRequest{ConnectionIDs: []string{"new"}})
	if err != nil || !bad.Partial || len(bad.Items) != 0 {
		t.Fatalf("missing listed binding accepted: %+v %v", bad, err)
	}
}

// relationFixture records a committed migration on the target host exactly
// as porter's recovery commit leaves it (relation plus operation record).
func relationFixture(t *testing.T, home string, target identity.AgentRef, source identity.AgentRef) {
	t.Helper()
	dir := filepath.Join(home, ".config/valise/identity")
	at := time.Now().UTC().Truncate(time.Second)
	relID := "0123456789abcdef0123456789abcdef"
	rel := identity.Relation{Schema: identity.Schema, RelationID: relID, Kind: identity.MigrationCompleted, SourceAgent: &source,
		SourceSnapshot: identity.SnapshotRef{VaultID: "vault", SnapshotID: "snap"}, TargetAgent: target, OperationRef: "op_fixture",
		VerifiedAt: at, SourceDisposition: identity.StoppedVerified}
	agentFixtureJSON(t, filepath.Join(dir, "relations", relID+".json"), rel)
	agentFixtureJSON(t, filepath.Join(dir, "operations", "op_fixture.json"), map[string]any{"schema": identity.Schema, "state": "complete", "relation": rel})
	var a identity.Agent
	b, err := os.ReadFile(filepath.Join(dir, "agents/demo.json"))
	if err != nil || json.Unmarshal(b, &a) != nil {
		t.Fatalf("agent fixture: %v", err)
	}
	a.RelationRefs = []string{relID}
	agentFixtureJSON(t, filepath.Join(dir, "agents/demo.json"), a)
}

func TestAgentReaderMigratedSourceFakeSSH(t *testing.T) {
	e := fakesshenv.New(t)
	src := e.AddHost("src")
	dst := e.AddHost("dst")
	e.InstallPorter("src")
	e.InstallPorter("dst")
	srcRef := agentFixture(t, src, "server-src", "generation-src")
	dstRef := agentFixture(t, dst, "server-dst", "generation-dst")
	relationFixture(t, dst, dstRef, srcRef)
	s := newService(t, e.SSH)
	if _, err := s.AddServers("src", "dst"); err != nil {
		t.Fatal(err)
	}
	r := NewAgentReader(s)
	got, err := r.ListAgents(context.Background(), AgentListRequest{})
	if err != nil || got.Partial || len(got.Items) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	var source, target AgentSummary
	for _, item := range got.Items {
		if item.ConnectionID == "src" {
			source = item
		} else {
			target = item
		}
	}
	if source.MigratedTo == nil || source.MigratedTo.ConnectionID != "dst" || source.MigratedTo.AgentRef != dstRef || source.MigratedTo.OperationRef != "op_fixture" {
		t.Fatalf("source not marked: %+v", source)
	}
	if target.MigratedTo != nil || len(target.Relations) != 1 || target.Relations[0].SourceAgent == nil || *target.Relations[0].SourceAgent != srcRef {
		t.Fatalf("target: %+v", target)
	}
	if obs := agentObservation(t, got, "src"); (*obs.Value)[0].MigratedTo == nil {
		t.Fatal("observation and items disagree")
	}
	// Only one of the two servers is reachable: nothing is inferred, even
	// from the cached value of the other.
	t.Setenv("FAKESSH_FAIL_CONNECT", "dst")
	partial, err := r.ListAgents(context.Background(), AgentListRequest{})
	if err != nil || !partial.Partial {
		t.Fatalf("%+v %v", partial, err)
	}
	for _, item := range partial.Items {
		if item.MigratedTo != nil {
			t.Fatalf("inferred with target unreachable: %+v", item)
		}
	}
	t.Setenv("FAKESSH_FAIL_CONNECT", "src")
	partial, err = r.ListAgents(context.Background(), AgentListRequest{})
	if err != nil || !partial.Partial {
		t.Fatalf("%+v %v", partial, err)
	}
	for _, item := range partial.Items {
		if item.MigratedTo != nil {
			t.Fatalf("inferred with source unreachable: %+v", item)
		}
	}
	t.Setenv("FAKESSH_FAIL_CONNECT", "")
	// Reading a single server never annotates.
	single, err := r.ListAgents(context.Background(), AgentListRequest{ConnectionIDs: []string{"src"}})
	if err != nil || len(single.Items) != 1 || single.Items[0].MigratedTo != nil {
		t.Fatalf("single: %+v %v", single, err)
	}
}

func TestAgentReaderMigratedSourceNeedsExactGenerationFakeSSH(t *testing.T) {
	e := fakesshenv.New(t)
	src := e.AddHost("src")
	dst := e.AddHost("dst")
	e.InstallPorter("src")
	e.InstallPorter("dst")
	// The source instance was re-enrolled since: same server, install,
	// instance id and display name, but a different generation.
	srcRef := agentFixture(t, src, "server-src", "generation-now")
	dstRef := agentFixture(t, dst, "server-dst", "generation-dst")
	old := srcRef
	old.Generation = "generation-before"
	relationFixture(t, dst, dstRef, old)
	s := newService(t, e.SSH)
	if _, err := s.AddServers("src", "dst"); err != nil {
		t.Fatal(err)
	}
	got, err := NewAgentReader(s).ListAgents(context.Background(), AgentListRequest{})
	if err != nil || got.Partial || len(got.Items) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, item := range got.Items {
		if item.MigratedTo != nil {
			t.Fatalf("marked a different generation: %+v", item)
		}
	}
}
