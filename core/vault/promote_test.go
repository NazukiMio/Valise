package vault

import (
	"strings"
	"testing"
)

func TestNormalizeSavepointNote(t *testing.T) {
	valid := strings.Repeat("旅", 140)
	if got, err := NormalizeNote(" " + valid + " "); err != nil || got != valid {
		t.Fatalf("valid note: %v", err)
	}
	for _, note := range []string{"first\nnext", "tab\tline", "\rstart", "x\u2028y", valid + "程"} {
		if _, err := NormalizeNote(note); err == nil {
			t.Errorf("malformed note accepted: %q", note)
		}
	}
}
