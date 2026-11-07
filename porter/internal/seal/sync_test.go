package seal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

// fakeRsync puts an rsync wrapper first on PATH that maps "nas:<path>" to
// a local path and drops -e, so sync, listing and deletion run against a
// local directory. It needs GNU rsync >= 3.2.3 (--mkpath); macOS's
// openrsync is not enough, so the test runs on the Linux targets.
func fakeRsync(t *testing.T, w *world) {
	t.Helper()
	real, err := exec.LookPath("rsync")
	if err != nil {
		t.Skip("rsync not installed")
	}
	if out, _ := exec.Command(real, "--help").CombinedOutput(); !strings.Contains(string(out), "--mkpath") {
		t.Skip("needs GNU rsync >= 3.2.3")
	}
	bin := t.TempDir()
	script := `#!/bin/bash
out=()
skip=0
for a in "$@"; do
  if [ $skip = 1 ]; then skip=0; continue; fi
  if [ "$a" = "-e" ]; then skip=1; continue; fi
  case "$a" in nas:*) a="${a#nas:}";; esac
  out+=("$a")
done
exec ` + real + ` "${out[@]}"
`
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	w.env.Vars = []string{"PATH=" + bin + ":/usr/bin:/bin"}
}

func TestCloudBackup(t *testing.T) {
	w := newWorld(t)
	fakeRsync(t, w)
	spawnDefault := Spawn
	t.Cleanup(func() { now = time.Now; Spawn = spawnDefault })
	spawned := 0
	Spawn = func(string) error { spawned++; return nil }
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return base }
	cloud := filepath.Join(t.TempDir(), "cloud")
	ctx := context.Background()

	if err := SetTarget(w.p, vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "nas:" + cloud + "/", Auto: &vaultrpc.AutoSync{Every: "10m"}}); err == nil {
		t.Fatal("auto interval under an hour accepted")
	}
	if err := SetTarget(w.p, vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "nas:" + cloud + "/", Auto: &vaultrpc.AutoSync{Every: "1d"}}); err != nil {
		t.Fatal(err)
	}
	first := w.seal(t, vault.TierSoul)
	if len(first.AutoSync) != 1 || spawned != 1 {
		t.Fatalf("auto sync not started: %v %d", first.AutoSync, spawned)
	}
	if _, err := Sync(ctx, w.env, w.p, "nas", "client"); err == nil {
		t.Fatal("synced a tree without a server name")
	}
	if err := SetServerName(w.p, "box"); err != nil {
		t.Fatal(err)
	}
	if err := AutoSync(ctx, w.env, w.p); err != nil {
		t.Fatal(err)
	}
	leafDir := filepath.Join(cloud, "box", first.Leaf)
	for _, f := range []string{"../../" + vault.TreeFile, vault.DescriptorFile, vault.PeriodFile, vault.MetaPath(first.SnapshotID)} {
		if _, err := os.Stat(filepath.Join(leafDir, f)); err != nil {
			t.Fatalf("%s not synced: %v", f, err)
		}
	}
	if probs, err := vault.VerifyNoKey(leafDir); err != nil || len(probs) != 0 {
		t.Fatal("cloud leaf does not verify", probs, err)
	}
	// Synced within the interval: the next seal does not start another.
	if w.seal(t, vault.TierSoul).AutoSync != nil {
		t.Fatal("auto sync started before the interval")
	}
	now = func() time.Time { return base.AddDate(0, 0, 40) }
	second := w.seal(t, vault.TierSoul)
	if second.Leaf == first.Leaf || len(second.AutoSync) != 1 {
		t.Fatalf("%s %v", second.Leaf, second.AutoSync)
	}
	if err := AutoSync(ctx, w.env, w.p); err != nil {
		t.Fatal(err)
	}
	list, err := TargetList(ctx, w.env, w.p, "nas")
	if err != nil || len(list.Leaves) != 2 || list.Leaves[0].Leaf != first.Leaf || len(list.Leaves[0].Snapshots) != 2 || len(list.Leaves[1].Packs) == 0 {
		t.Fatalf("%+v %v", list, err)
	}
	if err := TargetLeafDelete(ctx, w.env, w.p, "nas", second.Leaf, "client"); err == nil {
		t.Fatal("deleted the current leaf on the target")
	}
	if err := TargetLeafDelete(ctx, w.env, w.p, "nas", first.Leaf, "client"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leafDir); !os.IsNotExist(err) {
		t.Fatalf("leaf still on the target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cloud, "box", second.Leaf, vault.DescriptorFile)); err != nil {
		t.Fatal("other leaf touched")
	}
	list, _ = TargetList(ctx, w.env, w.p, "nas")
	if len(list.Leaves) != 1 {
		t.Fatalf("%+v", list.Leaves)
	}
	// The server copy is untouched by a target deletion.
	if st, _ := Status(w.p); len(st.Leaves) != 2 {
		t.Fatal("server leaves changed")
	}
}

func TestBusyNamesAutoSync(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	unlock, err := p.LockFor("autosync")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Lock()
	if err == nil || !strings.Contains(err.Error(), "自动同步到备份位置") {
		t.Fatalf("busy error does not name the holder: %v", err)
	}
	unlock()
	u2, err := p.Lock()
	if err != nil {
		t.Fatal(err)
	}
	u2()
}
