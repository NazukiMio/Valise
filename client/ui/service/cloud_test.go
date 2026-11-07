package service

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"valise/client/config"
	"valise/client/minisign/minisigntest"
	"valise/client/profiles"
	"valise/core/vault"
)

// The server's fake rclone: config create prints rclone's headless line,
// waits for the pasted result and writes a remote (an alias of a local
// folder); lsjson lists nothing; config delete removes the remote.
const fakeServerRclone = `#!/bin/sh
conf=""; prev=""
for a in "$@"; do [ "$prev" = "--config" ] && conf="$a"; prev="$a"; done
case "$1" in
listremotes) sed -n 's/^\[\(.*\)\]$/\1:/p' "$conf" ;;
config)
  case "$2" in
  create)
    echo 'Execute the following on the machine with the web browser'
    echo '	rclone authorize "drive" "eyJzY29wZSI6ImRyaXZlLmZpbGUifQ"'
    printf 'config_token> '
    read tok
    [ "$tok" = "PASTED" ] || { echo "bad token" >&2; printf '[%s]\ntype = drive\n' "$3" >> "$conf"; exit 1; }
    mkdir -p "$HOME/drive"
    printf '[%s]\ntype = alias\nremote = %s\n' "$3" "$HOME/drive" >> "$conf" ;;
  delete) grep -v "^\[$3\]" "$conf" > "$conf.new"; mv "$conf.new" "$conf" ;;
  esac ;;
lsjson) echo '[]' ;;
esac
`

// This computer's fake rclone prints the result to paste.
const fakeLocalRclone = `#!/bin/sh
case "$1" in
version) echo "rclone v1.75.2" ;;
authorize) echo "Paste the following into your remote machine --->"; echo PASTED ;;
esac
`

func cloudWorld(t *testing.T) (*Service, string) {
	t.Helper()
	s, home, _, _ := sealedWorld(t)
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	kit := filepath.Join(t.TempDir(), "tools.yaml")
	os.WriteFile(kit, []byte(`schema: valise-kit-tools/1
tools:
  - id: rclone
    tier: enhanced
    name: rclone
    purpose: test
    without: nothing
    check: { binary: rclone }
    binary: { version: "1.75.2", archives: [] }
    clouds:
      - { id: gdrive, name: Google Drive, rclone_type: drive, params: { scope: drive.file }, access: folder, access_note: n, loopback: false, verify: pending }
`), 0o644)
	signer := minisigntest.New()
	tgz, err := profiles.Build("../../../profiles", kit, "2026.11.08", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.Release.PublicKey = signer.PublicKey
	if err := s.InstallPack(&PackFile{Data: tgz, Sig: signer.Sign(tgz, "file:"+profiles.PackAsset)}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, ".local/bin/rclone"), []byte(fakeServerRclone), 0o755)
	local := filepath.Join(t.TempDir(), "rclone")
	os.WriteFile(local, []byte(fakeLocalRclone), 0o755)
	old := lookRclone
	lookRclone = func() (string, error) { return local, nil }
	t.Cleanup(func() { lookRclone = old })
	return s, home
}

