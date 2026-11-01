package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAskAdminPassphrase(t *testing.T) {
	m, _ := world(t)
	msgs := make(chan tea.Msg, 1)
	m.send = func(msg tea.Msg) { msgs <- msg }
	got := make(chan adminReply, 1)
	go func() {
		p, err := askAdminPassphrase(m.send, "vps")
		got <- adminReply{p, err}
	}()
	m.Update(<-msgs)
	if !strings.Contains(screenText(m), "vps 的管理口令") {
		t.Fatal(m.View().Content)
	}
	typeText(t, m, "secret admin pass")
	if strings.Contains(screenText(m), "secret admin pass") {
		t.Fatal("passphrase echoed")
	}
	key(t, m, "enter")
	if r := <-got; r.err != nil || r.pass != "secret admin pass" {
		t.Fatalf("%+v", r)
	}

	go func() {
		p, err := askAdminPassphrase(m.send, "vps")
		got <- adminReply{p, err}
	}()
	m.Update(<-msgs)
	key(t, m, "esc")
	if r := <-got; r.err == nil {
		t.Fatal("cancel should fail the unlock")
	}
}
