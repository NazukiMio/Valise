package vault

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
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
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
	"github.com/restic/chunker"
)

// PackTargetSize is the size at which a pack is finalised.
const PackTargetSize = 16 << 20

// Writer appends one snapshot to a vault. It never modifies existing files:
// every file is written to a temporary name, fsynced and renamed.
type Writer struct {
	dir        string
	desc       Descriptor
	params     DedupeParams
	hmacKey    []byte
	pol        chunker.Pol
	recipients []age.Recipient
	known      map[string]BlobEntry
	enc        *zstd.Encoder

	pack       *os.File
	packOff    int64
	packBlobs  []BlobEntry
	newBlobs   []BlobEntry
	added      int64
	plainTotal int64
	now        time.Time

	newEpochStarted bool
}

// Recipients parses age1… lines; blank lines and # comments are skipped.
func Recipients(text string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := age.ParseX25519Recipient(line); err != nil {
			return nil, fmt.Errorf("收件人公钥无效：%q", line)
		}
		out = append(out, line)
	}
	return out, nil
}

// Fingerprint is the first 16 hex digits of SHA-256 over a recipient
// string (docs/ARCHITECTURE.md section 3).
func Fingerprint(recipient string) string { return SHA256Hex([]byte(recipient))[:16] }

// OpenWriter opens (creating if needed) the vault at dir for one seal.
// recipients are the server's current age1… recipients; keyFile is the
// server's dedupe/<vault_id>.key path template ("%s" is replaced by the
// vault id). A new epoch is started when the recipient set differs from the
// latest epoch or the server no longer holds that epoch's parameters.
func OpenWriter(dir string, recipients []string, keyFile func(vaultID string) string, now time.Time) (*Writer, error) {
	if len(recipients) == 0 {
		return nil, errors.New("vault: no recipients")
	}
	w := &Writer{dir: dir, known: map[string]BlobEntry{}, now: now.UTC()}
	for _, r := range recipients {
		rc, err := age.ParseX25519Recipient(r)
		if err != nil {
			return nil, err
		}
		w.recipients = append(w.recipients, rc)
	}
	desc, err := createOrReadDescriptor(dir, w.now)
	if err != nil {
		return nil, err
	}
	w.desc = desc
	epochs, err := readEpochs(dir)
	if err != nil {
		return nil, err
	}
	fps := make([]string, 0, len(recipients))
	for _, r := range recipients {
		fps = append(fps, Fingerprint(r))
	}
	sort.Strings(fps)
	kf := keyFile(desc.VaultID)
	var params DedupeParams
	if b, err := os.ReadFile(kf); err == nil && json.Unmarshal(b, &params) == nil && params.VaultID == desc.VaultID {
		for _, epoch := range epochs {
			if params.Epoch == epoch.Epoch && equalStrings(sortedCopy(epoch.RecipientFingerprints), fps) {
				w.params = params
				break
			}
		}
	}
	if w.params.Epoch == 0 {
		next, err := nextEpoch(dir, epochs)
		if err != nil {
			return nil, err
		}
		if err := w.newEpoch(next, fps, kf); err != nil {
			return nil, err
		}
		w.newEpochStarted = true
	}
	if w.hmacKey, err = hex.DecodeString(w.params.HMACKey); err != nil || len(w.hmacKey) != 32 {
		return nil, errors.New("vault: bad dedupe key")
	}
	p, err := strconv.ParseUint(w.params.ChunkerPol, 16, 64)
	if err != nil {
		return nil, errors.New("vault: bad chunker polynomial")
	}
	w.pol = chunker.Pol(p)
	idx, err := readIndexes(dir)
	if err != nil {
		return nil, err
	}
	for _, ix := range idx {
		if ix.Epoch == w.params.Epoch {
			for _, b := range ix.Blobs {
				w.known[b.ID] = b
			}
		}
	}
	if w.enc, err = zstd.NewWriter(nil); err != nil {
		return nil, err
	}
	return w, nil
}

// NewEpoch reports whether opening the writer started a new epoch.
func (w *Writer) NewEpoch() bool { return w.newEpochStarted }

// Epoch returns the epoch this writer seals into.
func (w *Writer) Epoch() int { return w.params.Epoch }

// VaultID returns the vault id.
func (w *Writer) VaultID() string { return w.desc.VaultID }

func createOrReadDescriptor(dir string, now time.Time) (Descriptor, error) {
	var d Descriptor
	b, err := os.ReadFile(joinRel(dir, DescriptorFile))
	if err == nil {
		if err := json.Unmarshal(b, &d); err != nil || d.Format != FormatVault {
			return d, errors.New("vault: VAULT is not valise-vault/1")
		}
		return d, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return d, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return d, err
	}
	d = Descriptor{Format: FormatVault, VaultID: NewID("v", now), CreatedAt: now, Chunker: DefaultChunker}
	return d, writeOnce(dir, DescriptorFile, mustJSON(d))
}

