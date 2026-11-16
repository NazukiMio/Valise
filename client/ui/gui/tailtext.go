package gui

import (
	"strings"
	"unicode/utf8"
)

// maxFallbackText is how much output the text fallback keeps when the
// terminal could not be loaded: the newest part. The text is drawn whole
// on every frame and grows by small reads, so without a limit a long
// install would copy and lay out all of it again each time.
const maxFallbackText = 64 << 10

// tailText is output kept to its newest maxFallbackText bytes.
type tailText struct {
	s   string
	cut bool // earlier output was dropped
}

// add appends output and drops the oldest beyond the limit, at a line
// start when one is near, never inside a character.
func (t *tailText) add(more string) {
	t.s += more
	if len(t.s) <= maxFallbackText {
		return
	}
	from := len(t.s) - maxFallbackText
	for from < len(t.s) && !utf8.RuneStart(t.s[from]) {
		from++
	}
	if nl := strings.IndexByte(t.s[from:], '\n'); nl >= 0 && nl < 4096 {
		from += nl + 1
	}
	// A fresh copy, so the dropped part's memory is let go.
	t.s, t.cut = strings.Clone(t.s[from:]), true
}
