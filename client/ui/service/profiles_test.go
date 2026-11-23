package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/minisign/minisigntest"
	"valise/client/profiles"
	"valise/core/valise"
)

func TestFetchPack(t *testing.T) {
	signer := minisigntest.New()
	tgz, err := profiles.Build("../../../profiles", "../../../kit/tools.yaml", "2026.10.31", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sig := signer.Sign(tgz, "file:"+profiles.PackAsset)
	mux := http.NewServeMux()
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tag_name":"v0.1.0"},{"tag_name":"profiles-2026.10.31"},{"tag_name":"profiles-2026.10.20"},{"tag_name":"profiles-2027.01.01","draft":true}]`))
	})
	mux.HandleFunc("/dl/profiles-2026.10.31/"+profiles.PackAsset, func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) })
	mux.HandleFunc("/dl/profiles-2026.10.31/"+profiles.PackSigAsset, func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	old := ReleasesAPI
	ReleasesAPI = srv.URL + "/releases"
	defer func() { ReleasesAPI = old }()

	s := newService(t, "ssh")
	s.Release.BaseURL = srv.URL + "/dl"
	s.Release.PublicKey = signer.PublicKey
	if _, err := s.Enroll(context.Background(), "box", EnrollOptions{}); err == nil {
		t.Fatal("enroll without profiles")
	} else {
		wantCode(t, err, "no_profiles")
	}
	f, err := s.FetchPack(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if f.From != "profiles-2026.10.31" || f.SignatureErr != nil {
		t.Fatalf("%s %v", f.From, f.SignatureErr)
	}
	if err := s.InstallPack(f); err != nil {
		t.Fatal(err)
	}
	st, err := s.ProfilesStatus()
	if err != nil || st.Pack == nil || len(st.Local) != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	// Without the key the same download is only installable as local.
	s.Release.PublicKey = ""
	f, err = s.FetchPack(context.Background(), "profiles-2026.10.31")
	if err != nil || f.SignatureErr == nil {
		t.Fatal(err)
	}
	if err := s.InstallPack(f); err == nil {
		t.Fatal("installed an unverifiable pack")
	}
}

const localDemoYAML = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 2
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: demo } }
native_cli: [/bin/echo, onboard]
`

const localChildYAML = `schema: valise-profile/1
id: demo-child
display_name: Demo Child
profile_version: 1
status: draft
extends: demo
when: { exists: { root: state, glob: "mem.json" } }
paths:
  - { root: state, glob: "mem.json", category: memory }
`

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// installTestPack installs a signed pack holding localDemoYAML under packID.
func installTestPack(t *testing.T, s *Service, signer *minisigntest.Signer, body string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "p.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tgz, err := profiles.Build(src, "", "2026.11.20", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.Release.PublicKey = signer.PublicKey
	if _, err := profiles.Install(s.Dirs.Data, tgz, signer.Sign(tgz, "file:"+profiles.PackAsset), s.Release.PublicKey); err != nil {
		t.Fatal(err)
	}
}

func TestLocalProfileImportAndReplace(t *testing.T) {
	s := newService(t, "ssh")
	f, err := s.ReadLocalProfileFile(writeTemp(t, "demo.yaml", localDemoYAML))
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "demo" || f.Version != 2 || f.Pending != 1 || f.RestoreCap == "tested" || f.ReplacesLocal || len(f.RefusedCmds) != 0 || len(f.Commands) == 0 {
		t.Fatalf("%+v", f)
	}
	path, err := s.InstallLocalProfile(f)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st, err)
	}
	f2, err := s.ReadLocalProfileFile(writeTemp(t, "other-name.yaml", localDemoYAML))
	if err != nil || !f2.ReplacesLocal {
		t.Fatalf("%+v %v", f2, err)
	}
	if set, err := s.Profiles(); err != nil || set.ByID["demo"] == nil || !set.ByID["demo"].Local {
		t.Fatalf("%v", err)
	}
}

func TestLocalProfileOfficialOverrideDiffRemoveAndParent(t *testing.T) {
	s := newService(t, "ssh")
	installTestPack(t, s, minisigntest.New(), localDemoYAML)

	overrideYAML := `schema: valise-profile/1
id: demo
display_name: Demo Custom
profile_version: 3
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths:
  - { root: state, glob: "demo.json", category: config }
  - { root: state, glob: "memory/*.md", category: memory }
