package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"valise/core/agentkit"
	"valise/ui/service"
)

// Responses belong to a particular page and a particular refresh. A page
// beneath a dialog still receives its own results; a popped page does not.
type readStamp struct {
	page *instancePage
	seq  uint64
}
type readState struct {
	seq     uint64
	loading bool
	err     error
	at      time.Time
}

func (s *readState) begin(p *instancePage) readStamp {
	s.seq++
	s.loading = true
	s.err = nil
	return readStamp{p, s.seq}
}

type checkLoaded struct {
	c     *agentkit.CheckupResult
	err   error
	stamp readStamp
}
type healthLoaded struct {
	r     *service.HealthReport
	err   error
	stamp readStamp
}

func (p *instancePage) loadHealth(m *Model) tea.Cmd {
	stamp := p.healthRead.begin(p)
	p.healthErr = nil
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { r, err := svc.Health(ctx, host, inst); return healthLoaded{r, err, stamp} }
}
func (p *instancePage) loadCheckup(m *Model) tea.Cmd {
	stamp := p.checkRead.begin(p)
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { c, err := svc.Checkup(ctx, host, inst); return checkLoaded{c, err, stamp} }
}
func (p *instancePage) receiveRead(m *Model, msg tea.Msg) {
	switch v := msg.(type) {
	case checkLoaded:
		if v.stamp.page != p || v.stamp.seq != p.checkRead.seq {
			return
		}
		p.checkRead.loading, p.checkRead.err = false, v.err
		if v.err == nil {
			p.check = v.c
			p.checkRead.at = m.now()
		}
	case healthLoaded:
		if v.stamp.page != p || v.stamp.seq != p.healthRead.seq {
			return
		}
		p.healthRead.loading, p.healthRead.err, p.healthErr = false, v.err, v.err
		if v.err == nil {
			p.health = v.r
			p.healthRead.at = m.now()
		}
	}
}

func (m *Model) routeRead(msg tea.Msg, stamp readStamp) tea.Cmd {
	for _, s := range m.stack {
		if p, ok := s.(*instancePage); ok && p == stamp.page {
			p.receiveRead(m, msg)
			break
		}
	}
	return nil
}

// healthNotices are completeness facts, never dismissible findings.
func (p *instancePage) healthNotices() []string {
	var out []string
	add := func(title string, s readState, missing bool) {
		last := ""
		if !s.at.IsZero() {
			last = "；上次结果 " + s.at.Local().Format("01-02 15:04:05")
		}
		switch {
		case s.err != nil:
			out = append(out, title+"刷新失败"+last+"；"+service.HealthReadError(s.err))
		case s.loading || missing:
			out = append(out, title+"正在读取"+last)
		}
	}
	hs := p.healthRead
	if p.healthErr != nil {
		hs.err = p.healthErr
	}
	add("健康状态", hs, p.health == nil)
	add("会话与记忆检查", p.checkRead, p.check == nil)
	if p.health != nil {
		for _, c := range p.health.Checks {
			if c.State == "failed" {
				out = append(out, c.Title+"："+c.Reason)
			}
		}
	}
	return out
}

func needsView(m *Model, p *instancePage, d *srvData) string {
	var lines []string
	for _, a := range attention(m, d, p.inst) {
		lines = append(lines, warnStyle.Render("  ! "+a))
	}
	tabNo := ""
	for i, t := range p.tabs {
		if _, ok := t.(*healthTab); ok {
			tabNo = itoa(i + 1)
		}
	}
	for _, it := range topHealth(m, p, 2) {
		st := warnStyle
		if it.Level == service.HealthBad {
			st = errStyle
		}
		lines = append(lines, st.Render("  "+levelMarkPlain(it.Level)+" "+it.Title+"："+it.Text)+dimStyle.Render("  · 详见 "+tabNo+" 健康"))
	}
	quiet := 0
	for _, it := range (&healthTab{}).rows(m, p) {
		if it.Quiet() {
			quiet++
		}
	}
	note := ""
	if quiet > 0 {
		note = dimStyle.Render("（另有 " + itoa(quiet) + " 项已忽略，见 " + tabNo + " 健康）")
	}
	notices := p.healthNotices()
	if len(notices) > 0 {
		lines = append(lines, warnStyle.Render("  ! 部分检查未完成，暂无法完整判断"))
		for _, n := range notices {
			lines = append(lines, dimStyle.Render("    "+n))
		}
	}
	if len(lines) > 0 {
		return warnStyle.Render("需要处理") + note + "\n" + strings.Join(lines, "\n") + "\n\n"
	}
	return dimStyle.Render("需要处理：无") + note + "\n\n"
}
