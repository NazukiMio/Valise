package service

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/revive"
	"valise/core/vaultrpc"
)

func environmentServiceWorld(t *testing.T) (*Service, string) {
	t.Helper()
	s, home := enrollWorld(t)
	ctx := context.Background()
	text := strings.Replace(demoProfile, "provenance: { kind: binary, binary: { name: demo } }", `provenance: { kind: npm_global, npm: { package: fixture, roots: ["${STATE_DIR}/tools/npm/lib/node_modules"] } }
body:
  paths: [{root: state, glob: "tools/**"}, {root: state, glob: "bin/**"}]
entries: [{id: gateway, kind: web, host: 127.0.0.1, port: 18391, path: /}]
health:
  checks: [{kind: http, entry: gateway, expect_status: [200]}]
environments:
  engine: linux-bwrap
  start_argv: ["${STATE_DIR}/bin/gateway"]
  working_dir: "${STATE_DIR}"
  path: ["${HOME}/.demo/bin"]
  program_paths: [{root: state, glob: bin}, {root: state, glob: tools}]
  gateway_entry: gateway
  verification: {status: untested}`, 1)
	files := map[string]string{
		".demo/tools/npm/lib/node_modules/fixture/package.json": `{"name":"fixture","version":"1.2.3"}`,
		".demo/bin/gateway": "private fixture program",
		".local/bin/npm":    "#!/bin/sh\ncase \"$1\" in\nconfig) echo https://registry.example.test/ ;;\nview) echo sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64)) + " ;;\n*) exit 1 ;;\nesac\n",
	}
	for rel, body := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "box", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	return s, home
}

func TestSelectedEnvironmentTestKeepsPassedFileChecksWhenIdentityUnknown(t *testing.T) {
	s, home := environmentServiceWorld(t)
	ctx := context.Background()
	sealed, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	page, err := s.ListTimeline(ctx, JourneyListRequest{AllLocations: true, Limit: 100})
	if err != nil || len(page.Items) != 1 || page.Items[0].SnapshotRef.SnapshotID != sealed.SnapshotID {
		t.Fatal("missing exact selected backup", err)
	}
	point := page.Items[0]
	sources := s.RestoreSources(point)
	if len(sources) != 1 {
		t.Fatal("unexpected sources", sources)
	}
	result, err := s.RunSelectedRestoreTest(ctx, SelectedRestoreTest{Depth: "isolated", Target: "box", Instance: "demo", Snapshot: point.SnapshotRef, Source: sources[0]})
	if err != nil || result == nil || result.OK || result.State != "failed" || !result.LocalVerified || !result.ServerVerified || result.Cleanup != "removed" || result.Depth != "isolated" || !strings.Contains(result.Problem, "无法核实这个备份") {
		t.Fatalf("complete test did not preserve independent file evidence: %+v %v", result, err)
	}
	if readFile(t, filepath.Join(home, ".demo/memory/a.md")) != "m" {
		t.Fatal("isolated test changed live Agent")
	}
	o, err := s.ownerPorter(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	var journal revive.Journal
	if err := s.callOwner(ctx, o, nil, revive.MethodJournal, revive.OpParams{OpID: result.OpID}, &journal); err != nil || !journal.Plan.TestOnly || journal.Plan.IsolatedTest == nil || journal.State != revive.StateRolledBack {
		t.Fatal("test lost frozen input or did not clean", journal.State, err)
	}
}

func TestSelectedEnvironmentTestNeverDowngradesUnsupportedSnapshot(t *testing.T) {
	s, req, _ := selectedTestWorld(t, false)
	req.Depth = "isolated"
	if result, err := s.RunSelectedRestoreTest(context.Background(), req); err == nil || result != nil {
		t.Fatal("unsupported snapshot silently passed a file-only test", result, err)
	}
	if _, ok := s.LastRestoreTest(req.Target, req.Instance); ok {
		t.Fatal("unsupported request fabricated a test result")
	}
}
