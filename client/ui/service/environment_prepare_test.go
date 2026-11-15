package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/vault"
)

func TestParallelPreparationNeedsSeparateChoicesAndFreshSingleUsePlan(t *testing.T) {
	s, _ := enrollWorld(t)
	p := &PreparedParallelRestore{plan: &RevivePlan{}, RequiresCredentials: true, Materials: &TestMaterialReview{}, View: revive.EnvironmentPlan{ProfileUntested: true}, Expires: time.Now().Add(time.Minute)}
	for _, choice := range []ParallelPrepareChoice{{}, {Credentials: true}, {Credentials: true, AllowUntested: true}} {
		if _, err := s.RunParallelPreparation(context.Background(), p, choice, nil); journeyCode(err) != "confirmation_required" || p.consumed {
			t.Fatal("missing impact choice consumed or ran plan", choice, err)
		}
	}
	p.Expires = time.Now().Add(-time.Second)
	if _, err := s.RunParallelPreparation(context.Background(), p, ParallelPrepareChoice{Credentials: true, AllowUntested: true, Baseline: true}, nil); journeyCode(err) != "confirmation_expired" {
		t.Fatal("expired review executed", err)
	}
	for _, method := range []string{environment.MethodPreparationBegin, environment.MethodPreparationFinish, environment.MethodPreparationStatus, environment.MethodPreparationCancel, environment.MethodPreparationCleanupReview, environment.MethodPreparationCleanup} {
		if n2Compatibility(&rpc.HelloResult{}, method, nil) == nil {
			t.Fatal("preparation silently sent to old porter", method)
		}
	}
}

func TestParallelPreparationFailurePersistsOnlyOriginalIdentityOverFakeSSH(t *testing.T) {
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
	snapshot := journey.SnapshotRef{VaultID: "selected-vault", SnapshotID: "selected-snapshot"}
	p := &PreparedParallelRestore{View: revive.EnvironmentPlan{Mode: environment.Parallel, Digest: strings.Repeat("a", 64)},
		plan: &RevivePlan{Target: "box", From: "box"}, Expires: time.Now().Add(time.Minute), trust: trust,
		request: RestoreRequest{Snapshot: snapshot}, params: revive.EnvironmentPlanParams{Agent: settings.Agent, SettingsDigest: settings.Digest}}
	task, err := s.RunParallelPreparation(ctx, p, ParallelPrepareChoice{}, nil)
	// No synthetic kernel/service is claimed: the real porter must refuse this
	// deliberately incomplete source before any programme or stage is created.
	if err == nil || task.Kind != RestoreKindEnvironment || task.OpID == "" || task.State != "reconciling" || task.Verified {
		t.Fatal("unprepared source was run or lost its original operation", task, err)
	}
	if _, err := s.RunParallelPreparation(ctx, p, ParallelPrepareChoice{}, nil); journeyCode(err) != "confirmation_expired" {
		t.Fatal("uncertain request was replayed", err)
	}
	for i := 0; i < 2; i++ {
		if _, _, err := s.InspectParallelPreparation(ctx, task); journeyCode(err) != "environment_prepare_not_found" {
			t.Fatal("query did not retain an uncertain missing record", err)
		}
	}
	tasks, err := s.ListRestoreTasks()
	if err != nil || len(tasks) != 1 || tasks[0].OpID != task.OpID {
		t.Fatal("query or retry created another task", tasks, err)
	}
	for _, dir := range []string{"environment-preparations", "environments", "programs", "journal"} {
		if _, err := os.Stat(filepath.Join(home, ".local/state/valise", dir)); !os.IsNotExist(err) {
			t.Fatal("refused input or query created execution state", dir, err)
		}
	}
	raw, err := os.ReadFile(s.restoreTaskPath(task.OpID))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields["kind"] != RestoreKindEnvironment {
		t.Fatal("missing durable operation kind")
	}
	for _, key := range []string{"input", "profile", "files", "allow_untested", "credentials", "approved", "grant", "binding_digest"} {
		if _, ok := fields[key]; ok {
			t.Fatal("task retained private input or consent", key)
		}
	}
	if _, _, err := s.InspectRestore(ctx, task); journeyCode(err) != "environment_operation_required" {
		t.Fatal("parallel task used old directory recovery", err)
	}
}

func TestParallelManifestCanonicalizesNestedPathsAndSQLite(t *testing.T) {
	p := &RevivePlan{Plan: revive.Plan{Roots: []revive.RootPlan{{ID: "state", Path: "/state"}, {ID: "workspace", Path: "/state/work", SwapRoot: "state", Rel: "work"}}},
		files: []vault.File{{Root: "workspace", Path: "z.db", Size: 2, SHA256: "database", Category: "workspace"}, {Root: "state", Path: "a", Size: 1, SHA256: "first", Category: "memory"}}, sqlite: map[string]bool{"workspace:z.db": true}}
	manifest := parallelManifest(p)
	if len(manifest) != 2 || manifest[0].Path != "state/a" || manifest[1].Path != "state/work/z.db" || !manifest[1].SQLite {
		t.Fatal("receipt could not bind physical nested stage manifest", manifest)
	}
}
