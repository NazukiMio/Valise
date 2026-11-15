package service

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/journey"
	"valise/core/vault"
)

func selectedTestWorld(t *testing.T, local bool) (*Service, SelectedRestoreTest, string) {
	t.Helper()
	s, home, older, _ := sealedWorld(t)
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if local {
		repo := t.TempDir()
		if err := writeTree(filepath.Join(repo, "box"), "box"); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(home, ".local/share/valise/vaults")
		if err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			dest := filepath.Join(repo, "box", rel)
			if e.IsDir() {
				return os.MkdirAll(dest, 0700)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(dest, b, 0600)
		}); err != nil {
			t.Fatal(err)
		}
		s.Config.Repos = []config.Repo{{Name: "disk", Path: repo}}
	}
	page, err := s.ListTimeline(context.Background(), JourneyListRequest{AllLocations: true, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page.Items {
		if p.SnapshotRef.SnapshotID != older {
			continue
		}
		for _, source := range s.RestoreSources(p) {
			if (source.Copy.LocationRef.Kind == "local_repo") == local {
				return s, SelectedRestoreTest{Target: "box", Instance: "demo", Snapshot: p.SnapshotRef, Source: source}, home
			}
		}
	}
	t.Fatal("selected point/copy missing")
	return nil, SelectedRestoreTest{}, ""
}

func TestSelectedRestoreTestUsesOlderPointAndChosenCopy(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "server", true: "local"}[local], func(t *testing.T) {
			s, req, home := selectedTestWorld(t, local)
			log := filepath.Join(t.TempDir(), "ssh.log")
			t.Setenv("FAKESSH_LOG", log)
			if local {
				// Simulate an older copy which survives only in the repository,
				// while a different, newer backup remains on the source server.
				_, leaf, _ := strings.Cut(req.Source.Copy.SourceLocator, "/")
				for _, rel := range []string{vault.MetaPath(req.Snapshot.SnapshotID), vault.SnapshotPath(req.Snapshot.SnapshotID)} {
					p := filepath.Join(home, ".local/share/valise/vaults", filepath.FromSlash(leaf), rel)
					if err := os.Rename(p, filepath.Join(t.TempDir(), filepath.Base(p))); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := s.RunSelectedRestoreTest(context.Background(), req)
			if err != nil || result == nil || !result.OK || result.Snapshot != req.Snapshot.SnapshotID || result.VaultID != req.Snapshot.VaultID || result.HeaderDigest != req.Source.Copy.HeaderDigest || result.SourceLocation == nil || *result.SourceLocation != req.Source.Copy.LocationRef {
				t.Fatalf("selected point/copy was replaced: %+v, %v", result, err)
			}
			if result.Tier != "soul" {
				t.Fatal("tested latest instead of selected older point")
			}
			if b, err := os.ReadFile(log); err != nil || local && strings.Contains(string(b), "vault-cat") {
				t.Fatal("selected local copy fell back to remote material", err)
			}
			previous, _ := s.LastRestoreTest(req.Target, req.Instance)
			req.Source.Copy.HeaderDigest = strings.Repeat("0", 64)
			if result, err := s.RunSelectedRestoreTest(context.Background(), req); err == nil || result != nil {
				t.Fatal("changed source digest was accepted")
			}
			after, _ := s.LastRestoreTest(req.Target, req.Instance)
			if after.OpID != previous.OpID {
				t.Fatal("invalid selection started or replaced a test record")
			}
		})
	}
}

func TestSelectedRestoreTestRejectsWrongInstanceAndUnconfiguredSource(t *testing.T) {
	s := &Service{}
	for _, req := range []SelectedRestoreTest{
		{},
		{Target: "box", Instance: "other", Snapshot: journey.SnapshotRef{VaultID: "bad", SnapshotID: "bad"}},
	} {
		if result, err := s.RunSelectedRestoreTest(context.Background(), req); err == nil || result != nil {
			t.Fatal("unbound selection accepted")
		}
	}
	s, req, _ := selectedTestWorld(t, false)
	req.Source.Spec = "dir:/outside-the-selected-copy"
	if result, err := s.RunSelectedRestoreTest(context.Background(), req); err == nil || result != nil {
		t.Fatal("arbitrary source substituted")
	}
}

func TestSelectedRestoreTestRejectsMetadataDisagreeingWithEncryptedHeader(t *testing.T) {
	s, req, _ := selectedTestWorld(t, false)
	req.Source.Copy.Metadata.CreatedAt = req.Source.Copy.Metadata.CreatedAt.Add(time.Hour)
	if result, err := s.RunSelectedRestoreTest(context.Background(), req); err == nil || result != nil {
		t.Fatal("plaintext date replaced authenticated snapshot date")
	}
	if _, ok := s.LastRestoreTest(req.Target, req.Instance); ok {
		t.Fatal("mismatched metadata started a test")
	}
}
