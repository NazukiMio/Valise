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
	w.env = w.env.With(map[string]string{"PATH": t.TempDir()})
	if _, err := Sync(ctx, w.env, w.p, "gd", "client"); err == nil || !strings.Contains(err.Error(), "rclone_missing") {
		t.Fatalf("synced without rclone: %v", err)
	}
}

func TestCloudPrepareAndForget(t *testing.T) {
	w := newWorld(t)
	rcloneWorld(t, w) // valise-gd exists already
	ctx := context.Background()
	gd := kittools.Cloud{ID: "gdrive", Name: "Google Drive", RcloneType: "drive", Params: map[string]string{"scope": "drive.file"},
		Access: kittools.AccessFolder, AccessNote: "n", AskApp: true}
	// Non-cloud locations reject collisions; saved cloud locations stay
	// protected against unconfirmed CloudForget, while allowing re-login
	// (replacing the rclone remote without dropping the saved target).
	if err := SetTarget(w.p, vaultrpc.Target{Name: "nas", Kind: "sftp", Dest: "nas:/backup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "nas", Cloud: gd}); err == nil || !strings.Contains(err.Error(), "target_exists") {
		t.Fatalf("non-cloud collision: %v", err)
	}
	if err := SetTarget(w.p, vaultrpc.Target{Name: "gd", Kind: "rclone", Dest: "valise-gd:valise-vault/"}); err != nil {
		t.Fatal(err)
	}
	if err := CloudForget(ctx, w.env, w.p, "gd", false); err == nil {
		t.Fatal("forgot a login a location uses")
	}
	if _, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "gd", Cloud: gd}); err != nil {
		t.Fatalf("relogin on existing cloud location: %v", err)
	}
	if names, _ := rcloneRemotes(ctx, w.env, w.p); slices.Contains(names, "valise-gd") {
		t.Fatalf("old remote still present before fresh login: %v", names)
	}
	if t2, _ := findTarget(w.p, "gd"); t2 == nil {
		t.Fatal("saved cloud target dropped during relogin prepare")
	}
	if code, _, _, err := (envRunner{w.env, w.p.Home}).Run(ctx, 30*time.Second, w.p.Rclone(), "config", "create", "valise-gd", "alias", "remote="+t.TempDir(), "--config", w.p.RcloneConfig()); err != nil || code != 0 {
		t.Fatal(code, err)
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

// pathRclone puts a logging fake rclone alone on a fresh PATH dir (no Kit
// rclone) and returns the log file of the argv lines it was run with.
func pathRclone(t *testing.T, w *world) string {
	t.Helper()
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "argv.log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\ncase \"$1\" in listremotes) echo valise-gd: ;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "rclone"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	w.env = w.env.With(map[string]string{"PATH": dir + ":/usr/bin:/bin"})
	os.MkdirAll(filepath.Dir(w.p.RcloneConfig()), 0o700)
	os.WriteFile(w.p.RcloneConfig(), []byte("[valise-gd]\ntype = alias\nremote = "+t.TempDir()+"\n"), 0o600)
	return log
}

func fakeBin(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRclone(t *testing.T) {
	w := newWorld(t)
	dir := t.TempDir()
	pathBin := filepath.Join(dir, "rclone")
	fakeBin(t, pathBin)
	w.env = w.env.With(map[string]string{"PATH": dir})
	got, err := w.p.ResolveRclone(w.env)
	if err != nil || got != pathBin {
		t.Fatalf("PATH fallback: %q %v", got, err)
	}
	fakeBin(t, w.p.Rclone())
	got, err = w.p.ResolveRclone(w.env)
	if err != nil || got != w.p.Rclone() {
		t.Fatalf("Kit should win: %q %v", got, err)
	}
	// A non-executable Kit file is ignored.
	os.Chmod(w.p.Rclone(), 0o644)
	if got, _ := w.p.ResolveRclone(w.env); got != pathBin {
		t.Fatalf("non-executable Kit rclone used: %q", got)
	}
	os.Remove(w.p.Rclone())
	os.Remove(pathBin)
	if _, err := w.p.ResolveRclone(w.env); err == nil || !strings.Contains(err.Error(), "rclone_missing") {
		t.Fatalf("want rclone_missing: %v", err)
	}
	if _, err := w.p.ResolveRclone(nil); err == nil || !strings.Contains(err.Error(), "rclone_missing") {
		t.Fatalf("nil env: %v", err)
	}
}

func TestRcloneFromPath(t *testing.T) {
	w := newWorld(t)
	log := pathRclone(t, w)
	ctx := context.Background()
	w.seal(t, vault.TierSoul)
	if err := SetServerName(w.p, "box"); err != nil {
		t.Fatal(err)
	}
	if err := SetTarget(w.p, vaultrpc.Target{Name: "gd", Kind: "rclone", Dest: "valise-gd:valise-vault/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.p.Rclone()); err == nil {
		t.Fatal("Kit rclone should not exist")
	}
	notMissing := func(what string, err error) {
		t.Helper()
		if err != nil && strings.Contains(err.Error(), "rclone_missing") {
			t.Fatalf("%s: PATH rclone not used: %v", what, err)
		}
	}
	_, err := CheckTarget(ctx, w.env, w.p, vaultrpc.TargetCheckParams{Name: "gd"})
	notMissing("check", err)
	_, err = Sync(ctx, w.env, w.p, "gd", "client")
	notMissing("sync", err)
	gd := kittools.Cloud{ID: "gdrive", Name: "Google Drive", RcloneType: "drive", Params: map[string]string{"scope": "drive.file"},
		Access: kittools.AccessFolder, AccessNote: "n", AskApp: true}
	res, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "drive2", Cloud: gd, Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Argv[0] == w.p.Rclone() || !strings.HasSuffix(res.Argv[0], "/rclone") || res.Argv[len(res.Argv)-2] != "--config" ||
		res.Argv[len(res.Argv)-1] != w.p.RcloneConfig() {
		t.Fatalf("%v", res.Argv)
	}
	_, err = CloudStep(ctx, w.env, w.p, vaultrpc.CloudStepParams{Name: "drive3", Cloud: gd})
	notMissing("step", err)
	if err := SetTarget(w.p, vaultrpc.Target{Name: "gd", Kind: "rclone", Dest: "valise-gd:valise-vault/"}); err != nil {
		t.Fatal(err)
	}
	notMissing("forget", CloudForget(ctx, w.env, w.p, "gd", true))
	b, _ := os.ReadFile(log)
	for _, want := range []string{"listremotes --config " + w.p.RcloneConfig(), "config delete valise-gd --config " + w.p.RcloneConfig()} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("fake rclone never ran %q:\n%s", want, b)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !strings.Contains(line, "--config "+w.p.RcloneConfig()) {
			t.Fatalf("rclone run without our config: %q", line)
		}
	}
}

func TestRcloneMissingEverywhere(t *testing.T) {
	w := newWorld(t)
	w.env = w.env.With(map[string]string{"PATH": t.TempDir()})
	ctx := context.Background()
	gd := kittools.Cloud{ID: "gdrive", Name: "Google Drive", RcloneType: "drive", Params: map[string]string{"scope": "drive.file"},
		Access: kittools.AccessFolder, AccessNote: "n", AskApp: true}
	if _, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "x", Cloud: gd}); err == nil || !strings.Contains(err.Error(), "rclone_missing") {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := CloudStep(ctx, w.env, w.p, vaultrpc.CloudStepParams{Name: "x", Cloud: gd}); err == nil || !strings.Contains(err.Error(), "rclone_missing") {
		t.Fatalf("step: %v", err)
	}
	if err := CloudForget(ctx, w.env, w.p, "x", false); err == nil || !strings.Contains(err.Error(), "rclone_missing") {
		t.Fatalf("forget: %v", err)
	}
}

func TestRcloneBucketTargetAndNonBrowserPrepare(t *testing.T) {
	w := newWorld(t)
	rcloneWorld(t, w)
	ctx := context.Background()
	if err := SetTarget(w.p, vaultrpc.Target{Name: "s3b", Kind: "rclone", Dest: "valise-s3b:my-bucket.01/valise-vault/"}); err != nil {
		t.Fatalf("bucket dest rejected: %v", err)
	}
	noBrowser := false
	wd := kittools.Cloud{ID: "webdav", Name: "WebDAV", RcloneType: "webdav", Browser: &noBrowser, Access: kittools.AccessAccount, AccessNote: "n"}
	res, err := CloudPrepare(ctx, w.env, w.p, vaultrpc.CloudPrepareParams{Name: "dav", Cloud: wd})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(res.Argv, " "), "config_is_local") {
		t.Fatalf("non-browser CloudPrepare included config_is_local: %v", res.Argv)
	}
}
