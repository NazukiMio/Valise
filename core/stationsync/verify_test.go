package stationsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/vault"
)

// dirRemote serves a local directory as a station.
type dirRemote struct {
	Remote
	fetched []string
}

func (d *dirRemote) Fetch(_ context.Context, base string, rels []string, dir string) error {
	for _, rel := range rels {
		b, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil {
			return err
		}
		out := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(out), 0o700)
		if err := os.WriteFile(out, b, 0o600); err != nil {
			return err
		}
		d.fetched = append(d.fetched, rel)
	}
	return nil
}

func TestReadLeafHashesAndReusesPacks(t *testing.T) {
	base := t.TempDir()
	put := func(rel, body string) {
		p := filepath.Join(base, rel)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(body), 0o600)
	}
	pack := []byte("pack bytes")
	id := vault.SHA256Hex(pack)
	put(vault.PackPath(id), string(pack))
	put("VAULT", "{}")
	put("DELETING", "x")
	entries := []Entry{{Path: vault.PackPath(id), Size: int64(len(pack)), Stamp: "s1"}, {Path: "VAULT", Size: 2}, {Path: "DELETING", Size: 1}, {Path: "packs", Dir: true}}
	files, deleting := LeafEntries(entries)
	if !deleting || len(files) != 2 {
		t.Fatalf("%v %v", files, deleting)
	}
	if PendingPackBytes(files, nil) != int64(len(pack)) {
		t.Fatal("pending")
	}
	r := &dirRemote{}
	got, err := ReadLeaf(context.Background(), r, base, files, t.TempDir(), true, nil)
	if err != nil || len(got.Hashed) != 1 || got.Materials[1].SHA256 != id || string(got.Small["VAULT"]) != "{}" {
		t.Fatalf("%+v %v", got, err)
	}
	// A pack known at the same size and stamp is not downloaded again.
	known := func(rel string, size int64, stamp string) (string, bool) { return id, stamp == "s1" }
	r = &dirRemote{}
	got, _ = ReadLeaf(context.Background(), r, base, files, t.TempDir(), true, known)
	if len(r.fetched) != 1 || len(got.Hashed) != 0 || got.Materials[1].SHA256 != id || PendingPackBytes(files, known) != 0 {
		t.Fatalf("%v %+v", r.fetched, got)
	}
	// Without packs, an unknown pack stays unhashed.
	files[1].Stamp = "s2"
	got, _ = ReadLeaf(context.Background(), &dirRemote{}, base, files, t.TempDir(), false, known)
	if got.Materials[1].SHA256 != "" {
		t.Fatal("pack hashed without being asked")
	}
}
