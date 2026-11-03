package vault

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

// Vault is a read-only view of a vault directory. Opening it needs no key.
type Vault struct {
	Dir    string
	Desc   Descriptor
	Epochs []Epoch
	Metas  []Meta
	blobs  map[string]BlobEntry
}

// Open reads VAULT, epochs, every index file and every meta file.
func Open(dir string) (*Vault, error) {
	v := &Vault{Dir: dir, blobs: map[string]BlobEntry{}}
	b, err := os.ReadFile(joinRel(dir, DescriptorFile))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &v.Desc); err != nil || v.Desc.Format != FormatVault {
		return nil, errors.New("vault: VAULT is not valise-vault/1")
	}
	if v.Epochs, err = readEpochs(dir); err != nil {
		return nil, err
	}
	idx, err := readIndexes(dir)
	if err != nil {
		return nil, err
	}
	for _, ix := range idx {
		for _, e := range ix.Blobs {
			v.blobs[e.ID] = e
		}
	}
	if v.Metas, err = readMetas(dir); err != nil {
		return nil, err
	}
	return v, nil
}

func readEpochs(dir string) ([]Epoch, error) {
	ents, err := os.ReadDir(joinRel(dir, EpochsDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Epoch
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(n); err != nil {
			continue
		}
		var ep Epoch
		b, err := os.ReadFile(filepath.Join(joinRel(dir, EpochsDir), e.Name()))
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &ep); err != nil {
			return nil, fmt.Errorf("vault: %s: %w", e.Name(), err)
		}
		out = append(out, ep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Epoch < out[j].Epoch })
	return out, nil
}

func readIndexes(dir string) ([]Index, error) {
	ents, err := os.ReadDir(joinRel(dir, IndexDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Index
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(joinRel(dir, IndexDir), e.Name()))
		if err != nil {
			return nil, err
		}
		var ix Index
		if err := json.Unmarshal(b, &ix); err != nil || ix.Format != FormatIndex {
			return nil, fmt.Errorf("vault: index %s is invalid", e.Name())
		}
		out = append(out, ix)
	}
	return out, nil
}

func readMetas(dir string) ([]Meta, error) {
	ents, err := os.ReadDir(joinRel(dir, SnapshotsDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".meta.json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(joinRel(dir, SnapshotsDir), e.Name()))
		if err != nil {
			return nil, err
		}
		var m Meta
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("vault: meta %s is invalid", e.Name())
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SnapshotID < out[j].SnapshotID })
	return out, nil
}

// Blob returns the index entry of a blob.
func (v *Vault) Blob(id string) (BlobEntry, bool) {
	e, ok := v.blobs[id]
	return e, ok
}

// ReadBlob returns the plaintext of a blob.
func (v *Vault) ReadBlob(id string, ids ...age.Identity) ([]byte, error) {
	e, ok := v.blobs[id]
	if !ok {
		return nil, fmt.Errorf("vault: blob %s not in any index", short(id))
	}
	f, err := os.Open(joinRel(v.Dir, PackPath(e.Pack)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ct := make([]byte, e.Length)
	if _, err := f.ReadAt(ct, e.Offset); err != nil {
		return nil, fmt.Errorf("vault: pack %s: %w", short(e.Pack), err)
	}
	r, err := age.Decrypt(bytes.NewReader(ct), ids...)
	if err != nil {
		return nil, fmt.Errorf("vault: blob %s: %w", short(id), err)
	}
	z, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("vault: blob %s: %w", short(id), err)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	plain, err := dec.DecodeAll(z, nil)
	if err != nil {
		return nil, fmt.Errorf("vault: blob %s: %w", short(id), err)
	}
	if int64(len(plain)) != e.PlainSize {
		return nil, fmt.Errorf("vault: blob %s: size mismatch", short(id))
	}
	return plain, nil
}

// SnapshotHeader decrypts only a snapshot's header: enough to know the
// blobs of its file list without any pack.
func (v *Vault) SnapshotHeader(snapshotID string, ids ...age.Identity) (*Snapshot, error) {
	if !ValidID(snapshotID) {
		return nil, errors.New("vault: invalid snapshot id")
	}
	ct, err := os.ReadFile(joinRel(v.Dir, SnapshotPath(snapshotID)))
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(ct), ids...)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.NewDecoder(r).Decode(&s); err != nil || s.Format != FormatSnapshot || s.SnapshotID != snapshotID {
		return nil, errors.New("vault: snapshot header is invalid")
	}
	return &s, nil
}

// PacksOf lists the packs holding blobs (each once, in first-use order);
// unknown lists blob ids no index knows.
func (v *Vault) PacksOf(blobs []string) (packs, unknown []string) {
	seen := map[string]bool{}
	for _, id := range blobs {
		e, ok := v.blobs[id]
		switch {
		case !ok:
			unknown = append(unknown, id)
		case !seen[e.Pack]:
			seen[e.Pack] = true
			packs = append(packs, e.Pack)
		}
	}
	return packs, unknown
}

// Snapshot decrypts a snapshot header and its file list.
func (v *Vault) Snapshot(snapshotID string, ids ...age.Identity) (*Snapshot, *Files, error) {
	sh, err := v.SnapshotHeader(snapshotID, ids...)
	if err != nil {
		return nil, nil, err
	}
	s := *sh
	var doc bytes.Buffer
	for _, id := range s.FilesBlobs {
		b, err := v.ReadBlob(id, ids...)
		if err != nil {
			return nil, nil, err
		}
		doc.Write(b)
	}
	if SHA256Hex(doc.Bytes()) != s.FilesSHA256 {
		return nil, nil, errors.New("vault: file list does not match files_sha256")
	}
	var files Files
	if err := json.Unmarshal(doc.Bytes(), &files); err != nil || files.Format != FormatFiles {
		return nil, nil, errors.New("vault: file list is invalid")
	}
	return &s, &files, nil
}

// WriteFile streams a file's content to w and checks its SHA-256.
func (v *Vault) WriteFile(f File, w io.Writer, ids ...age.Identity) error {
	h := sha256.New()
	mw := io.MultiWriter(w, h)
	for _, id := range f.Blobs {
		b, err := v.ReadBlob(id, ids...)
		if err != nil {
			return err
		}
		mw.Write(b)
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("vault: %s:%s does not match its sha256", f.Root, f.Path)
	}
	return nil
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
