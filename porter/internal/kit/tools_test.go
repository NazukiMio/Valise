package kit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"valise/core/valise"
)

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func code(err error) string {
	if ue, ok := valise.As(err); ok {
		return ue.Code
	}
	return ""
}

// Enhanced tools: written only with the expected content, never over a
// file Valise did not put there, removed only while unchanged.
func TestToolPutAndRemove(t *testing.T) {
	home := t.TempDir()
	bin := []byte("#!/bin/sh\necho tool\n")
	if err := ToolPut(home, "tool", sum([]byte("other")), bytes.NewReader(bin)); code(err) != "kit_tool_mismatch" {
		t.Fatalf("wrong content installed: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(home, ".local/bin/.valise-tool-*")); len(left) > 0 {
		t.Fatalf("temporary file left: %v", left)
	}
	if err := ToolPut(home, "../x", sum(bin), bytes.NewReader(bin)); code(err) != "kit_tool_invalid" {
		t.Fatalf("bad name: %v", err)
	}
	if err := ToolPut(home, "tool", sum(bin), bytes.NewReader(bin)); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(home, ".local/bin/tool"))
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("%v %v", st, err)
	}
	// Reinstalling Valise's own unchanged file is allowed.
	if err := ToolBegin(home, "tool", sum(bin)); err != nil {
		t.Fatal(err)
	}
	// Someone else's file is never overwritten.
	os.WriteFile(filepath.Join(home, ".local/bin/mine"), []byte("user's"), 0o755)
	if err := ToolBegin(home, "mine", sum(bin)); code(err) != "kit_tool_foreign" {
		t.Fatalf("foreign file: %v", err)
	}
	if err := ToolRemove(home, "mine"); code(err) != "kit_tool_not_ours" {
		t.Fatalf("removed a foreign file: %v", err)
	}
	// A changed file stays.
	os.WriteFile(filepath.Join(home, ".local/bin/tool"), []byte("edited"), 0o755)
	if err := ToolRemove(home, "tool"); code(err) != "kit_tool_changed" {
		t.Fatalf("removed a changed file: %v", err)
	}
	os.WriteFile(filepath.Join(home, ".local/bin/tool"), bin, 0o755)
	if err := ToolRemove(home, "tool"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local/bin/tool")); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
}
