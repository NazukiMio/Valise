package tui

import (
	"fmt"
	"strings"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
)

// Finding titles and plain explanations (docs/AGENT_KIT.md 5, 8.6).
var findingInfo = map[string]struct{ title, why, generic string }{
	agentkit.FindingLongSession: {"有一个会话太长了",
		"会话越长，agent 每次回复要读的上下文越多：回复变慢、花费变高，也更容易忘记早期的约定。",
		"开一个新会话；想保留上下文就先让 agent 做一次交接（逃生舱技能 valise-handoff）"},
	agentkit.FindingCostGrowth: {"花费连续几天在涨",
		"最近几天每天用掉的 token 越来越多，通常是会话越积越长，或 agent 在反复读大文件。",
		"看看最近的会话是否过长；需要时开新会话，并让 agent 交接"},
	agentkit.FindingMemoryBloat: {"记忆文件太长了",
		"记忆文件每次都会被读进上下文，太长会挤占对话空间，也会让重要的约定被淹没。",
		"让 agent 把记忆整理成要点，过时的内容归档到单独的文件"},
	agentkit.FindingSessionPileup: {"旧会话堆积",
		"很久没碰过的会话占了大部分空间；它们也会进入逃生舱，让每次建立更慢、更大。",
		"用 harness 自己的功能归档或清理很久不用的会话"},
	agentkit.FindingNoMemoryMechanism: {"没有长期记忆",
		"这个 agent 没有任何长期记忆文件或记忆插件，换会话后它会忘记你说过的偏好。",
		"建立一个记忆文件，或启用 harness 自带的记忆功能（见 harness 文档）"},
}

func findingTitle(id string) string {
	if f, ok := findingInfo[id]; ok {
		return f.title
	}
	return id
}

const agentAsk = "请用 valise-checkup 技能看一下健康页中的会话与记忆检查结果，提出整理方案，等我确认后再做。"

// findingBars draws the measured value against its threshold.
func findingBars(ck *agentkit.CheckupResult, id string, th *profile.Thresholds) string {
	days, tokens, pile := float64(agentkit.DefaultLongSessionDays), float64(agentkit.DefaultLongSessionTokens), float64(agentkit.DefaultPileupBytes)
	if th != nil {
		if th.LongSessionDays > 0 {
			days = float64(th.LongSessionDays)
		}
		if th.LongSessionTokens > 0 {
			tokens = float64(th.LongSessionTokens)
		}
		if th.PileupBytes > 0 {
			pile = float64(th.PileupBytes)
		}
	}
	switch id {
	case agentkit.FindingLongSession:
		s := ck.Current
		if s == nil && len(ck.Sessions) > 0 {
			s = &ck.Sessions[0]
		}
		if s == nil {
			return ""
		}
		est := ""
		if s.Estimated {
			est = "（估算）"
		}
		return bar("会话持续", s.Days, days, fmt.Sprintf("%.0f 天", s.Days), fmt.Sprintf("%.0f 天开始提醒", days)) + "\n" +
			bar("token 用量", float64(s.Tokens), tokens, fmt.Sprintf("%d 万%s", s.Tokens/10000, est), fmt.Sprintf("%.0f 万开始提醒", tokens/10000))
	case agentkit.FindingMemoryBloat:
		var rows []string
		for _, mf := range ck.Memory {
			if mf.WarnLines > 0 {
				rows = append(rows, bar(truncate(mf.Path, 14), float64(mf.Lines), float64(mf.WarnLines), itoa(mf.Lines)+" 行", itoa(mf.WarnLines)+" 行开始提醒"))
			}
		}
		return strings.Join(rows, "\n")
	case agentkit.FindingSessionPileup:
		return bar("会话总量", float64(ck.TotalBytes), pile, size(ck.TotalBytes), size(int64(pile))+" 开始提醒") + "\n" +
			bar("很久没碰过", float64(ck.ColdBytes), float64(max(ck.TotalBytes, 1)), size(ck.ColdBytes), "占全部的比例")
	case agentkit.FindingCostGrowth:
		var peak int64 = 1
		for _, d := range ck.Daily {
			peak = max(peak, d.Tokens)
		}
		var rows []string
		for _, d := range ck.Daily {
			rows = append(rows, bar(d.Day[5:], float64(d.Tokens), float64(peak), itoa(int(d.Tokens/1000))+"k", ""))
		}
		return strings.Join(rows, "\n")
	}
	return ""
}

// bar: label, a 20-cell bar scaled so the threshold sits at 80%, value.
func bar(label string, v, limit float64, value, note string) string {
	const cells = 20
	pos := int(v / limit * cells * 0.8)
	pos = max(0, min(cells, pos))
	mark := int(cells * 0.8)
	var b strings.Builder
	for i := 0; i < cells; i++ {
		switch {
		case i == mark:
			b.WriteString(dimStyle.Render("│"))
		case i < pos && v >= limit:
			b.WriteString(warnStyle.Render("█"))
		case i < pos:
			b.WriteString(okStyle.Render("█"))
		default:
			b.WriteString(faintStyle.Render("░"))
		}
	}
	out := padRight(label, 14) + b.String() + "  " + value
	if note != "" {
		out += dimStyle.Render("（" + note + "）")
	}
	return out
}

var categoryNames = map[string]string{"identity": "人格", "memory": "记忆", "skills": "技能", "config": "配置", "sessions": "会话",
	"state": "状态库", "body_patch": "身体补丁", "secrets": "凭据", "workspace": "工作区", "cache": "缓存", "ignore": "不备份", "body": "身体"}

func categoryName(c string) string {
	if n, ok := categoryNames[c]; ok {
		return n
	}
	return c
}

func provenanceText(pv enroll.Provenance) string {
	switch {
	case pv.NPM != nil:
		return "npm 全局包 " + pv.NPM.Package + "@" + pv.NPM.Version
	case pv.Git != nil:
		s := "git " + pv.Git.Remote + " @ " + short(pv.Git.Commit)
		if pv.Git.Dirty {
			s += warnStyle.Render("（有本地修改）")
		}
		return s
	case pv.Tool != nil:
		return pv.Kind + " " + pv.Tool.Package + " " + pv.Tool.Version
	case pv.Binary != nil:
		return "程序 " + shortPath(pv.Binary.Path, 48)
	}
	return dimStyle.Render("未知")
}

// shortPath keeps the end of a long path.
func shortPath(p string, n int) string {
	r := []rune(p)
	if len(r) <= n {
		return p
	}
	return "…" + string(r[len(r)-n+1:])
}

func short(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}
