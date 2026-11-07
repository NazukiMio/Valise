package tui

import (
	"strings"
	"testing"
)

// The checklist ticks from real state and its guides lead to real pages.
func TestChecklist(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	// world has a key, a server, an enrolled and sealed agent: three done.
	wantText(t, m, "第一次使用", "已完成 3 / 5", "下一步：存一份到别处")
	key(t, m, "g")
	v := wantText(t, m, "准备工作", "建立第一个备份", "做一次恢复测试", "认识 Kit 与高级模式")
	if strings.Count(v, "✓") != 3 {
		t.Fatalf("ticks:\n%s", v)
	}
	// Skip the restore test, then read the Kit guide to the end.
	for range 4 {
		key(t, m, "down")
	}
	key(t, m, "x")
	wantText(t, m, "已跳过")
	key(t, m, "down")
	key(t, m, "down")
	key(t, m, "enter")
	wantText(t, m, "Valise Kit", "1 / 2")
	key(t, m, "enter")
	wantText(t, m, "高级模式", "2 / 2")
	key(t, m, "enter")
	if v := wantText(t, m, "认识 Kit 与高级模式"); strings.Count(v, "✓") != 4 {
		t.Fatalf("kit not ticked:\n%s", v)
	}
	// The escape pod guide ends on the real seal.
	key(t, m, "up")
	key(t, m, "up")
	key(t, m, "up")
	key(t, m, "up")
	key(t, m, "enter")
	wantText(t, m, "保存所选内容在此刻的状态", "1 / 2")
	key(t, m, "enter")
	key(t, m, "enter")
	wantText(t, m, "建立备份 · 选择内容")
}
