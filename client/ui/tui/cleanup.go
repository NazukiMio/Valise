package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/revive"
	"valise/core/vault"
	"valise/ui/service"
)

// cleanupScreen gathers what can be deleted on a server to save space,
// each with what deleting it means (docs/UI.md 9.3 "空间清理").
type cleanupScreen struct {
	host   string
	cursor int
	prev   *revive.PrevListResult
	old    *revive.OldListResult
	loaded int
}

type prevLoaded struct{ r *revive.PrevListResult }
type oldLoaded struct{ r *revive.OldListResult }

type cleanItem struct {
	kind, label, what string
	bytes             int64
	act               func(m *Model) tea.Cmd
}

func newCleanup(host string) *cleanupScreen { return &cleanupScreen{host: host} }

func (c *cleanupScreen) title() string { return "空间清理" }
func (c *cleanupScreen) help() string {
	return menu("↑↓", "选择", "d", "删除选中项", "r", "刷新")
}

func (c *cleanupScreen) init(m *Model) tea.Cmd {
	svc, ctx, host := m.svc, m.ctx, c.host
	c.loaded = 0
	return tea.Batch(
		func() tea.Msg { r, _ := svc.PrevList(ctx, host); return prevLoaded{r} },
		func() tea.Msg { r, _ := svc.OldList(ctx, host); return oldLoaded{r} },
	)
}

func (c *cleanupScreen) items(m *Model) []cleanItem {
	var out []cleanItem
	host := c.host
	if c.prev != nil {
		for _, e := range c.prev.Entries {
			e := e
			out = append(out, cleanItem{kind: "恢复前的旧状态", label: e.Path + dimStyle.Render("（"+e.Created.Local().Format("01-02 15:04")+"）"), bytes: e.Bytes,
				what: "这是某次恢复之前 agent 的样子。删除后就不能再撤回那次恢复；备份不受影响。",
				act: func(m *Model) tea.Cmd {
					return m.push(confirm("删除这份恢复前的旧状态？", []string{e.Path}, func(m *Model) tea.Cmd {
						svc, ctx := m.svc, m.ctx
						return func() tea.Msg {
							if _, err := svc.PrevDrop(ctx, host, e.OpID); err != nil {
								return failed(err)
							}
							return refreshed{host, "已删除恢复前的旧状态"}
						}
					}))
				}})
		}
	}
	if c.old != nil {
		for _, e := range c.old.Entries {
			e := e
			var n int64
			for _, l := range e.Leftovers {
				n += l.Bytes
			}
			out = append(out, cleanItem{kind: "已被取代的旧实例", label: e.InstanceID + dimStyle.Render("（被 "+e.Superseded.By+" 取代）"), bytes: n,
				what: "删除它的实例记录和那次操作保留的旧目录；它的备份还在备份库里。",
				act: func(m *Model) tea.Cmd {
					return m.push(confirm("永久删除旧实例 "+e.InstanceID+" 的记录和旧目录？", nil, func(m *Model) tea.Cmd {
						svc, ctx := m.svc, m.ctx
						return func() tea.Msg {
							if _, err := svc.OldCleanup(ctx, host, e.InstanceID); err != nil {
								return failed(err)
							}
							return refreshed{host, "已清理旧实例 " + e.InstanceID}
						}
					}))
				}})
		}
	}
	if st := m.server(host).status; st != nil {
		for _, l := range st.Leaves {
			if l.Current || vault.IsSavepointLeaf(l.Leaf) {
				continue
			}
			l := l
			label := strings.Replace(l.Leaf, "_", " ~ ", 1)
			if l.Leaf == vault.LegacyLeaf {
				label = "分档之前的旧备份库"
			}
			out = append(out, cleanItem{kind: "服务器上已结束的周期", label: label + dimStyle.Render("（"+itoa(len(l.Snapshots))+" 个备份）"), bytes: l.Bytes,
				what: "只删除服务器上的这一份；备份位置上的副本不受影响。删除前会告诉你其他地方有没有完整副本。",
				act: func(m *Model) tea.Cmd {
					return confirmLeafDelete(m, host, l.Leaf, "server", func(m *Model) tea.Cmd { return m.loadServer(host) })
				}})
		}
	}
	return out
}

