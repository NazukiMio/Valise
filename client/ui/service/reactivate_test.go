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

func buildInlinePorter(t *testing.T, home, src string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local/bin/valise-porter")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, source)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build inline porter: %v\n%s", err, out)
	}
}

func setupHostWithService(t *testing.T, e *fakesshenv.Env, host, initialActive string) string {
	t.Helper()
	home := e.AddHost(host)
	e.InstallPorter(host)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".demo/demo.json"), "{}")
	write(filepath.Join(home, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")
	write(filepath.Join(home, "unit-load"), "loaded\n")
	write(filepath.Join(home, "unit-active"), initialActive+"\n")
	write(filepath.Join(home, ".local/bin/systemctl"), `#!/bin/sh
shift # --user
act="$1"
case "$act" in
show)
  load=$(cat "$HOME/unit-load")
  active=$(cat "$HOME/unit-active")
  printf 'LoadState=%s\nActiveState=%s\nUnitFileState=enabled\n' "$load" "$active"
  ;;
is-active)
  cat "$HOME/unit-active"
  ;;
stop)
  echo inactive > "$HOME/unit-active"
  ;;
reset-failed)
  ;;
start|restart)
  echo active > "$HOME/unit-active"
  ;;
esac
`)
	return home
}

func findAgentItem(t *testing.T, res AgentListResult, conn, inst string) AgentSummary {
	t.Helper()
	for _, it := range res.Items {
		if it.ConnectionID == conn && it.Instance.InstanceID == inst {
			return it
		}
	}
	t.Fatalf("agent %s/%s not found in %+v", conn, inst, res.Items)
	return AgentSummary{}
}

func readJSONFile[T any](t *testing.T, path string) T {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return v
}

func writeCompletedMigrationFixture(t *testing.T, homeB string, srcRef identity.AgentRef) identity.Agent {
	t.Helper()
	idDir := filepath.Join(homeB, ".config/valise/identity")
	srvB := readJSONFile[identity.Server](t, filepath.Join(idDir, "server.json"))
	priorB := readJSONFile[identity.Agent](t, filepath.Join(idDir, "agents/demo.json"))

	newGen, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	relID, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	opID := "op-migrate-a-to-b"
	now := time.Now().UTC()
	newB := identity.Agent{
		Schema: identity.Schema,
		Ref: identity.AgentRef{
			ServerUID:  srvB.ServerUID,
			InstallID:  priorB.Ref.InstallID,
			InstanceID: priorB.Ref.InstanceID,
			Generation: newGen,
		},
		Roots:         priorB.Roots,
		ProfileDigest: priorB.ProfileDigest,
		State:         "active",
		CreatedAt:     now,
		AcceptedAt:    now,
		RelationRefs:  []string{relID},
	}
	rel := identity.Relation{
		Schema:            identity.Schema,
		RelationID:        relID,
		Kind:              identity.MigrationCompleted,
		SourceAgent:       &srcRef,
		SourceSnapshot:    identity.SnapshotRef{VaultID: "v1", SnapshotID: "s1"},
		TargetAgent:       newB.Ref,
		OperationRef:      opID,
		VerifiedAt:        now,
		SourceDisposition: identity.StoppedVerified,
	}
	opCommit := map[string]any{
		"schema":   identity.Schema,
		"state":    "complete",
		"server":   srvB,
		"agent":    newB,
		"relation": rel,
	}
	agentFixtureJSON(t, filepath.Join(idDir, "operations", opID+".json"), opCommit)
	agentFixtureJSON(t, filepath.Join(idDir, "relations", relID+".json"), rel)
	agentFixtureJSON(t, filepath.Join(idDir, "agents/demo.json"), newB)
	return newB
}

func TestReactivateAgentSwitchBackSymmetricAndCoexistFakeSSH(t *testing.T) {
	oldSettle, oldPoll := serviceControlSettle, serviceControlPoll
	serviceControlSettle, serviceControlPoll = 200*time.Millisecond, 20*time.Millisecond
	defer func() { serviceControlSettle, serviceControlPoll = oldSettle, oldPoll }()

	e := fakesshenv.New(t)
	homeA := setupHostWithService(t, e, "server-a", "inactive")
	homeB := setupHostWithService(t, e, "server-b", "active")

	s := newService(t, e.SSH)
	s.RiskAccepted = true
	if _, err := s.AddServers("server-a", "server-b"); err != nil {
		t.Fatal(err)
	}
	prof := demoProfile + `service:
  candidates: [ { scope: user, unit: demo.service } ]
`
	if err := os.MkdirAll(filepath.Join(s.Dirs.Data, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := s.Enroll(ctx, "server-a", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatalf("enroll A: %v", err)
	}
	if _, err := s.Enroll(ctx, "server-b", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatalf("enroll B: %v", err)
	}

	// Simulate A -> B migration completed on server-b.
	agentA := readJSONFile[identity.Agent](t, filepath.Join(homeA, ".config/valise/identity/agents/demo.json"))
	migratedB := writeCompletedMigrationFixture(t, homeB, agentA.Ref)

	reader := NewAgentReader(s)
	s.readersMu.Lock()
	s.agentsReader = reader
	s.readersMu.Unlock()

	// Verify initial state: A is MigratedTo B, B is active.
	list0, err := reader.ListAgents(ctx, AgentListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	itemA0 := findAgentItem(t, list0, "server-a", "demo")
	itemB0 := findAgentItem(t, list0, "server-b", "demo")
	if itemA0.MigratedTo == nil || itemA0.MigratedTo.ConnectionID != "server-b" || itemA0.MigratedTo.AgentRef != migratedB.Ref {
		t.Fatalf("expected A MigratedTo B, got %+v", itemA0.MigratedTo)
	}
	if itemB0.MigratedTo != nil {
		t.Fatalf("expected B active, got %+v", itemB0.MigratedTo)
	}

	// 1. Switch back B -> A (auto-resolving target from MigratedTo, stopping B, starting A).
	sw1, err := s.ReactivateAgent(ctx, ReactivateAgentRequest{
		ConnectionID: "server-a",
		InstanceID:   "demo",
		Mode:         enroll.ReactivateModeSwitchBack,
		StartService: true,
	})
	if err != nil {
		t.Fatalf("switch_back B->A: %v", err)
	}
	if !sw1.TargetStopped || !sw1.ServiceStarted || sw1.Relation == nil || sw1.Relation.Kind != identity.MigrationCompleted {
		t.Fatalf("unexpected sw1: %+v", sw1)
	}
	// Verify B service stopped and A service started.
	svcB, err := s.ServiceStatus(ctx, "server-b", "demo")
	if err != nil || svcB.Active != "inactive" {
		t.Fatalf("expected B service inactive, got %+v %v", svcB, err)
	}
	svcA, err := s.ServiceStatus(ctx, "server-a", "demo")
	if err != nil || svcA.Active != "active" {
		t.Fatalf("expected A service active, got %+v %v", svcA, err)
	}
	// Verify cross-server listing: B is now MigratedTo A, and A is active!
	list1, err := reader.ListAgents(ctx, AgentListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	itemA1 := findAgentItem(t, list1, "server-a", "demo")
	itemB1 := findAgentItem(t, list1, "server-b", "demo")
	if itemA1.MigratedTo != nil {
		t.Fatalf("expected A active after switch_back, got MigratedTo=%+v", itemA1.MigratedTo)
	}
	if itemB1.MigratedTo == nil || itemB1.MigratedTo.ConnectionID != "server-a" || itemB1.MigratedTo.AgentRef != sw1.Agent.Ref {
		t.Fatalf("expected B MigratedTo A, got %+v", itemB1.MigratedTo)
	}

	// 2. Symmetric switch back A -> B!
	sw2, err := s.ReactivateAgent(ctx, ReactivateAgentRequest{
		ConnectionID: "server-b",
		InstanceID:   "demo",
		Mode:         enroll.ReactivateModeSwitchBack,
		StartService: true,
	})
	if err != nil {
		t.Fatalf("symmetric switch_back A->B: %v", err)
	}
	if !sw2.TargetStopped || !sw2.ServiceStarted {
		t.Fatalf("unexpected sw2: %+v", sw2)
	}
	list2, err := reader.ListAgents(ctx, AgentListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	itemA2 := findAgentItem(t, list2, "server-a", "demo")
	itemB2 := findAgentItem(t, list2, "server-b", "demo")
	if itemB2.MigratedTo != nil {
		t.Fatalf("expected B active after symmetric switch_back, got %+v", itemB2.MigratedTo)
	}
	if itemA2.MigratedTo == nil || itemA2.MigratedTo.ConnectionID != "server-b" || itemA2.MigratedTo.AgentRef != sw2.Agent.Ref {
		t.Fatalf("expected A MigratedTo B after symmetric switch_back, got %+v", itemA2.MigratedTo)
	}

	// 3. Coexist gate: must fail without ConfirmBotConflict.
	_, err = s.ReactivateAgent(ctx, ReactivateAgentRequest{
		ConnectionID:       "server-a",
		InstanceID:         "demo",
		Mode:               enroll.ReactivateModeCoexist,
		ConfirmBotConflict: false,
	})
	wantCode(t, err, "bot_conflict_confirm_required")

	// 4. Coexist with ConfirmBotConflict: both A and B become active without stopping B!
	co, err := s.ReactivateAgent(ctx, ReactivateAgentRequest{
		ConnectionID:       "server-a",
		InstanceID:         "demo",
		Mode:               enroll.ReactivateModeCoexist,
		StartService:       true,
		ConfirmBotConflict: true,
	})
	if err != nil {
		t.Fatalf("coexist: %v", err)
	}
	if co.Relation != nil || !co.ServiceStarted {
		t.Fatalf("unexpected coexist result: %+v", co)
	}
	svcBFinal, err := s.ServiceStatus(ctx, "server-b", "demo")
	if err != nil || svcBFinal.Active != "active" {
		t.Fatalf("expected B service still active under coexist, got %+v %v", svcBFinal, err)
	}
	list3, err := reader.ListAgents(ctx, AgentListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	itemA3 := findAgentItem(t, list3, "server-a", "demo")
	itemB3 := findAgentItem(t, list3, "server-b", "demo")
	if itemA3.MigratedTo != nil || itemB3.MigratedTo != nil {
		t.Fatalf("expected both A and B active under coexist: A=%+v B=%+v", itemA3.MigratedTo, itemB3.MigratedTo)
	}
}

func TestReactivateSwitchBackRejectsUnverifiedStop(t *testing.T) {
	oldSettle, oldPoll := serviceControlSettle, serviceControlPoll
	serviceControlSettle, serviceControlPoll = 200*time.Millisecond, 20*time.Millisecond
	defer func() { serviceControlSettle, serviceControlPoll = oldSettle, oldPoll }()

	e := fakesshenv.New(t)
	homeA := setupHostWithService(t, e, "server-a", "inactive")
	homeB := setupHostWithService(t, e, "server-b", "unknown")
	// Override server-b systemctl so stop leaves ActiveState=unknown (not verified inactive/failed).
	if err := os.WriteFile(filepath.Join(homeB, ".local/bin/systemctl"), []byte(`#!/bin/sh
shift # --user
act="$1"
case "$act" in
show)
  printf 'LoadState=loaded\nActiveState=unknown\nUnitFileState=enabled\n'
  ;;
is-active)
  echo unknown
  ;;
stop|reset-failed|start|restart)
  ;;
esac
`), 0o700); err != nil {
		t.Fatal(err)
	}

	s := newService(t, e.SSH)
	s.RiskAccepted = true
	if _, err := s.AddServers("server-a", "server-b"); err != nil {
		t.Fatal(err)
	}
	prof := demoProfile + `service:
  candidates: [ { scope: user, unit: demo.service } ]
`
	if err := os.MkdirAll(filepath.Join(s.Dirs.Data, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := s.Enroll(ctx, "server-a", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatalf("enroll A: %v", err)
	}
	if _, err := s.Enroll(ctx, "server-b", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatalf("enroll B: %v", err)
	}

	agentA := readJSONFile[identity.Agent](t, filepath.Join(homeA, ".config/valise/identity/agents/demo.json"))
	writeCompletedMigrationFixture(t, homeB, agentA.Ref)

	_, err := s.ReactivateAgent(ctx, ReactivateAgentRequest{
		ConnectionID: "server-a",
		InstanceID:   "demo",
		Mode:         enroll.ReactivateModeSwitchBack,
	})
	wantCode(t, err, "target_stop_failed")
}
