package backuprun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cp "valise/core/protection"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/seal"
)

func TestAcceptedTimerRetiresOnlyAfterItSeals(t *testing.T) {
	s, params := fixture(t)
	paths := seal.Paths{Home: s.Home}
	ctx := context.Background()
	desired := cp.NewDefault()
	desired.Schedule = cp.Schedule{Enabled: new(true), DailyAt: "03:00", Timezone: "UTC"}
	desired.Period = cp.Period{Unit: "month", Timezone: "UTC", KeepAutoPeriods: 1}
	put(t, filepath.Join(s.Home, ".config/valise/protection/demo.json"), cp.ServerPlan{
		SchemaVersion: cp.Schema, AgentRef: params.AgentRef, Revision: 1, Desired: desired, AcceptedAt: time.Now().UTC(),
	})
	recipients, err := seal.ReadRecipients(paths)
	if err != nil {
		t.Fatal(err)
	}
	var ended []string
	for month := time.January; month <= time.February; month++ {
		at := time.Date(2020, month, 6, 12, 0, 0, 0, time.UTC)
		period := vault.NextPeriod("demo", nil, vault.DefaultPeriodSetting, at)
		dir, err := paths.LeafDir(period.Leaf())
		if err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(dir, vault.PeriodFile), period)
		w, err := vault.OpenWriter(dir, recipients, paths.DedupeKey, at)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Commit(vault.Snapshot{InstanceID: "demo", Tier: vault.TierSoul, CreatedAt: at}, vault.Files{}, vault.Meta{Kind: vault.KindAuto}); err != nil {
			t.Fatal(err)
		}
		ended = append(ended, dir)
	}
	manual, err := s.Run(ctx, params)
	if err != nil || manual.State != "succeeded" {
		t.Fatalf("manual: %+v %v", manual, err)
	}
	if _, err := os.Stat(ended[0]); err != nil {
		t.Fatal("manual backup retired a period", err)
	}

	params.RunID, params.Reason = strings.Repeat("b", 32), vaultrpc.InitiatorTimer
	timer, err := s.Run(ctx, params)
	if err != nil || timer.State != "succeeded" || len(timer.Components) != 2 ||
		timer.Components[1].Kind != "leaf_retire" || timer.Components[1].State != "succeeded" {
		t.Fatalf("timer: %+v %v", timer, err)
	}
	if _, err := os.Stat(ended[0]); !os.IsNotExist(err) {
		t.Fatal("expired leaf remains", err)
	}
	if _, err := os.Stat(ended[1]); err != nil {
		t.Fatal("most recent ended period was removed", err)
	}
	status, err := seal.Status(paths)
	if err != nil {
		t.Fatal(err)
	}
	if !vault.IsSavepointLeaf(status.LeafOf(manual.Snapshot.SnapshotID)) ||
		status.LeafOf(timer.Snapshot.SnapshotID) == "" || vault.IsSavepointLeaf(status.LeafOf(timer.Snapshot.SnapshotID)) {
		t.Fatalf("timer and manual backups on wrong tracks: %+v", status.Leaves)
	}
}

func TestNeedsFullCycleBundleMembers(t *testing.T) {
	s, _ := fixture(t)
	paths := seal.Paths{Home: s.Home}
	recipients, err := seal.ReadRecipients(paths)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 25, 12, 0, 0, 0, time.UTC)
	sched := cp.Schedule{FullCycle: cp.FullCycleWeekly, Timezone: "UTC"}
	period := cp.Period{Unit: "month", Timezone: "UTC"}

	sealAuto := func(inst, tier, kind string) {
		t.Helper()
		per := vault.NextPeriod(inst, nil, vault.DefaultPeriodSetting, now)
		dir, err := paths.LeafDir(per.Leaf())
		if err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(dir, vault.PeriodFile), per)
		w, err := vault.OpenWriter(dir, recipients, paths.DedupeKey, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Commit(vault.Snapshot{InstanceID: inst, Tier: tier, CreatedAt: now}, vault.Files{}, vault.Meta{Kind: kind}); err != nil {
			t.Fatal(err)
		}
	}

	// Neither member has a full snapshot yet -> needs full.
	if !needsFullCycle(paths, "main", []string{"main", "sub1"}, sched, period, now) {
		t.Fatal("expected needsFullCycle=true when no member has full snapshot")
	}

	// Only main has an auto full snapshot; sub1 has only soul -> still needs full!
	sealAuto("main", vault.TierFull, vault.KindAuto)
	sealAuto("sub1", vault.TierSoul, vault.KindAuto)
	if needsFullCycle(paths, "main", nil, sched, period, now) {
		t.Fatal("single main with auto full should not need full")
	}
	if !needsFullCycle(paths, "main", []string{"main", "sub1"}, sched, period, now) {
		t.Fatal("bundle missing sub1 auto full must upgrade to full")
	}

	// Manual savepoint full for sub1 does not satisfy automatic full cycle.
	sealAuto("sub1", vault.TierFull, vault.KindSavepoint)
	if !needsFullCycle(paths, "main", []string{"main", "sub1"}, sched, period, now) {
		t.Fatal("savepoint full for sub1 should not satisfy automatic full cycle")
	}

	// Once sub1 also has an auto full (or raw) snapshot -> no longer needs full.
	sealAuto("sub1", vault.TierRaw, vault.KindAuto)
	if needsFullCycle(paths, "main", []string{"main", "sub1"}, sched, period, now) {
		t.Fatal("expected needsFullCycle=false once all bundle members have auto full/raw")
	}
}
