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

func TestLocalProfileOfficialConflictAndParent(t *testing.T) {
	s := newService(t, "ssh")
	installTestPack(t, s, minisigntest.New(), localDemoYAML)
	_, err := s.ReadLocalProfileFile(writeTemp(t, "demo.yaml", localDemoYAML))
	wantCode(t, err, "local_profile_official_conflict")
	// A child resolves its parent from the official pack.
	f, err := s.ReadLocalProfileFile(writeTemp(t, "child.yaml", localChildYAML))
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "demo-child" {
		t.Fatalf("%+v", f)
	}
	if _, err := s.InstallLocalProfile(f); err != nil {
		t.Fatal(err)
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
