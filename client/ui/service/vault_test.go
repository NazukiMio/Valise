package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

func TestSealPullVerifyOverFakeSSH(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	_, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	wantCode(t, err, "instance_not_enrolled")
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	wantCode(t, err, "no_recipients")

	reg, info, err := s.KeyRegister(ctx, "box", "")
	if err != nil || !reg.Added || reg.Fingerprints[0] != info.Fingerprint {
		t.Fatal(reg, err)
	}
	if reg, _, _ := s.KeyRegister(ctx, "box", ""); reg.Added {
		t.Fatal("registered twice")
	}
	rep, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul, Reason: "test"})
	if err != nil || rep.Categories["secrets"].Files != 0 || rep.Categories["memory"].Files != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	rep2, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoulSecrets})
	if err != nil || rep2.Categories["secrets"].Files != 1 {
		t.Fatalf("%+v %v", rep2, err)
	}
	st, err := s.VaultStatus(ctx, "box")
	if err != nil || len(st.Snapshots) != 2 {
		t.Fatal(st, err)
	}
	if res, err := s.VaultVerifyRemote(ctx, "box"); err != nil || len(res.Problems) != 0 {
		t.Fatal(res, err)
	}

	dir := filepath.Join(t.TempDir(), "local")
	pr, err := s.Pull(ctx, "box", dir)
	if err != nil || pr.Downloaded == 0 || len(pr.Problems) != 0 {
		t.Fatalf("%+v %v", pr, err)
	}
	if pr, err := s.Pull(ctx, "box", dir); err != nil || pr.Downloaded != 0 || pr.Present == 0 {
		t.Fatalf("second pull: %+v %v", pr, err)
	}
	p := pass
	res, err := s.VerifyLocal(dir, &p)
	if err != nil || !res.WithKey || res.Files < 3 || len(res.Problems) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// Tampering with the local copy is detected with and without the key.
	filepath.Walk(filepath.Join(dir, "packs"), func(path string, fi os.FileInfo, err error) error {
		if strings.HasSuffix(path, ".pack") {
			b, _ := os.ReadFile(path)
			b[len(b)-1] ^= 1
			os.WriteFile(path, b, 0o600)
		}
		return nil
	})
	if res, _ := s.VerifyLocal(dir, nil); len(res.Problems) == 0 {
		t.Fatal("tamper not detected without key")
	}
	if res, _ := s.VerifyLocal(dir, &p); len(res.Problems) < 2 {
		t.Fatalf("tamper not detected with key: %+v", res.Problems)
	}
}
