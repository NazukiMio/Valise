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
			out = append(out, attention{Key: "svc-" + r.Server + "/" + r.Instance, Title: r.Name + " " + r.Service,
				Reason: "在 " + r.Server + " 上，" + r.Harness + " 的服务现在没有运行，Agent 不会响应。",
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
		points := a.pointsOf(r.Server, r.Instance)
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
		out = append(out, attention{Key: "stale", Title: fmt.Sprintf("%d 个 Agent 有新备份还没复制到这台电脑", len(toCopy)),
			Reason: names(toCopy) + "。服务器上有更新的备份，这台电脑上的还停在 7 天以前；服务器损坏时，只能找回这台电脑上已有的那份。",
			State:  strings.Join(toCopyState, ","), Go: "/locations"})
	}
	if len(none) > 0 {
		out = append(out, attention{Key: "nobackup", Title: fmt.Sprintf("%d 个 Agent 超过 7 天没有新备份", len(none)),
			Reason: names(none) + "。服务器上最近一次备份也在 7 天以前，或从来没有：这段时间的新内容没有被保存。看看定时备份是否开着、是否在运行。",
			State:  strings.Join(noneState, ","), Go: noneGo, Tab: tabHealth})
	}
	return out
}

// names lists up to four agents, then how many in all.
func names(list []string) string {
	s := strings.Join(list[:min(len(list), 4)], "、")
	if len(list) > 4 {
		s += fmt.Sprintf(" 等 %d 个", len(list))
	}
	return s
}

// verdict is the home page's one-line answer.
func verdict(agents, issues int) string {
	switch {
	case agents == 0:
		return "还没有接入 Agent"
	case issues == 0:
		return fmt.Sprintf("%d 个 Agent，一切正常", agents)
	}
	return fmt.Sprintf("%d 个 Agent，%d 件事需要你处理", agents, issues)
}

// quietLine is an agent's state when nothing needs attention: one muted
// line, the facts still told apart.
func quietLine(r agentRow, newest *time.Time, now time.Time) string {
	parts := []string{shortService(r.Service)}
	if !r.HasService {
		parts = []string{"没有 Valise 控制的服务"}
	}
	if newest != nil {
		parts = append(parts, when(*newest, now)+" 备份到本机")
	}
	return strings.Join(parts, " · ")
}

// agentVerdict is one sentence about an agent for its page.
func agentVerdict(r agentRow, newest *time.Time, now time.Time) (string, bool) {
	if r.Note != "" {
		return r.Note + "，这里是它原来的位置", true
	}
	if r.HasService && r.ServiceTone != "ok" {
		return r.Service + "，Agent 现在不会响应", true
	}
	s := quietLine(r, newest, now)
	if newest == nil {
		return s + " · 这台电脑上还没有它的备份", false
	}
	return s, false
}
