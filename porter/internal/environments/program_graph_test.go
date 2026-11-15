package environments

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func linkFile(p, target string) ProgramFile {
	return ProgramFile{Path: p, LinkTarget: target, SHA256: hashFixture([]byte(target)), Mode: 0o777}
}

func setProgramArchive(t *testing.T, in *BuildInput, contents map[string][]byte, extraPAX bool) {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, f := range in.Program.Files {
		h := &tar.Header{Name: f.Path, Size: f.Size, Mode: int64(f.Mode), Typeflag: tar.TypeReg}
		if f.LinkTarget != "" {
			h.Typeflag, h.Linkname = tar.TypeSymlink, f.LinkTarget
		}
		if extraPAX {
			h.Format, h.PAXRecords = tar.FormatPAX, map[string]string{"SCHILY.xattr.user.unexpected": "not permitted"}
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if f.LinkTarget == "" {
			if _, err := w.Write(contents[f.Path]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	in.Program.ArchiveSHA, in.Program.ArchiveBytes = hashFixture(b.Bytes()), int64(b.Len())
	in.Archive = bytes.NewReader(b.Bytes())
}

func addProgramEntryLink(t *testing.T, in *BuildInput, binary []byte) {
	t.Helper()
	in.Program.Files = append(in.Program.Files, linkFile(".fixture/program/launch", in.Layout.Home+"/.fixture/program/agent"))
	setProgramArchive(t, in, map[string][]byte{".fixture/program/agent": binary}, false)
	in.Profile.Environments.StartArgv[0] = "${STATE_DIR}/program/launch"
	in.Target.Source.ProfileDigest = Digest(in.Profile)
}

func TestPrivateProgramLinksAndStandardLongNames(t *testing.T) {
	in := fixtureInput(t, []byte("private runtime"))
	long := ".fixture/program/lib/" + strings.Repeat("long-path/", 15) + "library"
	library := ProgramFile{Path: long, Size: 7, SHA256: hashFixture([]byte("library")), Mode: 0o600}
	in.Program.Files = append(in.Program.Files, library,
		linkFile(".fixture/program/bin/agent", "../agent"),
		linkFile(".fixture/program/launch", in.Layout.Home+"/.fixture/program/bin/agent"),
		linkFile(".fixture/program/lib64", "lib"),
		linkFile(".fixture/program/long-library", in.Layout.Home+"/"+long))
	in.Profile.Environments.StartArgv[0] = "${STATE_DIR}/program/launch"
	in.Target.Source.ProfileDigest = Digest(in.Profile)
	setProgramArchive(t, &in, map[string][]byte{".fixture/program/agent": []byte("private runtime"), long: []byte("library")}, false)
	candidate, err := Build(context.Background(), t.TempDir(), in)
	if err != nil {
		t.Fatal("safe private runtime/long-name material was refused", err)
	}
	for _, f := range in.Program.Files {
		if f.LinkTarget != "" {
			got, err := os.Readlink(filepath.Join(candidate.Workspace.ProgramDir(), f.Path))
			if err != nil || got != f.LinkTarget {
				t.Fatal("private link metadata changed", err)
			}
		}
	}
	if candidate.Spec.Argv[0] != in.Layout.Home+"/.fixture/program/launch" {
		t.Fatal("entry path was rewritten instead of resolved inside the guest")
	}
}

func TestProgramLinksCannotBorrowDataHostOrMissingRuntime(t *testing.T) {
	for _, target := range []string{"/usr/bin/python3", "/slots/active/python", "/source/home/.fixture/memory/new", "missing", "../missing", "launch", ".", "../../../../etc/passwd"} {
		t.Run(target, func(t *testing.T) {
			in := fixtureInput(t, []byte("private runtime"))
			in.Program.Files = append(in.Program.Files, linkFile(".fixture/program/launch", target))
			setProgramArchive(t, &in, map[string][]byte{".fixture/program/agent": []byte("private runtime")}, false)
			if c, err := Build(context.Background(), t.TempDir(), in); err != ErrProgram || c != nil {
				t.Fatal("unsafe private runtime link accepted", err)
			}
		})
	}
	for _, kind := range []string{"cycle", "directory cycle", "link hash", "link size", "link mode", "pax", "bad archive"} {
		t.Run(kind, func(t *testing.T) {
			in := fixtureInput(t, []byte("private runtime"))
			link := linkFile(".fixture/program/launch", "agent")
			switch kind {
			case "cycle":
				link = linkFile(link.Path, "other")
				in.Program.Files = append(in.Program.Files, linkFile(".fixture/program/other", "launch"))
			case "directory cycle":
				in.Program.Files = append(in.Program.Files,
					ProgramFile{Path: ".fixture/program/one/empty", SHA256: hashFixture(nil), Mode: 0o600},
					ProgramFile{Path: ".fixture/program/two/empty", SHA256: hashFixture(nil), Mode: 0o600},
					linkFile(".fixture/program/one/alias", "../two"), linkFile(".fixture/program/two/alias", "../one"))
			case "link hash":
				link.SHA256 = strings.Repeat("0", 64)
			case "link size":
				link.Size = 1
			case "link mode":
				link.Mode = 0o700
			}
			in.Program.Files = append(in.Program.Files, link)
			if kind == "link size" {
				// Invalid size is rejected before any tar reader is consulted.
				in.Program.ArchiveSHA = hashFixture([]byte("unused"))
			} else {
				setProgramArchive(t, &in, map[string][]byte{".fixture/program/agent": []byte("private runtime")}, kind == "pax")
			}
			if kind == "bad archive" {
				in.Program.ArchiveSHA = strings.Repeat("0", 64)
			}
			parent := t.TempDir()
			if c, err := Build(context.Background(), parent, in); !errors.Is(err, ErrProgram) || c != nil {
				t.Fatal("invalid link material accepted", err)
			}
			if kind == "bad archive" {
				if err := filepath.WalkDir(parent, func(_ string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.Type()&os.ModeSymlink != 0 {
						t.Fatal("link created before complete archive digest passed")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
