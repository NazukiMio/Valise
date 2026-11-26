package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
)

// WP3: scrollable line-by-line LCS diff modal for changed agent-kit skills.

const (
	diffCtx = 0
	diffDel = -1
	diffAdd = 1
)

type diffLine struct {
	kind int
	text string
}

func computeLineDiff(oldText, newText string) []diffLine {
	a := splitDiffLines(oldText)
	b := splitDiffLines(newText)
	// Trim common prefix and suffix to keep DP small and fast.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	var out []diffLine
	for i := 0; i < pre; i++ {
		out = append(out, diffLine{kind: diffCtx, text: a[i]})
	}
	if len(ma)*len(mb) > 250_000 {
		for _, l := range ma {
			out = append(out, diffLine{kind: diffDel, text: l})
		}
		for _, l := range mb {
			out = append(out, diffLine{kind: diffAdd, text: l})
		}
	} else {
		out = append(out, lcsDiff(ma, mb)...)
	}
	for i := len(a) - suf; i < len(a); i++ {
		out = append(out, diffLine{kind: diffCtx, text: a[i]})
	}
	return out
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func lcsDiff(a, b []string) []diffLine {
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []diffLine
	i, j := 0, 0
	for i < m && j < n {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{kind: diffCtx, text: a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, diffLine{kind: diffDel, text: a[i]})
			i++
		default:
			out = append(out, diffLine{kind: diffAdd, text: b[j]})
			j++
		}
	}
	for ; i < m; i++ {
		out = append(out, diffLine{kind: diffDel, text: a[i]})
	}
	for ; j < n; j++ {
		out = append(out, diffLine{kind: diffAdd, text: b[j]})
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func (a *App) kitDiffModal(c *ui.Context, row *agentRow, st *agentKitState) {
	if st.diffSkill == "" || st.status == nil {
		return
	}
	var skill *agentkit.SkillState
	for i := range st.status.Skills {
		if st.status.Skills[i].Skill == st.diffSkill {
			skill = &st.status.Skills[i]
			break
		}
	}
	if skill == nil {
		st.diffSkill = ""
		return
	}
	s := *skill
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 640).Children(func() {
			dialogTitle(ui.Text(c, T("kit.diff.title", "skill", s.Skill)))
			subPanel(ui.Column(c)).Gap(4).Children(func() {
				tone, label := skillStateMeta(s, c.Now())
				statusRow(c, tone, T("kit.diff.status"), label)
				mono(muted(ui.Text(c, s.Path))).FontSize(fsCaption).Wrap().Selectable()
				var meta []string
				if s.InstalledAt != nil {
					meta = append(meta, T("kit.diff.installed_at", "when", s.InstalledAt.Local().Format("2006-01-02 15:04")))
				}
				if s.DiskMTime != nil {
					meta = append(meta, T("kit.diff.disk_mtime", "when", s.DiskMTime.Local().Format("2006-01-02 15:04")))
				}
				if s.InstalledSHA256 != "" {
					meta = append(meta, T("kit.diff.installed_sha", "sha", shortSHA(s.InstalledSHA256)))
				}
				if s.DiskSHA256 != "" {
					meta = append(meta, T("kit.diff.disk_sha", "sha", shortSHA(s.DiskSHA256)))
				}
				if len(meta) > 0 {
					mono(muted(ui.Text(c, strings.Join(meta, T("common.dot_sep"))))).FontSize(fsCaption).Wrap().Selectable()
				}
			})
			muted(ui.Text(c, T("kit.diff.legend"))).FontSize(fsCaption)
			if s.DiskContent == "" && s.ShippedContent == "" {
				notice(c, "neutral", T("kit.diff.unavailable"))
			} else {
				lines := computeLineDiff(s.DiskContent, s.ShippedContent)
				ui.Scroll(c).MaxHeight(340).Children(func() {
					codeBox(ui.Column(c)).Gap(2).Children(func() {
						for i, ln := range lines {
							switch ln.kind {
							case diffDel:
								mono(ui.Text(c.Key("dl-"+itoa(i)), "- "+ln.text)).FontSize(fsCaption).TextColor(palette.Warn).Wrap().Selectable()
							case diffAdd:
								mono(ui.Text(c.Key("dl-"+itoa(i)), "+ "+ln.text)).FontSize(fsCaption).TextColor(palette.Ok).Wrap().Selectable()
							default:
								mono(muted(ui.Text(c.Key("dl-"+itoa(i)), "  "+ln.text))).FontSize(fsCaption).Wrap().Selectable()
							}
						}
					})
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				ui.Button(c, T("kit.diff.close")).OnClick(func() { st.diffSkill = "" })
				ui.PrimaryButton(c, T("kit.diff.go_update")).OnClick(func() {
					st.diffSkill = ""
					st.wireMCP = st.guide != nil && len(st.guide.MCPCommand) > 0
					st.restartSvc = st.guide != nil && st.guide.HasService
					st.installOpen = true
				})
			})
		})
	}).Dismissed() {
		st.diffSkill = ""
	}
}
