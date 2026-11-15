//go:build linux

package revive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/profile"
	"valise/porter/internal/activeexec"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

var nativeAttachment = flag.Bool("native-attachment", false, "synthetic private native terminal")
var nativeAttachmentToken = flag.Bool("native-attachment-token", false, "synthetic private token command")
var nativeAttachmentApproval = flag.Bool("native-attachment-approval", false, "synthetic read-only approval command")

func TestNativeAttachmentGuest(t *testing.T) {
	if !*nativeAttachment && !*nativeAttachmentToken && !*nativeAttachmentApproval {
		t.Skip("synthetic native guest only")
	}
	if os.Getppid() != 1 || os.Getenv("HOME") == "" || os.Getenv("SSH_AUTH_SOCK") != "" {
		t.Fatal("native command escaped its private runtime")
	}
	if *nativeAttachmentToken {
		fmt.Fprintln(os.Stdout, "http://127.0.0.1:18391/native#token=fixture-only")
		return
	}
	data, err := os.ReadFile("SOUL.md")
	if err != nil {
		t.Fatal(err)
	}
	if *nativeAttachmentApproval {
		for _, path := range []string{"SOUL.md", ".approval-unwanted", filepath.Join(filepath.Dir(os.Args[0]), "approval-unwanted")} {
			if err := os.WriteFile(path, []byte("must never reach active or programme files"), 0600); err == nil {
				t.Fatal("approval command could write protected material", path)
			}
		}
		if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".approval-ephemeral"), []byte("private temporary home"), 0600); err != nil {
			t.Fatal("approval did not get an ephemeral HOME", err)
		}
		fmt.Fprintln(os.Stderr, "SECRET_APPROVAL_STDERR")
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"data": string(data), "read_only": true, "secret": "SECRET_APPROVAL_OUTPUT"}); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // The fixed JSON check has no testing framework suffix.
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != "native-fixture-input" {
		t.Fatal("native stdin was not passed through")
	}
	fmt.Fprintln(os.Stdout, "NATIVE-DATA:", string(data))
	fmt.Fprintln(os.Stdout, "NATIVE-HOME:", os.Getenv("HOME"))
}

func nativeFixtureDeclarations(p *profile.Profile) {
	base := profile.Argv{"gateway", "-test.run=^TestNativeAttachmentGuest$"}
	p.NativeCLI = append(append(profile.Argv{}, base...), "-native-attachment")
	p.Entries[0].Auth = &profile.EntryAuth{URLArgv: append(append(profile.Argv{}, base...), "-native-attachment-token")}
	p.ApprovalCheck = &profile.ApprovalCheck{Argv: append(append(profile.Argv{}, base...), "-native-attachment-approval"),
		BypassWhen: map[string]string{"data": "probe wrote", "read_only": "true"},
		AskWhen:    []map[string]string{{"data": "current soul", "read_only": "true"}}, DefaultWhen: []map[string]string{{"data": "probe wrote"}}}
}

func assertAttachedNative(t *testing.T, ctx context.Context, w *world, control managedruntime.Controller) {
	t.Helper()
	lock, unlock, err := (seal.Paths{Home: w.home}).ActionLock("")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	before := privateReadFingerprint(t, w.home)
	c := activeexec.Controller{Home: w.home, Runtime: control}
	cmd, _, err := c.Native(ctx, "demo", lock)
	if err != nil {
		t.Fatal("cannot bind current native programme", err)
	}
	var output bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader("native-fixture-input\n"), &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatal("private native execution failed", err, output.String())
	}
	if !strings.Contains(output.String(), "NATIVE-DATA: "+read(t, w.state+"/SOUL.md")) || !strings.Contains(output.String(), "NATIVE-HOME: "+w.home) {
		t.Fatal("native command borrowed another environment", output.String())
	}
	entry := w.p.Entries[0]
	cmd, _, err = c.Entry(ctx, "demo", entry, lock)
	if err != nil {
		t.Fatal("cannot bind current token command", err)
	}
	output.Reset()
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil || !strings.Contains(output.String(), "/native#token=fixture-only") {
		t.Fatal("private token command failed", err, output.String())
	}
	forged := entry
	forged.Auth = &profile.EntryAuth{URLArgv: profile.Argv{"/bin/sh", "-c", "echo not-accepted"}}
	if cmd, _, err := c.Entry(ctx, "demo", forged, lock); err == nil || cmd != nil {
		t.Fatal("client replaced accepted token command")
	}
	if privateReadFingerprint(t, w.home) != before {
		t.Fatal("attached execution rewrote programme, DNS/CA, parent or data journals")
	}
	approval, err := c.Approval(ctx, "demo", lock)
	if err != nil || approval == nil || !approval.Declared {
		t.Fatal("cannot read approval from current programme", approval, err)
	}
	expected := profile.ApprovalAsk
	if read(t, w.state+"/SOUL.md") == "probe wrote" {
		expected = profile.ApprovalBypass
	}
	if approval.Mode != expected || approval.Default != (expected == profile.ApprovalBypass) {
		t.Fatal("approval borrowed a different environment or lost read-only protection", approval, expected)
	}
	encoded, _ := json.Marshal(approval)
	if strings.Contains(string(encoded), "SECRET_") {
		t.Fatal("approval returned raw command output")
	}
	if _, err := os.Stat(filepath.Join(w.home, ".approval-ephemeral")); !os.IsNotExist(err) {
		t.Fatal("approval used persistent HOME", err)
	}
	if privateReadFingerprint(t, w.home) != before {
		t.Fatal("approval rewrote private programme or runtime metadata")
	}
}
