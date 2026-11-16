package gui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"valise/ui/service"
)

// The text fallback keeps the newest output only: bounded however much is
// printed, cut at a line start, never inside a character.
func TestTailTextKeepsTheNewestOutput(t *testing.T) {
	var tt tailText
	tt.add("short\n")
	if tt.s != "short\n" || tt.cut {
		t.Fatalf("short output changed: %q cut %v", tt.s, tt.cut)
	}
	tt = tailText{}
	line := 0
	for tt.s == "" || line < 20000 { // about 400 KiB, in small reads
		line++
		tt.add("第 " + itoa(line) + " 行 line\n")
		if len(tt.s) > maxFallbackText {
			t.Fatalf("kept %d bytes after line %d", len(tt.s), line)
		}
	}
	if !tt.cut || len(tt.s) < maxFallbackText-4096-64 {
		t.Fatalf("cut %v, kept %d bytes", tt.cut, len(tt.s))
	}
	if !utf8.ValidString(tt.s) || !strings.HasPrefix(tt.s, "第 ") || !strings.HasSuffix(tt.s, "第 "+itoa(line)+" 行 line\n") {
		t.Fatalf("not whole newest lines: %q … %q", tt.s[:20], tt.s[len(tt.s)-30:])
	}
	// One long line with no line break is still cut at a character.
	tt = tailText{}
	tt.add(strings.Repeat("字", maxFallbackText))
	if len(tt.s) > maxFallbackText || !utf8.ValidString(tt.s) || !tt.cut {
		t.Fatalf("long line: %d bytes, valid %v", len(tt.s), utf8.ValidString(tt.s))
	}
	// One read larger than the limit keeps its tail.
	tt = tailText{}
	tt.add(strings.Repeat("a", 3*maxFallbackText) + "\nend\n")
	if len(tt.s) > maxFallbackText || !strings.HasSuffix(tt.s, "\nend\n") {
		t.Fatalf("large read: %d bytes", len(tt.s))
	}
}

// The install screen says when earlier output was dropped.
func TestInstallTextFallbackSaysWhenCut(t *testing.T) {
	a, tt := world(t)
	ir := &installRun{plan: &service.InstallPlan{Host: "home", ProfileID: "openclaw"}, conn: newInstallConn(), running: true}
	a.installs["home/openclaw"] = ir
	ir.text.add("first line\n")
	a.router.Push("/servers/home/install/openclaw")
	tt.Frame()
	note := T("install.text_cut", "n", maxFallbackText>>10)
	if !tt.HasText("first line") || tt.HasText(note) {
		t.Fatalf("short output: %q", tt.Texts())
	}
	ir.text.add(strings.Repeat("x", maxFallbackText) + "\nlast line\n")
	tt.Frame()
	if !tt.HasText(note) || !tt.HasText("last line") || tt.HasText("first line") {
		t.Fatalf("cut output is not said: %q", tt.Texts())
	}
}
