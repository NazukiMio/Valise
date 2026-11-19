package service

import (
	"context"

	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"valise/core/action"
	"valise/core/profile"
)

func healthItem(t *testing.T, r *HealthReport, id string) HealthItem {
	t.Helper()
	for _, it := range r.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no %s item: %+v", id, r.Items)
	return HealthItem{}
}

// The health page: problems first, plain words, a restore test that was
// done shows as text only (docs/UI.md 9.5).
func TestHealth(t *testing.T) {
	s, _, _, _ := sealedWorld(t)
	ctx := context.Background()
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }

	r, err := s.Health(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	b := healthItem(t, r, "backup")
	if b.Level != HealthWarn || b.Key != "u" || !strings.Contains(b.Text, "只在服务器上") {
		t.Fatalf("%+v", b)
	}
	if rt := healthItem(t, r, "restore_test"); rt.Level != HealthWarn || rt.Text != "还没有测试过" {
		t.Fatalf("%+v", rt)
	}
	if r.Items[0].Group != HealthPods || Rank(r.Items[0].Level) > Rank(r.Items[len(r.Items)-1].Level) && r.Items[len(r.Items)-1].Group == HealthPods {
		t.Fatalf("order: %+v", r.Items)
	}
	for _, it := range r.Items {
		for _, w := range []string{"逃生舱", "空间站", "着陆", "上云", "演练"} {
			if strings.Contains(it.Text+it.Detail, w) {
				t.Fatalf("themed word %q in %+v", w, it)
			}
		}
	}

	if _, err := s.RepoAdd("usb", filepath.Join(t.TempDir(), "usb")); err != nil {
		t.Fatal(err)
	}
	s.ServerNameSet(ctx, "box", "box")
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunRestoreTest(ctx, "box", "demo", ""); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Health(ctx, "box", "demo")
	if b := healthItem(t, r, "backup"); b.Level != HealthOK || !strings.Contains(b.Text, "仓库 usb") {
		t.Fatalf("%+v", b)
	}
	if rt := healthItem(t, r, "restore_test"); rt.Level != HealthOK || !strings.Contains(rt.Text, "通过") {
		t.Fatalf("%+v", rt)
	}
}

// Dismissing: a warn stops counting, for good; a bad stays bad on the
// health page but leaves the overview for 30 days (longer when set); a
// worse item is not dismissed.
func TestHealthDismiss(t *testing.T) {
	s, _, _, _ := sealedWorld(t)
	ctx := context.Background()
	r, _ := s.Health(ctx, "box", "demo")
	before := r.Problems()
	rt := healthItem(t, r, "restore_test")
	if on, err := s.ToggleDismiss("box", "demo", rt); err != nil || !on {
		t.Fatal(on, err)
	}
	r, _ = s.Health(ctx, "box", "demo")
	if rt := healthItem(t, r, "restore_test"); rt.Dismissed == nil || !rt.Dismissed.IsZero() || rt.Counts() || !rt.Quiet() || r.Problems() != before-1 {
		t.Fatalf("%+v %d", rt, r.Problems())
	}
	// A dismissed warn that turns red is shown again.
	items := []HealthItem{{ID: "restore_test", Level: HealthBad}}
	s.MarkDismissed("box", "demo", items)
	if items[0].Dismissed != nil {
		t.Fatal("a worse item stayed dismissed")
	}
	// A dismissed bad still counts on the health page.
	bad := HealthItem{ID: "approval", Level: HealthBad}
	s.ToggleDismiss("box", "demo", bad)
	items = []HealthItem{bad}
	s.MarkDismissed("box", "demo", items)
	if items[0].Dismissed == nil || !items[0].Counts() || !items[0].Quiet() {
		t.Fatalf("%+v", items[0])
	}
	if days := time.Until(*items[0].Dismissed).Hours() / 24; days < 29.9 || days > 30 {
		t.Fatalf("red dismissed for %.1f days", days)
	}
	if err := s.SetRedDismissDays(10); err == nil {
		t.Fatal("shorter than 30 days accepted")
	}
	if err := s.SetRedDismissDays(90); err != nil || s.RedDismissDays() != 90 {
		t.Fatal(err)
	}
	// Toggling again lifts it; an expired one is gone.
	rt = healthItem(t, r, "restore_test")
	if on, _ := s.ToggleDismiss("box", "demo", rt); on {
		t.Fatal("not lifted")
	}
	d := s.loadDismissals()
	x := d["box"]["demo"]["approval"]
	x.Until = time.Now().Add(-time.Minute)
	d["box"]["demo"]["approval"] = x
	b, _ := json.Marshal(d)
	os.WriteFile(filepath.Join(s.Dirs.Data, dismissFile), b, 0o600)
	s.MarkDismissed("box", "demo", items)
	if items[0].Dismissed != nil {
		t.Fatal("an expired dismissal still applies")
	}
}

// Approval: driven solely by deterministic profile approval_check results.
func TestApprovalItem(t *testing.T) {
	bypass := &action.ApprovalResult{Declared: true, Mode: profile.ApprovalBypass, Detail: "security.effective=full ask.effective=off", Pending: true, Default: true}
	ask := &action.ApprovalResult{Declared: true, Mode: profile.ApprovalAsk, Detail: "ask.effective=always"}
	for _, tc := range []struct {
		chk   *action.ApprovalResult
		level string
		has   string
	}{
		{bypass, HealthBad, "出厂设置"},
		{ask, HealthOK, "harness 设置读出"},
		{&action.ApprovalResult{}, HealthInfo, "未能"},
		{&action.ApprovalResult{Declared: true, Mode: profile.ApprovalUnknown}, HealthInfo, "未能"},
	} {
		it := approvalItem(tc.chk)
		if it.Level != tc.level || !strings.Contains(it.Text+it.Detail, tc.has) {
			t.Errorf("%+v -> %s %q / %q", tc.chk, it.Level, it.Text, it.Detail)
		}
	}
}
