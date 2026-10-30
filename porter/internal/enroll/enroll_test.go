package enroll

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"valise/core/enroll"
	"valise/core/profile"
)

// fixture profile; harness names in tests are fixtures only.
const fixtureYAML = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 3
status: draft
detect:
  binaries: [demo]
  version_argv: [demo, --version]
  state_dir_candidates: ["${env:DEMO_HOME}", "${HOME}/.demo"]
  markers_any: [demo.json]
roots:
  - id: workspace
    discover_argv: [demo, where]
    default: "${STATE_DIR}/workspace"
paths:
  - { root: state, glob: "agents/*/agent/*.sqlite", category: state }
  - { root: state, glob: "demo.json", category: config }
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "logs/**", category: ignore }
  - { root: workspace, glob: "SOUL.md", category: identity }
  - { root: workspace, glob: "**", category: workspace }
consistency:
  sqlite: [ { root: state, glob: "agents/*/agent/*.sqlite", secret_tables: [auth_store] } ]
provenance: { kind: npm_global, npm: { package: "@acme/demo" } }
body: { paths: [ { root: state, glob: "runtime/**" } ] }
service: { candidates: [ { scope: user, unit: demo.service } ] }
entries: [ { id: web, kind: web, host: 127.0.0.1, port: 1, path: "/" } ]
`

func fixtureProfile(t *testing.T) profile.Profile {
	t.Helper()
	var p profile.Profile
	if err := yaml.Unmarshal([]byte(fixtureYAML), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

type world struct {
	home, bin, state, ws string
	env                  *Env
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{home: t.TempDir(), bin: t.TempDir()}
	w.state = filepath.Join(w.home, ".demo")
	w.ws = filepath.Join(w.home, "elsewhere-ws")
	w.env = &Env{Home: w.home, Vars: []string{"PATH=" + w.bin + ":/usr/bin:/bin"}}
	// state tree
	write(t, filepath.Join(w.state, "demo.json"), "{}", 0o600)
	write(t, filepath.Join(w.state, ".env"), "K=v", 0o600)
	write(t, filepath.Join(w.state, "agents/main/agent/a.sqlite"), "db", 0o600)
	write(t, filepath.Join(w.state, "agents/main/agent/a.sqlite-wal"), "wal", 0o600)
	write(t, filepath.Join(w.state, "logs/x.log"), "log", 0o644)
	write(t, filepath.Join(w.state, "runtime/node/bin/node"), "bin", 0o755)
	write(t, filepath.Join(w.state, "mystery/deep/a.bin"), "12345", 0o644)
	write(t, filepath.Join(w.state, "mystery/deep/b.bin"), "1", 0o644)
	write(t, filepath.Join(w.state, "workspace/SOUL.md"), "default ws, should not be used", 0o644)
	os.Symlink("demo.json", filepath.Join(w.state, "link.json"))
	write(t, filepath.Join(w.ws, "SOUL.md"), "soul", 0o644)
	write(t, filepath.Join(w.ws, "notes/a.md"), "n", 0o644)
	// npm global install
	prefix := filepath.Join(w.home, ".npm-global")
	pkg := filepath.Join(prefix, "lib/node_modules/@acme/demo")
	write(t, filepath.Join(pkg, "package.json"), `{"name":"@acme/demo","version":"1.2.3"}`, 0o644)
	write(t, filepath.Join(pkg, "bin/demo.js"), "#!/bin/sh\ncase \"$1\" in --version) echo 'Demo 1.2.3';; where) echo '"+w.ws+"';; esac\n", 0o755)
	os.Symlink(filepath.Join(pkg, "bin/demo.js"), filepath.Join(w.bin, "demo"))
	write(t, filepath.Join(w.bin, "npm"), `#!/bin/sh
