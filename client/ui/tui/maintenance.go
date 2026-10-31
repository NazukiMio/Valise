package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/revive"
)

// prevScreen lists kept pre-swap states and drops them on request.
type prevScreen struct {
	host   string
	list   []revive.PrevEntry
	cursor int
	loaded bool
}

func newPrev(host string) *prevScreen { return &prevScreen{host: host} }

type prevLoaded struct {
	list []revive.PrevEntry
	err  error
}

func (p *prevScreen) title() string { return "交换前状态" }
func (p *prevScreen) help() string {
	return "↑↓ 选择  ·  d 永久删除所选操作保留的状态"
}
func (p *prevScreen) init(m *Model) tea.Cmd {
	return func() tea.Msg {
		r, err := m.svc.PrevList(m.ctx, p.host)
		if err != nil {
			return prevLoaded{err: err}
		}
		return prevLoaded{list: r.Entries}
	}
}
func (p *prevScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case prevLoaded:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		p.list, p.loaded = msg.list, true
		p.cursor = min(p.cursor, max(0, len(p.list)-1))
		return nil, true
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			p.cursor = max(0, p.cursor-1)
		case "down", "j":
			p.cursor = min(len(p.list)-1, p.cursor+1)
		case "d":
			if p.cursor >= len(p.list) {
				return nil, true
			}
			op := p.list[p.cursor].OpID
			return m.push(confirm("永久删除操作 "+op+" 保留的交换前状态？删除后无法再退回到交换前。", nil, func(m *Model) tea.Cmd {
				return func() tea.Msg {
					removed, err := m.svc.PrevDrop(m.ctx, p.host, op)
					if err != nil {
						return failed(err)
					}
					m.send(flashMsg(fmt.Sprintf("已删除 %d 个目录", len(removed))))
					r, err := m.svc.PrevList(m.ctx, p.host)
					if err != nil {
						return prevLoaded{err: err}
					}
					return prevLoaded{list: r.Entries}
				}
			})), true
		default:
			return nil, false
		}
		return nil, true
	}
	return nil, false
}
func (p *prevScreen) view(m *Model) string {
	if !p.loaded {
		return dimStyle.Render("读取中…")
	}
	if len(p.list) == 0 {
		return "没有保留的交换前状态。"
	}
	var b strings.Builder
	for i, e := range p.list {
		b.WriteString(cursorLine(i == p.cursor, fmt.Sprintf("%s  %s  %9s  %s", e.OpID, e.Created.Local().Format("2006-01-02 15:04"), size(e.Bytes), e.Path)) + "\n")
	}
	return b.String()
}

// oldScreen lists instances superseded by a migrate or clone.
type oldScreen struct {
	host   string
	list   []revive.OldEntry
	cursor int
	loaded bool
}

func newOld(host string) *oldScreen { return &oldScreen{host: host} }

type oldLoaded struct {
	list []revive.OldEntry
	err  error
}

func (o *oldScreen) title() string { return "被取代的旧实例" }
func (o *oldScreen) help() string {
	return "↑↓ 选择  ·  d 清理所选（记录与保留的旧目录；金库快照不受影响）"
}
func (o *oldScreen) init(m *Model) tea.Cmd {
	return func() tea.Msg {
		r, err := m.svc.OldList(m.ctx, o.host)
		if err != nil {
			return oldLoaded{err: err}
		}
		return oldLoaded{list: r.Entries}
	}
}
func (o *oldScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case oldLoaded:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		o.list, o.loaded = msg.list, true
		o.cursor = min(o.cursor, max(0, len(o.list)-1))
		return nil, true
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			o.cursor = max(0, o.cursor-1)
		case "down", "j":
			o.cursor = min(len(o.list)-1, o.cursor+1)
		case "d":
			if o.cursor >= len(o.list) {
				return nil, true
			}
			id := o.list[o.cursor].InstanceID
			return m.push(confirm("永久删除旧实例 "+id+" 的记录、profile 副本和保留的旧目录？", []string{"金库中这个实例的快照不受影响，仍然可以用来复活。"}, func(m *Model) tea.Cmd {
				return func() tea.Msg {
					removed, err := m.svc.OldCleanup(m.ctx, o.host, id)
					if err != nil {
						return failed(err)
					}
					m.send(flashMsg(fmt.Sprintf("已删除 %d 项", len(removed))))
					r, err := m.svc.OldList(m.ctx, o.host)
					if err != nil {
						return oldLoaded{err: err}
					}
					return oldLoaded{list: r.Entries}
				}
			})), true
		default:
			return nil, false
		}
		return nil, true
	}
	return nil, false
}
func (o *oldScreen) view(m *Model) string {
	if !o.loaded {
		return dimStyle.Render("读取中…")
	}
	if len(o.list) == 0 {
		return "没有被取代的旧实例。"
	}
	var b strings.Builder
	for i, e := range o.list {
		s := e.Superseded
		b.WriteString(cursorLine(i == o.cursor, fmt.Sprintf("%s  由 %s 取代（%s，操作 %s）", e.InstanceID, s.By, s.At.Local().Format("2006-01-02 15:04"), s.OpID)) + "\n")
		for _, l := range e.Leftovers {
			b.WriteString(dimStyle.Render(fmt.Sprintf("      保留的旧目录 %s  %s", l.Path, size(l.Bytes))) + "\n")
		}
	}
	return b.String()
}
