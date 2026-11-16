package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/vaultrpc"
)

// On the backup locations screen x removes a copy location after a
// confirmation that says what goes and what stays; the server's record is
// gone afterwards and the screen no longer lists it.
func TestCloudScreenRemovesACopyLocation(t *testing.T) {
	m, home := world(t)
	ctx := context.Background()
	if err := m.svc.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	if err := m.svc.TargetSet(ctx, "box", vaultrpc.Target{Name: "old", Kind: "rsync", Dest: "sink:/backup/old"}); err != nil {
		t.Fatal(err)
	}
	drive(t, m, m.loadServer("box"))
	drive(t, m, m.push(newCloud("box")))
	if ps := places(m, "box"); len(ps) != 1 || ps[0].name != "old" {
		t.Fatalf("places %+v", ps)
	}
	if !strings.Contains(m.top().(*cloudScreen).help(), "移除这个位置") {
		t.Fatal("the screen does not offer removal")
	}
	key(t, m, "x")
	c, ok := m.top().(*confirmScreen)
	if !ok {
		t.Fatalf("no confirmation, top is %T", m.top())
	}
	text := c.question + strings.Join(c.detail, "\n")
	for _, want := range []string{"移除备份位置 old", "删掉 box 上指向它的记录", "已有的备份不会删除", "保护计划", "没有读到的服务器"} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation lacks %q: %s", want, text)
		}
	}
	targets := filepath.Join(home, ".config/valise/targets.json")
	if b, _ := os.ReadFile(targets); !strings.Contains(string(b), `"old"`) {
		t.Fatal("asking already removed it")
	}
	wantText(t, m, "移除备份位置 old") // shown, so it can be answered
	key(t, m, "y")
	if b, _ := os.ReadFile(targets); strings.Contains(string(b), `"old"`) {
		t.Fatalf("server still has the record: %s", b)
	}
	if ps := places(m, "box"); len(ps) != 0 {
		t.Fatalf("still listed after the server was read again: %+v", ps)
	}
	if m.flashErr || !strings.Contains(m.flash, "已移除备份位置 old") {
		t.Fatalf("flash %q err %v", m.flash, m.flashErr)
	}
}
