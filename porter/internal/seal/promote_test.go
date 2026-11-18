package seal

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func (w *world) sealAuto(t *testing.T) *vaultrpc.SealReport {
	t.Helper()
	report, err := Seal(context.Background(), w.env, w.p,
		vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul, Reason: "test"}, vaultrpc.InitiatorTimer)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestPromotionSurvivesAutomaticRetirement(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 6, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return at }
	t.Cleanup(func() { now = time.Now })
	manual := w.seal(t, vault.TierSoul)
	first := w.sealAuto(t)
	write(t, filepath.Join(w.state, "SOUL.md"), "promote this revision")
	second := w.sealAuto(t) // reuses blobs first written by the earlier snapshot
	if !vault.IsSavepointLeaf(manual.Leaf) || first.Leaf != second.Leaf || manual.Leaf == second.Leaf {
		t.Fatalf("seals did not use the expected tracks: %s %s %s", manual.Leaf, first.Leaf, second.Leaf)
	}
	p := vaultrpc.SnapshotPromoteParams{InstanceID: "demo", SnapshotID: second.SnapshotID, Note: "  升级前  "}
	res, err := PromoteSnapshot(ctx, w.p, p, vaultrpc.InitiatorClient)
	if err != nil || res.Leaf != manual.Leaf || res.SnapshotID != second.SnapshotID || res.AddedBytes <= 0 {
		t.Fatalf("promote: %+v %v", res, err)
	}
	// Importing an epoch does not displace the manual writer's dedupe key.
	anotherManual := w.seal(t, vault.TierSoul)
	if anotherManual.Epoch != manual.Epoch || anotherManual.NewEpoch {
		t.Fatalf("manual dedupe epoch changed after promotion: %+v", anotherManual)
	}
	view, err := Journey(ctx, w.env, w.p, "")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, pt := range view.Result.Points {
		if pt.SnapshotRef.SnapshotID == second.SnapshotID {
			found++
			if pt.Kind != vault.KindSavepoint || pt.Note != "升级前" || pt.MetadataState == "conflict" {
				t.Fatalf("promotion not reflected in journey: %+v", pt)
			}
		}
	}
	if found != 1 {
		t.Fatalf("promotion produced %d journey points", found)
	}

	at = at.AddDate(0, 1, 0)
	keep := w.sealAuto(t)
	at = at.AddDate(0, 1, 0)
	current := w.sealAuto(t)
	if err := RetireExpiredAutoLeaves(w.p, "demo", 0); err != nil {
		t.Fatal(err)
	}
	sourceDir, _ := w.p.LeafDir(first.Leaf)
	if _, err := os.Stat(sourceDir); err != nil {
		t.Fatal("disabled policy deleted a cycle:", err)
	}
	if err := RetireExpiredAutoLeaves(w.p, "demo", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sourceDir); !os.IsNotExist(err) {
		t.Fatal("expired auto cycle remains", err)
	}
	for _, report := range []*vaultrpc.SealReport{manual, keep, current} {
		dir, _ := w.p.LeafDir(report.Leaf)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("retirement removed %s: %v", report.Leaf, err)
		}
	}
	if _, err := DeleteLeaf(w.p, current.Leaf, "client"); err == nil {
		t.Fatal("current leaf was deleted")
	}
	if _, err := DeleteLeaf(w.p, manual.Leaf, "client"); err == nil {
		t.Fatal("permanent leaf was deleted")
	}
	p.Note = "different"
	if again, err := PromoteSnapshot(ctx, w.p, p, vaultrpc.InitiatorClient); err != nil || again.AddedBytes != 0 {
		t.Fatalf("retry after retirement: %+v %v", again, err)
	}
	dest, _ := w.p.LeafDir(manual.Leaf)
	if n, problems, err := vault.VerifyWithKey(dest, []string{p.SnapshotID}, w.id); err != nil || len(problems) != 0 || n < 3 {
		t.Fatalf("verify promoted without original leaf: %d %+v %v", n, problems, err)
	}
	if got := string(w.restore(t, p.SnapshotID)["state:SOUL.md"]); got != "promote this revision" {
		t.Fatalf("restored %q", got)
	}
	audit, _ := os.ReadFile(w.p.Audit())
	if !strings.Contains(string(audit), `"op":"leaf_retire"`) || !strings.Contains(string(audit), `"op":"snapshot_promote"`) {
		t.Fatal("retirement or promotion missing from audit")
	}

	t.Run("standalone restore", func(t *testing.T) {
		for _, tool := range []string{"python3", "age", "zstd"} {
			if _, err := exec.LookPath(tool); err != nil {
				t.Skip(tool + " is unavailable")
			}
		}
		root := t.TempDir()
		idPath := filepath.Join(root, "identity.txt")
		if err := os.WriteFile(idPath, []byte(w.id.String()+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(root, "restore")
		b, err := exec.Command("python3", "../../../tools/standalone-restore/restore.py", "--vault", dest,
			"--identity", idPath, "--snapshot", p.SnapshotID, "--out", out).CombinedOutput()
		if err != nil {
			t.Fatalf("standalone restore: %s %v", b, err)
		}
		got, err := os.ReadFile(filepath.Join(out, "state", "SOUL.md"))
		if err != nil || string(got) != "promote this revision" {
			t.Fatalf("standalone restored %q: %v", got, err)
		}
	})
}

func TestRetirementLeavesHistoricalCycles(t *testing.T) {
	w := newWorld(t)
	at := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return at }
	t.Cleanup(func() { now = time.Now })
	old := w.sealAuto(t)
	dir, _ := w.p.LeafDir(old.Leaf)
	metaPath := filepath.Join(dir, filepath.FromSlash(vault.MetaPath(old.SnapshotID)))
	b, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var m vault.Meta
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.Kind = ""
	b, _ = json.Marshal(m)
	if err := os.WriteFile(metaPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	w.sealAuto(t) // mixed historical/automatic period
	for range 3 {
		at = at.AddDate(0, 1, 0)
		w.sealAuto(t)
	}
	if err := RetireExpiredAutoLeaves(w.p, "demo", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("historical period was retired", err)
	}
	_, err = Seal(context.Background(), w.env, w.p, vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul, Kind: vault.KindAuto}, vaultrpc.InitiatorClient)
	if ue, ok := valise.As(err); !ok || ue.Code != "request_invalid" {
		t.Fatalf("client requested an automatic snapshot: %v", err)
	}
}