func TestCloudLoginHeadless(t *testing.T) {
	s, home := cloudWorld(t)
	ctx := context.Background()
	if _, err := s.CloudLoginServer(ctx, "box", "gd", "gdrive", CloudOptions{}, CloudLoginIO{}); err == nil || !strings.Contains(err.Error(), "allow-pending") {
		t.Fatalf("pending cloud used: %v", err)
	}
	r, w := io.Pipe()
	var out bytes.Buffer
	var asked string
	res, err := s.CloudLoginServer(ctx, "box", "gd", "gdrive", CloudOptions{AllowPending: true}, CloudLoginIO{
		LoginIO: LoginIO{Stdin: r, Stdout: &syncWriter{w: &out}},
		ConfirmAuthorize: func(line string) bool {
			asked = line
			// The user pastes what this computer's rclone printed.
			go func() { time.Sleep(500 * time.Millisecond); w.Write([]byte("PASTED\n")) }()
			return true
		},
	})
	if err != nil || !res.Saved || !res.Headless {
		t.Fatalf("%+v %v\n%s", res, err, out.String())
	}
	if asked != `rclone authorize drive eyJzY29wZSI6ImRyaXZlLmZpbGUifQ` {
		t.Fatalf("asked %q", asked)
	}
	st, err := s.VaultStatus(ctx, "box")
	if err != nil || len(st.Targets) != 1 || st.Targets[0].Kind != "rclone" || st.Targets[0].Dest != "valise-gd:valise-vault/" {
		t.Fatalf("%+v %v", st.Targets, err)
	}
	conf, _ := os.ReadFile(filepath.Join(home, ".config/valise/rclone/rclone.conf"))
	if !strings.Contains(string(conf), "[valise-gd]") {
		t.Fatalf("%s", conf)
	}
	if err := s.CloudRemove(ctx, "box", "gd"); err != nil {
		t.Fatal(err)
	}
	conf, _ = os.ReadFile(filepath.Join(home, ".config/valise/rclone/rclone.conf"))
	if st, _ := s.VaultStatus(ctx, "box"); len(st.Targets) != 0 || strings.Contains(string(conf), "valise-gd") {
		t.Fatalf("%+v %s", st.Targets, conf)
	}
}

func TestCloudLoginFailureLeavesNothing(t *testing.T) {
	s, home := cloudWorld(t)
	r, w := io.Pipe()
	_, err := s.CloudLoginServer(context.Background(), "box", "gd", "gdrive", CloudOptions{AllowPending: true}, CloudLoginIO{
		LoginIO: LoginIO{Stdin: r, Stdout: io.Discard},
		ConfirmAuthorize: func(string) bool {
			go func() { time.Sleep(500 * time.Millisecond); w.Write([]byte("WRONG\n")) }()
			return true
		},
	})
	if err == nil {
		t.Fatal("failed login reported as done")
	}
	conf, _ := os.ReadFile(filepath.Join(home, ".config/valise/rclone/rclone.conf"))
	if strings.Contains(string(conf), "valise-gd") {
		t.Fatalf("half a login left: %s", conf)
	}
	if st, _ := s.VaultStatus(context.Background(), "box"); len(st.Targets) != 0 {
		t.Fatal("location saved")
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// A cloud drive logged in on this computer (an alias of a local folder,
// with the real rclone named by VALISE_TEST_RCLONE): relayed copies and
// checks from here.
func TestCloudHereRelay(t *testing.T) {
	bin := os.Getenv("VALISE_TEST_RCLONE")
	if bin == "" {
		t.Skip("VALISE_TEST_RCLONE not set")
	}
	s, _, _, _ := sealedWorld(t)
	ctx := context.Background()
	old := lookRclone
	lookRclone = func() (string, error) { return bin, nil }
	t.Cleanup(func() { lookRclone = old })
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	drive := filepath.Join(t.TempDir(), "drive")
	s.ensureLocalRcloneConfig()
	os.WriteFile(s.LocalRcloneConfig(), []byte("[valise-here]\ntype = alias\nremote = "+drive+"\n"), 0o600)
	s.Config.Clouds = []config.Cloud{{Name: "here", Cloud: "gdrive"}}
	res, err := s.SyncCloud(ctx, "box", "here")
	if err != nil || res.Path != PathTemp || len(res.Leaves) != 1 || len(res.Checks) != 1 || res.Checks[0].State != CopyListed {
		t.Fatalf("%+v %v", res, err)
	}
	leafDir := filepath.Join(drive, "valise-vault", "box", filepath.FromSlash(res.Leaves[0]))
	if probs, err := vault.VerifyNoKey(leafDir); err != nil || len(probs) != 0 {
		t.Fatal(probs, err)
	}
	cr, err := s.VerifyCopies(ctx, "box", res.Leaves[0], VerifyOptions{Packs: true})
	if err != nil || len(cr.Checks) != 2 || cr.Checks[1].Where != "here" || cr.Checks[1].State != CopyComplete {
		t.Fatalf("%+v %v", cr, err)
	}
	if err := s.CloudRemoveHere(ctx, "here"); err != nil || s.IsCloudHere("here") {
		t.Fatal(err)
	}
}
