package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOfficial(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ByID) < 3 {
		t.Fatalf("official profiles: %v", s.ByID)
	}
	for id, l := range s.ByID {
		if l.Local || l.Effective == nil {
			t.Errorf("%s: %+v", id, l)
		}
	}
}

func TestLocalProfiles(t *testing.T) {
	dir := t.TempDir()
	good := `schema: valise-profile/1
id: my-local
display_name: Mine
profile_version: 1
status: draft
detect: { binaries: [mine], state_dir_candidates: ["${HOME}/.mine"] }
paths: [ { root: state, glob: "**", category: memory, verify: pending } ]
provenance: { kind: binary, binary: { name: mine } }
`
	os.WriteFile(filepath.Join(dir, "mine.yaml"), []byte(good), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(strings.Replace(good, "id: my-local", "id: Bad", 1)), 0o644)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l := s.ByID["my-local"]; l == nil || !l.Local {
		t.Fatalf("%+v", s.ByID)
	}
	if len(s.Rejected) != 1 {
		t.Fatalf("rejected %v", s.Rejected)
	}
}
