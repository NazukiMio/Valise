package tui

import (
	"context"
	"strings"
	"testing"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/profile"
	"valise/ui/service"
)

func selectDirectForTUITest(t *testing.T, m *Model) {
	t.Helper()
	ctx := context.Background()
	v, err := m.svc.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	previous := m.svc.OwnerAuth.Passphrase
	m.svc.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	defer func() { m.svc.OwnerAuth.Passphrase = previous }()
	if _, err := m.svc.SaveEnvironmentSettings(ctx, "box", v, environment.Direct); err != nil {
		t.Fatal(err)
	}
}

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
