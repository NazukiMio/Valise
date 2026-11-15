package environments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/vault"
)

func installedFixture(t *testing.T) InstalledSource {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, ".fixture")
	prefix := filepath.Join(state, "tools", "runtime")
	p := profile.Profile{ID: "fixture", ProfileVersion: 1,
		Provenance: &profile.Provenance{Kind: profile.ProvenanceNPMGlobal, NPM: &profile.NPMProvenance{Package: "@scope/fixture"}},
		Body:       &profile.Body{Paths: []profile.PathRef{{Root: "state", Glob: "tools/**"}, {Root: "state", Glob: "bin/**"}}}}
	in := InstalledSource{Profile: p, Layout: Layout{Home: home, Roots: map[string]string{"state": state}}, MaxBytes: 1 << 20,
		Origin: enroll.Provenance{Kind: profile.ProvenanceNPMGlobal, NPM: &enroll.NPMRecord{Package: "@scope/fixture", Version: "1.2.3", Prefix: prefix,
			Registry: "https://registry.example.test/", Integrity: "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))}}}
	for rel, data := range map[string]string{
		".fixture/bin/launcher": "private launcher", ".fixture/tools/runtime/bin/node": "private interpreter",
		".fixture/tools/runtime/lib/node_modules/@scope/fixture/package.json": `{"name":"@scope/fixture","version":"1.2.3"}`,
		".fixture/tools/runtime/lib/node_modules/@scope/fixture/entry.js":     "private program"} {
		writeInstalledFixture(t, home, rel, data)
	}
	if err := os.Symlink("../lib/node_modules/@scope/fixture/entry.js", filepath.Join(prefix, "bin", "fixture")); err != nil {
		t.Fatal(err)
	}
	return in
}

func writeInstalledFixture(t *testing.T, home, rel, data string) {
	t.Helper()
	p := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o700); err != nil {
		t.Fatal(err)
	}
}

func observedHeader(t *testing.T, in InstalledSource, observed *ProgramObservation) *vault.RuntimeRecord {
	t.Helper()
	roots := map[string]vault.RuntimeRoot{}
	for id, p := range in.Layout.Roots {
		real, _ := filepath.EvalSymlinks(p)
		roots[id] = vault.RuntimeRoot{Path: p, Resolved: real}
	}
	r := &vault.RuntimeRecord{Format: vault.FormatRuntime, OS: runtime.GOOS, Arch: runtime.GOARCH, Home: vault.RuntimeRoot{Path: in.Layout.Home},
		Installation: &vault.RuntimeInstallation{ProfileDoc: &in.Profile, RegisteredProvenance: in.Origin, Roots: roots}, Dependencies: observed.Dependencies}
	// A decrypted historical header owns its values independently of today's
	// mutable registration. Do not alias the fixture input's nested pointers.
	b, _ := json.Marshal(r)
	var frozen vault.RuntimeRecord
	if err := json.Unmarshal(b, &frozen); err != nil {
		t.Fatal(err)
	}
	return &frozen
}

func TestInstalledNPMFreezesExactlyRecordedPrivateProgramAndRuntime(t *testing.T) {
	in := installedFixture(t)
	observed, err := ObserveInstalledNPM(context.Background(), in)
	if err != nil || observed == nil || len(observed.Dependencies) != 2 || len(observed.Material.Files) != 5 {
		t.Fatal("complete private trees were not observed", err)
	}
	header := observedHeader(t, in, observed)
	archive, err := FreezeInstalledNPM(context.Background(), t.TempDir(), in, header)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.File.Close()
	if st, err := archive.File.Stat(); err != nil || st.Mode().Perm() != 0o600 || archive.Material.ArchiveBytes != st.Size() {
		t.Fatal("archive is not private", err)
	}
	if archive.Material.SourceDigest != ProgramSourceDigest(in.Origin) || archive.Material.ArchiveSHA == "" {
		t.Fatal("source/material digests lost")
	}
	// Mutating the original program after preparation cannot change the copy.
	writeInstalledFixture(t, in.Layout.Home, ".fixture/tools/runtime/bin/node", "changed live interpreter")
	private := t.TempDir()
	r, err := os.OpenRoot(private)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := unpackProgram(context.Background(), r, archive.Material, archive.File); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(private, ".fixture/tools/runtime/bin/node")); err != nil || string(b) != "private interpreter" {
		t.Fatal("prepared runtime still depended on active program", err)
	}
	if len(header.Dependencies) != 2 || header.Dependencies[1].SHA256 == "" {
		t.Fatal("historical evidence changed")
	}
}