func (c *cleanupScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case prevLoaded:
		c.prev = msg.r
		c.loaded++
		return nil, true
	case oldLoaded:
		c.old = msg.r
		c.loaded++
		return nil, true
	case srvLoaded:
		return c.init(m), false
	case tea.KeyPressMsg:
		items := c.items(m)
		if cur, moved := moveList(c.cursor, len(items), msg.String()); moved {
			c.cursor = cur
			return nil, true
		}
		switch msg.String() {
		case "d":
			if c.cursor < len(items) {
				return items[c.cursor].act(m), true
			}
			return nil, true
		case "r":
			return tea.Batch(m.loadServer(c.host), c.init(m)), true
		}
	}
	return nil, false
}

func (c *cleanupScreen) view(m *Model) string {
	if c.loaded < 2 {
		return dimStyle.Render("读取中…")
	}
	items := c.items(m)
	c.cursor = min(c.cursor, max(0, len(items)-1))
	head := dimStyle.Render("这些东西可以删除以节约空间。每一项都要你确认，Valise 不会自动删除。")
	if len(items) == 0 {
		return head + "\n\n" + okStyle.Render("没有可以清理的东西。")
	}
	var rows []string
	for i, it := range items {
		rows = append(rows, cursorLine(i == c.cursor, padRight(dimStyle.Render(it.kind), 22)+padRight(size(it.bytes), 10)+it.label))
	}
	w := min(m.width-2, 100)
	detail := box("删除后会怎样", items[c.cursor].what, w)
	return head + "\n\n" + scrollLines(rows, c.cursor, m.bodyHeight()-6) + "\n\n" + detail
}

// confirmLeafDelete checks every other copy of a period again
// (docs/ARCHITECTURE.md 7.1.1), then asks; with no other complete copy it
// asks twice (docs/VAULT_FORMAT.md 10.4). where is "server" or a target
// name. Places that need pack downloads are first read without them; the
// person then chooses to download and check, or to go on with those places
// unchecked.
func confirmLeafDelete(m *Model, host, leaf, where string, after func(m *Model) tea.Cmd) tea.Cmd {
	return verifyThenAsk(m, host, leaf, where, false, after)
}

func verifyThenAsk(m *Model, host, leaf, where string, download bool, after func(m *Model) tea.Cmd) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	m.flash, m.flashErr = "正在核对这个周期在各处的副本…", false
	return func() tea.Msg {
		var pending []string
		var total int64
		o := service.VerifyOptions{Packs: true, Skip: map[string]bool{where: true}}
		if !download {
			o.Confirm = func(place string, bytes int64) bool {
				pending, total = append(pending, place), total+bytes
				return false
			}
		}
		rep, err := svc.VerifyCopies(ctx, host, leaf, o)
		if err != nil {
			return failed(err)
		}
		detail := append([]string{"这个周期在各处的副本："}, indent(rep.Lines())...)
		if len(pending) > 0 {
			return pushMsg{pick("核对 "+strings.Join(pending, "、")+" 的数据包要下载约 "+size(total), []choice{
				{label: "下载并核对", value: "download"},
				{label: "不核对，继续", value: "skip", desc: "这些位置按未核对处理，不算完整副本"},
			}, func(m *Model, v string) tea.Cmd {
				if v == "download" {
					return verifyThenAsk(m, host, leaf, where, true, after)
				}
				return m.push(deleteAsk(svc, ctx, host, leaf, where, rep, detail, after))
			})}
		}
		return pushMsg{deleteAsk(svc, ctx, host, leaf, where, rep, detail, after)}
	}
}

func indent(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + l
	}
	return out
}

// deleteAsk is the confirmation itself, doubled when no other place holds
// a complete copy.
func deleteAsk(svc *service.Service, ctx context.Context, host, leaf, where string, rep *service.CopyReport, detail []string, after func(m *Model) tea.Cmd) screen {
	place := "服务器上"
	if where != "server" {
		place = where + " 上"
	}
	doDelete := func(m *Model) tea.Cmd {
		return func() tea.Msg {
			var err error
			if where == "server" {
				_, err = svc.LeafDelete(ctx, host, leaf)
			} else {
				err = svc.TargetLeafDelete(ctx, host, where, leaf)
			}
			if err != nil {
				return failed(err)
			}
			m.send(flashMsg("已删除" + place + "的周期 " + leaf))
			return after(m)()
		}
	}
	ask := confirm("删除"+place+"的周期 "+leaf+"？", detail, doDelete)
	if !rep.CompleteElsewhere(where) {
		return confirm("其他地方都没有核对过的完整副本。删除后，这些备份将无法恢复。确定继续？", detail,
			func(m *Model) tea.Cmd { return m.push(ask) })
	}
	return ask
}
