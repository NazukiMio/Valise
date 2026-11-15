package environments

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
)

func hashFixture(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func programTar(t *testing.T, data []byte) (ProgramMaterial, []byte) {
	t.Helper()
	f := ProgramFile{Path: ".fixture/program/agent", Size: int64(len(data)), SHA256: hashFixture(data), Mode: 0o700}
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	if err := w.WriteHeader(&tar.Header{Name: f.Path, Size: f.Size, Mode: int64(f.Mode), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return ProgramMaterial{SourceDigest: "pinned-recorded-version", ArchiveSHA: hashFixture(b.Bytes()), ArchiveBytes: int64(b.Len()),
		OS: runtime.GOOS, Arch: runtime.GOARCH, Files: []ProgramFile{f}}, b.Bytes()
}

func fixtureInput(t *testing.T, program []byte) BuildInput {
	t.Helper()
	p := profile.Profile{Schema: profile.Schema, ID: "fixture", DisplayName: "Fixture", ProfileVersion: 1, Status: profile.StatusDraft,
		Provenance: &profile.Provenance{Kind: profile.ProvenanceBinary, Binary: &profile.BinaryProvenance{Name: "fixture-agent"}},
		Detect:     &profile.Detect{StateDirCandidates: []string{"${HOME}/.fixture"}},
		Roots:      []profile.Root{{ID: "workspace", Default: "${STATE_DIR}/work"}},
		Paths:      []profile.PathRule{{Root: "state", Glob: "memory/**", Category: "memory"}, {Root: "workspace", Glob: "**", Category: "workspace"}},
		Entries:    []profile.Entry{{ID: "gateway", Kind: "web", Host: "127.0.0.1", Port: 18391, Path: "/"}},
		Health:     &profile.Health{Checks: []profile.HealthCheck{{Kind: "http", Entry: "gateway", ExpectStatus: []int{200}}}},
		Environments: &profile.Environments{Engine: "linux-bwrap", StartArgv: profile.Argv{"${STATE_DIR}/program/agent"}, WorkingDir: "${STATE_DIR}",
			Path: []string{"${HOME}/.fixture/program"}, ProgramPaths: []profile.PathRef{{Root: "state", Glob: "program"}},
			GatewayEntry: "gateway", Verification: profile.Capability{Status: "untested"}}}
	if issues := profile.Lint([]*profile.Document{{Profile: &p}})[0].Issues; len(issues) != 0 {
		t.Fatal(issues)
	}
	l := Layout{Home: "/source/home", Roots: map[string]string{"state": "/source/home/.fixture", "workspace": "/source/home/.fixture/work"}}
	now := time.Unix(200, 0)
	src := environment.Source{Agent: identity.AgentRef{ServerUID: "server", InstallID: "fixture", InstanceID: "fixture", Generation: "generation"},
		Snapshot: identity.SnapshotRef{VaultID: vault.NewID("v", now), SnapshotID: vault.NewID("s", now)}, Time: now, Tier: vault.TierFull,
		ProfileDigest: Digest(p), LayoutDigest: LayoutDigest(l, nil), ProgramDigest: "pinned-recorded-version", Verified: true}
	material, archive := programTar(t, program)
	m := environment.Material{Source: src, Files: vault.Files{Format: vault.FormatFiles}}
	return BuildInput{Profile: p, Layout: l, Target: m, Program: material, Archive: bytes.NewReader(archive), MaxBytes: int64(len(program)) + 1<<20}
}

func dataFile(root, p, cat, content string) vault.File {
	return vault.File{Root: root, Path: p, Category: cat, Size: int64(len(content)), SHA256: hashFixture([]byte(content)), Mode: 0o644}
}

func TestBuildUsesExactDataSourcesAndPreservesActiveFiles(t *testing.T) {
	in := fixtureInput(t, []byte("fixture private program"))
	active := filepath.Join(t.TempDir(), "active")
	if err := os.WriteFile(active, []byte("active unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := in.Target
	base.Source.Time = time.Unix(100, 0)
	base.Source.Snapshot.SnapshotID = vault.NewID("s", base.Source.Time)
	base.Files.Files = []vault.File{dataFile("state", "memory/deleted", "memory", "old memory"), dataFile("workspace", "kept", "workspace", "baseline workspace")}
	in.Baseline = &base
	in.Target.Source.Tier = vault.TierSoul
	in.Target.Files.Files = []vault.File{dataFile("state", "memory/new", "memory", "target memory")}
	in.Target.Files.Files[0].MTime = time.Unix(66, 0)
	in.Target.Files.Symlinks = []vault.Symlink{{Root: "state", Path: "memory/current", Target: "new"}}
	var reads []identity.SnapshotRef
	in.OpenData = func(_ context.Context, ref identity.SnapshotRef, root, p string) (io.ReadCloser, error) {
		reads = append(reads, ref)
		if ref == in.Target.Source.Snapshot && root == "state" && p == "memory/new" {
			return io.NopCloser(strings.NewReader("target memory")), nil
		}
		if ref == base.Source.Snapshot && root == "workspace" && p == "kept" {
			return io.NopCloser(strings.NewReader("baseline workspace")), nil
		}
		t.Fatal("read outside exact authenticated sources")
		return nil, ErrData
	}
	c, err := Build(context.Background(), t.TempDir(), in)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{".fixture/memory/new": "target memory", ".fixture/work/kept": "baseline workspace", ".fixture/memory/current": "target memory"} {
		b, err := os.ReadFile(filepath.Join(c.Workspace.HomeDir(), p))
		if err != nil || string(b) != want {
			t.Fatal(p, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(c.Workspace.ProgramDir(), ".fixture/program/agent")); err != nil || string(b) != "fixture private program" {
		t.Fatal("program was not placed in its independent tree", err)
	}
	if _, err := os.Stat(filepath.Join(c.Workspace.HomeDir(), ".fixture/program/agent")); !os.IsNotExist(err) {
		t.Fatal("program was duplicated into exchangeable data")
	}
	if _, err := os.Stat(filepath.Join(c.Workspace.HomeDir(), ".fixture/memory/deleted")); !os.IsNotExist(err) {
		t.Fatal("deleted baseline content reappeared")
	}
	if st, err := os.Stat(filepath.Join(c.Workspace.HomeDir(), ".fixture/memory/new")); err != nil || !st.ModTime().Equal(time.Unix(66, 0)) {
		t.Fatal("selected metadata was not restored", err)
	}
	if b, err := os.ReadFile(active); err != nil || string(b) != "active unchanged" {
		t.Fatal("active data changed")
	}
	if !c.Receipt.CandidateBuilt || c.Receipt.Files != 4 || c.Receipt.DataPlanDigest == "" || len(reads) != 2 {
		t.Fatal(c.Receipt, reads)
	}
	if c.Spec.Argv[0] != "/source/home/.fixture/program/agent" || c.Spec.Home != in.Layout.Home || c.Workspace.GuestHome() != in.Layout.Home {
		t.Fatal("shared program selected")
	}
}

func TestBuildRejectsBadSourcesBeforeReadingData(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*BuildInput)
	}{
		{"archive digest", func(in *BuildInput) { in.Program.ArchiveSHA = strings.Repeat("0", 64) }},
		{"source", func(in *BuildInput) { in.Program.SourceDigest = "other" }},
		{"architecture", func(in *BuildInput) { in.Program.Arch = "other" }},
		{"profile", func(in *BuildInput) { in.Profile.ProfileVersion++ }},
		{"external root", func(in *BuildInput) {
			in.Layout.Roots["workspace"] = "/outside"
			in.Target.Source.LayoutDigest = LayoutDigest(in.Layout, nil)
		}},
		{"reserved home", func(in *BuildInput) {
			in.Layout.Home = "/usr"
			in.Target.Source.LayoutDigest = LayoutDigest(in.Layout, nil)
		}},
		{"root hidden by program", func(in *BuildInput) {
			in.Layout.Roots["workspace"] = "/source/home/.fixture/program/work"
			in.Target.Source.LayoutDigest = LayoutDigest(in.Layout, nil)
		}},
		{"nested programs", func(in *BuildInput) {
			in.Profile.Environments.ProgramPaths = append(in.Profile.Environments.ProgramPaths, profile.PathRef{Root: "state", Glob: "program/subdir"})
			in.Target.Source.ProfileDigest = Digest(in.Profile)
		}},
		{"aliased roots", func(in *BuildInput) {
			in.Layout.Roots["workspace"] = in.Layout.Roots["state"]
			in.Target.Source.LayoutDigest = LayoutDigest(in.Layout, nil)
		}},
		{"cross root collision", func(in *BuildInput) {
			in.Layout.Roots["workspace"] = "/source/home/.fixture/memory"
			in.Target.Source.LayoutDigest = LayoutDigest(in.Layout, nil)
			in.Target.Files.Files = []vault.File{dataFile("state", "memory/same", "memory", "one"), dataFile("workspace", "same", "workspace", "two")}
		}},
		{"program escape", func(in *BuildInput) { in.Program.Files[0].Path = "../active" }},
		{"budget", func(in *BuildInput) { in.MaxBytes = 1 }},
		{"no foundation", func(in *BuildInput) { in.Target.Source.Tier = vault.TierSoul }},
		{"shared entry", func(in *BuildInput) {
			in.Profile.Environments.StartArgv[0] = "/usr/bin/shared"
			in.Target.Source.ProfileDigest = Digest(in.Profile)
		}},
		{"program data overlap", func(in *BuildInput) {
			in.Profile.Paths = append(in.Profile.Paths, profile.PathRule{Root: "state", Glob: "program/**", Category: "memory"})
			in.Target.Source.ProfileDigest = Digest(in.Profile)
			in.Target.Files.Files = []vault.File{dataFile("state", "program/agent", "memory", "would overwrite program")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fixtureInput(t, []byte("program"))
			tc.edit(&in)
			in.OpenData = func(context.Context, identity.SnapshotRef, string, string) (io.ReadCloser, error) {
				t.Fatal("invalid plan reached data reader")
				return nil, ErrData
			}
			if c, err := Build(context.Background(), t.TempDir(), in); err == nil || c != nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
}

func TestProgramArchiveRejectsLinksAndUnlistedEntries(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "duplicate", "unlisted", "device"} {
		t.Run(kind, func(t *testing.T) {
			in := fixtureInput(t, []byte("program"))
			f := in.Program.Files[0]
			var body bytes.Buffer
			w := tar.NewWriter(&body)
			header := tar.Header{Name: f.Path, Size: f.Size, Mode: int64(f.Mode), Typeflag: tar.TypeReg}
			switch kind {
			case "symlink":
				header.Typeflag, header.Size, header.Linkname = tar.TypeSymlink, 0, "../../active"
			case "hardlink":
				header.Typeflag, header.Size, header.Linkname = tar.TypeLink, 0, "../../active"
			case "unlisted":
				header.Name = ".fixture/program/unlisted"
			case "device":
				header.Typeflag, header.Size = tar.TypeChar, 0
			}
			if err := w.WriteHeader(&header); err != nil {
				t.Fatal(err)
			}
			if header.Typeflag == tar.TypeReg {
				w.Write([]byte("program"))
			}
			if kind == "duplicate" {
				if err := w.WriteHeader(&header); err != nil {
					t.Fatal(err)
				}
				w.Write([]byte("program"))
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			in.Program.ArchiveSHA, in.Program.ArchiveBytes = hashFixture(body.Bytes()), int64(body.Len())
			in.Archive = bytes.NewReader(body.Bytes())
			if c, err := Build(context.Background(), t.TempDir(), in); err == nil || c != nil {
				t.Fatal("unsafe verified archive accepted")
			}
		})
	}
}

func TestBuildCorruptDataAndCancellationHaveNoReceipt(t *testing.T) {
	in := fixtureInput(t, []byte("program"))
	in.Target.Files.Files = []vault.File{dataFile("state", "memory/file", "memory", "expected")}
	in.OpenData = func(context.Context, identity.SnapshotRef, string, string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("wrong contents must never be logged")), nil
	}
	if c, err := Build(context.Background(), t.TempDir(), in); err == nil || c != nil || strings.Contains(err.Error(), "wrong contents") {
		t.Fatal("corrupt data accepted or leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parent := t.TempDir()
	if c, err := Build(ctx, parent, in); err != context.Canceled || c != nil {
		t.Fatal("cancelled build proceeded", err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatal("cancelled build wrote files")
	}
}
