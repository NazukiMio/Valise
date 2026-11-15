package managedruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/profile"
)

func TestNativeEvidenceBindsDeclaredUserUnitAndExactHashes(t *testing.T) {
	unit := profile.ServiceUnit{Scope: "user", Unit: "fixture.service"}
	p := profile.Profile{Service: &profile.Service{Candidates: []profile.ServiceUnit{unit}}}
	ref := NativeRef{Unit: unit.Unit, Definition: strings.Repeat("a", 64), Executable: "/home/fixture/runtime", ExecutableSHA256: strings.Repeat("b", 64), MainPID: 42, KernelStart: "123", ControlGroup: fmt.Sprintf("/user.slice/user-%d.slice/user@%d.service/app.slice/%s", os.Getuid(), os.Getuid(), unit.Unit)}
	if err := ref.Validate(p); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*NativeRef){
		func(r *NativeRef) { r.Unit = "other.service" },
		func(r *NativeRef) { r.Unit = "../fixture.service" },
		func(r *NativeRef) { r.Unit = "fixture%u.service" },
		func(r *NativeRef) { r.Definition = "not-a-hash" },
		func(r *NativeRef) { r.ExecutableSHA256 = strings.Repeat("B", 64) },
		func(r *NativeRef) { r.KernelStart = "unknown" },
		func(r *NativeRef) { r.MainPID = 0 },
		func(r *NativeRef) { r.ControlGroup = "/system.slice/fixture.service" },
		func(r *NativeRef) { r.Executable = "../runtime" },
	} {
		mutated := ref
		change(&mutated)
		if mutated.Validate(p) == nil {
			t.Fatal("invalid native evidence accepted", mutated)
		}
	}
	p.Service.Candidates[0].Scope = "system"
	if ref.Validate(p) == nil {
		t.Fatal("system service accepted as a native user service")
	}
}

func TestNativeDefinitionHashDoesNotFollowAliasesOrAcceptExposedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unit.service")
	if err := os.WriteFile(path, []byte("fixed definition"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := nativeHash(path, 100)
	if err != nil || len(hash) != 64 {
		t.Fatal(hash, err)
	}
	if _, err := nativeHash(path, 2); err == nil {
		t.Fatal("unbounded definition accepted")
	}
	if err := os.Symlink(path, filepath.Join(dir, "symlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeHash(filepath.Join(dir, "symlink"), 100); err == nil {
		t.Fatal("aliased unit accepted")
	}
	if err := os.Link(path, filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeHash(path, 100); err == nil {
		t.Fatal("hard-linked unit accepted")
	}
	other := filepath.Join(dir, "exposed")
	if err := os.WriteFile(other, []byte("definition"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(other, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeHash(other, 100); err == nil {
		t.Fatal("writable unit accepted")
	}
}
