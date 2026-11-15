package revive

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
)

func copyBinding(t *testing.T, home string, ref identity.SnapshotRef, id string, differentGeneration bool) {
	t.Helper()
	var run protection.BackupRun
	b, err := os.ReadFile(filepath.Join(home, ".local/state/valise/runs", strings.Repeat("a", 32)+".json"))
	if err != nil || json.Unmarshal(b, &run) != nil {
		t.Fatal("binding fixture missing", err)
	}
	run.RunID, run.Request.RunID, run.Snapshot = id, id, &ref
	if differentGeneration {
		run.AgentRef.Generation = "different"
		run.Request.AgentRef = run.AgentRef
		run.Plan.AgentRef = run.AgentRef
	}
	run.RequestDigest, err = protection.Digest(run.Request)
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".local/state/valise/runs", id+".json"), string(b), 0600)
}

func composeWorld(t *testing.T, program ...[]byte) (*world, revive.Plan) {
	t.Helper()
	w, p := environmentWorld(t, program...)
	q := p.IsolatedTest
	var base environment.TestSnapshot
	b, err := json.Marshal(q)
	if err != nil || json.Unmarshal(b, &base) != nil {
		t.Fatal("cannot copy encrypted snapshot fixture", err)
	}
	base.Header.CreatedAt = base.Header.CreatedAt.Add(-time.Hour)
	base.Files.Files = []vault.File{
		{Root: "state", Path: "SOUL.md", Category: "identity", Size: 3, SHA256: vault.SHA256Hex([]byte("old")), Mode: 0600},
		{Root: "state", Path: "memory/deleted", Category: "memory", Size: 7, SHA256: vault.SHA256Hex([]byte("deleted")), Mode: 0600},
		{Root: "state", Path: ".env", Category: "secrets", Size: 11, SHA256: vault.SHA256Hex([]byte("base secret")), Mode: 0600},
	}
	q.Baseline = &base
	p.SnapshotID = vault.NewID("s", q.Header.CreatedAt.Add(time.Minute))
	p.Tier, q.Header.Tier = vault.TierSoul, vault.TierSoul
	q.Header.SnapshotID = p.SnapshotID
	copyBinding(t, w.home, identity.SnapshotRef{VaultID: q.VaultID, SnapshotID: p.SnapshotID}, strings.Repeat("b", 32), false)
	return w, p
}

func TestEnvironmentCompositionKeepsOnlyOutsideScopeFromExactBase(t *testing.T) {
	w, p := composeWorld(t)
	w.stage(t, p, []snapFile{{"SOUL.md", "snapshot"}, {".env", "base secret"}})
	j, _ := w.r.Store.Load(p.OpID)
	in, source, _, err := w.r.environmentTestInput(context.Background(), j)
	if err != nil || in.Baseline == nil {
		t.Fatal("compatible base refused", err)
	}
	if _, err := in.OpenData(context.Background(), in.Target.Source.Snapshot, "state", ".env"); err == nil {
		t.Fatal("outside-scope data read under target provenance")
	}
	reader, err := in.OpenData(context.Background(), in.Baseline.Source.Snapshot, "state", ".env")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(b) != "base secret" {
		t.Fatal("wrong baseline bytes", err)
	}
	if !isolated.ValidHome(in.Layout.Home) {
		t.Skip("reserved temporary HOME; composition/reader verified, private build covered on compatible layout and explicit Linux lab")
	}
	archive, err := environments.FreezeInstalledNPM(context.Background(), t.TempDir(), source, p.IsolatedTest.Header.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.File.Close()
	in.Program, in.Archive = archive.Material, archive.File
	in.Target.Source.LayoutDigest = environments.LayoutDigest(in.Layout, in.Overrides)
	in.Baseline.Source.LayoutDigest = in.Target.Source.LayoutDigest
	candidate, err := environments.Build(context.Background(), t.TempDir(), in)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(candidate.Workspace.HomeDir(), ".demo")
	if read(t, filepath.Join(root, "SOUL.md")) != "snapshot" || read(t, filepath.Join(root, ".env")) != "base secret" {
		t.Fatal("source composition changed")
	}
	if _, err := os.Lstat(filepath.Join(root, "memory/deleted")); !os.IsNotExist(err) {
		t.Fatal("deleted target-scope content resurrected")
	}
	if read(t, filepath.Join(w.state, ".env")) != "KEY=current" {
		t.Fatal("active credentials were used or changed")
	}
}

func TestEnvironmentCompositionRejectsFutureOrWrongGeneration(t *testing.T) {
	for _, kind := range []string{"future", "generation", "dependency", "full_target"} {
		t.Run(kind, func(t *testing.T) {
			w, p := composeWorld(t)
			switch kind {
			case "future":
				p.IsolatedTest.Baseline.Header.CreatedAt = p.IsolatedTest.Header.CreatedAt.Add(time.Second)
			case "generation":
				copyBinding(t, w.home, identity.SnapshotRef{VaultID: p.IsolatedTest.Baseline.VaultID, SnapshotID: p.IsolatedTest.Baseline.Header.SnapshotID}, strings.Repeat("a", 32), true)
			case "dependency":
				p.IsolatedTest.Baseline.Header.Runtime.Dependencies[0].SHA256 = strings.Repeat("0", 64)
			case "full_target":
				p.Tier = vault.TierFull
				p.IsolatedTest.Header.Tier = vault.TierFull
			}
			w.stage(t, p, []snapFile{{"SOUL.md", "snapshot"}, {".env", "base secret"}})
			j, _ := w.r.Store.Load(p.OpID)
			if _, _, _, err := w.r.environmentTestInput(context.Background(), j); err == nil {
				t.Fatal("invalid baseline admitted")
			}
			if j.EnvironmentTest != nil {
				t.Fatal("rejected base started an execution")
			}
		})
	}
}
