package tui

import (
	"fmt"
	"strings"

	"valise/core/agentkit"
)

// proposalsView lists the agent's classification proposals; sel is the
// selected one (negative for none). It also returns the selected lines.
func (c *healthTab) proposalsView(w, sel int) (string, int, int) {
	var b strings.Builder
	from, to := -1, -1
	b.WriteString(dimStyle.Render("agent 认为这些文件也该装进备份，或应该换一类：") + "\n")
	for i, pr := range c.proposals {
		if i == sel {
			from = strings.Count(b.String(), "\n") + 1
		}
		var line string
		switch {
		case pr.EffectiveKind() == agentkit.ProposalKindSQLite && pr.SQLite != nil:
			line = "sqlite:" + pr.SQLite.Root + ":" + pr.SQLite.Glob
		case pr.EffectiveKind() == agentkit.ProposalKindExternalRef && pr.ExternalRef != nil:
			line = "external:" + pr.ExternalRef.Path + " (" + pr.ExternalRef.RefKind + ")"
		case pr.EffectiveKind() == agentkit.ProposalKindWebEntry && pr.WebEntry != nil:
			line = fmt.Sprintf("web_entry:%s %s (127.0.0.1:%d%s)", pr.WebEntry.ID, pr.WebEntry.DisplayName, pr.WebEntry.Port, pr.WebEntry.Path)
		default:
			line = pr.Rule.Root + ":" + pr.Rule.Glob + " → " + categoryName(pr.Rule.Category)
		}
		if pr.Demotion {
			line += warnStyle.Render("  降级：不再进轻装备份，需要单独确认")
		}
		b.WriteString(cursorLine(i == sel, line) + "\n")
		b.WriteString(dimStyle.Render("    理由（agent 提供）："+pr.Reason) + "\n")
		if pr.Unclassified > 0 {
			b.WriteString(warnStyle.Render(fmt.Sprintf("    其中 %d 个文件现在没有归类（按凭据对待），接受后会进入轻装备份；请确认里面没有密钥", pr.Unclassified)) + "\n")
		}
		if i == sel {
			to = strings.Count(b.String(), "\n")
		}
	}
	return box("归档建议", strings.TrimRight(b.String(), "\n"), w), from, to
}
