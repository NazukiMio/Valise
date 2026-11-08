package gui

import (
	"time"

	"valise/ui/service"
)

// serverState is a server in the four words of PRODUCT.md 8.1: 在线 (read
// this time), 离线 (could not be reached), 需要处理 (reached, but logging in
// or the porter failed), 未知 (not read yet).
type serverState struct {
	Word, Tone string // tone: ok, warn, stop (gray)
	Reason     string // one sentence for 离线 and 需要处理
	Suggestion string
	Old        string // “下面是 X 前的情况” when an older result is shown
}

// offlineCodes are the failures that mean the server could not be reached.
var offlineCodes = map[string]bool{"ssh_connect_failed": true}

func (a *App) observation(name string) *service.AgentServerObservation {
	for i := range a.agents.value.Observations {
		if a.agents.value.Observations[i].ConnectionID == name {
			return &a.agents.value.Observations[i]
		}
	}
	return nil
}

func (a *App) serverState(name string, now time.Time) serverState {
	obs := a.observation(name)
	switch {
	case obs == nil:
		return serverState{Word: "未知", Tone: "stop", Reason: "还没读取"}
	case obs.Status == "fresh":
		return serverState{Word: "在线", Tone: "ok"}
	}
	st := serverState{Word: "离线", Tone: "stop", Reason: "没有回应", Suggestion: "检查服务器是否开机、网络是否通畅"}
	if e := obs.Error; e != nil {
		st.Reason, st.Suggestion = e.Reason, e.Suggestion
		if !offlineCodes[e.Code] {
			st.Word, st.Tone = "需要处理", "warn"
		}
	}
	if obs.Status == "stale" && obs.ObservedAt != nil {
		st.Old = "下面是 " + ago(*obs.ObservedAt, now) + "的情况"
	}
	return st
}

// ago is a short “3 小时前”.
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "刚才"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + " 分钟前"
	case d < 48*time.Hour:
		return itoa(int(d/time.Hour)) + " 小时前"
	}
	return itoa(int(d/(24*time.Hour))) + " 天前"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