func (w *Writer) newEpoch(n int, fps []string, keyFile string) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	pol, err := chunker.RandomPolynomial()
	if err != nil {
		return err
	}
	w.params = DedupeParams{Format: FormatDedupe, VaultID: w.desc.VaultID, Epoch: n,
		HMACKey: hex.EncodeToString(key), ChunkerPol: strconv.FormatUint(uint64(pol), 16)}
	plain := mustJSON(w.params)
	var ct bytes.Buffer
	aw, err := age.Encrypt(&ct, w.recipients...)
	if err != nil {
		return err
	}
	if _, err := aw.Write(plain); err != nil {
		return err
	}
	if err := aw.Close(); err != nil {
		return err
	}
	if err := writeOnce(w.dir, DedupeKeyPath(n), ct.Bytes()); err != nil {
		return err
	}
	if err := writeOnce(w.dir, EpochPath(n), mustJSON(Epoch{Epoch: n, Since: w.now, RecipientFingerprints: fps})); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(keyFile, plain, 0o600)
}

// blobID is HMAC-SHA256(hmac_key, plaintext).
func (w *Writer) blobID(plain []byte) string {
	m := hmac.New(sha256.New, w.hmacKey)
	m.Write(plain)
	return hex.EncodeToString(m.Sum(nil))
}

// AddBlob stores one plaintext chunk unless the epoch already has it.
func (w *Writer) AddBlob(plain []byte) (string, error) {
	id := w.blobID(plain)
	w.plainTotal += int64(len(plain))
	if _, ok := w.known[id]; ok {
		return id, nil
	}
	var ct bytes.Buffer
	aw, err := age.Encrypt(&ct, w.recipients...)
	if err != nil {
		return "", err
	}
	if _, err := aw.Write(w.enc.EncodeAll(plain, nil)); err != nil {
		return "", err
	}
	if err := aw.Close(); err != nil {
		return "", err
	}
	return id, w.appendCiphertext(bytes.NewReader(ct.Bytes()), BlobEntry{
		ID: id, Length: int64(ct.Len()), PlainSize: int64(len(plain)),
	})
}

func (w *Writer) appendCiphertext(ciphertext io.Reader, entry BlobEntry) error {
	if w.pack == nil {
		if err := os.MkdirAll(joinRel(w.dir, PacksDir), 0o700); err != nil {
			return err
		}
		f, err := os.CreateTemp(joinRel(w.dir, PacksDir), "tmp-*")
		if err != nil {
			return err
		}
		w.pack, w.packOff = f, 0
	}
	if _, err := io.CopyN(w.pack, ciphertext, entry.Length); err != nil {
		return err
	}
	entry.Offset = w.packOff
	w.packBlobs = append(w.packBlobs, entry)
	w.packOff += entry.Length
	w.known[entry.ID] = entry
	if w.packOff >= PackTargetSize {
		return w.finishPack()
	}
	return nil
}

func (w *Writer) finishPack() error {
	if w.pack == nil {
		return nil
	}
	f := w.pack
	w.pack = nil
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	id := hex.EncodeToString(h.Sum(nil))
	final := joinRel(w.dir, PackPath(id))
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(final); err == nil {
		os.Remove(f.Name()) // identical pack already present
	} else if err := os.Rename(f.Name(), final); err != nil {
		return err
	} else {
		syncDir(filepath.Dir(final))
		w.added += w.packOff
	}
	for _, b := range w.packBlobs {
		b.Pack = id
		w.newBlobs = append(w.newBlobs, b)
		w.known[b.ID] = b
	}
	w.packBlobs = nil
	return nil
}

// AddStream chunks r (small files whole, larger ones by content-defined
// chunking) and returns the blob ids, the SHA-256 of all bytes read and
// their count.
func (w *Writer) AddStream(r io.Reader) (blobs []string, sum string, n int64, err error) {
	h := sha256.New()
	tee := io.TeeReader(r, h)
	small := w.desc.Chunker.SmallFileMax
	head := make([]byte, small+1)
	k, err := io.ReadFull(tee, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, "", 0, err
	}
	if int64(k) <= small {
		id, err := w.AddBlob(head[:k])
		if err != nil {
			return nil, "", 0, err
		}
		return []string{id}, hex.EncodeToString(h.Sum(nil)), int64(k), nil
	}
	ch := chunker.NewWithBoundaries(io.MultiReader(bytes.NewReader(head[:k]), tee), w.pol,
		uint(w.desc.Chunker.Min), uint(w.desc.Chunker.Max))
	buf := make([]byte, w.desc.Chunker.Max)
	for {
		c, err := ch.Next(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", 0, err
		}
		id, err := w.AddBlob(c.Data)
		if err != nil {
			return nil, "", 0, err
		}
		blobs = append(blobs, id)
		n += int64(c.Length)
	}
	return blobs, hex.EncodeToString(h.Sum(nil)), n, nil
}

