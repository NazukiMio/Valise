package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/valise"
	"valise/core/vaultrpc"
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
	g := &GateResult{Agents: []GateAgent{{Instance: "a", Pod: nil, Reason: "还没有备份"}}}
	if g.OK() || g.Err() == nil {
		t.Fatal("no pod passed")
	}
	if (&GateResult{}).Err() != nil {
		t.Fatal("a server without agents is not restricted")
	}
}

// A repository on this computer that holds the pod settles the
// precondition by itself: the server's copy locations are not listed, so a
// slow or dead one does not hold up the native interface. Without a pod
// here they are asked, and a dead one is said in the reason.
func TestGateDoesNotAskCopyLocationsWhenARepositoryHasThePod(t *testing.T) {
	s, home, _, ss := sealedWorld(t)
	ctx := context.Background()
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	// A copy location that cannot be reached: listing it fails, and leaves
	// a mark that it was tried.
	tried := filepath.Join(home, "listing-tried")
	fake := "#!/bin/sh\necho x >> " + tried + "\necho 'ssh: connect to host dead port 22: Connection timed out' >&2\nexit 255\n"
	for _, tool := range []string{"rsync", "ssh"} {
		if err := os.WriteFile(filepath.Join(home, ".local/bin", tool), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: "dead", Kind: "rsync", Dest: "dead:/backup"}); err != nil {
		t.Fatal(err)
	}
	// Nothing here yet: the copy location is asked, fails, and is named.
	g, err := s.AdvancedGate(ctx, "box", "")
	if err != nil || g.OK() || !strings.Contains(g.Agents[0].Reason, "dead") {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := os.Stat(tried); err != nil {
		t.Fatal("without a pod here the copy location must be asked")
	}
	os.Remove(tried)

	dir := filepath.Join(t.TempDir(), "usb")
	if _, err := s.RepoAdd("usb", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
	os.Remove(tried) // syncing may have looked at locations; the gate is what is measured
	g, err = s.AdvancedGate(ctx, "box", "")
	if err != nil || !g.OK() || g.Agents[0].Pod.SnapshotID != ss || g.Agents[0].Where != "仓库 usb" || g.Warning() != "" {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := os.Stat(tried); err == nil {
		t.Fatal("the dead copy location was asked although the repository has the pod")
	}
	if err := s.RequireGate(ctx, "box", "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tried); err == nil {
		t.Fatal("RequireGate asked the dead copy location")
	}
	// An old pod here still settles it at once; the warning speaks of the
	// repository that was checked and claims nothing about other places.
	old := staleAfter
	staleAfter = 0
	defer func() { staleAfter = old }()
	g, err = s.AdvancedGate(ctx, "box", "")
	if err != nil || !g.OK() || !g.Agents[0].Stale {
		t.Fatalf("%+v %v", g, err)
	}
	if w := g.Warning(); !strings.Contains(w, "仓库 usb") || strings.Contains(w, "最新") {
		t.Fatalf("warning: %q", w)
	}
	if _, err := os.Stat(tried); err == nil {
		t.Fatal("an old pod in the repository made the gate ask the dead copy location")
	}
}
