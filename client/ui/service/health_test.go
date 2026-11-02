package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
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
		for _, w := range []string{"着陆", "上云", "演练"} {
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
	if rt := healthItem(t, r, "restore_test"); rt.Level != HealthInfo || !strings.Contains(rt.Text, "通过") {
		t.Fatalf("%+v", rt)
	}
}
