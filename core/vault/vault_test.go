package vault

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

type fixture struct {
	dir, keyDir string
	id          *age.X25519Identity
	recipients  []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	id, _ := age.GenerateX25519Identity()
	return &fixture{dir: filepath.Join(t.TempDir(), "vault"), keyDir: t.TempDir(), id: id, recipients: []string{id.Recipient().String()}}
}

func (f *fixture) keyFile(v string) string { return filepath.Join(f.keyDir, v+".key") }

// data: a small file and a 24 MiB file of pseudo-random bytes. The chunker
// polynomial is random per epoch, so the big file must be large enough to be
// split into many chunks (average 1 MiB) whatever the polynomial.
func testData() map[string][]byte {
	big := make([]byte, 24<<20)
	rand.New(rand.NewSource(1)).Read(big)
	return map[string][]byte{"small.md": []byte("hello soul\n"), "big.bin": big, "empty": {}}
}

func (f *fixture) seal(t *testing.T, data map[string][]byte) *Meta {
	t.Helper()
	w, err := OpenWriter(f.dir, f.recipients, f.keyFile, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var files Files
	for name, b := range data {
		blobs, sum, n, err := w.AddStream(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if n != int64(len(b)) || sum != SHA256Hex(b) {
			t.Fatalf("%s: n=%d", name, n)
		}
		files.Files = append(files.Files, File{Root: "state", Path: name, Category: "memory", Mode: 0o644,
			MTime: time.Unix(1, 0).UTC(), Size: n, SHA256: sum, Blobs: blobs})
	}
	m, err := w.Commit(Snapshot{InstanceID: "demo", Tier: TierSoul, Profile: ProfileRef{ID: "demo", Version: 1}, CreatedAt: time.Now().UTC(),
		Roots: map[string]string{"state": "/s"}}, files, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRoundTripAndDedupe(t *testing.T) {
	f := newFixture(t)
	data := testData()
	m1 := f.seal(t, data)
	if m1.Epoch != 1 || m1.FileCount != 3 || m1.PlainBytes != int64(len(data["big.bin"])+len(data["small.md"])) {
		t.Fatalf("%+v", m1)
	}
	v, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	snap, files, err := v.Snapshot(m1.SnapshotID, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.InstanceID != "demo" || len(files.Files) != 3 || files.Files[0].Path != "big.bin" {
		t.Fatalf("%+v %+v", snap, files.Files)
	}
	for _, fl := range files.Files {
		var buf bytes.Buffer
		if err := v.WriteFile(fl, &buf, f.id); err != nil || !bytes.Equal(buf.Bytes(), data[fl.Path]) {
			t.Fatalf("%s: %v", fl.Path, err)
		}
	}
	if len(files.Files[0].Blobs) < 4 {
		t.Fatalf("big file was not chunked: %d blobs", len(files.Files[0].Blobs))
	}

	// Second seal of unchanged data: same epoch, < 1% new bytes.
	m2 := f.seal(t, data)
	if m2.Epoch != 1 || m2.AddedBytes*100 >= m1.AddedBytes {
		t.Fatalf("second seal added %d of %d", m2.AddedBytes, m1.AddedBytes)
	}
	// A small change in the big file only rewrites the affected chunk(s).
	changed := testData()
	changed["big.bin"][12<<20] ^= 1
	m3 := f.seal(t, changed)
	if m3.AddedBytes >= m1.AddedBytes/2 {
		t.Fatalf("one-byte change added %d of %d", m3.AddedBytes, m1.AddedBytes)
	}
	// A download interrupted mid-file leaves its temporary name behind;
	// it is not part of the vault and does not fail the check.
	subs, _ := os.ReadDir(filepath.Join(f.dir, PacksDir))
	os.WriteFile(filepath.Join(f.dir, PacksDir, subs[0].Name(), ".tmp-3792185698"), []byte("partial"), 0o600)
	if probs, err := VerifyNoKey(f.dir); err != nil || len(probs) != 0 {
		t.Fatalf("%v %v", probs, err)
	}
	if n, probs, err := VerifyWithKey(f.dir, nil, f.id); err != nil || len(probs) != 0 || n != 9 {
		t.Fatalf("%d %v %v", n, probs, err)
	}
}

func TestEpochRotation(t *testing.T) {
	f := newFixture(t)
	f.seal(t, testData())
	// Same recipients but the server lost its dedupe key: new epoch.
	ents, _ := os.ReadDir(f.keyDir)
	os.Remove(filepath.Join(f.keyDir, ents[0].Name()))
	if m := f.seal(t, testData()); m.Epoch != 2 {
		t.Fatalf("epoch %d", m.Epoch)
	}
	// Adding a recipient starts epoch 3; no blobs are shared across epochs.
	other, _ := age.GenerateX25519Identity()
	f.recipients = append(f.recipients, other.Recipient().String())
	m := f.seal(t, testData())
	if m.Epoch != 3 || m.AddedBytes < int64(24<<20) {
		t.Fatalf("%+v", m)
	}
	v, _ := Open(f.dir)
	if len(v.Epochs) != 3 || len(v.Epochs[2].RecipientFingerprints) != 2 {
		t.Fatalf("%+v", v.Epochs)
	}
	// Both identities can read the epoch-3 snapshot.
	for _, id := range []*age.X25519Identity{f.id, other} {
		if _, _, err := v.Snapshot(m.SnapshotID, id); err != nil {
			t.Fatal(err)
		}
	}
	if probs, _ := VerifyNoKey(f.dir); len(probs) != 0 {
		t.Fatal(probs)
	}
}

func TestTamperDetected(t *testing.T) {
	f := newFixture(t)
	m := f.seal(t, testData())
	var pack string
	filepath.Walk(filepath.Join(f.dir, PacksDir), func(p string, info os.FileInfo, err error) error {
		if strings.HasSuffix(p, ".pack") {
			pack = p
		}
		return nil
	})
	b, _ := os.ReadFile(pack)
	b[len(b)/2] ^= 0xff
	os.WriteFile(pack, b, 0o600)
	probs, err := VerifyNoKey(f.dir)
	if err != nil || len(probs) == 0 || !strings.Contains(probs[0].What, "SHA-256") {
		t.Fatalf("%v %v", probs, err)
	}
	if _, probs, _ := VerifyWithKey(f.dir, []string{m.SnapshotID}, f.id); len(probs) == 0 {
		t.Fatal("with-key verification missed the tampered pack")
	}
	// A missing snapshot file is reported too.
	os.Remove(filepath.Join(f.dir, SnapshotPath(m.SnapshotID)))
	probs, _ = VerifyNoKey(f.dir)
	found := false
	for _, p := range probs {
		found = found || strings.Contains(p.What, "快照文件")
	}
	if !found {
		t.Fatal(probs)
	}
}

func TestWrongKeyCannotRead(t *testing.T) {
	f := newFixture(t)
	m := f.seal(t, testData())
	other, _ := age.GenerateX25519Identity()
	v, _ := Open(f.dir)
	if _, _, err := v.Snapshot(m.SnapshotID, other); err == nil {
		t.Fatal("decrypted with a foreign key")
	}
}

func TestRecipients(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	got, err := Recipients("# comment\n\n" + id.Recipient().String() + "\n")
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if _, err := Recipients("age1notakey"); err == nil {
		t.Fatal("accepted")
	}
	if _, err := OpenWriter(t.TempDir(), nil, func(string) string { return "" }, time.Now()); err == nil {
		t.Fatal("writer without recipients")
	}
}

func TestVaultFilesAreWriteOnce(t *testing.T) {
	f := newFixture(t)
	f.seal(t, testData())
	if err := writeOnce(f.dir, DescriptorFile, []byte("{}")); err == nil {
		t.Fatal("VAULT overwritten")
	}
	// Every file in the vault has a valid relative path (no temporaries).
	filepath.Walk(f.dir, func(p string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(f.dir, p)
		if !ValidRel(filepath.ToSlash(rel)) {
			t.Errorf("unexpected file %s", rel)
		}
		return nil
	})
}
