package backuprun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	cp "valise/core/protection"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/seal"
)

func put(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (Store, Params) {
	t.Helper()
	home := t.TempDir()
	a := identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "generation"}
	s := Store{Home: home, Env: &porterenroll.Env{Home: home, Vars: []string{"PATH=/usr/bin:/bin"}}, ValidateAgent: func(_ context.Context, got identity.AgentRef) error {
		if got != a {
			return failure("identity_conflict")
		}
		return nil
	}}
	d := cp.NewDefault()
	d.Schedule = cp.Schedule{Enabled: new(false), DailyAt: "03:00", Timezone: "UTC"}
	d.Period = cp.Period{Unit: "month", Timezone: "UTC"}
	plan := cp.ServerPlan{SchemaVersion: cp.Schema, AgentRef: a, Revision: 1, Desired: d, AcceptedAt: time.Now().UTC()}
	put(t, filepath.Join(home, ".config/valise/protection/demo.json"), plan)
	put(t, filepath.Join(home, ".config/valise/instances/demo.vault.json"), vault.PeriodSetting{Unit: "month", TZ: "UTC"})
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "identity.txt"), []byte("soul"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "secret.txt"), []byte("PRIVATE-CONTENT"), 0600); err != nil {
		t.Fatal(err)
	}
	prof := profile.Profile{ID: "fixture", ProfileVersion: 2, Status: "draft", Paths: []profile.PathRule{{Root: "state", Glob: "identity.txt", Category: "identity"}, {Root: "state", Glob: "secret.txt", Category: "secrets"}}}
	_, err := porterenroll.WriteRecord(home, enroll.Record{InstanceID: "demo", ProfileID: "fixture", ProfileVersion: 2, Roots: map[string]string{"state": state}}, prof)
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = seal.AddRecipient(seal.Paths{Home: home}, key.Recipient().String(), "test")
	if err != nil {
		t.Fatal(err)
	}
	// The legacy automatic target must never be dispatched by planned sealing.
	put(t, filepath.Join(home, ".config/valise/targets.json"), map[string]any{"targets": []vaultrpc.Target{{Name: "legacy", Auto: &vaultrpc.AutoSync{Every: "1h"}}}})
	return s, Params{AgentRef: a, ExpectedRevision: 1, RunID: strings.Repeat("a", 32), Reason: "manual"}
}
func TestRunBindsSnapshotAndDeduplicates(t *testing.T) {
	s, p := fixture(t)
	ctx := context.Background()
	r, err := s.Run(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "succeeded" || r.Snapshot == nil || r.Snapshot.VaultID == "" || r.Plan.Scope.Tier != "full" {
		t.Fatalf("run %+v", r)
	}
	again, err := s.Run(ctx, p)
	if err != nil || again.Snapshot == nil || *again.Snapshot != *r.Snapshot {
		t.Fatalf("retry %+v %v", again, err)
	}
	p.Reason = " timer "
	if _, err = s.Run(ctx, p); err == nil {
		t.Fatal("different normalized request accepted")
	}
	status, err := s.Status(ctx, StatusParams{AgentRef: p.AgentRef, RunID: p.RunID})
	if err != nil || status.State != "succeeded" {
		t.Fatalf("status %+v %v", status, err)
	}
	st, err := seal.Status(seal.Paths{Home: s.Home})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Snapshots) != 1 {
		t.Fatalf("replayed backup: %+v", st)
	}
	for _, path := range []string{s.dir(), s.path(p.RunID)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0600)
		if info.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("mode %v", info.Mode())
		}
	}
	b, _ := os.ReadFile(s.path(p.RunID))
	if strings.Contains(string(b), "PRIVATE-CONTENT") {
		t.Fatal("secret in record")
	}
}
func TestInterruptedRunIsReadOnlyAndNeverReplayed(t *testing.T) {
	s, p := fixture(t)
	ctx := context.Background()
	r, err := s.Run(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"accepted", "running"} {
		r.State = state
		r.Snapshot = nil
		r.Components = nil
		if err = s.save(r); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(s.path(p.RunID))
		got, err := s.Run(ctx, p)
		if err != nil || got.State != "reconciling" {
			t.Fatalf("retry %+v %v", got, err)
		}
		got, err = s.Status(ctx, StatusParams{AgentRef: p.AgentRef, RunID: p.RunID})
		if err != nil || got.State != "reconciling" {
			t.Fatalf("status %+v %v", got, err)
		}
		after, _ := os.ReadFile(s.path(p.RunID))
		if string(before) != string(after) {
			t.Fatal("read changed durable state")
		}
	}
	st, _ := seal.Status(seal.Paths{Home: s.Home})
	if len(st.Snapshots) != 1 {
		t.Fatal("interrupted run replayed")
	}
}
func TestRejectsStalePlanAndCorruption(t *testing.T) {
	s, p := fixture(t)
	p.ExpectedRevision = 2
	if _, err := s.Run(context.Background(), p); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, err := os.Stat(s.path(p.RunID)); !os.IsNotExist(err) {
		t.Fatal("stale run persisted")
	}
	p.ExpectedRevision = 1
	if err := os.WriteFile(s.path(p.RunID), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(context.Background(), p); err == nil {
		t.Fatal("corruption accepted")
	}
	if _, err := s.Status(context.Background(), StatusParams{AgentRef: p.AgentRef, RunID: p.RunID}); err == nil {
		t.Fatal("corruption hidden")
	}
}
func TestRunLockIsProcessShared(t *testing.T) {
	s, p := fixture(t)
	unlock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	other := s
	if _, err := other.Run(context.Background(), p); err == nil {
		t.Fatal("concurrent store bypassed process lock")
	}
}
func TestCopyFailureKeepsSnapshotAndMinimumInterval(t *testing.T) {
	s, p := fixture(t)
	path := filepath.Join(s.Home, ".config/valise/protection/demo.json")
	b, _ := os.ReadFile(path)
	var plan cp.ServerPlan
	if err := json.Unmarshal(b, &plan); err != nil {
		t.Fatal(err)
	}
	yes := true
	target := cp.LocationRef{Kind: "legacy_target", ID: "missing", AuthorityID: p.AgentRef.ServerUID}
	plan.ServerCopies = []cp.ServerCopy{{TargetRef: target, Enabled: &yes, MinimumInterval: "24h"}}
	put(t, path, plan)
	r, err := s.Run(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "partial" || r.Snapshot == nil || len(r.Components) != 2 || r.Components[1].State != "failed" {
		t.Fatalf("partial %+v", r)
	}
	// A prior successful copy throttles the next accepted backup. No real SSH
	// command is executed: this fixture records the fixed executor's evidence.
	at := time.Now().UTC()
	r.Components[1].State = "succeeded"
	r.Components[1].CompletedAt = &at
	r.State = "succeeded"
	if err = s.save(r); err != nil {
		t.Fatal(err)
	}
	p.RunID = strings.Repeat("b", 32)
	next, err := s.Run(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if next.State != "succeeded" || next.Components[1].State != "skipped" || next.Snapshot == nil || *next.Snapshot == *r.Snapshot {
		t.Fatalf("interval %+v", next)
	}
}

func TestScheduleAndPendingAreCheckedBeforeAcceptance(t *testing.T) {
	for _, mode := range []string{"disabled", "pending", "projection", "mcp"} {
		t.Run(mode, func(t *testing.T) {
			s, p := fixture(t)
			switch mode {
			case "disabled":
				p.Reason = "timer"
			case "mcp":
				p.Reason = "mcp"
			case "pending":
				put(t, filepath.Join(s.Home, ".local/state/valise/protection-changes", strings.Repeat("c", 32)+".json"), map[string]string{"state": "pending"})
			case "projection":
				put(t, filepath.Join(s.Home, ".config/valise/instances/demo.vault.json"), vault.PeriodSetting{Unit: "week", TZ: "UTC"})
			}
			if _, err := s.Run(context.Background(), p); err == nil {
				t.Fatal("unsafe run accepted")
			}
			if _, err := os.Stat(s.path(p.RunID)); !os.IsNotExist(err) {
				t.Fatal("run accepted before validation")
			}
		})
	}
}
func TestFailureHasDurableAcceptanceAndNoReplay(t *testing.T) {
	s, p := fixture(t)
	unlock, err := (seal.Paths{Home: s.Home}).Lock()
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Run(context.Background(), p)
	unlock()
	if err != nil || r == nil || r.State != "reconciling" || r.Snapshot != nil {
		t.Fatalf("failure %+v %v", r, err)
	}
	durable, err := s.load(p.RunID)
	if err != nil || durable.RequestDigest == "" || durable.Plan.Revision != 1 || durable.Components[0].State != "unknown" {
		t.Fatalf("durable %+v %v", durable, err)
	}
	again, err := s.Run(context.Background(), p)
	if err != nil || again.State != "reconciling" || again.Snapshot != nil {
		t.Fatalf("retry %+v %v", again, err)
	}
	leaves, err := seal.Leaves(seal.Paths{Home: s.Home})
	if err != nil || len(leaves) != 0 {
		t.Fatalf("replayed after unlock %+v %v", leaves, err)
	}
}
func TestNormalizedReasonReturnsSameRun(t *testing.T) {
	s, p := fixture(t)
	p.Reason = ""
	first, err := s.Run(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	p.Reason = " manual "
	again, err := s.Run(context.Background(), p)
	if err != nil || again.Snapshot == nil || *again.Snapshot != *first.Snapshot {
		t.Fatalf("normalized %+v %v", again, err)
	}
}
