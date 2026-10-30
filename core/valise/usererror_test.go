package valise

import (
	"fmt"
	"testing"
)

func TestUserErrorRoundTrip(t *testing.T) {
	e := New("x", "原因", "建议").WithDetail("细节")
	wrapped := fmt.Errorf("ctx: %w", e)
	got, ok := As(wrapped)
	if !ok || got.Detail != "细节" {
		t.Fatalf("As = %v %v", got, ok)
	}
	p, ok := Parse(e.Error())
	if !ok || *p != *e {
		t.Fatalf("Parse = %v %v", p, ok)
	}
	if _, ok := Parse("plain text"); ok {
		t.Fatal("parsed plain text")
	}
}
