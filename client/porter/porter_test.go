package porter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/crypto/blake2b"

	"valise/client/ssh"
	"valise/core/valise"
	"valise/testbed/fakesshenv"
)

func transport(t *testing.T, e *fakesshenv.Env, host string) *ssh.Transport {
	t.Helper()
	tr, err := ssh.New(host, ssh.Options{Binary: e.SSH, ControlDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

var hostPlatform = Platform{runtime.GOOS, runtime.GOARCH}

func wantCode(t *testing.T, err error, code string) *valise.UserError {
	t.Helper()
	ue, ok := valise.As(err)
	if !ok || ue.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	return ue
}

func TestProbeChainOrder(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	ctx := context.Background()
	tr := transport(t, e, "box")

	_, err := Probe(ctx, tr)
	wantCode(t, err, "porter_not_found")

	second := e.InstallPorterAt("box", ".local/lib/valise/valise-porter")
	f, err := Probe(ctx, tr)
	if err != nil || f.Path != second {
		t.Fatalf("%+v %v", f, err)
	}
	first := e.InstallPorter("box")
	f, err = Probe(ctx, tr)
	if err != nil || f.Path != first || f.Path != filepath.Join(home, ".local/bin/valise-porter") {
		t.Fatalf("%+v %v", f, err)
	}
	if f.Info.OS != runtime.GOOS || len(f.Info.SelfSHA256) != 64 {
		t.Fatalf("%+v", f.Info)
	}
}

func TestProbeBareCommand(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	dir := t.TempDir()
	data, _ := os.ReadFile(e.Porter())
	os.WriteFile(filepath.Join(dir, "valise-porter"), data, 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	f, err := Probe(context.Background(), transport(t, e, "box"))
	if err != nil || f.Path != BareCommand {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestProbeBrokenPorter(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	p := filepath.Join(home, ".local/bin/valise-porter")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	_, err := Probe(context.Background(), transport(t, e, "box"))
	wantCode(t, err, "porter_unusable")
}

func TestUploadLocal(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	ctx := context.Background()
	tr := transport(t, e, "box")
	p, err := RemotePlatform(ctx, tr)
	if err != nil || p != hostPlatform {
		t.Fatalf("%v %v", p, err)
	}
	bin, _ := os.ReadFile(e.Porter())
	f, err := Upload(ctx, tr, bin, p)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != filepath.Join(home, ".local/bin/valise-porter") {
		t.Fatalf("%+v", f)
	}
	st, _ := os.Stat(f.Path)
	if st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", st.Mode())
	}
	sum := sha256.Sum256(bin)
	if f.Info.SelfSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("hash mismatch")
	}
}

func TestUploadRejectsWrongBinary(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	old := e.InstallPorter("box")
	before, _ := os.ReadFile(old)
	ctx := context.Background()
	tr := transport(t, e, "box")

	_, err := Upload(ctx, tr, []byte("#!/bin/sh\necho nope\n"), hostPlatform)
	wantCode(t, err, "porter_wrong_binary")
	bin, _ := os.ReadFile(e.Porter())
	_, err = Upload(ctx, tr, bin, Platform{"linux", "riscv64"})
	wantCode(t, err, "porter_wrong_binary")

	after, _ := os.ReadFile(filepath.Join(home, ".local/bin/valise-porter"))
	if string(after) != string(before) {
		t.Fatal("existing porter was replaced after a failed deploy")
	}
}

func TestUploadConnectFailure(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")
	_, err := Upload(context.Background(), transport(t, e, "box"), []byte("x"), hostPlatform)
	wantCode(t, err, "ssh_connect_failed")
}

// releaseServer serves signed release assets for tag v9.9.9.
type releaseServer struct {
	assets map[string][]byte
	pub    string
	priv   ed25519.PrivateKey
	keyID  [8]byte
}

func newReleaseServer(t *testing.T, binary []byte, p Platform) (*releaseServer, *httptest.Server) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	rs := &releaseServer{priv: priv, assets: map[string][]byte{}}
	rand.Read(rs.keyID[:])
	rs.pub = base64.StdEncoding.EncodeToString(append(append([]byte("Ed"), rs.keyID[:]...), pub...))
	sum := sha256.Sum256(binary)
	rs.assets[AssetName(p)] = binary
	rs.setSums([]byte(hex.EncodeToString(sum[:]) + "  " + AssetName(p) + "\n"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/v9.9.9/")
		b, found := rs.assets[name]
		if !ok || !found {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return rs, srv
}

func (rs *releaseServer) setSums(sums []byte) {
	h := blake2b.Sum512(sums)
	sig := ed25519.Sign(rs.priv, h[:])
	trusted := "timestamp:0 file:SHA256SUMS"
	global := ed25519.Sign(rs.priv, append(sig, trusted...))
	rs.assets[SumsAsset] = sums
	rs.assets[SigAsset] = []byte("untrusted comment: sig\n" +
		base64.StdEncoding.EncodeToString(append(append([]byte("ED"), rs.keyID[:]...), sig...)) + "\n" +
		"trusted comment: " + trusted + "\n" + base64.StdEncoding.EncodeToString(global) + "\n")
}

func TestReleaseFetch(t *testing.T) {
	p := Platform{"linux", "arm64"}
	bin := []byte("porter binary bytes")
	rs, srv := newReleaseServer(t, bin, p)
	r := Release{HTTP: srv.Client(), BaseURL: srv.URL, PublicKey: rs.pub}
	ctx := context.Background()

	got, err := r.Fetch(ctx, "v9.9.9", p)
	if err != nil || string(got) != string(bin) {
		t.Fatalf("%q %v", got, err)
	}
	_, err = r.Fetch(ctx, "v9.9.9", Platform{"linux", "amd64"})
	wantCode(t, err, "release_asset_missing")
	_, err = r.Fetch(ctx, "v1.0.0", p)
	wantCode(t, err, "release_download_failed")
	_, err = r.Fetch(ctx, "v9.9.9", Platform{"darwin", "arm64"})
	wantCode(t, err, "release_unsupported_platform")

	// Binary swapped on the server: hash check fails.
	rs.assets[AssetName(p)] = []byte("evil")
	_, err = r.Fetch(ctx, "v9.9.9", p)
	wantCode(t, err, "release_hash_mismatch")

	// SHA256SUMS rewritten to match the evil binary but not re-signed.
	evil := sha256.Sum256([]byte("evil"))
	good := rs.assets[SigAsset]
	rs.setSums([]byte(hex.EncodeToString(evil[:]) + "  " + AssetName(p) + "\n"))
	rs.assets[SigAsset] = good
	_, err = r.Fetch(ctx, "v9.9.9", p)
	wantCode(t, err, "release_signature_invalid")
}

func TestReleaseRequiresKey(t *testing.T) {
	_, err := Release{}.Fetch(context.Background(), "v1", Platform{"linux", "amd64"})
	wantCode(t, err, "release_key_missing")
}

func TestLookupSum(t *testing.T) {
	h := strings.Repeat("ab", 32)
	sums := []byte(h + "  a\n" + strings.ToUpper(h) + " *b\nshort  c\n")
	if v, ok := lookupSum(sums, "a"); !ok || v != h {
		t.Fatal("a")
	}
	if v, ok := lookupSum(sums, "b"); !ok || v != h {
		t.Fatal("b")
	}
	if _, ok := lookupSum(sums, "c"); ok {
		t.Fatal("c")
	}
}

// oldPorter is a stand-in for a porter release that predates capability
// declarations: it answers version --json with its own hash and no
// capabilities.
func oldPorter() []byte {
	return []byte(`#!/bin/sh
if command -v sha256sum >/dev/null 2>&1; then h=$(sha256sum "$0" | cut -d' ' -f1); else h=$(shasum -a 256 "$0" | cut -d' ' -f1); fi
printf '{"name":"valise-porter","version":"0.0.1","protocol":"1.3","os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","self_sha256":"%s"}\n' "$h"
`)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stagedLeft(t *testing.T, home string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(home, ".local/bin/valise-porter.tmp*"))
	return m
}

func TestUploadRefusesDowngradeOnceAdopted(t *testing.T) {
	for name, rel := range map[string]string{
		"accepted plan":       ".config/valise/protection/agent.json",
		"pending transaction": ".local/state/valise/protection-changes/" + strings.Repeat("a", 32) + ".json",
	} {
		t.Run(name, func(t *testing.T) {
			e := fakesshenv.New(t)
			home := e.AddHost("box")
			installed := e.InstallPorter("box")
			before, _ := os.ReadFile(installed)
			writeFile(t, filepath.Join(home, rel), "{}")
			_, err := Upload(context.Background(), transport(t, e, "box"), oldPorter(), hostPlatform)
			wantCode(t, err, "porter_downgrade_refused")
			after, _ := os.ReadFile(installed)
			if string(after) != string(before) {
				t.Fatal("installed porter replaced by a porter without the protection capability")
			}
			if left := stagedLeft(t, home); len(left) != 0 {
				t.Fatalf("staged file left: %v", left)
			}
		})
	}
}

func TestUploadOldPorterAllowedWithoutPlans(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
	f, err := Upload(context.Background(), transport(t, e, "box"), oldPorter(), hostPlatform)
	if err != nil {
		t.Fatal(err)
	}
	if f.Supports(ProtectionCapability) || f.Path != filepath.Join(home, ".local/bin/valise-porter") {
		t.Fatalf("%+v", f)
	}
}

func TestUploadCapablePorterWithPlan(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	writeFile(t, filepath.Join(home, ".config/valise/protection/agent.json"), "{}")
	bin, _ := os.ReadFile(e.Porter())
	f, err := Upload(context.Background(), transport(t, e, "box"), bin, hostPlatform)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Supports(ProtectionCapability) {
		t.Fatalf("built porter does not declare the capability: %+v", f.Info)
	}
	if left := stagedLeft(t, home); len(left) != 0 {
		t.Fatalf("staged file left: %v", left)
	}
}

// A protection transaction holding store.lock blocks publication; the check
// and the mv never run outside that lock.
func TestUploadWaitsForProtectionLock(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	installed := e.InstallPorter("box")
	before, _ := os.ReadFile(installed)
	lock := filepath.Join(home, ".local/state/valise/protection-changes/store.lock")
	writeFile(t, lock, "")
	f, err := os.OpenFile(lock, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	bin, _ := os.ReadFile(e.Porter())
	_, err = Upload(context.Background(), transport(t, e, "box"), bin, hostPlatform)
	wantCode(t, err, "resource_busy")
	after, _ := os.ReadFile(installed)
	if string(after) != string(before) {
		t.Fatal("porter replaced while the protection lock was held")
	}
	if left := stagedLeft(t, home); len(left) != 0 {
		t.Fatalf("staged file left: %v", left)
	}
}
