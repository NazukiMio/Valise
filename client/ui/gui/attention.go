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
	Next               string // the button's words; "" for none
	Server, Instance   string // the agent to open, when there is one
	Reread             bool   // the next step reads the servers again
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
		it := attention{Key: "srv-" + obs.ConnectionID, Title: obs.ConnectionID + " 这次没有读到", Next: "重新读取", Reread: true,
			Reason: "没有回应。检查服务器是否开机、网络是否通畅。"}
		if obs.Error != nil {
			it.Reason = obs.Error.Reason
			if obs.Error.Suggestion != "" {
				it.Reason += "。" + obs.Error.Suggestion
			}
		}
		if obs.Status == "stale" && obs.ObservedAt != nil {
			it.Reason += fmt.Sprintf("（下面显示的是 %s 读到的结果）", when(*obs.ObservedAt, now))
		}
		out = append(out, it)
	}
	var stale []string
	for _, r := range rows {
		if r.Retired() {
			continue
		}
		if r.HasService && r.ServiceTone != "ok" {
			out = append(out, attention{Key: "svc-" + r.Server + "/" + r.Instance, Title: r.Name + " " + r.Service,
				Reason: "在 " + r.Server + " 上，" + r.Harness + " 的服务现在没有运行，Agent 不会响应。", Next: "查看 " + r.Name,
				Server: r.Server, Instance: r.Instance})
		}
		if a.hasRepos {
			points := pointsFor(a.timeline.value, r.Server, r.Instance)
			if len(points) == 0 || now.Sub(points[0].CreatedAt) > staleAfter {
				stale = append(stale, r.Name+"（"+r.Server+"）")
			}
		}
	}
	if len(stale) > 0 {
		names := strings.Join(stale[:min(len(stale), 4)], "、")
		if len(stale) > 4 {
			names += fmt.Sprintf(" 等 %d 个", len(stale))
		}
		out = append(out, attention{Key: "stale", Title: fmt.Sprintf("%d 个 Agent 超过 7 天没有新备份到这台电脑", len(stale)),
			Reason: names + "。服务器损坏时，只能找回这台电脑上已有的那份。打开 Valise 时会按设置把新备份补到本机仓库；外接盘要先接上。"})
	}
	return out
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
