package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoProfile = "../../../profiles/openclaw.yaml"

func TestRunDefault(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{repoProfile}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "OK   ") || !strings.Contains(out.String(), "pending=") {
		t.Errorf("%s", out.String())
	}
}

func TestRunCommandsAndJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--commands", repoProfile}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "CMD") {
		t.Errorf("no commands: %s", out.String())
	}
	out.Reset()
	if code := run([]string{"--json", "--commands", repoProfile}, &out, &errb); code != 0 {
		t.Fatal(code)
	}
	var reps []fileReport
	if err := json.Unmarshal(out.Bytes(), &reps); err != nil || len(reps) != 1 || len(reps[0].Commands) == 0 {
		t.Fatalf("%v %s", err, out.String())
	}
}

func TestRunClassify(t *testing.T) {
	f := filepath.Join(t.TempDir(), "s.txt")
	os.WriteFile(f, []byte(".env\nfoo.bin\n"), 0o600)
	var out, errb bytes.Buffer
	if code := run([]string{"--classify", f, repoProfile}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "分类模拟：共 2") {
		t.Errorf("%s", out.String())
	}
	if code := run([]string{"--classify", f, repoProfile, "../../../profiles/hermes.yaml"}, &out, &errb); code != 2 {
		t.Errorf("want 2, got %d", code)
	}
}

func TestRunUsageAndMissing(t *testing.T) {
	var out, errb bytes.Buffer
	if run(nil, &out, &errb) != 2 {
		t.Error("usage")
	}
	if run([]string{"nope.yaml"}, &out, &errb) != 1 {
		t.Error("missing file")
	}
}

func TestRunClassifyReverseChain(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.yaml")
	child := filepath.Join(dir, "child.yaml")
	base, err := os.ReadFile(repoProfile)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(parent, base, 0o600)
	id := ""
	for _, l := range strings.Split(string(base), "\n") {
		if strings.HasPrefix(l, "id: ") {
			id = strings.TrimSpace(strings.TrimPrefix(l, "id: "))
			break
		}
	}
	os.WriteFile(child, []byte("schema: valise-profile/1\nid: kid\ndisplay_name: Kid\nprofile_version: 1\nstatus: draft\nextends: "+id+"\nwhen: { exists: { root: state, glob: kid.marker } }\npaths:\n  - { root: state, glob: \"kid/**\", category: memory }\n"), 0o600)
	s := filepath.Join(dir, "s.txt")
	os.WriteFile(s, []byte("kid/a.md\n"), 0o600)
	for _, order := range [][]string{{parent, child}, {child, parent}} {
		var out, errb bytes.Buffer
		args := append([]string{"--classify", s}, order...)
		if code := run(args, &out, &errb); code != 0 {
			t.Fatalf("%v: code %d: %s", order, code, errb.String())
		}
		if !strings.Contains(out.String(), "memory") {
			t.Errorf("%v: child rule not applied: %s", order, out.String())
		}
	}
}
