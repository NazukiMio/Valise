package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// confirmRemove asks before a copy location is forgotten: the record of it
// on every server this session has read that has one (a cloud drive's
// login on the server with it) and, for a location Valise manages, this
// computer's record. What the location holds is never deleted. A server
// refuses while a protection plan in force copies there
// (docs/ARCHITECTURE.md section 7); the servers go first, so a refusal
// leaves this computer's record alone.
func (c *cloudScreen) confirmRemove(m *Model, p place) *confirmScreen {
	// The current server first, then the others already read, in name order.
	kinds := map[string]string{}
	var hosts []string
	for host, d := range m.data {
		if d.status == nil {
			continue
		}
		for _, t := range d.status.Targets {
			if t.Name == p.name {
				kinds[host] = t.Kind
				hosts = append(hosts, host)
			}
		}
	}
	sort.Slice(hosts, func(i, j int) bool {
		if (hosts[i] == c.host) != (hosts[j] == c.host) {
			return hosts[i] == c.host
		}
		return hosts[i] < hosts[j]
	})
	where := c.host
	if len(hosts) > 0 {
		where = strings.Join(hosts, "、")
	}
	detail := []string{"删掉 " + where + " 上指向它的记录；它上面已有的备份不会删除。"}
	if p.station {
		detail = append(detail, "同时删掉这台电脑上它的记录与钥匙。")
	}
	detail = append(detail, "它正被保护计划使用时不会移除，要先在保护计划里取消这个位置。",
		"这次没有读到的服务器上如果还有它的记录，要等连上那台服务器后在它的页面移除。")
	return confirm("移除备份位置 "+p.name+"？", detail, func(m *Model) tea.Cmd {
		svc, ctx := m.svc, m.ctx
		c.target = 0
		var reload []tea.Cmd // read the servers again once it is done
		for _, h := range hosts {
			reload = append(reload, m.loadServer(h))
		}
		return func() tea.Msg {
			for _, h := range hosts {
				var err error
				if kinds[h] == "rclone" {
					err = svc.CloudRemove(ctx, h, p.name)
				} else {
					err = svc.TargetRemove(ctx, h, p.name)
				}
				if err != nil {
					return failed(err)
				}
			}
			if p.station {
				if err := svc.StationRemove(p.name); err != nil {
					return failed(err)
				}
			}
			done := func() tea.Msg { return flashMsg("已移除备份位置 " + p.name + "，它上面的备份保留") }
			return tea.Batch(append(reload, done)...)()
		}
	})
}
