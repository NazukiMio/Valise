package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Below 80×24 only a notice shows and keys wait; a confirmation that does
// not fit scrolls, and yes waits until all of it has been on screen.
func TestWindowMinimumAndConfirmScroll(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
	wantText(t, m, "窗口太小", "80×24")
	key(t, m, "a") // ignored while too small
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(screenText(m), "窗口太小") {
		t.Fatal("notice at the minimum")
	}
	yes := false
	var detail []string
	for i := range 40 {
		detail = append(detail, "第 "+itoa(i+1)+" 行："+strings.Repeat("很长的命令参数 ", 12))
	}
	drive(t, m, m.push(confirm("执行？", detail, func(m *Model) tea.Cmd { yes = true; return nil })))
	wantText(t, m, "看完才能确认")
	key(t, m, "y")
	if yes {
		t.Fatal("confirmed before the end was shown")
	}
	for range 60 {
		key(t, m, "space")
	}
	wantText(t, m, "第 40 行")
	key(t, m, "y")
	if !yes {
		t.Fatal("not confirmed after reading it all")
	}
}
