package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/client/minisign/minisigntest"
	"valise/client/profiles"
	"valise/core/kittools"
)

// The form answers fixed questions itself, sends the browser step to the
// relay, and asks the person the rest, again when rclone complains.
func TestRunForm(t *testing.T) {
	script := []kittools.Question{
		{State: "a", Name: kittools.QuestionSharedApp},
		{State: "b", Name: kittools.QuestionAdvanced},
		{State: "c", Name: kittools.QuestionIsLocal},
		{State: "d", Name: "config_change_team_drive", Default: "false"},
		{State: "d", Name: "config_change_team_drive", Error: "bad"},
		{State: ""},
	}
	var answers, asked []string
	i := 0
	next := func() *kittools.Question { q := script[i]; i++; return &q }
	step := func(state, result string) (*kittools.Question, error) {
		if state != "" {
			answers = append(answers, state+"="+result)
		}
		return next(), nil
	}
	browser := func(state string) (*kittools.Question, error) {
		answers = append(answers, state+"=browser")
		return next(), nil
	}
	f := CloudForm{Ask: func(q kittools.Question) (string, error) {
		asked = append(asked, q.Name+":"+q.Error)
		return "false", nil
	}}
	if err := runForm(context.Background(), f, step, browser); err != nil {
		t.Fatal(err)
	}
	want := []string{"a=true", "b=false", "c=browser", "d=false", "d=false"}
	if len(answers) != len(want) {
		t.Fatalf("%v", answers)
	}
	for k := range want {
		if answers[k] != want[k] {
			t.Fatalf("%v", answers)
		}
	}
	if len(asked) != 2 || asked[1] != "config_change_team_drive:bad" {
		t.Fatalf("%v", asked)
	}
	// Without a form an unknown question cannot be answered.
	i = 3
	if err := runForm(context.Background(), CloudForm{}, func(string, string) (*kittools.Question, error) { return next(), nil }, browser); err != ErrFormUnavailable {
		t.Fatal(err)
	}
}

func TestWebDAVAndS3FormLogin(t *testing.T) {
	// Occupy loopback port 53682 if free so the test proves non-browser clouds
	// never require loopback OAuth availability.
	if ln, err := net.Listen("tcp", "127.0.0.1:53682"); err == nil {
		defer ln.Close()
	}

	s, home, _, _ := sealedWorld(t)
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	driveDir := filepath.Join(t.TempDir(), "cloud-root")
	os.MkdirAll(filepath.Join(driveDir, "my-bucket", "valise-vault"), 0o700)

	kit := filepath.Join(t.TempDir(), "tools.yaml")
	if err := os.WriteFile(kit, []byte(`schema: valise-kit-tools/1
tools:
  - id: rclone
    tier: enhanced
    name: rclone
    purpose: test
    without: nothing
    check: { binary: rclone }
    binary: { version: "1.75.2", archives: [] }
    clouds:
      - { id: webdav, name: WebDAV, rclone_type: webdav, browser: false, access: account, access_note: n, verify: pending }
      - { id: s3, name: S3, rclone_type: s3, browser: false, needs_bucket: true, access: folder, access_note: n, verify: pending }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	signer := minisigntest.New()
	tgz, err := profiles.Build("../../../profiles", kit, "2026.11.19", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.Release.PublicKey = signer.PublicKey
	if err := s.InstallPack(&PackFile{Data: tgz, Sig: signer.Sign(tgz, "file:"+profiles.PackAsset)}); err != nil {
		t.Fatal(err)
	}

	fakeRclone := `#!/bin/sh
conf=""; prev=""
for a in "$@"; do [ "$prev" = "--config" ] && conf="$a"; prev="$a"; done
case "$1" in
version) echo "rclone v1.75.2" ;;
listremotes) [ -f "$conf" ] && sed -n 's/^\[\(.*\)\]$/\1:/p' "$conf" || true ;;
config)
  case "$2" in
  create)
    for a in "$@"; do case "$a" in config_is_local*) echo "unexpected config_is_local" >&2; exit 2 ;; esac; done
    printf '[%s]\ntype = alias\nremote = %s\n' "$3" "` + driveDir + `" >> "$conf"
    printf '{"State":"s1","Option":{"Name":"url","Help":"Endpoint","Type":"string","Required":true}}\n' ;;
  update)
    printf '{"State":""}\n' ;;
  delete)
    if [ -f "$conf" ]; then grep -v "^\[$3\]" "$conf" > "$conf.new" || true; mv "$conf.new" "$conf"; fi ;;
  esac ;;
