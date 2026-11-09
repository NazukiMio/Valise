package gui

import (
	"time"

	"valise/ui/service"
)

// serverState is a server in the four words of PRODUCT.md 8.1: 在线 (read
// this time), 离线 (could not be reached), 需要处理 (reached, but logging in
// or the porter failed), 未知 (not read yet).
type serverState struct {
	Key        string // online, offline, attention, unknown
	Word, Tone string // tone: ok, warn, stop (gray)
	Reason     string // one sentence for 离线 and 需要处理
	Suggestion string
	Old        string // “下面是 X 前的情况” when an older result is shown
}

// offlineCodes are the failures that mean the server could not be reached.
var offlineCodes = map[string]bool{"ssh_connect_failed": true, "server_timeout": true}

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
		return serverState{Key: "unknown", Word: T("server.state.unknown"), Tone: "stop", Reason: T("server.reason.not_read")}
	case obs.Status == "fresh":
		return serverState{Key: "online", Word: T("server.state.online"), Tone: "ok"}
	}
	st := serverState{Key: "offline", Word: T("server.state.offline"), Tone: "stop", Reason: T("server.reason.no_answer"), Suggestion: T("server.suggest.check_power")}
	if e := obs.Error; e != nil {
		st.Reason, st.Suggestion = e.Reason, e.Suggestion
		if !offlineCodes[e.Code] {
			st.Key, st.Word, st.Tone = "attention", T("server.state.attention"), "warn"
		}
	}
	if obs.Status == "stale" && obs.ObservedAt != nil {
		st.Old = T("server.old", "ago", ago(*obs.ObservedAt, now))
	}
	return st
}

// ago is a short “3 小时前”.
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return T("time.just_now")
	case d < time.Hour:
		return T("time.minutes_ago", "n", int(d/time.Minute))
	case d < 48*time.Hour:
		return T("time.hours_ago", "n", int(d/time.Hour))
	}
	return T("time.days_ago", "n", int(d/(24*time.Hour)))
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
