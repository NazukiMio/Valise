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
