package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"valise/core/action"
	"valise/core/environment"
	"valise/core/profile"
	"valise/core/rpc"
)

func TestApprovalNeverFallsBackToOldProgrammeOverFakeSSH(t *testing.T) {
	if !rpc.Classified(action.MethodApproval) || rpc.TierOf(action.MethodApproval) != rpc.TierDaily {
		t.Fatal("fixed read-only check changed permission tier")
	}
	s, home := enrollWorld(t)
	check := filepath.Join(home, ".local/bin/fixture-approval")
	if err := os.WriteFile(check, []byte("#!/bin/sh\nprintf ran > \"$HOME/approval-was-run\"\nprintf '%s\\n' '{\"security\":\"full\",\"extra\":\"SECRET_APPROVAL_OUTPUT\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := demoProfile + "approval_check:\n  argv: ['${HOME}/.local/bin/fixture-approval']\n  bypass_when: { security: full }\n  ask_when: [{security: ask}]\n  default_when: [{security: full}]\n"
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), []byte(p), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	var observed action.ApprovalResult
	// Extra request fields cannot replace the stored command or forge its result.
	params := map[string]any{"instance_id": "demo", "argv": []string{"/bin/echo", `{"security":"ask"}`}}
	if err := s.call(ctx, "box", action.MethodApproval, params, &observed); err != nil || observed.Mode != profile.ApprovalBypass || !observed.Default {
		t.Fatal(observed, err)
	}
	encoded, _ := json.Marshal(observed)
	if strings.Contains(string(encoded), "SECRET_") {
		t.Fatal("check returned unrelated raw output")
	}
	marker := filepath.Join(home, "approval-was-run")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	// A query cannot run the old check while a restore/backup holds the actual
	// cross-process lock, even though approval_check remains a daily method.
	lock, err := os.OpenFile(filepath.Join(home, ".local/state/valise/vault.lock"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	err = s.call(ctx, "box", action.MethodApproval, params, &observed)
	if journeyCode(err) != "vault_busy" {
		t.Fatal("approval ran during another mutation", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("busy query executed host programme", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	settings, err := s.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	r, err := environment.New(settings.Agent)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local/state/valise/environments")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(dir, environment.Key(r.Agent)+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	err = s.call(ctx, "box", action.MethodApproval, params, &observed)
	if journeyCode(err) != "environment_binding_unverified" {
		t.Fatal("unknown current programme borrowed the old check", observed, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unknown current environment executed host programme", err)
	}
}
