package seal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/vault"
	"valise/core/vaultrpc"
)

func syncedCloud(t *testing.T, w *world) (cloud, leaf string) {
	t.Helper()
	first := w.seal(t, vault.TierSoul)
	w.seal(t, vault.TierSoul)
	cloud = filepath.Join(t.TempDir(), "cloud")
	if err := SetServerName(w.p, "box"); err != nil {
		t.Fatal(err)
	}
	if err := SetTarget(w.p, vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "nas:" + cloud + "/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), w.env, w.p, "nas", "client"); err != nil {
		t.Fatal(err)
	}
	return cloud, first.Leaf
}

func problems(ps []vault.Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.String() + "\n")
	}
	return b.String()
}

func TestTargetVerifyBySFTPDownload(t *testing.T) {
	w := newWorld(t)
	fakeSFTP(t, w)
	cloud, leaf := syncedCloud(t, w)
	ctx := context.Background()
	basis, err := LeafMaterials(w.p, leaf, true)
	if err != nil || !basis.Exists || len(basis.Integrity) != 0 || !basis.Current {
		t.Fatalf("%+v %v", basis, err)
	}
	q := vaultrpc.TargetVerifyParams{Target: "nas", Leaf: leaf}
	// The list and small files only: pack contents are pending.
	res, err := TargetVerify(ctx, w.env, w.p, q)
	if err != nil || res.PendingBytes == 0 || !strings.Contains(problems(res.Integrity), "内容未核对") {
		t.Fatalf("%+v %v", res, err)
	}
	if d := vault.CompareMaterials(basis.Materials, res.Materials); !d.OK() {
		t.Fatalf("%+v", d)
	}
	q.Packs = true
	res, err = TargetVerify(ctx, w.env, w.p, q)
	if err != nil || len(res.Integrity) != 0 || len(res.Hashed) == 0 || res.PendingBytes != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// Packs hashed before are not downloaded again.
	q.Known = res.Materials
	again, err := TargetVerify(ctx, w.env, w.p, q)
	if err != nil || len(again.Hashed) != 0 || len(again.Integrity) != 0 || again.FetchedBytes >= res.FetchedBytes {
		t.Fatalf("%+v %v", again, err)
	}
	// Same snapshot count, a pack gone and a meta changed: both named.
	leafDir := filepath.Join(cloud, "box", leaf)
	var pack string
	for _, m := range basis.Materials {
		if vault.IsPackPath(m.Path) {
			pack = m.Path
		}
	}
	os.Remove(filepath.Join(leafDir, filepath.FromSlash(pack)))
	meta := vault.MetaPath(vault.Snapshots(basis.Materials)[0])
	b, _ := os.ReadFile(filepath.Join(leafDir, meta))
	os.WriteFile(filepath.Join(leafDir, meta), append(b[:len(b)-2], ' ', '}'), 0o600)
	q.Known = nil
	res, err = TargetVerify(ctx, w.env, w.p, q)
	if err != nil {
		t.Fatal(err)
	}
	d := vault.CompareMaterials(basis.Materials, res.Materials)
	if len(d.Missing) != 1 || d.Missing[0] != pack || len(d.Different) != 1 || d.Different[0] != meta || len(vault.Snapshots(res.Materials)) != 2 {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(problems(res.Integrity), "不存在或未通过校验") {
		t.Fatal(problems(res.Integrity))
	}
}

func TestTargetVerifyByRsyncChecksum(t *testing.T) {
	w := newWorld(t)
	fakeRsync(t, w)
	cloud, leaf := syncedCloud(t, w)
	ctx := context.Background()
	basis, _ := LeafMaterials(w.p, leaf, true)
	res, err := TargetVerify(ctx, w.env, w.p, vaultrpc.TargetVerifyParams{Target: "nas", Leaf: leaf})
	if err != nil || res.FetchedBytes != 0 || len(res.Integrity) != 0 || !vault.CompareMaterials(basis.Materials, res.Materials).OK() {
		t.Fatalf("%+v %v", res, err)
	}
	// A corrupted pack of the same size is found by checksum.
	var pack string
	for _, m := range basis.Materials {
		if vault.IsPackPath(m.Path) {
			pack = m.Path
		}
	}
	p := filepath.Join(cloud, "box", leaf, filepath.FromSlash(pack))
	b, _ := os.ReadFile(p)
	b[len(b)/2] ^= 0xff
	os.WriteFile(p, b, 0o600)
	res, err = TargetVerify(ctx, w.env, w.p, vaultrpc.TargetVerifyParams{Target: "nas", Leaf: leaf, Packs: true})
	if err != nil || !strings.Contains(problems(res.Integrity), "SHA-256") {
		t.Fatalf("%+v %v", res, err)
	}
	if d := vault.CompareMaterials(basis.Materials, res.Materials); len(d.Different) != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestSyncChecksWhatItCopied(t *testing.T) {
	w := newWorld(t)
	fakeSFTP(t, w)
	cloud, leaf := syncedCloud(t, w)
	ctx := context.Background()
	st, _ := Status(w.p)
	if len(st.Targets) != 1 || len(st.Targets[0].Checks) != 1 || st.Targets[0].Checks[0].Result != "passed" || st.Targets[0].Checks[0].Snapshots != 2 {
		t.Fatalf("%+v", st.Targets)
	}
	// Nothing new on the server: the leaf is not read again.
	res, err := Sync(ctx, w.env, w.p, "nas", "client")
	if err != nil || len(res.Checks) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// A changed meta at the location is found once the leaf changes again.
	meta := filepath.Join(cloud, "box", leaf, vault.MetaPath(st.Snapshots[0].SnapshotID))
	b, _ := os.ReadFile(meta)
	os.WriteFile(meta, append(b[:len(b)-2], ' ', '}'), 0o600)
	w.seal(t, vault.TierSoul)
	res, err = Sync(ctx, w.env, w.p, "nas", "client")
	if err != nil || len(res.Checks) != 1 || res.Checks[0].Result != "failed" || res.Checks[0].Different != 1 || res.Checks[0].First == "" {
		t.Fatalf("%+v %v", res, err)
	}
	if st, _ := Status(w.p); st.Targets[0].Checks[0].Result != "failed" {
		t.Fatalf("%+v", st.Targets[0].Checks)
	}
}
