package service

import "valise/core/enroll"

func runtimeServiceItem(v *enroll.RuntimeState) HealthItem {
	it := HealthItem{ID: "service", Group: HealthAgent, Title: "当前运行环境"}
	switch {
	case !v.Known:
		it.Level, it.Text = HealthWarn, "当前环境尚未核实；请刷新原恢复记录后再处理"
		if v.IssueCode == "environment_binding_changing" {
			it.Text = "运行环境的记录或状态有变化，请重新检查本次恢复的结果"
		}
	case v.GatewayPassed && v.Running && v.PortOwned && v.HTTPResponded:
		it.Level, it.Text = HealthOK, "当前环境正在运行，网关检查通过"
		it.Detail = "网关检查与实际对话确认分别记录。"
	case v.Running:
		it.Level, it.Text = HealthWarn, "当前环境正在运行，网关尚未通过检查"
	case v.Stopped:
		it.Level, it.Text = HealthBad, "当前环境已停止；请查看原恢复记录中的运行结果"
	default:
		it.Level, it.Text = HealthWarn, "当前环境的运行状态尚未核实"
	}
	return it
}

// RuntimeHealthLines is the compact text projection for CLI/TUI. Each fact
// stays independent; GUI uses the same fields with its localized catalog.
func RuntimeHealthLines(v *enroll.RuntimeState) []string {
	if v == nil {
		return nil
	}
	if !v.Known || v.Slot != "A" && v.Slot != "B" {
		return []string{"当前运行环境：尚未核实"}
	}
	kind := "独立程序"
	if v.Native {
		kind = "原启动入口"
	}
	running, gateway, conversation := "尚未核实", "尚未通过检查", "尚无确认记录"
	if v.Running {
		running = "运行中"
	} else if v.Stopped {
		running = "已停止"
	}
	if v.Running && v.PortOwned && v.HTTPResponded && v.GatewayPassed {
		gateway = "检查通过"
	}
	if v.ConversationConfirmed {
		conversation = "你已确认可以对话"
	}
	return []string{"当前环境：" + v.Slot + " · " + kind, "运行：" + running + "；网关：" + gateway, "对话确认：" + conversation}
}
