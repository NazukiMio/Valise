package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/valise"
	"valise/core/vault"
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

	// A newly sealed time point in the same local repository needs the
	// keyless verification, even after the Valise unlock window was closed.
	next, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoulSecrets})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
	nextDir := filepath.Join(dir, "box", leafDirName(next.Leaf))
	if err := s.RequireGate(ctx, "box", "demo"); err != nil || asked != 1 {
		t.Fatalf("second time point: %v, asked %d", err, asked)
	}
	g, err = s.AdvancedGate(ctx, "box", "demo")
	if err != nil || !g.OK() || g.Agents[0].Pod.SnapshotID != next.SnapshotID || asked != 1 {
		t.Fatalf("latest time point: %+v %v, asked %d", g, err, asked)
	}
	trialPath := filepath.Join(s.Dirs.Data, repoTrialFile)
	trialKey := nextDir + "#" + next.SnapshotID
	if _, ok := readRepoTrials(trialPath)[trialKey]; !ok {
		t.Fatal("new time point was not recorded")
	}

	// A damaged repository copy no longer counts.
	packs, _ := filepath.Glob(filepath.Join(dir, "box", "demo", "*", "packs", "*", "*"))
	if len(packs) == 0 {
		t.Fatal("no packs in the repository")
	}
	for _, p := range packs {
		os.WriteFile(p, []byte("damaged"), 0o600)
	}
	done := readRepoTrials(trialPath)
	delete(done, trialKey)
	b, err := json.Marshal(done)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicFile(trialPath, b); err != nil {
		t.Fatal(err)
	}
	if err := s.trialDecrypt("box", nextDir, next.SnapshotID); err == nil || asked != 1 {
		t.Fatalf("unverified damaged time point: %v, asked %d", err, asked)
	}
	os.Remove(trialPath)
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
	if err := s.call(ctx, "box", vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: "dead", Snapshots: []string{ss}}, nil); err != nil {
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

func TestGateSkipsHangingRemoteTargetWithinTimeout(t *testing.T) {
	s, home, _, ss := sealedWorld(t)
	ctx := context.Background()
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}

	tried := filepath.Join(home, "slow-tried")
	slow := "#!/bin/sh\necho x >> " + tried + "\nsleep 30\n"
	for _, tool := range []string{"rsync", "ssh"} {
		if err := os.WriteFile(filepath.Join(home, ".local/bin", tool), []byte(slow), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Target without LastSynced skips listing completely.
	if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: "unsynced", Kind: "rsync", Dest: "slow:/backup1"}); err != nil {
		t.Fatal(err)
	}
	// Target with LastSynced invokes listing, times out independently without blocking 30s.
	if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: "slownas", Kind: "rsync", Dest: "slow:/backup2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.call(ctx, "box", vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: "slownas", Snapshots: []string{ss}}, nil); err != nil {
		t.Fatal(err)
	}

	if targetGateTimeout != 3*time.Second {
		t.Fatalf("targetGateTimeout = %v, want 3s", targetGateTimeout)
	}
	orig := targetGateTimeout
	targetGateTimeout = 200 * time.Millisecond
	defer func() { targetGateTimeout = orig }()

	start := time.Now()
	g, err := s.AdvancedGate(ctx, "box", "demo")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("AdvancedGate stalled on hanging remote target: %v", elapsed)
	}
	if g.OK() || !strings.Contains(g.Agents[0].Reason, "slownas") || !strings.Contains(g.Agents[0].Reason, "超时") {
		t.Fatalf("expected skipped timeout reason for slownas, got: %+v", g)
	}
	b, _ := os.ReadFile(tried)
	if strings.Count(string(b), "x") != 1 {
		t.Fatalf("expected only synced slownas to attempt listing once (unsynced skipped), got %q", string(b))
	}
}
