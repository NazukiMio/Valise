package tui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"valise/core/enroll"
)

// Instance cards and horizontal paging (docs/UI.md 8.1): every card in a
// view has the same size, one row of cards, ←→ moves, [ ] turns the page.
const (
	cardW        = 52 // outer width of a standard card
	cardH        = 14
	compactCardW = 36
	compactCardH = 8
	cardGap      = 2
)

// compactCards reports whether the terminal is too small for standard
// cards; then every card in the view is compact.
func compactCards(width, height int) bool { return width < 80 || height < 24 }

// perPage is how many cards of width w fit in one row.
func perPage(width, w int) int { return max(1, (width+cardGap)/(w+cardGap)) }

// cardFrame draws a card of exactly w×h, frame included (lipgloss v2
// sizes include the border): a heavy frame when selected.
func cardFrame(body string, w, h int, selected, muted bool) string {
	b, c := lipgloss.RoundedBorder(), cLine
	if selected {
		b, c = lipgloss.ThickBorder(), cSelLine
	}
	st := lipgloss.NewStyle().Border(b).BorderForeground(c).Padding(0, 1).Width(w).Height(h).MaxHeight(h)
	if muted {
		st = st.Foreground(cDim)
	}
	return st.Render(body)
}

// fitLines cuts every line to width and the list to n lines.
func fitLines(lines []string, width, n int) string {
	if len(lines) > n {
		lines = lines[:n]
	}
	for i, l := range lines {
		lines[i] = lipgloss.NewStyle().MaxWidth(width).Render(l)
	}
	return strings.Join(lines, "\n")
}

// instanceCard is a standard (or compact) card for one instance.
func instanceCard(m *Model, d *srvData, in enroll.InstanceSummary, selected, compact bool) string {
	dp := m.display(in.ProfileID)
	name := displayName(m, in.ProfileID)
	attn := attention(m, d, in)
	if compact {
		icon := compactIcon(dp)
		info := []string{headStyle.Render(in.InstanceID), runStateShort(in)}
		switch {
		case in.Superseded != nil:
			info = append(info, warnStyle.Render("已被取代"))
		case len(attn) > 0:
			info = append(info, entryState(in), warnStyle.Render("! "+attn[0]))
		default:
			info = append(info, entryState(in), backupLine(m, d, in, true))
		}
		inner := compactCardW - 4
		body := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(icon, "\n"), "  ", fitLines(info, inner-compactIconW-2, compactCardH-2))
		return cardFrame(body, compactCardW, compactCardH, selected, in.Superseded != nil)
	}
	icon := standardIcon(dp)
	info := []string{headStyle.Render(in.InstanceID), dimStyle.Render(name)}
	if in.ProfileStatus != "verified" {
		info = append(info, faintStyle.Render("（profile 未核实）"))
	} else {
		info = append(info, "")
	}
	if in.Superseded != nil {
		info = append(info, warnStyle.Render("已被取代"), dimStyle.Render("被 "+in.Superseded.By+" 取代"))
	} else {
		info = append(info, runState(in), entryState(in), "")
		if len(attn) == 0 {
			info = append(info, dimStyle.Render("需要处理：无"))
		} else {
			info = append(info, warnStyle.Render("需要处理："))
			for _, a := range attn[:min(2, len(attn))] {
				info = append(info, warnStyle.Render("! "+a))
			}
		}
		info = append(info, "", backupLine(m, d, in, false))
	}
	inner := cardW - 4
	body := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(icon, "\n"), "  ", fitLines(info, inner-iconW-2, cardH-2))
	return cardFrame(body, cardW, cardH, selected, in.Superseded != nil)
}

func displayName(m *Model, profileID string) string {
	if m.prof != nil && m.prof.ByID[profileID] != nil {
		return m.prof.ByID[profileID].Effective.DisplayName
	}
	return profileID
}

