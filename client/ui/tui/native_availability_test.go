package tui

import (
	"strings"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/ui/service"
)

func TestNativeEntriesRequireAcceptedServerAvailability(t *testing.T) {
	p := &instancePage{host: "box", inst: enroll.InstanceSummary{InstanceID: "demo"}, target: &service.LoginTarget{Profile: profile.Profile{ID: "demo", NativeCLI: []string{"fixture-native"}}}}
	for _, available := range []bool{false, true} {
		p.inst.NativeCLI = available
		views := map[string][]string{}
		for _, row := range (&overviewTab{}).quicks(p) {
			views["overview"] = append(views["overview"], row.label)
		}
		for _, row := range (&chatTab{}).entries(p) {
			views["chat"] = append(views["chat"], row.label)
		}
		for _, row := range (&manageTab{}).rows(&Model{}, p) {
			views["manage"] = append(views["manage"], row.label)
		}
		for name, labels := range views {
			if shown := strings.Contains(strings.Join(labels, "\n"), "原生界面"); shown != available {
				t.Fatal("native action did not follow accepted server availability", name, available, labels)
			}
		}
	}
}
