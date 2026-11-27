package vault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// PromotionSource validates the original reference recorded in a promoted
// snapshot. Both physical locations retain their own vault identities.
func PromotionSource(meta Meta) (string, bool) {
	vid, sid, ok := strings.Cut(meta.PromotedFrom, "/")
	return vid, ok && meta.Kind == KindSavepoint && sid == meta.SnapshotID &&
		strings.HasPrefix(vid, "v_") && ValidID(vid) && strings.HasPrefix(sid, "s_") && ValidID(sid)
}

// Promote re-packs an automatic snapshot's complete ciphertext dependencies
// into a standard vault. The caller must hold the vault tree's write lock.
func Promote(ctx context.Context, source *Vault, dest string, meta Meta, note string) (*Meta, error) {
	if meta.Kind != KindAuto || !strings.HasPrefix(meta.SnapshotID, "s_") || !ValidID(meta.SnapshotID) ||
		!strings.HasPrefix(source.Desc.VaultID, "v_") || !ValidID(source.Desc.VaultID) {
		return nil, errors.New("vault: invalid automatic snapshot reference")
	}
	id := meta.SnapshotID
	b, err := os.ReadFile(joinRel(source.Dir, IndexPath(id)))
	if err != nil {
		return nil, err
	}
	var ix Index
	if json.Unmarshal(b, &ix) != nil || ix.Format != FormatIndex || ix.SnapshotID != id || ix.Epoch != meta.Epoch || len(ix.Blobs) == 0 {
		return nil, errors.New("vault: automatic snapshot index is invalid")
	}
	header, err := os.ReadFile(joinRel(source.Dir, SnapshotPath(id)))
	if err != nil {
		return nil, err
	}
	if len(header) == 0 {
		return nil, errors.New("vault: automatic snapshot header is empty")
	}
	key, err := os.ReadFile(joinRel(source.Dir, DedupeKeyPath(meta.Epoch)))
	if err != nil {
		return nil, err
	}
	if _, err := createOrReadDescriptor(dest, time.Now().UTC()); err != nil {
		return nil, err
	}
	target, err := Open(dest)
	if err != nil {
		return nil, err
	}
	for _, m := range target.Metas {
		if m.SnapshotID == id {
			return nil, errors.New("vault: snapshot already exists in the destination")
		}
	}
	sourceEpoch, ok := epochByNumber(source.Epochs, meta.Epoch)
	if !ok {
		return nil, errors.New("vault: source epoch missing")
	}
	epoch, err := importEpoch(target, sourceEpoch, key)
	if err != nil {
		return nil, err
	}
	w := &Writer{dir: dest, known: target.blobs}
	defer w.Abort()
	packSizes := map[string]int64{}
	seen := map[string]bool{}
	for _, entry := range ix.Blobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[entry.ID] || !validDigest(entry.ID) || !validDigest(entry.Pack) || entry.Offset < 0 || entry.Length <= 0 || entry.PlainSize < 0 {
			return nil, errors.New("vault: invalid automatic blob entry")
		}
		seen[entry.ID] = true
		path := joinRel(source.Dir, PackPath(entry.Pack))
		size, ok := packSizes[entry.Pack]
		if !ok {
			size, err = verifyPack(path, entry.Pack)
			if err != nil {
				return nil, err
			}
			packSizes[entry.Pack] = size
		}
		if entry.Offset > size || entry.Length > size-entry.Offset {
			return nil, errors.New("vault: automatic blob extends beyond its pack")
		}
		if _, ok := w.known[entry.ID]; ok {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		err = w.appendCiphertext(io.NewSectionReader(f, entry.Offset, entry.Length), entry)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := w.finishPack(); err != nil {
		return nil, err
	}
	for i, entry := range ix.Blobs {
		ix.Blobs[i] = w.known[entry.ID]
	}
	ix.Epoch = epoch
	ix.FullDeps = true
	index := mustJSON(ix)
	for _, entry := range []struct {
		path string
		data []byte
	}{{IndexPath(id), index}, {SnapshotPath(id), header}} {
		if err := writeSame(dest, entry.path, entry.data); err != nil {
			return nil, err
		}
	}
	if problems, err := VerifyNoKey(dest); err != nil {
		return nil, err
	} else if len(problems) != 0 {
		return nil, errors.New(problems[0].String())
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	meta.Epoch, meta.Kind, meta.Note = epoch, KindSavepoint, note
	meta.PromotedFrom = source.Desc.VaultID + "/" + id
	base := w.added + int64(len(index)+len(header))
	meta.AddedBytes = base
	for range 3 {
		meta.AddedBytes = base + int64(len(mustJSON(meta)))
	}
	if err := writeOnce(dest, MetaPath(id), mustJSON(meta)); err != nil {
		return nil, err
	}
	return &meta, nil
}

func validDigest(id string) bool {
	_, err := packIDFromName(id + ".pack")
	return err == nil
}

func verifyPack(path, id string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	m, err := f.Stat()
	if err != nil || !m.Mode().IsRegular() {
		return 0, errors.New("vault: invalid source pack")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, err
	}
	if hex.EncodeToString(h.Sum(nil)) != id {
		return 0, errors.New("vault: source pack checksum mismatch")
	}
	return n, nil
}

func epochByNumber(epochs []Epoch, number int) (Epoch, bool) {
	for _, ep := range epochs {
		if ep.Epoch == number {
			return ep, true
		}
	}
	return Epoch{}, false
}

func importEpoch(v *Vault, epoch Epoch, key []byte) (int, error) {
	for _, ep := range v.Epochs {
		if !equalStrings(sortedCopy(ep.RecipientFingerprints), sortedCopy(epoch.RecipientFingerprints)) {
			continue
		}
		b, err := os.ReadFile(joinRel(v.Dir, DedupeKeyPath(ep.Epoch)))
		if err == nil && bytes.Equal(key, b) {
			return ep.Epoch, nil
		}
	}
	next, err := nextEpoch(v.Dir, v.Epochs)
	if err != nil {
		return 0, err
	}
	epoch.Epoch = next
	if err := writeOnce(v.Dir, DedupeKeyPath(next), key); err != nil {
		return 0, err
	}
	if err := writeOnce(v.Dir, EpochPath(next), mustJSON(epoch)); err != nil {
		return 0, err
	}
	return next, nil
}

func nextEpoch(dir string, epochs []Epoch) (int, error) {
	next := 1
	if len(epochs) > 0 {
		next = epochs[len(epochs)-1].Epoch + 1
	}
	for next > 0 {
		if _, err := os.Lstat(joinRel(dir, DedupeKeyPath(next))); errors.Is(err, os.ErrNotExist) {
			return next, nil
		} else if err != nil {
			return 0, err
		}
		next++
	}
	return 0, errors.New("vault: epoch space exhausted")
}

func writeSame(dir, rel string, data []byte) error {
	got, err := os.ReadFile(joinRel(dir, rel))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return writeOnce(dir, rel, data)
	case err != nil:
		return err
	case !bytes.Equal(got, data):
		return fmt.Errorf("vault: existing promotion material differs at %s", rel)
	default:
		return nil
	}
}
