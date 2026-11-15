package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func TestJourneyScopedFakeSSHNeverContactsUnrelatedServer(t *testing.T) {
	s, home, _ := acceptedPlanWorld(t)
	ctx := context.Background()
	if _, err := s.AddServers("box", "offline"); err != nil {
		t.Fatal(err)
	}
	first, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: "full"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, rel := range []string{vault.DescriptorFile, vault.MetaPath(first.SnapshotID), vault.SnapshotPath(first.SnapshotID)} {
		b, err := os.ReadFile(filepath.Join(home, ".local/share/valise/vaults", filepath.FromSlash(first.Leaf), rel))
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(root, "box", filepath.FromSlash(first.Leaf), rel)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.Config.Repos = []config.Repo{{Name: "disk", Path: root}}
	reader := s.timelineReader()
	other := journey.LocationRef{Kind: "server_vault", ID: "offline", AuthorityID: "offline"}
	reader.remoteCache = map[journey.LocationRef]journey.Result{other: journeyUnavailable(other, "unsupported_capability")}
	log := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKESSH_LOG", log)
	t.Setenv("FAKESSH_HANG", "offline")
	reader.locationTimeout = 2 * time.Second
	var last TimelineUpdate
	check := func(u TimelineUpdate) {
		last = u
		for _, pending := range u.Pending {
			if pending == other {
				t.Error("unrelated server entered pending count")
			}
		}
		for _, obs := range u.Page.Observations {
			if obs.LocationRef == other {
				t.Error("unrelated cached error entered scoped view")
			}
		}
	}
	if err := s.ScanTimelineInScope(ctx, TimelineScanRequest{ConnectionIDs: []string{"box"}}, check); err != nil {
		t.Fatal(err)
	}
	if !last.Done || len(last.Page.Items) != 1 || len(last.Page.Items[0].Copies) != 2 {
		t.Fatalf("related copies not merged: %+v", last)
	}
	// A stale UI's retry cannot expand the explicit connection boundary.
	if err := s.ScanTimelineInScope(ctx, TimelineScanRequest{ConnectionIDs: []string{"box"}, Only: []journey.LocationRef{other}}, check); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "alias=offline ") || !strings.Contains(string(b), "alias=box ") {
		t.Fatalf("scope did not constrain SSH: %s", b)
	}
	if err := os.WriteFile(log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ScanTimelineInScope(ctx, TimelineScanRequest{LocalOnly: true}, func(u TimelineUpdate) {
		for _, obs := range u.Page.Observations {
			if obs.LocationRef.Kind != "local_repo" {
				t.Error("startup inherited remote observation")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(log)
	if err != nil || len(b) != 0 {
		t.Fatalf("startup contacted remote: %s %v", b, err)
	}
}

func TestJourneyConnectionsOnlyFollowExactMigrationGenerations(t *testing.T) {
	ref := func(server, generation string) identity.AgentRef {
		return identity.AgentRef{ServerUID: server, InstallID: "demo", InstanceID: "demo", Generation: generation}
	}
	a, b, clone, reused := ref("a", "a1"), ref("b", "b1"), ref("clone", "c1"), ref("a", "a2")
	items := []AgentSummary{
		{ConnectionID: "a", Instance: enroll.InstanceSummary{InstanceID: "demo"}, AgentRef: &a},
		{ConnectionID: "b", Instance: enroll.InstanceSummary{InstanceID: "demo"}, AgentRef: &b, Relations: []identity.RelationSummary{{Kind: identity.MigrationCompleted, SourceAgent: &a, TargetAgent: b}}},
		{ConnectionID: "clone", Instance: enroll.InstanceSummary{InstanceID: "demo"}, AgentRef: &clone, Relations: []identity.RelationSummary{{Kind: identity.CloneFrom, SourceAgent: &b, TargetAgent: clone}}},
		{ConnectionID: "reused", Instance: enroll.InstanceSummary{InstanceID: "demo"}, AgentRef: &reused},
	}
	for _, conn := range []string{"a", "b"} {
		if got := JourneyConnections(items, conn, "demo"); !slices.Equal(got, []string{"a", "b"}) {
			t.Fatalf("migration scope %+v", got)
		}
	}
	for _, conn := range []string{"clone", "reused", "unknown"} {
		if got := JourneyConnections(items, conn, "demo"); !slices.Equal(got, []string{conn}) {
			t.Fatalf("independent instance joined another journey: %+v", got)
		}
	}
}
