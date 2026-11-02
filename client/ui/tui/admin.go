package tui

import (
	tea "charm.land/bubbletea/v2"

	"valise/core/valise"
)

type adminReply struct {
	pass string
	err  error
}

// askAdminPassphrase is called from a background operation: it opens a
// masked input and waits for the answer. The passphrase is handed to the
// service once and not kept.
// askOwnerPassphrase asks for the Valise passphrase that proves the owner
// (docs/ARCHITECTURE.md 2.2) from a service goroutine.
func askOwnerPassphrase(send func(tea.Msg), host string) (string, error) {
	reply := make(chan adminReply, 1)
	in := newInput("输入 Valise 口令以确认是你本人（"+host+"；一段时间内不再询问）", "", true, func(m *Model, v string) tea.Cmd {
		reply <- adminReply{pass: v}
		return nil
	})
	in.cancel = func() {
		reply <- adminReply{err: valise.New("cancelled", "已取消", "")}
	}
	send(pushMsg{in})
	r := <-reply
	return r.pass, r.err
}

func askAdminPassphrase(send func(tea.Msg), host string) (string, error) {
	reply := make(chan adminReply, 1)
	in := newInput("输入 "+host+" 的管理口令以继续（解锁后一段时间内不再询问）", "", true, func(m *Model, v string) tea.Cmd {
		reply <- adminReply{pass: v}
		return nil
	})
	in.cancel = func() {
		reply <- adminReply{err: valise.New("cancelled", "已取消，没有解锁开发者模式", "")}
	}
	send(pushMsg{in})
	r := <-reply
	return r.pass, r.err
}
