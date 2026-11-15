package revive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"

	"valise/core/vault"
)

// Metadata only. The sole parsed file is the public VAULT descriptor, to
// prevent a nested backup from becoming an old-environment cleanup casualty.
func oldCleanupTree(ctx context.Context, dir string, protectVault bool) (string, int64, int64, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return "", 0, 0, oldCleanupError("environment_cleanup_unknown")
	}
	defer r.Close()
	h := sha256.New()
	enc := json.NewEncoder(h)
	var files, bytes int64
	err = fs.WalkDir(r.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return oldCleanupError("environment_cleanup_unknown")
		}
		st, err := r.Lstat(rel)
		if err != nil || st.IsDir() != d.IsDir() {
			return oldCleanupError("environment_cleanup_unknown")
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			return oldCleanupError("environment_cleanup_unknown")
		}
		if st.IsDir() && protectVault {
			// A damaged descriptor must not make an otherwise recognizable
			// backup layout disappear from this protection.
			layout := true
			for _, name := range []string{"packs", "snapshots", "index"} {
				child, e := r.Lstat(path.Join(rel, name))
				layout = layout && e == nil && child.IsDir()
			}
			if layout {
				return oldCleanupError("environment_cleanup_contains_vault")
			}
		} else if !st.IsDir() {
			files++
			if st.Mode().IsRegular() {
				if st.Size() < 0 || bytes > (1<<63-1)-st.Size() {
					return oldCleanupError("environment_cleanup_unknown")
				}
				bytes += st.Size()
			}
			if protectVault && path.Base(rel) == vault.DescriptorFile && st.Mode().IsRegular() && st.Size() <= 64<<10 {
				f, e := r.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
				if e != nil {
					return oldCleanupError("environment_cleanup_unknown")
				}
				var descriptor struct {
					Format string `json:"format"`
				}
				e = json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&descriptor)
				f.Close()
				if e == nil && strings.HasPrefix(descriptor.Format, "valise-vault/") {
					return oldCleanupError("environment_cleanup_contains_vault")
				}
			}
		}
		return enc.Encode(struct {
			Path           string
			Mode           uint32
			Size, Modified int64
			Device, Inode  uint64
			Owner          uint32
		}{rel, uint32(st.Mode()), st.Size(), st.ModTime().UnixNano(), uint64(owner.Dev), owner.Ino, owner.Uid})
	})
	if err != nil {
		return "", 0, 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), files, bytes, nil
}
