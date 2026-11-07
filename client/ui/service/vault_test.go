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
	// Progress arrives as porter notifications while the seal runs.
	var stages []string
	var packDone, packTotal int64
	pctx := WithProgress(ctx, func(p Progress) {
		if len(stages) == 0 || stages[len(stages)-1] != p.Stage {
			stages = append(stages, p.Stage)
		}
		if p.Stage == StageSealPack {
			packDone, packTotal = p.Done, p.Total
		}
	})
	rep2, err := s.Seal(pctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoulSecrets})
	if strings.Join(stages, ",") != StageSealScan+","+StageSealPack+","+StageSealCommit || packTotal == 0 || packDone != packTotal {
		t.Fatalf("progress %v %d/%d", stages, packDone, packTotal)
	}
	if err != nil || rep2.Categories["secrets"].Files != 1 {
		t.Fatalf("%+v %v", rep2, err)
	}
	st, err := s.VaultStatus(ctx, "box")
	if err != nil || len(st.Snapshots) != 2 || len(st.Leaves) != 1 || st.LeafOf(rep.SnapshotID) != rep.Leaf || !st.Leaves[0].Current {
		t.Fatal(st, err)
	}
	// Period setting, server name and server-side deletion over RPC.
	if err := s.PeriodSet(ctx, "box", "demo", vault.PeriodSetting{Unit: vault.UnitWeek, TZ: "Asia/Shanghai"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ServerNameSet(ctx, "box", "Bad Name"); err == nil {
		t.Fatal("bad server name accepted")
	}
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LeafDelete(ctx, "box", rep.Leaf); err == nil {
		t.Fatal("deleted the current period")
	} else {
		wantCode(t, err, "leaf_current")
	}
	if cr, err := s.VerifyCopies(ctx, "box", rep.Leaf, VerifyOptions{Packs: true}); err != nil || len(cr.Checks) != 1 || cr.Checks[0].State != CopyComplete {
		t.Fatal(cr, err)
	}
	if res, err := s.VaultVerifyRemote(ctx, "box"); err != nil || len(res.Problems) != 0 {
		t.Fatal(res, err)
	}

	dir := filepath.Join(t.TempDir(), "local")
	pr, err := s.Pull(ctx, "box", rep.Leaf, dir)
	if err != nil || pr.Downloaded == 0 || len(pr.Problems) != 0 {
		t.Fatalf("%+v %v", pr, err)
	}
	if pr, err := s.Pull(ctx, "box", rep.Leaf, dir); err != nil || pr.Downloaded != 0 || pr.Present == 0 {
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