consistency:
  sqlite:
    - { root: state, glob: "state.db" }
provenance: { kind: binary, binary: { name: demo } }
service:
  candidates: [ { scope: user, unit: demo.service } ]
  install_argv: [demo, service, install]
entries:
  - { id: web, kind: web, host: 127.0.0.1, port: 8080, path: / }
actions:
  - { id: ping, name: Ping, explain: test, risk: read, argv: [/bin/echo, pong], output: text }
agent_kit:
  skills_dir: { root: state, path: "skills/" }
native_cli: [/bin/echo, onboard-v2]
native_session_cli: [/bin/echo, chat]
`
	f, err := s.ReadLocalProfileFile(writeTemp(t, "demo.yaml", overrideYAML))
	if err != nil {
		t.Fatalf("official override must be allowed: %v", err)
	}
	if !f.OverridesOfficial || f.OfficialVersion != 2 || f.ReplacesLocal || f.Diff == nil {
		t.Fatalf("unexpected override preview: %+v", f)
	}
	d := f.Diff
	if d.OldVersion != 2 || d.NewVersion != 3 ||
		len(d.AddedEntries) != 1 || d.AddedEntries[0] != "web" ||
		len(d.AddedActions) != 1 || d.AddedActions[0] != "ping" ||
		!d.NativeCLIChanged || !d.NativeSessionChanged || !d.ServiceInstallChanged || !d.AgentKitChanged ||
		d.PathRulesDelta != 1 || d.SQLiteDelta != 1 || len(d.Highlights) == 0 {
		t.Fatalf("unexpected diff: %+v", d)
	}
	if _, err := s.InstallLocalProfile(f); err != nil {
		t.Fatal(err)
	}
	st, err := s.ProfilesStatus()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Overrides) != 1 || st.Overrides[0] != "demo" || len(st.Loaded) != 1 ||
		!st.Loaded[0].OverridesOfficial || st.Loaded[0].OfficialVersion != 2 || st.Loaded[0].Version != 3 {
		t.Fatalf("unexpected status after override: %+v", st)
	}

	// A child resolves its parent (currently the overriding local demo).
	child, err := s.ReadLocalProfileFile(writeTemp(t, "child.yaml", localChildYAML))
	if err != nil {
		t.Fatal(err)
	}
	if child.ID != "demo-child" {
		t.Fatalf("%+v", child)
	}
	if _, err := s.InstallLocalProfile(child); err != nil {
		t.Fatal(err)
	}

	// Removing the local override restores the official profile.
	if err := s.RemoveLocalProfile("demo"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.RemoveLocalProfile("demo"), "local_profile_not_found")
	st, err = s.ProfilesStatus()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Overrides) != 0 {
		t.Fatalf("expected no overrides after remove: %+v", st)
	}
	foundOfficial := false
	for _, sum := range st.Loaded {
		if sum.ID == "demo" && !sum.Local && sum.Version == 2 {
			foundOfficial = true
		}
	}
	if !foundOfficial {
		t.Fatalf("official demo v2 not restored: %+v", st.Loaded)
	}
}

func TestFetchPackAtomFallback(t *testing.T) {
	signer := minisigntest.New()
	tgz, err := profiles.Build("../../../profiles", "../../../kit/tools.yaml", "2026.11.20", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sig := signer.Sign(tgz, "file:"+profiles.PackAsset)
	mux := http.NewServeMux()
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "API rate limit exceeded", http.StatusForbidden)
	})
	mux.HandleFunc("/releases.atom", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><link rel="alternate" type="text/html" href="https://github.com/NazukiMio/Valise/releases/tag/v0.1.0-beta.1"/></entry>
  <entry><link rel="alternate" type="text/html" href="https://github.com/NazukiMio/Valise/releases/tag/profiles-2026.11.10"/></entry>
  <entry><link rel="alternate" type="text/html" href="https://github.com/NazukiMio/Valise/releases/tag/profiles-2026.11.20"/></entry>
</feed>`))
	})
	mux.HandleFunc("/dl/profiles-2026.11.20/"+profiles.PackAsset, func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) })
	mux.HandleFunc("/dl/profiles-2026.11.20/"+profiles.PackSigAsset, func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	oldAPI, oldAtom := ReleasesAPI, ReleasesAtomURL
	ReleasesAPI = srv.URL + "/releases"
	ReleasesAtomURL = srv.URL + "/releases.atom"
	defer func() {
		ReleasesAPI, ReleasesAtomURL = oldAPI, oldAtom
	}()

	s := newService(t, "ssh")
	s.Release.BaseURL = srv.URL + "/dl"
	s.Release.PublicKey = signer.PublicKey
	f, err := s.FetchPack(context.Background(), "")
	if err != nil {
		t.Fatalf("expected Atom fallback to succeed on API 403: %v", err)
	}
	if f.From != "profiles-2026.11.20" || f.SignatureErr != nil {
		t.Fatalf("unexpected fetched pack: from=%q sigErr=%v", f.From, f.SignatureErr)
	}
}

