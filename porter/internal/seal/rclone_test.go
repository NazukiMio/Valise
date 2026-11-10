package seal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/core/kittools"
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

func TestCloudPrepareAndForget(t *testing.T) {
	w := newWorld(t)
	rcloneWorld(t, w) // valise-gd exists already
	ctx := context.Background()
	gd := kittools.Cloud{ID: "gdrive", Name: "Google Drive", RcloneType: "drive", Params: map[string]string{"scope": "drive.file"},
		Access: kittools.AccessFolder, AccessNote: "n", AskApp: true}
	// gd is a saved location: its login stays.
	if err := SetTarget(w.p, vaultrpc.Target{Name: "gd", Kind: "rclone", Dest: "valise-gd:valise-vault/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "gd", Cloud: gd}); err == nil || !strings.Contains(err.Error(), "target_exists") {
		t.Fatalf("existing location: %v", err)
	}
	// A login no location uses is left over and replaced.
	if code, _, _, err := (envRunner{w.env, w.p.Home}).Run(ctx, 30*time.Second, w.p.Rclone(), "config", "create", "valise-left", "alias", "remote=/nonexistent", "--config", w.p.RcloneConfig()); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "left", Cloud: gd}); err != nil {
		t.Fatalf("leftover login: %v", err)
	}
	if names, _ := rcloneRemotes(ctx, w.env, w.p); slices.Contains(names, "valise-left") {
		t.Fatalf("leftover kept: %v", names)
	}
	bad := gd
	bad.Params = map[string]string{"token": "x"}
	if _, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "other", Cloud: bad}); err == nil {
		t.Fatal("token parameter accepted")
	}
	res, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "drive2", Cloud: gd, Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{w.p.Rclone(), "config", "create", "valise-drive2", "drive", "scope=drive.file", "config_is_local=false", "--no-output", "--all", "--config", w.p.RcloneConfig()}
	if strings.Join(res.Argv, " ") != strings.Join(want, " ") {
		t.Fatalf("%v", res.Argv)
	}
	if err := CloudForget(ctx, w.env, w.p, "gd", false); err == nil {
		t.Fatal("forgot a login a location uses")
	}
	if err := CloudForget(ctx, w.env, w.p, "gd", true); err != nil {
		t.Fatal(err)
	}
	if t2, _ := findTarget(w.p, "gd"); t2 != nil {
		t.Fatal("location kept")
	}
	if b, _ := os.ReadFile(w.p.RcloneConfig()); strings.Contains(string(b), "valise-gd") {
		t.Fatal("login still in the config")
	}
}

// The form login: each step returns only rclone's question; the browser
// step is prepared for the login relay, continuing the same login.
func TestCloudStep(t *testing.T) {
	w := newWorld(t)
	rcloneWorld(t, w)
	ctx := context.Background()
	gd := kittools.Cloud{ID: "gdrive", Name: "Google Drive", RcloneType: "drive", Params: map[string]string{"scope": "drive.file"},
		Access: kittools.AccessFolder, AccessNote: "n", AskApp: true}
	q, err := CloudStep(ctx, w.env, w.p, vaultrpc.CloudStepParams{Name: "drive3", Cloud: gd})
	if err != nil || q.Name != kittools.QuestionSharedApp || q.State == "" {
		t.Fatalf("%+v %v", q, err)
	}
	q, err = CloudStep(ctx, w.env, w.p, vaultrpc.CloudStepParams{Name: "drive3", Cloud: gd, State: q.State, Result: "true"})
	if err != nil || q.Name != kittools.QuestionIsLocal {
		t.Fatalf("%+v %v", q, err)
	}
	res, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "drive3", Cloud: gd, State: q.State})
	if err != nil || !slices.Contains(res.Argv, "--continue") || !slices.Contains(res.Argv, q.State) {
		t.Fatalf("%v %v", res, err)
	}
	// With the user's own app rclone goes straight to the browser step.
	q, err = CloudStep(ctx, w.env, w.p, vaultrpc.CloudStepParams{Name: "drive4", Cloud: gd, App: kittools.App{ClientID: "id.apps", ClientSecret: "sec"}})
	if err != nil || q.Name != kittools.QuestionIsLocal {
		t.Fatalf("%+v %v", q, err)
	}
	// A login that was never started cannot be continued.
	if _, err := CloudStep(ctx, w.env, w.p, vaultrpc.CloudStepParams{Name: "nope", Cloud: gd, State: "s", Result: "x"}); err == nil {
		t.Fatal("continued a login that does not exist")
	}
	if _, err := CloudStep(ctx, w.env, w.p, vaultrpc.CloudStepParams{Name: "gd", Cloud: gd, App: kittools.App{ClientSecret: "x"}}); err == nil {
		t.Fatal("secret without id accepted")
	}
}
