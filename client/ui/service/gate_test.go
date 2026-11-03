package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/valise"
)

// Advanced features need a pod kept elsewhere that restores on its own
// (docs/INSTALL.md 4.2): copies only on the server do not count, and a
// repository copy is trial-decrypted once.
func TestAdvancedGate(t *testing.T) {
	s, _, _, ss := sealedWorld(t)
	ctx := context.Background()
	asked := 0
	s.OwnerAuth.Passphrase = func(string) (string, error) { asked++; return pass, nil }

	err := s.RequireGate(ctx, "box", "")
	if ue, ok := valise.As(err); !ok || ue.Code != GateCode {
		t.Fatalf("pods only on the server must not count: %v", err)
	}
	// Accepting the risk lets one operation through; nothing else changes.
	s.RiskAccepted = true
	if err := s.RequireGate(ctx, "box", ""); err != nil {
		t.Fatalf("accepted risk still refused: %v", err)
	}
	s.RiskAccepted = false
	if _, _, err := s.ShellCommand(ctx, "box", ""); err == nil {
		t.Fatal("terminal opened without an offsite pod")
	}

	dir := filepath.Join(t.TempDir(), "usb")
	if _, err := s.RepoAdd("usb", dir); err != nil {
		t.Fatal(err)
	}
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
	g, err := s.AdvancedGate(ctx, "box", "")
	if err != nil || !g.OK() || len(g.Agents) != 1 || g.Agents[0].Pod.SnapshotID != ss || g.Agents[0].Where != "仓库 usb" {
		t.Fatalf("%+v %v", g, err)
	}
	if g.Warning() != "" || asked != 1 {
		t.Fatalf("fresh pod warned, or passphrase asked %d times", asked)
	}
	s.ForgetOwner()
	if _, err := s.AdvancedGate(ctx, "box", "demo"); err != nil || asked != 1 {
		t.Fatalf("trial decryption repeated: %v, asked %d", err, asked)
	}

	// A damaged repository copy no longer counts.
	packs, _ := filepath.Glob(filepath.Join(dir, "box", "demo", "*", "packs", "*", "*"))
	if len(packs) == 0 {
		t.Fatal("no packs in the repository")
	}
	for _, p := range packs {
		os.WriteFile(p, []byte("damaged"), 0o600)
	}
	os.Remove(filepath.Join(s.Dirs.Data, repoTrialFile))
	if g, err := s.AdvancedGate(ctx, "box", ""); err != nil || g.OK() {
		t.Fatalf("damaged copy counted: %+v %v", g, err)
	}
}

func TestGateWarning(t *testing.T) {
	g := &GateResult{Agents: []GateAgent{{Instance: "a", Pod: nil, Reason: "还没有逃生舱"}}}
	if g.OK() || g.Err() == nil {
		t.Fatal("no pod passed")
	}
	if (&GateResult{}).Err() != nil {
		t.Fatal("a server without agents is not restricted")
	}
}
