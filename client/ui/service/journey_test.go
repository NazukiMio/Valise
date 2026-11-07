package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/journey"
	"valise/core/valise"
	"valise/core/vault"
)

func journeyFixture(t *testing.T, root, vid, sid, tier string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0700); err != nil {
		t.Fatal(err)
	}
	objects := map[string]any{"VAULT": vault.Descriptor{Format: vault.FormatVault, VaultID: vid}, vault.MetaPath(sid): vault.Meta{Format: vault.FormatMeta, SnapshotID: sid, InstanceID: "sample", Tier: tier, CreatedAt: at, Epoch: 1}}
	for p, v := range objects {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(root, p), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, vault.SnapshotPath(sid)), []byte("encrypted header fixture"), 0600); err != nil {
		t.Fatal(err)
	}
}
func journeyCode(err error) string {
	if e, ok := valise.As(err); ok {
		return e.Code
	}
	return fmt.Sprint(err)
}

func TestJourneyFixedPaginationAndFilters(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	vid := vault.NewID("v", now)
	for i := 0; i < 55; i++ {
		journeyFixture(t, root, vid, vault.NewID("s", now.Add(time.Duration(i)*time.Second)), "full", now.Add(-time.Duration(i)*time.Hour))
	}
	r := NewJourneyReader([]config.Repo{{Name: "disk", Path: root}})
	first, err := r.ListTimeline(context.Background(), JourneyListRequest{})
	if err != nil || len(first.Items) != 50 || first.NextCursor == "" || first.Partial {
		t.Fatalf("page: %+v %v", first, err)
	}
	// A newly published point cannot enter the fixed view mid-pagination.
	journeyFixture(t, root, vid, vault.NewID("s", now.Add(time.Hour)), "soul", now.Add(time.Hour))
	// Returned memory cannot alter the retained view.
	first.Items[0].RecordedInstanceID = "mutated"
	second, err := r.ListTimeline(context.Background(), JourneyListRequest{Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 5 || second.NextCursor != "" || first.ViewRevision != second.ViewRevision {
		t.Fatalf("fixed page: %+v %v", second, err)
	}
	again, _ := r.ListTimeline(context.Background(), JourneyListRequest{Cursor: first.NextCursor})
	if again.Items[0].SnapshotRef != second.Items[0].SnapshotRef {
		t.Fatal("cursor was destructive")
	}
	_, err = r.ListTimeline(context.Background(), JourneyListRequest{Cursor: first.NextCursor, Tier: "soul"})
	if journeyCode(err) != "cursor_expired" {
		t.Fatalf("changed filter accepted: %v", err)
	}
	filtered, err := r.ListTimeline(context.Background(), JourneyListRequest{Tier: "soul"})
	if err != nil || len(filtered.Items) != 1 {
		t.Fatalf("tier filter: %+v %v", filtered, err)
	}
	from := now.Add(-time.Hour)
	to := now
	filtered, err = r.ListTimeline(context.Background(), JourneyListRequest{From: &from, To: &to})
	if err != nil || len(filtered.Items) != 2 {
		t.Fatalf("time filter: %+v %v", filtered, err)
	}
	r.Invalidate()
	_, err = r.ListTimeline(context.Background(), JourneyListRequest{Cursor: first.NextCursor})
	if journeyCode(err) != "cursor_expired" {
		t.Fatal(err)
	}
}

func TestJourneyExpiryLimitsOfflineAndDetail(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	vid := vault.NewID("v", now)
	sid := vault.NewID("s", now)
	journeyFixture(t, root, vid, sid, "full", now)
	journeyFixture(t, root, vid, vault.NewID("s", now.Add(time.Second)), "soul", now.Add(-time.Hour))
	r := NewJourneyReader([]config.Repo{{Name: "online", Path: root}, {Name: "offline", Path: filepath.Join(t.TempDir(), "missing")}})
	r.now = func() time.Time { return now }
	first, err := r.ListTimeline(context.Background(), JourneyListRequest{Limit: 1})
	if err != nil || !first.Partial || len(first.Items) != 1 || len(first.Observations) != 2 {
		t.Fatalf("offline: %+v %v", first, err)
	}
	r.now = func() time.Time { return now.Add(11 * time.Minute) }
	_, err = r.ListTimeline(context.Background(), JourneyListRequest{Limit: 1, Cursor: first.NextCursor})
	if journeyCode(err) != "cursor_expired" {
		t.Fatalf("expired: %v", err)
	}
	for _, limit := range []int{-1, 201} {
		_, err = r.ListTimeline(context.Background(), JourneyListRequest{Limit: limit})
		if journeyCode(err) != "invalid_limit" {
			t.Fatal(err)
		}
	}
	p, err := r.GetTimelinePoint(context.Background(), JourneyPointRequest{SnapshotRef: journey.SnapshotRef{VaultID: vid, SnapshotID: sid}})
	if err != nil || len(p.Copies) != 1 || p.MetadataState != "metadata_only" {
		t.Fatalf("detail: %+v %v", p, err)
	}
	_, err = r.GetTimelinePoint(context.Background(), JourneyPointRequest{SnapshotRef: journey.SnapshotRef{VaultID: vid, SnapshotID: "missing"}})
	if journeyCode(err) != "result_unknown" {
		t.Fatal(err)
	}
}

func TestJourneyExplicitSelectionAndCancellation(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	vid, sid := vault.NewID("v", now), vault.NewID("s", now)
	journeyFixture(t, root, vid, sid, "full", now)
	r := NewJourneyReader(nil)
	selected := []journey.Source{{Root: root, LocationRef: journey.LocationRef{Kind: "recovery_cache", ID: "selected", AuthorityID: "local"}}}
	page, err := r.ListTimeline(context.Background(), JourneyListRequest{Vaults: selected})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("explicit read: %+v %v", page, err)
	}
	selected[0].Root = "relative"
	_, err = r.ListTimeline(context.Background(), JourneyListRequest{Vaults: selected})
	if journeyCode(err) != "source_conflict" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.ListTimeline(ctx, JourneyListRequest{}); err != context.Canceled {
		t.Fatal(err)
	}
}