// entryState says whether the management page can be reached.
func entryState(in enroll.InstanceSummary) string {
	if len(in.Entries) == 0 {
		return dimStyle.Render("没有管理页")
	}
	for _, e := range in.Entries {
		if !e.Listening {
			return warnStyle.Render("管理页 ✗ 打不开")
		}
	}
	return "管理页 " + okStyle.Render("✓") + " 可打开"
}

// attention lists what needs the user, most important first.
func attention(m *Model, d *srvData, in enroll.InstanceSummary) []string {
	if in.Superseded != nil {
		return nil
	}
	var out []string
	for _, s := range in.Service {
		if s.LoadState == "loaded" && s.Active != "active" {
			out = append(out, "服务已停止")
			break
		}
	}
	for _, e := range in.Entries {
		if !e.Listening {
			out = append(out, "管理页打不开")
			break
		}
	}
	t, ok := d.lastSeal(in.InstanceID)
	switch {
	case !ok:
		out = append(out, "还没有逃生舱")
	case m.now().Sub(t) > 7*24*time.Hour:
		out = append(out, itoa(int(m.now().Sub(t).Hours()/24))+" 天没有建立逃生舱")
	}
	return out
}

// backupLine is the quiet one-line backup state (docs/UI.md section 9).
func backupLine(m *Model, d *srvData, in enroll.InstanceSummary, short bool) string {
	t, ok := d.lastSeal(in.InstanceID)
	if !ok {
		return warnStyle.Render("还没有逃生舱")
	}
	station := ""
	if d.status != nil && len(d.status.Targets) > 0 {
		station = " · 空间站 " + mark(d.synced(lastSnapshotID(d, in.InstanceID)), true)
	}
	if short {
		return dimStyle.Render("备份 " + ago(t, m.now()) + station)
	}
	return dimStyle.Render("备份正常") + "\n" + dimStyle.Render(ago(t, m.now())+station)
}

// pager lays out one page of equally sized cards with the page dots
// below, centered in width.
func pager(cards []string, cursor, width, w int) string {
	if len(cards) == 0 {
		return ""
	}
	n := perPage(width, w)
	page, pages := cursor/n, (len(cards)+n-1)/n
	from, to := page*n, min(len(cards), page*n+n)
	row := lipgloss.JoinHorizontal(lipgloss.Top, intersperse(cards[from:to], strings.Repeat(" ", cardGap))...)
	var dots []string
	for i := 0; i < pages; i++ {
		if i == page {
			dots = append(dots, accStyle.Render("●"))
		} else {
			dots = append(dots, dimStyle.Render("○"))
		}
	}
	line := dimStyle.Render("‹ ") + strings.Join(dots, " ") + dimStyle.Render("  "+itoa(page+1)+"/"+itoa(pages)+" ›")
	center := lipgloss.NewStyle().Width(width).Align(lipgloss.Center)
	return center.Render(row) + "\n" + center.Render(line)
}

// movePager moves a paged cursor: ←→ one card, [ ] one page.
func movePager(cursor, n, width, w int, key string) (int, bool) {
	per := perPage(width, w)
	switch key {
	case "left":
		cursor--
	case "right":
		cursor++
	case "[":
		cursor = (cursor/per - 1) * per
	case "]":
		cursor = (cursor/per + 1) * per
	default:
		return cursor, false
	}
	return max(0, min(n-1, cursor)), true
}

func runState(in enroll.InstanceSummary) string {
	if len(in.Service) == 0 {
		return dimStyle.Render("● 没有服务（前台运行）")
	}
	for _, s := range in.Service {
		if s.Active == "active" {
			return okStyle.Render("● 运行中")
		}
	}
	return errStyle.Render("● 已停止")
}

// runStateShort fits a compact card.
func runStateShort(in enroll.InstanceSummary) string {
	if len(in.Service) == 0 {
		return dimStyle.Render("● 前台运行")
	}
	return runState(in)
}

func lastSnapshotID(d *srvData, inst string) string {
	best := ""
	if d.status == nil {
		return ""
	}
	for _, s := range d.status.Snapshots {
		if s.InstanceID == inst && s.SnapshotID > best {
			best = s.SnapshotID
		}
	}
	return best
}
