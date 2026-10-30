package tools

import (
	"context"
	"errors"
	"testing"

	"valise/core/rpc"
)

func TestDetectOrderAndLookup(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "git" {
			return "/usr/bin/git", nil
		}
		return "", errors.New("not found")
	}
	got := Detect(context.Background(), lookPath)
	if len(got) != len(rpc.ToolNames) {
		t.Fatalf("%+v", got)
	}
	for i, tl := range got {
		if tl.Name != rpc.ToolNames[i] {
			t.Errorf("order: %d = %s", i, tl.Name)
		}
		if tl.Available != (tl.Name == "git") {
			t.Errorf("%s available = %v", tl.Name, tl.Available)
		}
	}
}
