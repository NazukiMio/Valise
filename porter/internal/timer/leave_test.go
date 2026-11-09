package timer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	porterenroll "valise/porter/internal/enroll"
)

// Removing the server stops every timed seal and deletes its units.
func TestRemoveAll(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "calls")
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"systemctl $*\" >> "+log+"\n"), 0o755)
	e := &porterenroll.Env{Home: home, Vars: []string{"PATH=" + bin + ":/usr/bin:/bin", "USER=u"}}
	if done, err := RemoveAll(context.Background(), e, home); err != nil || done != nil {
		t.Fatalf("nothing to remove: %v %v", done, err)
	}
	os.MkdirAll(unitDir(home), 0o700)
	for _, n := range []string{"a", "b"} {
		svc, tmr := UnitNames(n)
		os.WriteFile(filepath.Join(unitDir(home), svc), nil, 0o600)
		os.WriteFile(filepath.Join(unitDir(home), tmr), nil, 0o600)
	}
	os.WriteFile(filepath.Join(unitDir(home), "other.timer"), nil, 0o600)
	done, err := RemoveAll(context.Background(), e, home)
	if err != nil || strings.Join(done, ",") != "a,b" {
		t.Fatalf("%v %v", done, err)
	}
	if left, _ := filepath.Glob(filepath.Join(unitDir(home), "*")); len(left) != 1 || filepath.Base(left[0]) != "other.timer" {
		t.Fatalf("left %v", left)
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"disable --now valise-seal@a.timer", "disable --now valise-seal@b.timer", "daemon-reload"} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("missing %q in %s", want, calls)
		}
	}
}
