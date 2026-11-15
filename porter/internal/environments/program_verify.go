package environments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func verifyPrivateProgram(ctx context.Context, dir string, material ProgramMaterial, paths []string, home string) error {
	if ctx == nil {
		return ErrProgram
	}
	if err := ownedDirectory(dir); err != nil {
		return err
	}
	graph, err := newProgramGraph(material, paths, home)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ErrProgram
	}
	defer root.Close()
	seen := map[string]bool{}
	err = filepath.WalkDir(dir, func(name string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return ErrProgram
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return ErrProgram
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		st, err := root.Lstat(rel)
		if err != nil {
			return ErrProgram
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) {
			return ErrProgram
		}
		if st.IsDir() {
			if !graph.directories[rel] || st.Mode().Perm() != 0700 {
				return ErrProgram
			}
			return nil
		}
		f, ok := graph.nodes[rel]
		if !ok || seen[rel] {
			return ErrProgram
		}
		seen[rel] = true
		if f.LinkTarget != "" {
			if st.Mode()&os.ModeSymlink == 0 {
				return ErrProgram
			}
			target, err := root.Readlink(rel)
			if err != nil || target != f.LinkTarget {
				return ErrProgram
			}
			return nil
		}
		if !st.Mode().IsRegular() || owner.Nlink != 1 || st.Size() != f.Size || uint32(st.Mode().Perm()) != f.Mode&0700 {
			return ErrProgram
		}
		reader, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return ErrProgram
		}
		defer reader.Close()
		opened, err := reader.Stat()
		if err != nil || !os.SameFile(st, opened) || !programRegularMatches(opened, f) {
			return ErrProgram
		}
		h := sha256.New()
		n, readErr := io.Copy(h, io.LimitReader(&contextReader{ctx, reader}, f.Size+1))
		reader.Close()
		after, statErr := root.Lstat(rel)
		if readErr != nil || statErr != nil || !os.SameFile(st, after) || !st.ModTime().Equal(after.ModTime()) || st.Mode() != after.Mode() ||
			!programRegularMatches(after, f) || n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return ErrProgram
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(graph.nodes) {
		return ErrProgram
	}
	return ctx.Err()
}

func programRegularMatches(st os.FileInfo, f ProgramFile) bool {
	owner, ok := st.Sys().(*syscall.Stat_t)
	return ok && st.Mode().IsRegular() && owner.Uid == uint32(os.Getuid()) && owner.Nlink == 1 && st.Size() == f.Size && uint32(st.Mode().Perm()) == f.Mode&0700
}