case "$*" in
"config get registry") echo https://registry.example.test/ ;;
"view @acme/demo@1.2.3 dist.integrity --registry https://registry.example.test/") echo sha512-abc ;;
*) exit 1 ;;
esac
`, 0o755)
	write(t, filepath.Join(w.bin, "systemctl"), "#!/bin/sh\nprintf 'LoadState=loaded\\nActiveState=active\\nUnitFileState=enabled\\n'\n", 0o755)
	return w
}

func TestDetect(t *testing.T) {
	w := newWorld(t)
	p := fixtureProfile(t)
	child := profile.Profile{ID: "demo-x", Extends: "demo", When: &profile.When{Exists: &profile.PathRef{Root: "state", Glob: "mystery/**/b.bin"}}}
	other := profile.Profile{ID: "demo-y", Extends: "demo", When: &profile.When{Exists: &profile.PathRef{Root: "state", Glob: "nope/**"}}}
	res := Detect(context.Background(), w.env, []profile.Profile{p, child, other})
	if len(res.Candidates) != 1 {
		t.Fatalf("%+v", res)
	}
	c := res.Candidates[0]
	if c.StateDir != w.state || c.Version != "Demo 1.2.3" || !strings.HasSuffix(c.Binary, "/demo") ||
		len(c.Markers) != 1 || len(c.Children) != 1 || c.Children[0] != "demo-x" {
		t.Fatalf("%+v", c)
	}

	// ${env:DEMO_HOME} wins when set and has the marker.
	alt := filepath.Join(w.home, "alt")
	write(t, filepath.Join(alt, "demo.json"), "{}", 0o600)
	w.env.Set("DEMO_HOME", alt)
	if c := Detect(context.Background(), w.env, []profile.Profile{p}).Candidates[0]; c.StateDir != alt {
		t.Fatalf("%+v", c)
	}
	// A directory without any marker is not an instance.
	os.Remove(filepath.Join(alt, "demo.json"))
	os.Remove(filepath.Join(w.state, "demo.json"))
	if res := Detect(context.Background(), w.env, []profile.Profile{p}); len(res.Candidates) != 0 {
		t.Fatalf("%+v", res)
	}
}

func scan(t *testing.T, w *world, overrides ...profile.Override) *enroll.Report {
	t.Helper()
	r, err := Scan(context.Background(), w.env, enroll.ScanParams{Profile: fixtureProfile(t), StateDir: w.state, InstanceID: "demo", Overrides: overrides}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestScanClassification(t *testing.T) {
	w := newWorld(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p := fixtureProfile(t)
	p.Entries[0].Port = ln.Addr().(*net.TCPAddr).Port
	r, err := Scan(context.Background(), w.env, enroll.ScanParams{Profile: p, StateDir: w.state, InstanceID: "demo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// discover_argv found the workspace outside the default location.
	if r.Roots[1].Path != w.ws || r.Roots[1].Source != "discover" {
		t.Fatalf("roots %+v", r.Roots)
	}
	cat := func(c string) enroll.Tally { return r.Categories[c] }
	if cat("secrets").Files != 1 || len(r.Secrets) != 1 || r.Secrets[0].Path != ".env" {
		t.Fatalf("secrets %+v %+v", cat("secrets"), r.Secrets)
	}
	if cat("identity").Files != 1 || cat("workspace").Files != 1 || cat("config").Files != 1 ||
		cat("ignore").Files != 1 || cat("body").Files != 1 || cat("state").Files != 1 {
		t.Fatalf("categories %+v", r.Categories)
	}
	if len(r.SQLite) != 1 || r.SQLite[0].SecretTables[0] != "auth_store" || len(r.SQLite[0].Sidecars) != 1 || r.SQLite[0].Sidecars[0] != "-wal" {
		t.Fatalf("sqlite %+v", r.SQLite)
	}
	// Unclassified: mystery/deep/{a,b}.bin and workspace/SOUL.md under the
	// state root (the default workspace is not the resolved one).
	if r.Unclassified.Files != 3 || r.Unclassified.Bytes != 6+int64(len("default ws, should not be used")) {
		t.Fatalf("unclassified %+v %+v", r.Unclassified, r.UnclassGroups)
	}
	if r.Symlinks != 1 {
		t.Fatalf("symlinks %d", r.Symlinks)
	}
	wantPrefix, _ := filepath.EvalSymlinks(filepath.Join(w.home, ".npm-global"))
	if r.Provenance.NPM == nil || r.Provenance.NPM.Version != "1.2.3" || r.Provenance.NPM.Integrity != "sha512-abc" ||
		r.Provenance.NPM.Prefix != wantPrefix || len(r.Provenance.Warnings) != 0 {
		t.Fatalf("provenance %+v %+v", r.Provenance, r.Provenance.NPM)
	}
	if !r.Service[0].Exists() || r.Service[0].Active != "active" || !r.Entries[0].Listening {
		t.Fatalf("service %+v entries %+v", r.Service, r.Entries)
	}
}

func TestScanNestedRootAndOverrides(t *testing.T) {
	w := newWorld(t)
	// Without discover, the workspace is nested in the state dir and its
	// files belong only to the workspace root.
	os.Remove(filepath.Join(w.bin, "demo"))
	r := scan(t, w,
		profile.Override{Root: "state", Glob: "mystery/**", Category: "memory"},
		profile.Override{Root: "state", Glob: "demo.json", Category: "secrets"},
		profile.Override{Root: "state", Glob: ".env", Category: "memory"})
	if r.Roots[1].Source != "default" || r.Roots[1].Path != filepath.Join(w.state, "workspace") {
		t.Fatalf("%+v", r.Roots)
	}
	if r.Categories["identity"].Files != 1 || r.Unclassified.Files != 0 {
		t.Fatalf("%+v %+v", r.Categories, r.Unclassified)
	}
	// Overrides may add secrets but can never demote a profile secret.
	if r.Categories["secrets"].Files != 2 || r.Categories["memory"].Files != 2 {
		t.Fatalf("%+v", r.Categories)
	}
	if r.Provenance.NPM == nil || r.Provenance.NPM.Version != "" || len(r.Provenance.Warnings) == 0 {
		t.Fatalf("missing binary must be a warning: %+v", r.Provenance)
	}
}

func TestScanRejectsBadInput(t *testing.T) {
	w := newWorld(t)
	p := fixtureProfile(t)
	for _, params := range []enroll.ScanParams{
		{Profile: p, StateDir: w.state, InstanceID: "Bad ID"},
		{Profile: p, StateDir: filepath.Join(w.home, "nope"), InstanceID: "demo"},
		{Profile: p, StateDir: w.state, InstanceID: "demo", Overrides: []profile.Override{{Root: "nowhere", Glob: "x", Category: "memory"}}},
		{Profile: p, StateDir: w.state, InstanceID: "demo", Overrides: []profile.Override{{Root: "state", Glob: "../x", Category: "memory"}}},
	} {
		if _, err := Scan(context.Background(), w.env, params, nil); err == nil {
			t.Errorf("accepted %+v", params)
		}
	}
}

func TestGitProvenance(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	run("init", "-q")
	write(t, filepath.Join(dir, "a.txt"), "1", 0o644)
	run("add", ".")
	run("commit", "-qm", "init")
	run("remote", "add", "origin", "https://example.test/agent.git")
	e := &Env{Home: dir, Vars: []string{"PATH=/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin"}}
	var warns []string
	warn := func(s string) { warns = append(warns, s) }
	rec := gitProvenance(context.Background(), e, dir, warn)
	if rec.Dirty || rec.Remote != "https://example.test/agent.git" || len(rec.Commit) != 40 || len(warns) != 0 {
		t.Fatalf("%+v %v", rec, warns)
	}
	write(t, filepath.Join(dir, "a.txt"), "2", 0o644)
	write(t, filepath.Join(dir, "new.txt"), "x", 0o644)
	rec = gitProvenance(context.Background(), e, dir, warn)
	if !rec.Dirty || rec.PatchBytes == 0 || len(rec.PatchSHA256) != 64 || rec.Untracked != 1 || len(warns) != 1 {
		t.Fatalf("%+v %v", rec, warns)
	}
}

func TestBinaryProvenance(t *testing.T) {
	bin := t.TempDir()
	write(t, filepath.Join(bin, "tool"), "#!/bin/sh\n", 0o755)
	e := &Env{Vars: []string{"PATH=" + bin}}
	rec := binaryProvenance(e, "tool", func(string) {})
	if rec == nil || len(rec.SHA256) != 64 || !strings.HasSuffix(rec.Path, "/tool") {
		t.Fatalf("%+v", rec)
	}
}

func TestInstanceRecordRoundTrip(t *testing.T) {
	home := t.TempDir()
	if r, err := ReadRecord(home, "demo"); err != nil || r != nil {
		t.Fatalf("%v %v", r, err)
	}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": "/x"}}
	path, err := WriteRecord(home, rec, profile.Profile{ID: "demo", ProfileVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 || path != filepath.Join(home, ".config/valise/instances/demo.json") {
		t.Fatalf("%s %v", path, st.Mode())
	}
	got, err := ReadRecord(home, "demo")
	if err != nil || got.Roots["state"] != "/x" {
		t.Fatalf("%+v %v", got, err)
	}
	if p, err := ReadProfile(home, "demo"); err != nil || p.ID != "demo" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := WriteRecord(home, enroll.Record{InstanceID: "demo", ProfileVersion: 2}, profile.Profile{ID: "demo", ProfileVersion: 1}); err == nil {
		t.Fatal("version mismatch accepted")
	}
	if _, err := WriteRecord(home, enroll.Record{InstanceID: "../evil"}, profile.Profile{}); err == nil {
		t.Fatal("path traversal in instance id accepted")
	}
}

func TestLoadEnvUsesLoginShell(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".profile"), "export VALISE_LOGIN_PROBE=yes\n", 0o644)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", home)
	t.Setenv("ENV", "")
	e := LoadEnv(context.Background(), home)
	if e.Get("VALISE_LOGIN_PROBE") != "yes" || !containsPath(e.Get("PATH"), filepath.Join(home, ".local/bin")) {
		t.Fatalf("vars=%v warnings=%v", e.Get("PATH"), e.Warnings)
	}
}

func TestScanSymlinkedStateDir(t *testing.T) {
	w := newWorld(t)
	real := filepath.Join(w.home, "disk2", "demo-state")
	os.MkdirAll(filepath.Dir(real), 0o755)
	if err := os.Rename(w.state, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, w.state); err != nil {
		t.Fatal(err)
	}
	r := scan(t, w)
	wantReal, _ := filepath.EvalSymlinks(real)
	if r.Roots[0].Path != w.state || r.Roots[0].Resolved != wantReal {
		t.Fatalf("%+v", r.Roots[0])
	}
	if r.Categories["secrets"].Files != 1 || r.Categories["config"].Files != 1 {
		t.Fatalf("symlinked state dir was not scanned: %+v", r.Categories)
	}
}
