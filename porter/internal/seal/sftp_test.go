package seal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"valise/core/stationsync"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

// fakeSFTP puts an sftp on PATH that runs the batch commands Valise sends
// against the local filesystem (the host part is dropped), printing ls -ln
// lines with full paths as OpenSSH does. PATH holds no rsync, so syncing
// to an rsync target falls back to it.
func fakeSFTP(t *testing.T, w *world) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/bash
batch="$2"
while IFS= read -r line; do
  soft=0
  case "$line" in -*) soft=1; line="${line#-}";; esac
  eval "set -- $line"
  cmd="$1"; shift
  ok=0
  case "$cmd" in
    ls) dir="${!#}"; [ -d "$dir" ] || ok=1
        if [ $ok = 0 ]; then for f in "$dir"/* "$dir"/.[!.]*; do
          [ -e "$f" ] || continue
          if [ -d "$f" ]; then echo "drwx------ 1 0 0 0 Jan 1 00:00 $f"
          else echo "-rw------- 1 0 0 $(/usr/bin/wc -c < "$f" | /usr/bin/tr -d ' ') Jan 1 00:00 $f"; fi
        done; fi ;;
    mkdir) /bin/mkdir "$1" 2>/dev/null || ok=1 ;;
    put) /bin/cp "$1" "$2" || ok=1 ;;
    rename) [ -e "$2" ] && ok=1 || /bin/mv "$1" "$2" || ok=1 ;;
    chmod) /bin/chmod "$1" "$2" || ok=1 ;;
    rm) /bin/rm "$1" 2>/dev/null || ok=1 ;;
    rmdir) /bin/rmdir "$1" || ok=1 ;;
    *) ok=1 ;;
  esac
  if [ $ok != 0 ] && [ $soft = 0 ]; then echo "failed: $cmd $*" >&2; exit 1; fi
done < "$batch"
`
	if err := os.WriteFile(filepath.Join(bin, "sftp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	w.env.Vars = []string{"PATH=" + bin + ":/bin"}
}

func TestSyncFallsBackToSFTP(t *testing.T) {
	w := newWorld(t)
	first := w.seal(t, vault.TierSoul)
	fakeSFTP(t, w)
	cloud := filepath.Join(t.TempDir(), "cloud")
	ctx := context.Background()
	if err := SetServerName(w.p, "box"); err != nil {
		t.Fatal(err)
	}
	if err := SetTarget(w.p, vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "nas:" + cloud + "/"}); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(ctx, w.env, w.p, "nas", "client")
	if err != nil {
		t.Fatal(err)
	}
	if res.Method != stationsync.MethodSFTP || !strings.Contains(res.Note, "改用 sftp") {
		t.Fatalf("%+v", res)
	}
	leafDir := filepath.Join(cloud, "box", first.Leaf)
	for _, f := range []string{"../../" + vault.TreeFile, vault.DescriptorFile, vault.MetaPath(first.SnapshotID)} {
		if _, err := os.Stat(filepath.Join(leafDir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	// Append-only: a second seal adds files, nothing is rewritten.
	before, _ := os.Stat(filepath.Join(leafDir, vault.DescriptorFile))
	second := w.seal(t, vault.TierSoul)
	if _, err := Sync(ctx, w.env, w.p, "nas", "client"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(leafDir, vault.DescriptorFile))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("descriptor rewritten")
	}
	if _, err := os.Stat(filepath.Join(leafDir, vault.MetaPath(second.SnapshotID))); err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(cloud, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".part-") {
			t.Fatalf("part file left: %s", p)
		}
		return nil
	})
	// Listing goes through sftp too.
	list, err := TargetList(ctx, w.env, w.p, "nas")
	if err != nil || len(list.Leaves) != 1 || len(list.Leaves[0].Snapshots) != 2 {
		t.Fatalf("%+v %v", list, err)
	}
	// Deleting a directory: marker first, everything gone at the end.
	tg := &vaultrpc.Target{Name: "nas", Kind: "sftp", Dest: "nas:" + cloud + "/"}
	if _, _, err := withRemote(ctx, w.env, w.p, tg, func(r stationsync.Remote) error {
		return r.DeleteDir(ctx, "nas:"+filepath.Join(cloud, "box"), "demo")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cloud, "box", "demo")); !os.IsNotExist(err) {
		t.Fatalf("not deleted: %v", err)
	}
}
