package environments

import (
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
	"syscall"

	"valise/core/profile"
	"valise/core/vault"
)

func scanProgramTrees(ctx context.Context, in InstalledSource, dirs []string) ([]ProgramFile, error) {
	home, err := filepath.EvalSymlinks(in.Layout.Home)
	if err != nil {
		return nil, ErrSource
	}
	var files []ProgramFile
	remaining := in.MaxBytes
	for _, dir := range dirs {
		source := filepath.Join(in.Layout.Home, filepath.FromSlash(dir))
		real, err := filepath.EvalSymlinks(source)
		if err != nil || real != filepath.Join(home, filepath.FromSlash(dir)) {
			return nil, ErrSource
		}
		r, err := os.OpenRoot(source)
		if err != nil {
			return nil, ErrSource
		}
		err = fs.WalkDir(r.FS(), ".", func(rel string, entry fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return ErrSource
			}
			st, err := entry.Info()
			if err != nil || !sourceOwned(st) {
				return ErrSource
			}
			if entry.IsDir() {
				return nil
			}
			if len(files) >= 200000 {
				return ErrSource
			}
			dest := path.Join(dir, rel)
			if sourceCategory(in, dest) != profile.CategoryBody {
				return ErrSource
			}
			f := ProgramFile{Path: dest, Mode: uint32(st.Mode().Perm() & 0o700)}
			if st.Mode()&os.ModeSymlink != 0 {
				target, err := r.Readlink(rel)
				if err != nil {
					return ErrSource
				}
				h := sha256.Sum256([]byte(target))
				f.LinkTarget, f.Mode, f.SHA256 = target, 0o777, hex.EncodeToString(h[:])
			} else {
				if !st.Mode().IsRegular() || st.Size() < 0 || st.Size() > remaining || st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
					return ErrSource
				}
				f.Size = st.Size()
				reader, err := r.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
				if err != nil {
					return ErrSource
				}
				before, err := reader.Stat()
				if err != nil || !os.SameFile(st, before) || !sourceOwned(before) {
					reader.Close()
					return ErrSource
				}
				h := sha256.New()
				n, copyErr := io.Copy(h, io.LimitReader(&contextReader{ctx, reader}, f.Size+1))
				after, statErr := reader.Stat()
				reader.Close()
				if copyErr != nil || statErr != nil || n != f.Size || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
					return ErrSource
				}
				f.SHA256 = hex.EncodeToString(h.Sum(nil))
				remaining -= f.Size
			}
			files = append(files, f)
			return nil
		})
		r.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if len(files) == 0 {
		return nil, ErrSource
	}
	return files, nil
}

func sourceOwned(st os.FileInfo) bool {
	stat, ok := st.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid()) && (st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().IsRegular() && stat.Nlink == 1)
}

func openSourceFile(in InstalledSource, dirs []string, rel string) (*os.File, error) {
	for _, dir := range dirs {
		if !strings.HasPrefix(rel, dir+"/") {
			continue
		}
		r, err := os.OpenRoot(filepath.Join(in.Layout.Home, filepath.FromSlash(dir)))
		if err != nil {
			return nil, ErrSource
		}
		defer r.Close()
		f, err := r.OpenFile(strings.TrimPrefix(rel, dir+"/"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return nil, ErrSource
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() || !sourceOwned(st) {
			f.Close()
			return nil, ErrSource
		}
		return f, nil
	}
	return nil, ErrSource
}

func treeDependencies(in InstalledSource, dirs []string, files []ProgramFile) []vault.RuntimeDependency {
	var dependencies []vault.RuntimeDependency
	for _, dir := range dirs {
		var tree []ProgramFile
		var size int64
		for _, file := range files {
			if strings.HasPrefix(file.Path, dir+"/") {
				tree = append(tree, file)
				size += file.Size
			}
		}
		logical := filepath.Join(in.Layout.Home, filepath.FromSlash(dir))
		real, _ := filepath.EvalSymlinks(logical)
		hash := Digest(struct {
			Format string        `json:"format"`
			Files  []ProgramFile `json:"files"`
		}{"valise-program-tree/1", tree})
		dependencies = append(dependencies, vault.RuntimeDependency{Role: "program-tree", Path: logical, Resolved: real, Size: size, SHA256: hash, Provenance: &in.Origin})
	}
	return dependencies
}
