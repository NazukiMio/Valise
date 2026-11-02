package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/client/minisign/minisigntest"
	"valise/client/profiles"
)

// An enhanced Kit tool: downloaded from the signed list's address, checked,
// written to ~/.local/bin by porter, removed again (docs/AGENT_KIT.md 8.4.1).
func TestKitBinaryTool(t *testing.T) {
	s, home, _, _ := sealedWorld(t)
	ctx := context.Background()
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }

	bin := []byte("#!/bin/sh\necho demotool 1.0\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "demotool-1.0/demotool", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg})
	tw.Write(bin)
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive) }))
	defer srv.Close()

	kit := filepath.Join(t.TempDir(), "tools.yaml")
	os.WriteFile(kit, []byte(`schema: valise-kit-tools/1
tools:
  - id: demotool
    tier: enhanced
    name: demotool
    purpose: test
    without: nothing
    check: { binary: demotool }
    binary:
      version: "1.0"
      archives:
        - { arch: amd64, url: "`+srv.URL+`/demotool-1.0.tar.gz", sha256: "`+hex.EncodeToString(sum[:])+`", member: "demotool-1.0/demotool" }
        - { arch: arm64, url: "`+srv.URL+`/demotool-1.0.tar.gz", sha256: "`+hex.EncodeToString(sum[:])+`", member: "demotool-1.0/demotool" }
`), 0o644)
	signer := minisigntest.New()
	tgz, err := profiles.Build("../../../profiles", kit, "2026.11.02", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.Release.PublicKey, s.Release.HTTP = signer.PublicKey, srv.Client()
	if err := s.InstallPack(&PackFile{Data: tgz, Sig: signer.Sign(tgz, "file:"+profiles.PackAsset)}); err != nil {
		t.Fatal(err)
	}

	list, err := s.KitTools(ctx, "box")
	if err != nil || len(list) != 1 || list[0].Present || list[0].Why != "" {
		t.Fatalf("%+v %v", list, err)
	}
	k, err := s.InstallKitBinary(ctx, "box", "demotool")
	if err != nil || !k.Present || !k.Ours {
		t.Fatalf("%+v %v", k, err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".local/bin/demotool")); !bytes.Equal(got, bin) {
		t.Fatalf("installed %q", got)
	}
	if err := s.RemoveKitBinary(ctx, "box", "demotool"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local/bin/demotool")); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
}
