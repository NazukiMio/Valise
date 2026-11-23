package gui

import (
	"fmt"
	"strings"
	"time"
)

// staleAfter is how old the newest local backup may be before it needs
// attention (docs/UI.md 7.2, user decision).
const staleAfter = 7 * 24 * time.Hour

// attention is one thing the home page asks the person to look at: what is
// wrong, why it matters, and the next step.
type attention struct {
	Key, Title, Reason string
	// State is what “知道了” remembers: the reminder comes back when it
	// changes (docs/UI.md 7.3).
	State string
	Go    string // where “带我去处理” leads
	Tab   int    // the agent page's tab, when Go is an agent
}

// attentionItems are the only three kinds that count (docs/UI.md 7.2): a
// service not running, a server that could not be read, and local copies
// that are old or missing while a local repository is registered. Agents no
// longer in use never count.
func (a *App) attentionItems(rows []agentRow, now time.Time) []attention {
	var out []attention
	for _, obs := range a.agents.value.Observations {
		if obs.Status == "fresh" {
			continue
		}
		st := a.serverState(obs.ConnectionID, now)
		it := attention{Key: "srv-" + obs.ConnectionID, Title: obs.ConnectionID + " " + st.Word, Go: "/servers/" + obs.ConnectionID,
			Reason: st.Reason, State: obs.Status}
		if st.Suggestion != "" {
			it.Reason += "。" + st.Suggestion
		}
		if obs.Error != nil {
			it.State += ":" + obs.Error.Code
		}
		if st.Old != "" {
			it.Reason += "（" + st.Old + "）"
		}
		out = append(out, it)
	}
	for _, r := range rows {
		if !r.Retired() && r.HasService && r.ServiceTone != "ok" {
			reason := T("attention.service.reason", "server", r.Server, "harness", r.Harness)
			if r.Service == T("service.unknown") {
				reason = T("attention.service_unknown.reason", "server", r.Server)
			}
			out = append(out, attention{Key: "svc-" + r.Server + "/" + r.Instance, Title: r.Name + " " + r.Service,
				Reason: reason,
				State:  r.Service, Go: "/agents/" + r.Server + "/" + r.Instance, Tab: tabHealth})
		}
	}
	// Old local copies, split by cause (docs/UI.md 7.2): the server has
	// newer backups not yet copied here, or the server itself has none.
	var toCopy, toCopyState, none, noneState []string
	noneGo := ""
	for _, r := range rows {
		if r.Retired() || !a.hasRepos {
			continue
		}
		points := a.localPointsOf(r.Server, r.Instance)
		var local time.Time
		if len(points) > 0 {
			local = points[0].CreatedAt
		}
		if !local.IsZero() && now.Sub(local) <= staleAfter {
			continue
		}
		vr, read := a.vaults[r.Server]
		if !read {
			continue // the server was not read: its own card says so
		}
		name := r.Name + "（" + r.Server + "）"
		server := vr.newest[r.Instance]
		if server.After(local) {
			toCopy = append(toCopy, name)
			toCopyState = append(toCopyState, r.Server+"/"+r.Instance+"@"+server.UTC().Format(time.RFC3339))
			continue
		}
		weeks := "none"
		if !local.IsZero() {
			weeks = fmt.Sprint(int(now.Sub(local) / staleAfter))
		}
		none = append(none, name)
		noneState = append(noneState, r.Server+"/"+r.Instance+"@"+weeks)
		if noneGo == "" {
			noneGo = "/agents/" + r.Server + "/" + r.Instance
		}
	}
	if len(toCopy) > 0 {
		out = append(out, attention{Key: "stale", Title: T("attention.copy.title", "n", len(toCopy)),
			Reason: T("attention.copy.reason", "names", names(toCopy)),
			State:  strings.Join(toCopyState, ","), Go: "/locations"})
	}
	if len(none) > 0 {
		out = append(out, attention{Key: "nobackup", Title: T("attention.stale.title", "n", len(none)),
			Reason: T("attention.stale.reason", "names", names(none)),
			State:  strings.Join(noneState, ","), Go: noneGo, Tab: tabManage})
	}
	return out
}

// names lists up to four agents, then how many in all.
func names(list []string) string {
	s := strings.Join(list[:min(len(list), 4)], T("common.list_sep"))
	if len(list) > 4 {
		s = T("attention.names_more", "names", s, "n", len(list))
	}
	return s
}

// verdict is the home page's one-line answer.
func verdict(agents, issues int) string {
	switch {
	case agents == 0:
		return T("verdict.none")
	case issues == 0:
		return T("verdict.fine", "n", agents)
	}
	return T("verdict.issues", "n", agents, "issues", issues)
}

// quietLine is an agent's state when nothing needs attention: one muted
// line, the facts still told apart.
func quietLine(r agentRow, newest *time.Time, now time.Time) string {
	parts := []string{shortService(r.Service)}
	if !r.HasService {
		parts = []string{T("service.none")}
	}
	if newest != nil {
		parts = append(parts, T("agent.backed_up_local", "when", when(*newest, now)))
	}
	return strings.Join(parts, T("common.dot_sep"))
}

// agentVerdict is one sentence about an agent for its page.
func agentVerdict(r agentRow, newest *time.Time, now time.Time) (string, bool) {
	if r.Note != "" {
		return T("agent.verdict.moved", "note", r.Note), true
	}
	if r.StateMissing {
		return T("agent.verdict.state_missing"), true
	}
	if r.HasService && r.ServiceTone != "ok" {
		if r.Service == T("service.unknown") {
			return T("agent.verdict.unknown"), true
		}
		return T("agent.verdict.down", "service", r.Service), true
	}
	s := quietLine(r, newest, now)
	if newest == nil {
		return T("agent.verdict.no_local", "line", s), false
	}
	return s, false
}