lsjson) echo '[]' ;;
esac
`
	os.WriteFile(filepath.Join(home, ".local/bin/rclone"), []byte(fakeRclone), 0o755)
	localRclone := filepath.Join(t.TempDir(), "rclone")
	os.WriteFile(localRclone, []byte(fakeRclone), 0o755)
	oldLook := lookRclone
	lookRclone = func() (string, error) { return localRclone, nil }
	t.Cleanup(func() { lookRclone = oldLook })

	ctx := context.Background()
	if !s.CloudFormReady("webdav") || !s.CloudFormReady("s3") {
		t.Fatal("non-browser clouds should report form ready even when port 53682 is busy")
	}

	// Pending guard blocks without AllowPending.
	if _, err := s.CloudLoginServerForm(ctx, "box", "dav", "webdav", CloudForm{}); err == nil || !strings.Contains(err.Error(), "cloud_pending") {
		t.Fatalf("expected cloud_pending error, got %v", err)
	}
	// WebDAV server form login succeeds with AllowPending and Ask callback.
	res, err := s.CloudLoginServerForm(ctx, "box", "dav", "webdav", CloudForm{
		AllowPending: true,
		Ask:          func(kittools.Question) (string, error) { return "https://dav.example.com", nil },
	})
	if err != nil || !res.Saved {
		t.Fatalf("webdav server form failed: %+v %v", res, err)
	}

	// S3 requires valid bucket and writes bucket-prefixed target destination.
	if _, err := s.CloudLoginServerForm(ctx, "box", "s3b", "s3", CloudForm{
		AllowPending: true,
		Ask:          func(kittools.Question) (string, error) { return "ok", nil },
	}); err == nil || !strings.Contains(err.Error(), "cloud_bucket_required") {
		t.Fatalf("expected bucket required error, got %v", err)
	}
	if _, err := s.CloudLoginServerForm(ctx, "box", "s3b", "s3", CloudForm{
		AllowPending: true,
		Bucket:       "bad/bucket",
		Ask:          func(kittools.Question) (string, error) { return "ok", nil },
	}); err == nil || !strings.Contains(err.Error(), "cloud_bucket_invalid") {
		t.Fatalf("expected bucket invalid error, got %v", err)
	}
	res, err = s.CloudLoginServerForm(ctx, "box", "s3b", "s3", CloudForm{
		AllowPending: true,
		Bucket:       "my-bucket",
		Ask:          func(kittools.Question) (string, error) { return "https://s3.example.com", nil },
	})
	if err != nil || !res.Saved {
		t.Fatalf("s3 server form failed: %+v %v", res, err)
	}
	st, err := s.VaultStatus(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	var s3Dest string
	for _, tg := range st.Targets {
		if tg.Name == "s3b" {
			s3Dest = tg.Dest
		}
	}
	if s3Dest != "valise-s3b:my-bucket/valise-vault/" {
		t.Fatalf("unexpected targets: %+v", st.Targets)
	}

	// Here form login stores Folder on local Cloud config and ParseCopySource uses it.
	if err := s.CloudLoginHereForm(ctx, "s3here", "s3", CloudForm{
		AllowPending: true,
		Bucket:       "my-bucket",
		Ask:          func(kittools.Question) (string, error) { return "ok", nil },
	}); err != nil {
		t.Fatalf("s3 here form failed: %v", err)
	}
	if s.CloudBucketOf("s3here") != "my-bucket" || !strings.Contains(s.Config.Clouds[len(s.Config.Clouds)-1].Folder, "my-bucket/valise-vault/") {
		t.Fatalf("unexpected local clouds: %+v", s.Config.Clouds)
	}
	if c, err := config.Load(s.Dirs.Data); err != nil || len(c.Clouds) != 1 || c.Clouds[0].Folder != "my-bucket/valise-vault/" {
		t.Fatalf("saved config.json clouds: %+v %v", c.Clouds, err)
	}
	src, err := s.ParseCopySource("box", "cloud:s3here")
	if err != nil || src.cloudBase() != "valise-s3here:my-bucket/valise-vault/box" {
		t.Fatalf("ParseCopySource cloudBase = %q, err = %v", src.cloudBase(), err)
	}
}
