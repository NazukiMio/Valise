package tui

import (
	"strings"
	"testing"
)

// The 对话 tab is quick entries and can be switched off in settings.
func TestChatTabSwitch(t *testing.T) {
	m, _ := world(t)
	openInstance(t, m)
	key(t, m, "2")
	wantText(t, m, "用哪种方式和 agent 说话都一样", "设置 › 高级")
	if err := m.svc.SetChatEntry(false); err != nil {
		t.Fatal(err)
	}
	chatTabOn = false
	defer func() { chatTabOn = true }()
	p := newInstancePage("box", m.server("box").instances[0])
	for _, tb := range p.tabs {
		if _, ok := tb.(*chatTab); ok {
			t.Fatal("chat tab shown while switched off")
		}
	}
	if !strings.Contains(p.tabs[1].name(), "管理") {
		t.Fatalf("second tab %q", p.tabs[1].name())
	}
}