func TestFetchPackAtomRejectsTraversalTags(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/releases.atom", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><link rel="alternate" type="text/html" href="https://github.com/NazukiMio/Valise/releases/tag/profiles-2099.01.01%2f..%2fevil"/></entry>
  <entry><link rel="alternate" type="text/html" href="https://github.com/NazukiMio/Valise/releases/tag/profiles-2099.02.02..evil"/></entry>
  <entry><link rel="alternate" type="text/html" href="https://github.com/NazukiMio/Valise/releases/tag/profiles-2099.03.03%2e%2e"/></entry>
  <entry><link rel="alternate" type="text/html" href="https://github.com/NazukiMio/Valise/releases/tag/profiles-2026.11.20?ref=atom"/></entry>
</feed>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	oldAtom := ReleasesAtomURL
	ReleasesAtomURL = srv.URL + "/releases.atom"
	defer func() { ReleasesAtomURL = oldAtom }()

	s := newService(t, "ssh")
	tag, err := s.latestPackTagFromAtom(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag != "profiles-2026.11.20" {
		t.Fatalf("expected traversal tags to be ignored, got %q", tag)
	}

	if _, err := s.FetchPack(context.Background(), "profiles-2026..11"); err == nil {
		t.Fatal("expected FetchPack to reject tag containing '..'")
	} else {
		wantCode(t, err, "pack_bad_tag")
	}
}

func TestSyncProfileToInstances(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	// Bump local profile version and sync to one reachable + one missing host.
	updated := strings.Replace(demoProfile, "profile_version: 1", "profile_version: 2", 1)
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	outcomes, err := s.SyncProfileToInstances(ctx, "demo", []ProfileSyncTarget{
		{Host: "box", InstanceID: "demo"},
		{Host: "missing-host", InstanceID: "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 2 || !outcomes[0].OK || outcomes[0].Written == "" || outcomes[1].OK || outcomes[1].Err == "" {
		t.Fatalf("unexpected sync outcomes: %+v", outcomes)
	}
	insts, err := s.Instances(ctx, "box")
	if err != nil || len(insts) != 1 || insts[0].ProfileVersion != 2 {
		t.Fatalf("expected synced instance to have v2: %+v %v", insts, err)
	}
}

func TestLocalProfileLintAndRefused(t *testing.T) {
	s := newService(t, "ssh")
	_, err := s.ReadLocalProfileFile(writeTemp(t, "child.yaml", localChildYAML))
	wantCode(t, err, "local_profile_lint_failed")
	if ue, ok := valise.As(err); !ok || ue.Detail == "" {
		t.Fatalf("no detail: %v", err)
	}
	_, err = s.ReadLocalProfileFile(writeTemp(t, "junk.yaml", "not: [valid"))
	wantCode(t, err, "local_profile_parse")
	// sh -c is already a lint error; a wide deletion passes lint and is caught by the audit.
	shc := strings.Replace(localDemoYAML, "[/bin/echo, onboard]", "[sh, -c, \"x\"]", 1)
	_, err = s.ReadLocalProfileFile(writeTemp(t, "shc.yaml", shc))
	wantCode(t, err, "local_profile_lint_failed")
	bad := strings.Replace(localDemoYAML, "[/bin/echo, onboard]", "[rm, -rf, /]", 1)
	f, err := s.ReadLocalProfileFile(writeTemp(t, "bad.yaml", bad))
	if err != nil {
		t.Fatalf("lint rejected the command before audit: %v", err)
		return
	}
	if len(f.RefusedCmds) == 0 {
		t.Fatalf("refused command not flagged: %+v", f.Commands)
	}
	_, err = s.InstallLocalProfile(f)
	wantCode(t, err, "local_profile_refused")
}