// Commit stores the file list, then writes index, snapshot header and meta
// in that order. It fills the header's SnapshotID, Epoch, FilesBlobs and
// FilesSHA256 and returns the meta written.
func (w *Writer) Commit(snap Snapshot, files Files, meta Meta) (*Meta, error) {
	files.Format = FormatFiles
	sortFiles(&files)
	if files.Files == nil {
		files.Files = []File{}
	}
	if files.Symlinks == nil {
		files.Symlinks = []Symlink{}
	}
	doc := mustJSON(files)
	plainBefore := w.plainTotal
	blobs, sum, _, err := w.AddStream(bytes.NewReader(doc))
	if err != nil {
		return nil, err
	}
	w.plainTotal = plainBefore
	if err := w.finishPack(); err != nil {
		return nil, err
	}
	snap.Format, snap.SnapshotID, snap.Epoch = FormatSnapshot, NewID("s", w.now), w.params.Epoch
	snap.FilesBlobs, snap.FilesSHA256 = blobs, sum
	if snap.Notes.Unstable == nil {
		snap.Notes.Unstable = []string{}
	}
	if snap.SQLite == nil {
		snap.SQLite = []SQLiteNote{}
	}
	if w.newBlobs == nil {
		w.newBlobs = []BlobEntry{}
	}
	entries, err := w.snapshotEntries(blobs, files)
	if err != nil {
		return nil, err
	}
	ix := mustJSON(Index{Format: FormatIndex, SnapshotID: snap.SnapshotID, Epoch: w.params.Epoch, Blobs: entries, FullDeps: true})
	if err := writeOnce(w.dir, IndexPath(snap.SnapshotID), ix); err != nil {
		return nil, err
	}
	w.added += int64(len(ix))
	var ct bytes.Buffer
	aw, err := age.Encrypt(&ct, w.recipients...)
	if err != nil {
		return nil, err
	}
	aw.Write(mustJSON(snap))
	if err := aw.Close(); err != nil {
		return nil, err
	}
	if err := writeOnce(w.dir, SnapshotPath(snap.SnapshotID), ct.Bytes()); err != nil {
		return nil, err
	}
	w.added += int64(ct.Len())
	meta.Format, meta.SnapshotID, meta.Epoch = FormatMeta, snap.SnapshotID, w.params.Epoch
	meta.CreatedAt, meta.Tier, meta.InstanceID = snap.CreatedAt, snap.Tier, snap.InstanceID
	meta.ProfileID, meta.FileCount, meta.PlainBytes = snap.Profile.ID, len(files.Files), w.plainTotal
	// added_bytes includes the meta file itself.
	meta.AddedBytes = w.added
	for i := 0; i < 3; i++ {
		meta.AddedBytes = w.added + int64(len(mustJSON(meta)))
	}
	if err := writeOnce(w.dir, MetaPath(snap.SnapshotID), mustJSON(meta)); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (w *Writer) snapshotEntries(list []string, files Files) ([]BlobEntry, error) {
	ids := append([]string(nil), list...)
	for _, file := range files.Files {
		ids = append(ids, file.Blobs...)
	}
	seen := make(map[string]bool)
	entries := make([]BlobEntry, 0)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		entry, ok := w.known[id]
		if !ok || entry.Pack == "" {
			return nil, errors.New("vault: snapshot index is missing a blob")
		}
		seen[id] = true
		entries = append(entries, entry)
	}
	return entries, nil
}

// Abort removes the unfinished pack, if any.
func (w *Writer) Abort() {
	if w.pack != nil {
		name := w.pack.Name()
		w.pack.Close()
		os.Remove(name)
		w.pack = nil
	}
}

func sortFiles(f *Files) {
	sort.Slice(f.Files, func(i, j int) bool {
		a, b := f.Files[i], f.Files[j]
		return a.Root < b.Root || (a.Root == b.Root && a.Path < b.Path)
	})
	sort.Slice(f.Symlinks, func(i, j int) bool {
		a, b := f.Symlinks[i], f.Symlinks[j]
		return a.Root < b.Root || (a.Root == b.Root && a.Path < b.Path)
	})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// writeOnce writes a new vault file; an existing file is an error.
func writeOnce(dir, rel string, data []byte) error {
	p := joinRel(dir, rel)
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("vault: %s already exists", rel)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(p, data, 0o600)
}

func writeFileAtomic(p string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}
	syncDir(filepath.Dir(p))
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

func sortedCopy(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
