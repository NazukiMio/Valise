package environments

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/vault"
)

func validateDestinations(program ProgramMaterial, plan environment.DataPlan, roots map[string]string, dirs []string, home string, budget int64) error {
	seen := map[string]bool{}
	add := func(dest string, size int64, hash string) error {
		if !safeRelative(dest) || seen[dest] || size < 0 || size > budget || hash != "" && !validSHA(hash) {
			return ErrBuild
		}
		budget -= size
		seen[dest] = true
		return nil
	}
	for _, f := range program.Files {
		if !inPrograms(f.Path, dirs) || f.SHA256 == "" || f.Mode&^0o777 != 0 || add(f.Path, f.Size, f.SHA256) != nil {
			return ErrProgram
		}
	}
	for _, e := range plan.Entries {
		dest := path.Join(roots[e.Root], e.Path)
		for _, d := range dirs {
			if dest == d || strings.HasPrefix(dest, d+"/") || strings.HasPrefix(d, dest+"/") {
				return ErrBuild
			}
		}
		if e.File != nil {
			if e.File.SHA256 == "" || e.File.Mode&^0o777 != 0 || add(dest, e.File.Size, e.File.SHA256) != nil {
				return ErrData
			}
		} else if e.Link == nil || add(dest, 0, "") != nil {
			return ErrData
		}
	}
	for dest := range seen {
		for parent := path.Dir(dest); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return ErrBuild
			}
		}
		for _, root := range roots {
			if dest == root || strings.HasPrefix(root, dest+"/") {
				return ErrBuild
			}
		}
	}
	if _, err := newProgramGraph(program, dirs, home); err != nil {
		return err
	}
	return nil
}

func unpackProgram(ctx context.Context, home *os.Root, material ProgramMaterial, archive io.Reader) error {
	// Limit the complete archive, including trailing padding. Parsing a valid
	// prefix is insufficient evidence that all supplied bytes match the digest.
	if material.ArchiveBytes > 1<<40 {
		return ErrProgram
	}
	h := sha256.New()
	count := &countReader{r: io.TeeReader(io.LimitReader(&contextReader{ctx, archive}, material.ArchiveBytes+1), h)}
	tr := tar.NewReader(count)
	want := map[string]ProgramFile{}
	var links []ProgramFile
	entry := 0
	bad := func(code string) error { return &ProgramError{Code: code, Entry: entry} }
	for _, f := range material.Files {
		want[f.Path] = f
	}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return bad("archive_read")
		}
		entry++
		f, ok := want[header.Name]
		if !ok || header.Size != f.Size || uint32(header.Mode) != f.Mode {
			return bad("archive_manifest")
		}
		if !programPAX(header) {
			return bad("archive_metadata")
		}
		if f.LinkTarget != "" {
			if header.Typeflag != tar.TypeSymlink || header.Linkname != f.LinkTarget {
				return bad("archive_link")
			}
			links = append(links, f)
		} else {
			if header.Typeflag != tar.TypeReg || header.Linkname != "" {
				return bad("archive_regular")
			}
			if err := writeVerified(ctx, home, f.Path, f.Size, f.SHA256, fs.FileMode(f.Mode)&0o700, time.Time{}, tr); err != nil {
				return bad("archive_file_write_or_checksum")
			}
		}
		delete(want, header.Name)
	}
	if _, err := io.Copy(io.Discard, count); err != nil || len(want) != 0 || count.n != material.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != material.ArchiveSHA {
		return bad("archive_checksum")
	}
	for _, link := range links {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := home.MkdirAll(path.Dir(link.Path), 0o700); err != nil {
			return bad("archive_link_parent")
		}
		if err := home.Symlink(link.LinkTarget, link.Path); err != nil {
			return bad("archive_link_create")
		}
	}
	return nil
}

func programPAX(header *tar.Header) bool {
	for key, value := range header.PAXRecords {
		if key == "path" && value == header.Name || key == "linkpath" && value == header.Linkname {
			continue
		}
		return false
	}
	return true
}

func writeData(ctx context.Context, home *os.Root, roots map[string]string, plan environment.DataPlan,
	open func(context.Context, identity.SnapshotRef, string, string) (io.ReadCloser, error)) error {
	// Write every regular file before creating ANY data symlink. Parent/link
	// conflicts were checked across all mapped roots before the first write.
	for _, e := range plan.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.File == nil {
			continue
		}
		r, err := open(ctx, e.Source, e.Root, e.Path)
		if err != nil || r == nil {
			return ErrData
		}
		mode := fs.FileMode(e.File.Mode).Perm() & 0o700
		if vault.Sensitive("", e.Category) {
			mode = 0o600
		}
		err = writeVerified(ctx, home, path.Join(roots[e.Root], e.Path), e.File.Size, e.File.SHA256, mode, e.File.MTime, r)
		closeErr := r.Close()
		if err != nil || closeErr != nil {
			return ErrData
		}
	}
	for _, e := range plan.Entries {
		if e.Link == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		dest := path.Join(roots[e.Root], e.Path)
		if err := home.MkdirAll(path.Dir(dest), 0o700); err != nil {
			return ErrData
		}
		if err := home.Symlink(e.Link.Target, dest); err != nil {
			return ErrData
		}
	}
	return nil
}

func writeVerified(ctx context.Context, home *os.Root, dest string, size int64, hash string, mode fs.FileMode, mtime time.Time, r io.Reader) error {
	if size < 0 || size == 1<<63-1 || !validSHA(hash) || !safeRelative(dest) {
		return ErrData
	}
	if err := home.MkdirAll(path.Dir(dest), 0o700); err != nil {
		return ErrData
	}
	f, err := home.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrData
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(&contextReader{ctx, r}, size+1))
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		return ErrData
	}
	if err := f.Chmod(mode); err != nil {
		return ErrData
	}
	if !mtime.IsZero() {
		if err := home.Chtimes(dest, mtime, mtime); err != nil {
			return ErrData
		}
	}
	return f.Sync()
}

func syncTree(dir string) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err == nil && e.IsDir() {
			dirs = append(dirs, p)
		}
		return err
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		f, err := os.Open(d)
		if err != nil {
			return err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type countReader struct {
	r io.Reader
	n int64
}

func (r *countReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}
