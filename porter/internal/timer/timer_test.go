package timer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

func TestTimerUnits(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	for _, name := range []string{"systemctl", "loginctl"} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho \""+name+" $*\" >> "+log+"\n[ \"$1\" = show-user ] && echo yes\nexit 0\n"), 0o755)
	}
	e := &porterenroll.Env{Home: home, Vars: []string{"PATH=" + bin + ":/usr/bin:/bin", "USER=u"}}
	ctx := context.Background()
	if _, err := Set(ctx, e, home, vaultrpc.TimerSetParams{InstanceID: "demo", Enabled: true, Every: "1d"}); err == nil {
		t.Fatal("timer for an instance that is not enrolled")
	}
	porterenroll.WriteRecord(home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": home}}, profile.Profile{ID: "demo", ProfileVersion: 1})
	if _, err := Set(ctx, e, home, vaultrpc.TimerSetParams{InstanceID: "demo", Enabled: true, Every: "30m"}); err == nil {
		t.Fatal("interval under an hour accepted")
	}
	st, err := Set(ctx, e, home, vaultrpc.TimerSetParams{InstanceID: "demo", Enabled: true, Every: "1d"})
	if err != nil || !st.Enabled || st.Every != "1d" || !st.Linger || len(st.Units) != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	for _, u := range st.Units {
		b, _ := os.ReadFile(u)
		if strings.Contains(string(b), "Capability") {
			t.Fatalf("%s sets capabilities", u)
		}
		if strings.HasSuffix(u, ".service") && !strings.Contains(string(b), "NoNewPrivileges=yes") {
			t.Fatal("service without NoNewPrivileges")
		}
	}
	st, err = Set(ctx, e, home, vaultrpc.TimerSetParams{InstanceID: "demo", Enabled: false})
	if err != nil || st.Enabled {
		t.Fatalf("%+v %v", st, err)
	}
	if left, _ := filepath.Glob(filepath.Join(unitDir(home), "valise-seal@*")); len(left) != 0 {
		t.Fatal(left)
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"systemctl --user enable --now valise-seal@demo.timer", "systemctl --user disable --now valise-seal@demo.timer"} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("missing %q in %s", want, calls)
		}
	}
}
