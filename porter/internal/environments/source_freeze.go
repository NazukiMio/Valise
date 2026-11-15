package environments

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"

	"valise/core/vault"
)

// ProgramArchive is a private operation-owned standard tar. Close only closes
// the reader; explicit operation cleanup owns removal of its temporary files.
type ProgramArchive struct {
	RuntimeExecutable string
	Material          ProgramMaterial
	File              *os.File
}

// FreezeInstalledNPM copies matching program bytes into an independent archive.
// Missing historical fingerprints never authorize using the current program.
func FreezeInstalledNPM(ctx context.Context, parent string, in InstalledSource, expected *vault.RuntimeRecord) (*ProgramArchive, error) {
	if expected == nil || expected.Format != vault.FormatRuntime || expected.Installation == nil || expected.Home.Path != in.Layout.Home ||
		expected.OS != runtime.GOOS || expected.Arch != runtime.GOARCH || len(expected.Dependencies) == 0 ||
		Digest(expected.Installation.ProfileDoc) != Digest(in.Profile) || Digest(expected.Installation.RegisteredProvenance) != Digest(in.Origin) {
		return nil, ErrSource
	}
	if len(expected.Installation.Roots) != len(in.Layout.Roots) {
		return nil, ErrSource
	}
	for id, root := range in.Layout.Roots {
		if expected.Installation.Roots[id].Path != root {
			return nil, ErrSource
		}
	}
	observed, err := ObserveInstalledNPM(ctx, in)
	if err != nil {
		return nil, err
	}
	if Digest(observed.Dependencies) != Digest(expected.Dependencies) {
		return nil, ErrSource
	}
	if in.CheckSpace != nil {
		if err := in.CheckSpace(observed.Material); err != nil {
			return nil, err
		}
	}
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
	w := tar.NewWriter(io.MultiWriter(f, h))
	for _, file := range observed.Material.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header := &tar.Header{Name: file.Path, Size: file.Size, Mode: int64(file.Mode), Typeflag: tar.TypeReg}
		if file.LinkTarget != "" {
			header.Typeflag, header.Linkname = tar.TypeSymlink, file.LinkTarget
		}
		if err := w.WriteHeader(header); err != nil {
			return nil, ErrSource
		}
		if file.LinkTarget != "" {
			continue
		}
		reader, err := openSourceFile(in, observed.directories, file.Path)
		if err != nil {
			return nil, ErrSource
		}
		checksum := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(w, checksum), io.LimitReader(&contextReader{ctx, reader}, file.Size+1))
		reader.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if copyErr != nil || n != file.Size || hex.EncodeToString(checksum.Sum(nil)) != file.SHA256 {
			return nil, ErrSource
		}
	}
	if err := w.Close(); err != nil {
		return nil, ErrSource
	}
	if err := f.Sync(); err != nil {
		return nil, ErrSource
	}
	after, err := ObserveInstalledNPM(ctx, in)
	if err != nil {
		return nil, err
	}
	if after.Material.SourceDigest != observed.Material.SourceDigest || Digest(after.Dependencies) != Digest(observed.Dependencies) {
		return nil, ErrSource
	}
	if observed.runtime != nil && (after.runtime == nil || after.runtime.PID != observed.runtime.PID || after.runtime.Start != observed.runtime.Start) {
		return nil, ErrSource
	}
	st, err := f.Stat()
	if err != nil {
		return nil, ErrSource
	}
	material := observed.Material
	material.ArchiveBytes, material.ArchiveSHA = st.Size(), hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, ErrSource
	}
	success = true
	return &ProgramArchive{Material: material, File: f, RuntimeExecutable: observed.RuntimeExecutable}, nil
}
