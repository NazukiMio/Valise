package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"valise/core/valise"
)

const demoActions = `actions:
  - { id: hello, name: 打招呼, explain: "输出 hi", risk: read, argv: [/bin/echo, hi], output: text }
  - { id: token, name: 复制令牌, explain: "复制", risk: read, argv: [/bin/echo, "{\"url\":\"http://127.0.0.1:1/#token=tok-9\"}"], output: secret, secret: { json_field: url, url_param: token } }
  - { id: restart, name: 重启, explain: "重启", risk: manage, argv: [/bin/echo, ok], output: none }
`

func TestAcceptedRiskIsScopedToOneAction(t *testing.T) {
	s, _ := enrollWorld(t)
	s.RiskAccepted = false
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	definition := demoProfile + `actions:
  - { id: configure, name: 配置, explain: "固定配置动作", undo: "恢复备份", risk: config, argv: [/bin/echo, ok], output: none }
`
	if err := os.WriteFile(p, []byte(definition), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunAction(WithRiskAccepted(ctx), "box", "demo", "configure", ActionOptions{})
	if err != nil || res.Exit != 0 {
		t.Fatal(res, err)
	}
	if s.RiskAccepted {
		t.Fatal("single-operation acceptance changed shared service")
	}
	_, err = s.RunAction(ctx, "box", "demo", "configure", ActionOptions{})
	if ue, ok := valise.As(err); !ok || ue.Code != GateCode {
		t.Fatal("acceptance leaked into next action", err)
	}
}

func TestActions(t *testing.T) {
	s, _ := enrollWorld(t)
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	os.WriteFile(p, []byte(demoProfile+demoActions), 0o644)
	ctx := context.Background()
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	list, err := s.Actions(ctx, "box", "demo")
	if err != nil || len(list.Actions) != 3 || list.Stale {
		t.Fatalf("%+v %v", list, err)
	}
	res, err := s.RunAction(ctx, "box", "demo", "hello", ActionOptions{})
	if err != nil || res.Exit != 0 || res.Output != "hi" || res.Checked {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = s.RunAction(ctx, "box", "demo", "restart", ActionOptions{})
	if err != nil || res.Exit != 0 || !res.Checked || res.Output != "" {
		t.Fatalf("%+v %v", res, err)
	}

	res, err = s.RunAction(ctx, "box", "demo", "token", ActionOptions{})
	if err != nil || res.Secret != "tok-9" || res.Output != "" {
		t.Fatalf("%+v %v", res, err)
	}
	// The clearing goroutine and the test both touch the fake clipboard.
	var boardMu sync.Mutex
	var boardValue string
	board := func() string { boardMu.Lock(); defer boardMu.Unlock(); return boardValue }
	clipboardCopy = func(v string) error { boardMu.Lock(); boardValue = v; boardMu.Unlock(); return nil }
	clipboardPaste = func() (string, error) { return board(), nil }
	secretWindow = 50 * time.Millisecond
	t.Cleanup(func() { clipboardCopy, clipboardPaste, secretWindow = systemCopy, systemPaste, SecretWindow })
	clip, err := CopySecret(ctx, res.Secret)
	if err != nil || board() != "tok-9" {
		t.Fatalf("%q %v", board(), err)
	}
	<-clip.Done()
	if board() != "" {
		t.Fatalf("not cleared: %q", board())
	}
	// Something else copied meanwhile is left alone.
	clip, _ = CopySecret(ctx, "tok-9")
	_ = clipboardCopy("user text")
	clip.Clear()
	if board() != "user text" {
		t.Fatalf("cleared the user's clipboard: %q", board())
	}

	// An action only in the local profile: the server refuses it.
	os.WriteFile(p, []byte(strings.Replace(demoProfile, "profile_version: 1", "profile_version: 2", 1)+demoActions+
		"  - { id: newer, name: 新动作, explain: \"x\", risk: read, argv: [/bin/echo, x], output: text }\n"), 0o644)
	if list, _ := s.Actions(ctx, "box", "demo"); !list.Stale {
		t.Fatal("not stale")
	}
	_, err = s.RunAction(ctx, "box", "demo", "newer", ActionOptions{})
	if ue, ok := valise.As(err); !ok || ue.Code != "action_profile_stale" || !strings.Contains(ue.Suggestion, "重新接入") {
		t.Fatalf("%v", err)
	}
}