func TestInstalledNPMCannotSubstituteCurrentVersionOrIncompleteEvidence(t *testing.T) {
	for _, kind := range []string{"code changed", "runtime changed", "file added", "source version", "profile", "no historical runtime", "no fingerprints", "architecture"} {
		t.Run(kind, func(t *testing.T) {
			in := installedFixture(t)
			observed, err := ObserveInstalledNPM(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			header := observedHeader(t, in, observed)
			switch kind {
			case "code changed":
				writeInstalledFixture(t, in.Layout.Home, ".fixture/tools/runtime/lib/node_modules/@scope/fixture/entry.js", "new code same version")
			case "runtime changed":
				writeInstalledFixture(t, in.Layout.Home, ".fixture/tools/runtime/bin/node", "new runtime same location")
			case "file added":
				writeInstalledFixture(t, in.Layout.Home, ".fixture/tools/runtime/added", "new dependency")
			case "source version":
				in.Origin.NPM.Version = "2.0.0"
			case "profile":
				in.Profile.ProfileVersion++
			case "no historical runtime":
				header = nil
			case "no fingerprints":
				header.Dependencies = nil
			case "architecture":
				header.Arch = "another-architecture"
			}
			parent := t.TempDir()
			if copy, err := FreezeInstalledNPM(context.Background(), parent, in, header); err == nil || copy != nil {
				t.Fatal("substituted current/unbound material")
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("invalid source began writing preparation", err)
			}
		})
	}
}

func TestInstalledProgramCollectionNeverFollowsCredentialOrExternalLinks(t *testing.T) {
	for _, kind := range []string{"declared credential", "credential link", "credential hardlink", "external root", "wrong package", "invalid metadata", "wrong version", "missing integrity", "cancellation"} {
		t.Run(kind, func(t *testing.T) {
			in := installedFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const marker = "sensitive-fixture-never-log"
			switch kind {
			case "declared credential":
				in.Profile.Paths = append(in.Profile.Paths, profile.PathRule{Root: "state", Glob: "tools/private", Category: profile.CategorySecrets})
				writeInstalledFixture(t, in.Layout.Home, ".fixture/tools/private", marker)
			case "credential link", "credential hardlink":
				writeInstalledFixture(t, in.Layout.Home, ".fixture/.env", marker)
				source, dest := filepath.Join(in.Layout.Home, ".fixture/.env"), filepath.Join(in.Layout.Home, ".fixture/tools/alias")
				var err error
				if kind == "credential link" {
					err = os.Symlink(source, dest)
				} else {
					err = os.Link(source, dest)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "external root":
				tools := filepath.Join(in.Layout.Home, ".fixture/tools")
				if err := os.Rename(tools, tools+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), tools); err != nil {
					t.Fatal(err)
				}
			case "wrong package":
				in.Origin.NPM.Package = "different-package"
			case "invalid metadata":
				writeInstalledFixture(t, in.Layout.Home, ".fixture/tools/runtime/lib/node_modules/@scope/fixture/package.json", marker)
			case "wrong version":
				in.Origin.NPM.Version = "different-version"
			case "missing integrity":
				in.Origin.NPM.Integrity = ""
			case "cancellation":
				cancel()
			}
			if observed, err := ObserveInstalledNPM(ctx, in); err == nil || observed != nil || strings.Contains(err.Error(), marker) {
				t.Fatal("unsafe/incomplete source was accepted or exposed")
			}
		})
	}
}
