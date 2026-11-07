package tui

import tea "charm.land/bubbletea/v2"

func actionPendingBanner(d *srvData) string {
	if d.pendingActionsErr != nil {
		return warnStyle.Render("未能读取配置操作记录；按 r 重试") + "\n\n"
	}
	if len(d.pendingActions) == 0 {
		return ""
	}
	return warnStyle.Render("发现未完成的配置操作；按 a 查看当前配置与恢复选择") + "\n\n"
}
func (s *serverPage) openPendingActions(m *Model) tea.Cmd {
	d := m.server(s.host)
	if len(d.pendingActions) == 0 {
		return nil
	}
	if len(d.pendingActions) == 1 {
		return m.push(newActionOperation(s.host, d.pendingActions[0].OperationID))
	}
	var options []choice
	for _, j := range d.pendingActions {
		state := "配置尚未发布"
		if j.Published {
			state = "配置已经发布"
		}
		options = append(options, choice{label: j.InstanceID + " · " + state, value: j.OperationID})
	}
	return m.push(pick("选择未完成的配置操作", options, func(m *Model, op string) tea.Cmd { return m.push(newActionOperation(s.host, op)) }))
}
