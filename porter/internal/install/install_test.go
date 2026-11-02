package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/install"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
)

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "fakenode"), []byte("#!/bin/sh\necho v22.23.3\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "fakegit"), []byte("#!/bin/sh\necho git version 2.47.3\n"), 0o755)
	e := &porterenroll.Env{Home: dir, Vars: []string{"PATH=" + bin, "HOME=" + dir}}
	res := Check(context.Background(), e, install.CheckParams{Requires: []profile.Require{
		{Binary: "fakegit"}, {Binary: "fakenode", Version: ">=24.16.0"}, {Binary: "missing"},
	}})
	r := res.Requirement
	if len(r) != 3 || !r[0].OK || r[0].Version != "2.47.3" || r[1].OK || r[1].Version != "22.23.3" || r[1].Reason == "" || r[2].OK || r[2].Path != "" {
		t.Fatalf("%+v", r)
	}
	if res.FreeBytes == 0 {
		t.Fatal("free bytes")
	}
}

func TestOSRelease(t *testing.T) {
	p := filepath.Join(t.TempDir(), "os-release")
	os.WriteFile(p, []byte("PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nID=debian\nVERSION_ID=\"13\"\n"), 0o644)
	if id, v := OSRelease(p); id != "debian" || v != "13" {
		t.Fatal(id, v)
	}
}
