package seal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"valise/core/stationsync"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// rcloneWorld gives the server a Kit-installed rclone and a Valise rclone
// config whose remote is an alias of a local folder: no login involved.
func rcloneWorld(t *testing.T, w *world) string {
	t.Helper()
	bin, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not installed")
	}
	os.MkdirAll(filepath.Dir(w.p.Rclone()), 0o700)
	if err := os.Symlink(bin, w.p.Rclone()); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(t.TempDir(), "drive")
	os.MkdirAll(filepath.Dir(w.p.RcloneConfig()), 0o700)
	os.WriteFile(w.p.RcloneConfig(), []byte("[valise-gd]\ntype = alias\nremote = "+cloud+"\n"), 0o600)
	return cloud
}

func TestRcloneTarget(t *testing.T) {
	w := newWorld(t)
	cloud := rcloneWorld(t, w)
	first := w.seal(t, vault.TierSoul)
	ctx := context.Background()
	if err := SetServerName(w.p, "box"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"gd:valise-vault/", "valise-gd:/abs", "valise-gd:../x", "valise-gd:a b"} {
		if err := SetTarget(w.p, vaultrpc.Target{Name: "gd", Kind: "rclone", Dest: bad}); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if err := SetTarget(w.p, vaultrpc.Target{Name: "gd", Kind: "rclone", Dest: "valise-gd:valise-vault/"}); err != nil {
		t.Fatal(err)
	}
	chk, err := CheckTarget(ctx, w.env, w.p, vaultrpc.TargetCheckParams{Name: "gd"})
	if err != nil || !chk.Reachable {
		t.Fatalf("%+v %v", chk, err)
	}
	res, err := Sync(ctx, w.env, w.p, "gd", "client")
	if err != nil || res.Method != stationsync.MethodRclone || len(res.Checks) != 1 || res.Checks[0].Result != "passed" {
		t.Fatalf("%+v %v", res, err)
	}
	leafDir := filepath.Join(cloud, "valise-vault", "box", first.Leaf)
	if probs, err := vault.VerifyNoKey(leafDir); err != nil || len(probs) != 0 {
		t.Fatal(probs, err)
	}
	list, err := TargetList(ctx, w.env, w.p, "gd")
	if err != nil || len(list.Leaves) != 1 || len(list.Leaves[0].Snapshots) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	v, err := TargetVerify(ctx, w.env, w.p, vaultrpc.TargetVerifyParams{Target: "gd", Leaf: first.Leaf, Packs: true})
	basis, _ := LeafMaterials(w.p, first.Leaf, true)
	if err != nil || len(v.Integrity) != 0 || !vault.CompareMaterials(basis.Materials, v.Materials).OK() {
		t.Fatalf("%+v %v", v, err)
	}
	// Without Valise's rclone the location says so instead of guessing.
	os.Remove(w.p.Rclone())
	if _, err := Sync(ctx, w.env, w.p, "gd", "client"); err == nil {
		t.Fatal("synced without rclone")
	}
}
