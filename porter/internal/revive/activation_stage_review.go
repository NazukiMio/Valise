package revive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"valise/core/revive"
	"valise/core/vault"
	"valise/porter/internal/environments"
)

type reviewedStageFile struct {
	Path     string
	Device   uint64
	Inode    uint64
	Mode     uint32
	Modified int64
	Size     int64
	SHA256   string
}

// reviewManagedStage never chmods files or writes the journal. A matching
// byte digest also preserves the already passed SQLite integrity observation.
func reviewManagedStage(ctx context.Context, j *revive.Journal) (string, int, int64, error) {
	bad := func() (string, int, int64, error) { return "", 0, 0, activationError("activation_stage_changed") }
	if j.State != revive.StatePlanned || !j.StageVerified || !j.ManifestSaved || !managedManifestMatches(j, j.Manifest) || checkManagedLocations(j) != nil || checkManagedStage(j) != nil {
		return bad()
	}
	want := map[string]bool{}
	var observed []reviewedStageFile
	var total int64
	for _, m := range j.Manifest {
		if err := ctx.Err(); err != nil {
			return "", 0, 0, err
		}
		path, err := stageDest(j, m.Path)
		if err != nil || want[path] || !reviewStageParents(j, path) {
			return bad()
		}
		want[path] = true
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return bad()
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return bad()
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Size() != m.Size ||
			vault.Sensitive("", m.Category) && st.Mode().Perm()&0077 != 0 {
			f.Close()
			return bad()
		}
		h := sha256.New()
		n, readErr := io.Copy(h, stageReviewReader{ctx: ctx, reader: f})
		after, statErr := f.Stat()
		f.Close()
		atPath, pathErr := os.Lstat(path)
		if readErr != nil || statErr != nil || pathErr != nil || !os.SameFile(st, after) || !os.SameFile(st, atPath) || st.Mode() != after.Mode() || st.Size() != after.Size() || !st.ModTime().Equal(after.ModTime()) ||
			n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
			return bad()
		}
		observed = append(observed, reviewedStageFile{m.Path, uint64(owner.Dev), owner.Ino, uint32(st.Mode()), st.ModTime().UnixNano(), n, m.SHA256})
		total += n
	}
	for _, root := range j.Roots {
		if err := filepath.WalkDir(root.Stage, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !d.IsDir() && (!d.Type().IsRegular() || !want[path]) {
				return activationError("activation_stage_changed")
			}
			return nil
		}); err != nil {
			return bad()
		}
	}
	if err := checkManagedStage(j); err != nil {
		return bad()
	}
	return environments.Digest(observed), len(observed), total, nil
}

func reviewStageParents(j *revive.Journal, path string) bool {
	root := rootOf(j, path)
	if root == "" {
		return false
	}
	for current := filepath.Dir(path); ; current = filepath.Dir(current) {
		st, err := os.Lstat(current)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if current == root {
			return true
		}
		if current == filepath.Dir(current) {
			return false
		}
	}
}

type stageReviewReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r stageReviewReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
