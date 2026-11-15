package environments

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"syscall"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/vault"
)

// ArchiveBound copies only an already verified private programme. It neither
// reads live data nor consults or executes the old host installation.
func (s ProgramStore) ArchiveBound(ctx context.Context, parent string, agent identity.AgentRef, ref environment.Ref, expected string, runtime *vault.RuntimeRecord) (*ProgramArchive, error) {
	b, err := s.Load(agent, ref)
	if err != nil || Digest(b) != expected || b.Runtime == nil || runtime == nil || Digest(b.Runtime.Dependencies) != Digest(runtime.Dependencies) || Digest(b.Runtime.Installation) != Digest(runtime.Installation) || b.Runtime.Home != runtime.Home || b.Runtime.OS != runtime.OS || b.Runtime.Arch != runtime.Arch {
		return nil, ErrSource
	}
	w, _, err := s.Open(ctx, agent, ref, expected)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(w.ProgramDir())
	if err != nil {
		return nil, ErrSource
	}
	defer r.Close()
	f, err := os.CreateTemp(parent, "valise-program-*.tar")
	if err != nil {
		return nil, ErrSource
	}
	success := false
	defer func() {
		if !success {
			f.Close()
		}
	}()
	h := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(f, h))
	for _, item := range b.Program.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header := &tar.Header{Name: item.Path, Size: item.Size, Mode: int64(item.Mode), Typeflag: tar.TypeReg}
		if item.LinkTarget != "" {
			header.Typeflag, header.Linkname = tar.TypeSymlink, item.LinkTarget
		}
		if err := tw.WriteHeader(header); err != nil {
			return nil, ErrSource
		}
		if item.LinkTarget != "" {
			continue
		}
		reader, err := r.OpenFile(item.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, ErrSource
		}
		before, err := reader.Stat()
		if err != nil || !programRegularMatches(before, item) {
			reader.Close()
			return nil, ErrSource
		}
		digest := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(tw, digest), io.LimitReader(&contextReader{ctx, reader}, item.Size+1))
		reader.Close()
		if copyErr != nil || n != item.Size || hex.EncodeToString(digest.Sum(nil)) != item.SHA256 {
			return nil, ErrSource
		}
	}
	if tw.Close() != nil || f.Sync() != nil {
		return nil, ErrSource
	}
	if err := s.Verify(ctx, agent, ref, expected); err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		return nil, ErrSource
	}
	material := b.Program
	material.ArchiveBytes, material.ArchiveSHA = st.Size(), hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, ErrSource
	}
	success = true
	return &ProgramArchive{Material: material, File: f, RuntimeExecutable: b.Layout.RuntimeExecutable}, nil
}
