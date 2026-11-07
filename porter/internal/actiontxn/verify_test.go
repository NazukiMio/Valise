package actiontxn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVerificationSeparatesRemainingFromUnknown(t *testing.T) {
	r, p, rec := fixture(t, "inactive")
	env := r.Env.For(p).With(rec.Env)
	matched, err := verify(context.Background(), env, nil, rec.Roots["state"], p.ApprovalCheck)
	if matched || !errors.Is(err, errRepairRemaining) {
		t.Fatal("known bypass policy lost", matched, err)
	}
	if err := os.WriteFile(filepath.Join(rec.Roots["state"], "config.json"), []byte("invalid json"), 0600); err != nil {
		t.Fatal(err)
	}
	matched, err = verify(context.Background(), env, nil, rec.Roots["state"], p.ApprovalCheck)
	if matched || err != nil {
		t.Fatal("invalid output treated as known policy", matched, err)
	}
}
