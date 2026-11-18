package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"valise/core/journey"
	"valise/core/vault"
)

func TestPromoteAutoSnapshotOverFakeSSH(t *testing.T) {
	s, _, _, _ := sealedWorld(t)
	ctx := context.Background()
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if _, err := s.RepoAdd("local", repo); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(s.Config.SSHBinary, "box", "$HOME/.local/bin/valise-porter seal-timer --instance demo").CombinedOutput(); err != nil {
		t.Fatalf("fake timer: %s %v", out, err)
	}
	st, err := s.VaultStatus(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	auto := ""
	for _, m := range st.Snapshots {
		if m.Kind == vault.KindAuto {
			auto = m.SnapshotID
		}
	}
	if auto == "" {
		t.Fatal("timed backup did not produce an automatic snapshot")
	}
	if err := s.PromoteSnapshot(ctx, "box", "demo", auto, "  release  "); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(repo, "box", "demo", vault.SavepointsPeriod)
	if _, err := os.Stat(filepath.Join(dest, vault.MetaPath(auto))); err != nil {
		t.Fatal("promotion did not sync its permanent leaf locally:", err)
	}
	n := 0
	err = s.ScanTimelineInScope(ctx, TimelineScanRequest{ConnectionIDs: []string{"box"}}, func(update TimelineUpdate) {
		if !update.Done {
			return
		}
		for _, p := range update.Page.Items {
			if p.SnapshotRef.SnapshotID == auto {
				n++
				if p.Kind != vault.KindSavepoint || p.Note != "release" || p.MetadataState == "conflict" {
					t.Fatalf("wrong timeline projection: %+v", p)
				}
				for _, cp := range p.Copies {
					if cp.LocationRef.Kind == "local_repo" && !isLocalSavepoint(cp) {
						t.Fatalf("restoration points to the auto period: %+v", cp)
					}
				}
			}
		}
	})
	if err != nil || n != 1 {
		t.Fatalf("timeline has %d matching points: %v", n, err)
	}
}

func isLocalSavepoint(cp journey.Copy) bool {
	return filepath.Base(cp.SourceLocator) == vault.SavepointsPeriod
}
