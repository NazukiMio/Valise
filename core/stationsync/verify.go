package stationsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"valise/core/vault"
)

// Reading a leaf on a station for strict copies (docs/ARCHITECTURE.md
// 7.1.1): list it, download small files and, when asked, packs into a
// private temporary directory, hash each and delete it at once.

func (r *rsyncRemote) Fetch(ctx context.Context, base string, rels []string, dir string) error {
	if len(rels) == 0 {
		return nil
	}
	tmp, err := tempDir(r.st)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	list := filepath.Join(tmp, "files")
	if err := os.WriteFile(list, []byte(strings.Join(rels, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return r.exec(ctx, []string{"rsync", "-t", "--files-from=" + list, "-e", r.shell(), base + "/", dir + "/"}, "从 "+r.st.Name+" 读取失败")
}

// ChecksumDiff lists the files of the local leaf dir that dest lacks or
// holds with other content, compared by checksum on both ends; nothing is
// transferred.
func (r *rsyncRemote) ChecksumDiff(ctx context.Context, dir, dest string) ([]string, error) {
	code, stdout, stderr, err := r.run.Run(ctx, Timeout, "rsync", "-rnc", "--out-format=%n", "-e", r.shell(),
		"--exclude", "tmp-*", "--exclude", ".tmp-*", "--exclude", partPrefix+"*", "--exclude", vault.DeletingFile, dir+"/", dest+"/")
	if err != nil {
		return nil, &errNoRsync{failure(r.st, "无法运行 rsync", err.Error()), whyNoRsync(err.Error())}
	}
	if code != 0 {
		ue := failure(r.st, "无法核对 "+r.st.Name+" 上的内容", string(stderr))
		if rsyncMissing(code, string(stderr)) {
			return nil, &errNoRsync{ue, whyNoRsync(string(stderr))}
		}
		return nil, ue
	}
	var out []string
	for _, line := range strings.Split(string(stdout), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasSuffix(line, "/") {
			out = append(out, line)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ChecksumComparer is implemented by remotes that compare by checksum on
// the station itself.
type ChecksumComparer interface {
	ChecksumDiff(ctx context.Context, dir, dest string) ([]string, error)
}

func (r *sftpRemote) Fetch(ctx context.Context, base string, rels []string, dir string) error {
	if len(rels) == 0 {
		return nil
	}
	root := strings.TrimRight(destPath(base), "/")
	var lines []string
	for _, rel := range rels {
		local := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
			return err
		}
		lines = append(lines, "get "+quote(path.Join(root, rel))+" "+quote(local))
	}
	_, err := r.batch(ctx, lines, "从 "+r.st.Name+" 读取失败")
	return err
}

// LeafEntries are the files of a listing that belong to the material list.
func LeafEntries(entries []Entry) (files []Entry, deleting bool) {
	for _, e := range entries {
		switch {
		case e.Dir:
		case e.Path == vault.DeletingFile:
			deleting = true
		case !vault.MaterialExcluded(e.Path):
			files = append(files, e)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, deleting
}

// KnownPack returns the recorded SHA-256 of a pack already hashed at this
// location, when its size and stamp are unchanged.
type KnownPack func(rel string, size int64, stamp string) (string, bool)

// LeafReading is what was read of a leaf on a station.
type LeafReading struct {
	Materials []vault.Material
	// Small holds the contents of every non-pack file (index, meta...).
	Small map[string][]byte
	// Hashed are the packs hashed now (not from KnownPack).
	Hashed []string
	// Fetched counts downloaded bytes.
	Fetched int64
}

// PendingPackBytes is what hashing the packs still needs to download.
func PendingPackBytes(files []Entry, known KnownPack) int64 {
	var n int64
	for _, e := range files {
		if !vault.IsPackPath(e.Path) {
			continue
		}
		if known != nil {
			if _, ok := known(e.Path, e.Size, e.Stamp); ok {
				continue
			}
		}
		n += e.Size
	}
	return n
}

// packBatch bounds the bytes downloaded before hashing and deleting.
const packBatch = 256 << 20

// ReadLeaf reads the listed files of the leaf at base: every small file is
// downloaded and hashed; packs come from known or, when packs is set, are
// downloaded and hashed in batches. tmp must be private.
func ReadLeaf(ctx context.Context, r Remote, base string, files []Entry, tmp string, packs bool, known KnownPack) (*LeafReading, error) {
	work, err := os.MkdirTemp(tmp, "leaf-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	res := &LeafReading{Small: map[string][]byte{}}
	byPath := map[string]*vault.Material{}
	var small, todo []string
	for _, e := range files {
		res.Materials = append(res.Materials, vault.Material{Path: e.Path, Size: e.Size, Stamp: e.Stamp})
	}
	for i := range res.Materials {
		m := &res.Materials[i]
		byPath[m.Path] = m
		switch {
		case !vault.IsPackPath(m.Path):
			small = append(small, m.Path)
		case known != nil:
			if sum, ok := known(m.Path, m.Size, m.Stamp); ok {
				m.SHA256 = sum
				continue
			}
			fallthrough
		case packs:
			if packs {
				todo = append(todo, m.Path)
			}
		}
	}
	hash := func(rels []string, keep bool) error {
		for _, rel := range rels {
			p := filepath.Join(work, filepath.FromSlash(rel))
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			h := sha256.New()
			var buf strings.Builder
			var w io.Writer = h
			if keep {
				w = io.MultiWriter(h, &buf)
			}
			n, err := io.Copy(w, f)
			f.Close()
			os.Remove(p)
			if err != nil {
				return err
			}
			res.Fetched += n
			byPath[rel].SHA256 = hex.EncodeToString(h.Sum(nil))
			if keep {
				res.Small[rel] = []byte(buf.String())
			}
		}
		return nil
	}
	if err := r.Fetch(ctx, base, small, work); err != nil {
		return nil, err
	}
	if err := hash(small, true); err != nil {
		return nil, err
	}
	for len(todo) > 0 {
		var batch []string
		var size int64
		for len(todo) > 0 && (len(batch) == 0 || size+byPath[todo[0]].Size <= packBatch) {
			size += byPath[todo[0]].Size
			batch, todo = append(batch, todo[0]), todo[1:]
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := r.Fetch(ctx, base, batch, work); err != nil {
			return nil, err
		}
		if err := hash(batch, false); err != nil {
			return nil, err
		}
		res.Hashed = append(res.Hashed, batch...)
	}
	return res, nil
}
