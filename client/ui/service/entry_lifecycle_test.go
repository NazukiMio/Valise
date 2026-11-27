package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/login"
	"valise/core/valise"
	"valise/testbed/fakesshenv"
)

func TestEntryLifecycleServiceFakeSSHAndFallback(t *testing.T) {
	// 1. Full porter with entry-lifecycle/1.
	s, home := serviceWorld(t, true, "loaded", "active")
	ctx := context.Background()

	// Replace demo CLI with one supporting check/enable/serve.
	demoBin := filepath.Join(home, ".local/bin/demo")
	demoScript := `#!/bin/sh
case "$1" in
check)
  if [ -f "$HOME/.demo/webapp-enabled" ]; then
    echo "true"
  else
    echo "false"
  fi
  ;;
enable)
  echo "true" > "$HOME/.demo/webapp-enabled"
  ;;
serve)
  sleep 20
  ;;
*)
  echo demo 1.0
  ;;
esac
`
	if err := os.WriteFile(demoBin, []byte(demoScript), 0o700); err != nil {
		t.Fatal(err)
	}

	prof := demoProfile + `service:
  candidates: [ { scope: user, unit: demo.service } ]
entries:
  - id: webapp
    display_name: "Web 工作台"
    kind: web
    host: 127.0.0.1
    port: 9120
    path: "/"
    prepare:
      summary: "启用 Web 工作台"
      check_argv: [demo, check]
      expect_output: "true"
      steps:
        - [demo, enable]
      restart_service: true
    start:
      summary: "启动 Web 工作台"
      argv: [demo, serve]
      wait_seconds: 1
`
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}

	probe, err := s.ProbeEntry(ctx, "box", "demo", "webapp")
	if err != nil {
		t.Fatalf("ProbeEntry: %v", err)
	}
	if !probe.HasPrepare || probe.Prepared || !probe.HasStart || probe.ManagedByUs {
		t.Fatalf("unexpected probe: %+v", probe)
	}

	prep, err := s.PrepareEntry(ctx, "box", "demo", "webapp")
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}
	if prep.StepsRun != 1 || !prep.ServiceRestarted || !prep.Prepared {
		t.Fatalf("unexpected prepare: %+v", prep)
	}

	started, err := s.StartEntry(ctx, "box", "demo", "webapp")
	if err != nil {
		t.Fatalf("StartEntry: %v", err)
	}
	if !started.ManagedByUs || (started.ManagedPID <= 0 && started.ManagedUnit == "") {
		t.Fatalf("unexpected start: %+v", started)
	}
	t.Cleanup(func() {
		_, _ = s.StopEntry(context.Background(), "box", "demo", "webapp")
	})

	stopped, err := s.StopEntry(ctx, "box", "demo", "webapp")
	if err != nil {
		t.Fatalf("StopEntry: %v", err)
	}
	if !stopped.Stopped {
		t.Fatalf("expected stopped: %+v", stopped)
	}

	// 2. Legacy porter without entry-lifecycle/1: ProbeEntry degrades gracefully, StartEntry returns unsupported_capability.
	e := fakesshenv.New(t)
	oldHome := e.AddHost("old")
	legacySrc := `package main
import("bufio";"encoding/json";"fmt";"os")
func main(){
 if len(os.Args)>1&&os.Args[1]=="version"{fmt.Println("{\"name\":\"valise-porter\",\"protocol\":\"1.3\"}");return}
 scan:=bufio.NewScanner(os.Stdin);enc:=json.NewEncoder(os.Stdout)
 for scan.Scan(){
  var req struct{ID json.RawMessage ` + "`json:\"id\"`" + `;Method string ` + "`json:\"method\"`" + `};if json.Unmarshal(scan.Bytes(),&req)!=nil{return}
  var result any
  switch req.Method{
  case "hello":result=map[string]any{"protocol":"1.3","home":os.Getenv("HOME"),"tools":[]any{},"pending_ops":[]any{},"capabilities":[]string{}}
  case "instance_get":result=map[string]any{"record":map[string]any{"instance_id":"demo","profile_id":"demo","roots":map[string]string{"state":"/tmp"}}}
  case "listen_addrs":result=map[string]any{"addrs":[]string{"127.0.0.1"}}
  default:enc.Encode(map[string]any{"jsonrpc":"2.0","id":req.ID,"error":map[string]any{"code":-32601,"message":"not found"}});continue
  }
  enc.Encode(map[string]any{"jsonrpc":"2.0","id":req.ID,"result":result})
 }
}
`
	sOld := newService(t, e.SSH)
	buildInlinePorter(t, oldHome, legacySrc)
	if _, err := sOld.AddServers("old"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sOld.Dirs.Data, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sOld.Dirs.Data, "profiles/demo.yaml"), []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}
	pOld, err := sOld.ProbeEntry(ctx, "old", "demo", "webapp")
	if err != nil {
		t.Fatalf("legacy ProbeEntry failed: %v", err)
	}
	if !pOld.Listening || pOld.EntryID != "webapp" || !pOld.HasStart || !pOld.HasPrepare {
		t.Fatalf("unexpected legacy probe: %+v", pOld)
	}
	_, err = sOld.StartEntry(ctx, "old", "demo", "webapp")
	ue, ok := valise.As(err)
	if !ok || ue.Code != "unsupported_capability" {
		t.Fatalf("expected unsupported_capability for legacy StartEntry, got %v", err)
	}
	_ = login.EntryLifecycleCapability
}
