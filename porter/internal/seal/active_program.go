package seal

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"valise/core/profile"
	"valise/core/vault"
	"valise/porter/internal/activeview"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
)

type backupProgramFile struct {
	environments.ProgramFile
	dir string
}

// Map programme mounts back to declared data roots. Bytes hidden by those
// mounts are not the running programme and must never leak into a raw backup.
func activeProgramOverlay(ctx context.Context, home string, b *activeview.DataBinding, tier string, verified bool, c *profile.Classifier) ([]porterenroll.Item, []string, map[string]backupProgramFile, error) {
	var items []porterenroll.Item
	var hidden []string
	expected := map[string]backupProgramFile{}
	if b == nil || b.Native {
		return items, hidden, expected, nil
	}
	if tier == vault.TierRaw && !verified {
		return nil, nil, nil, programBackupError()
	}
	if b.Profile.Environments != nil {
		for _, mount := range b.Profile.Environments.ProgramPaths {
			if root := b.Record.Roots[mount.Root]; root != "" {
				hidden = append(hidden, filepath.Join(root, filepath.FromSlash(mount.Glob)))
			}
		}
	}
	if b.Program == nil {
		return items, hidden, expected, nil
	}
	p := b.Program
	for _, mount := range p.Paths {
		guest := filepath.Join(p.Layout.Home, filepath.FromSlash(mount))
		for id, root := range p.Layout.Roots {
			if rel, ok := within(root, guest); ok {
				hidden = append(hidden, filepath.Join(b.Record.Roots[id], rel))
			}
		}
	}
	if tier != vault.TierRaw {
		return items, hidden, expected, nil
	}
	base, err := (environments.ProgramStore{Home: home}).RevisionPath(b.Registry.Agent, b.Ref)
	if err != nil {
		return nil, nil, nil, programBackupError()
	}
	w, err := isolated.OpenWorkspace(filepath.Join(base, p.Workspace), p.Layout.Home, p.Paths)
	if err != nil {
		return nil, nil, nil, programBackupError()
	}
	// Prefer a declared body root, otherwise use the deepest logical root as
	// the ordinary walker does. Map iteration must not decide archive ownership.
	ids := make([]string, 0, len(p.Layout.Roots))
	for id := range p.Layout.Roots {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, z := p.Layout.Roots[ids[i]], p.Layout.Roots[ids[j]]
		if len(a) != len(z) {
			return len(a) > len(z)
		}
		return ids[i] < ids[j]
	})
	for _, f := range p.Program.Files {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		guest := filepath.Join(p.Layout.Home, filepath.FromSlash(f.Path))
		id, rel := "", ""
		if p.Profile.Body != nil {
			for _, body := range p.Profile.Body.Paths {
				if r, ok := within(p.Layout.Roots[body.Root], guest); ok && profile.MatchGlob(body.Glob, filepath.ToSlash(r)) {
					id, rel = body.Root, r
					break
				}
			}
		}
		if id == "" {
			for _, root := range ids {
				if r, ok := within(p.Layout.Roots[root], guest); ok {
					id, rel = root, r
					break
				}
			}
		}
		if id == "" {
			continue // e.g. an interpreter outside declared backup roots
		}
		abs := filepath.Join(w.ProgramDir(), filepath.FromSlash(f.Path))
		st, err := os.Lstat(abs)
		if err != nil {
			return nil, nil, nil, programBackupError()
		}
		it := porterenroll.Item{Root: id, Rel: filepath.ToSlash(rel), Abs: abs, Info: st, Symlink: f.LinkTarget != "", Match: c.Classify(id, filepath.ToSlash(rel))}
		if it.Match.Category == "" {
			it.Match.Category = vault.CategoryUnclassified
		}
		items = append(items, it)
		expected[abs] = backupProgramFile{ProgramFile: f, dir: w.ProgramDir()}
	}
	return items, hidden, expected, nil
}

func within(root, path string) (string, bool) {
	if root == "" || !filepath.IsAbs(root) {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	return rel, err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func hiddenProgramPath(path string, hidden []string) bool {
	for _, mount := range hidden {
		if path == mount || strings.HasPrefix(path, mount+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func sealProgramFile(w *vault.Writer, it porterenroll.Item, f backupProgramFile) (*vault.File, *vault.Symlink, error) {
	root, err := os.OpenRoot(f.dir)
	if err != nil {
		return nil, nil, programBackupError()
	}
	defer root.Close()
	st, err := root.Lstat(f.Path)
	if err != nil {
		return nil, nil, programBackupError()
	}
	if f.LinkTarget != "" {
		target, err := root.Readlink(f.Path)
		if err != nil || st.Mode()&os.ModeSymlink == 0 || target != f.LinkTarget {
			return nil, nil, programBackupError()
		}
		return nil, &vault.Symlink{Root: it.Root, Path: it.Rel, Target: target}, nil
	}
	reader, err := root.OpenFile(f.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, programBackupError()
	}
	defer reader.Close()
	opened, err := reader.Stat()
	if err != nil || !os.SameFile(st, opened) || !st.Mode().IsRegular() || st.Size() != f.Size || uint32(st.Mode().Perm()) != f.Mode&0700 {
		return nil, nil, programBackupError()
	}
	blobs, sum, n, err := w.AddStream(io.LimitReader(reader, f.Size+1))
	if err != nil {
		return nil, nil, err
	}
	after, err := root.Lstat(f.Path)
	if err != nil || !os.SameFile(st, after) || !st.ModTime().Equal(after.ModTime()) || st.Mode() != after.Mode() || n != f.Size || sum != f.SHA256 {
		return nil, nil, programBackupError()
	}
	return &vault.File{Root: it.Root, Path: it.Rel, Category: it.Match.Category, Mode: uint32(st.Mode().Perm()), MTime: st.ModTime().UTC(), Size: n, SHA256: sum, Blobs: blobs}, nil, nil
}
