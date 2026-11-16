package gui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/protection"
	"valise/core/valise"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

// Every location can be removed from its page, a server's own SSH host
// too: the dialog says what goes and that what it holds stays.
func TestEveryLocationOffersRemoval(t *testing.T) {
	a, tt := locWorld(t)
	st := a.locs().statuses["home"]
	st.Targets = append(st.Targets, vaultrpc.TargetState{Target: vaultrpc.Target{Name: "offsite", Kind: "rsync", Dest: "backup-host:/srv/valise"}})
	for name, kind := range map[string]string{"offsite": locSSH, "nas": locStation} {
		if l, ok := a.locationOf(name); !ok || l.Kind != kind {
			t.Fatalf("%s is %+v", name, l)
		}
		a.router.Push("/locations/" + name)
		tt.Frame()
		tt.Advance(time.Second)
		click(t, tt, T("loc.remove_open"))
		hasAll(t, tt.Texts(), T("loc.remove_title", "name", name), T("loc.remove_what."+kind), T("loc.remove_go"))
		click(t, tt, T("common.cancel"))
	}
}

func locationNames(a *App) []string {
	var out []string
	for _, l := range a.locations() {
		out = append(out, l.Name+"/"+l.Kind)
	}
	return out
}

// Over fake-ssh: removing a location goes to the server too, so it is not
// listed again from the server's status; a location the plan in force
// copies to is kept, with the server's reason shown, and nothing here is
// forgotten either.
func TestRemovingLocationsReachesTheServer(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
	dir := t.TempDir()
	s := &service.Service{Dirs: config.Dirs{Data: dir, Cache: t.TempDir()}, Config: config.Config{SSHBinary: e.SSH}, Home: dir, Version: "test"}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".demo/demo.json"), "{}")
	write(filepath.Join(home, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")
	write(filepath.Join(home, ".local/bin/systemctl"), "#!/bin/sh\nprintf 'LoadState=not-found\\n'\n")
	write(filepath.Join(home, ".local/bin/loginctl"), "#!/bin/sh\necho yes\n")
	write(filepath.Join(dir, "profiles/demo.yaml"), `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: demo } }
`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := s.Enroll(ctx, "box", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KeyInit("test-passphrase-1234"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "box", "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"offsite", "nas", "inuse"} {
		if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: name, Kind: "rsync", Dest: "sink:/backup/" + name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.SaveStations(dir, []config.Station{{Name: "nas", Address: "nas.home", User: "valise", Path: "/volume1/valise"}}); err != nil {
		t.Fatal(err)
	}
	a := New(s)
	a.run = func(f func()) { f() }
	a.refreshAgents()
	if len(a.agents.value.Items) != 1 || a.agents.value.Items[0].AgentRef == nil {
		t.Fatalf("agents %+v", a.agents.value)
	}
	ref := *a.agents.value.Items[0].AgentRef
	// The plan in force copies to "inuse".
	on := true
	d := protection.NewDefault()
	d.Schedule.DailyAt, d.Schedule.Timezone = "04:40", "UTC"
	d.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	d.ServerCopies = []protection.ServerCopy{{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "inuse", AuthorityID: ref.ServerUID}, Enabled: &on, MinimumInterval: "1h"}}
	change, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{AgentRef: ref, Desired: d})
	if err != nil {
		t.Fatal(err)
	}
	a.confirm(change.PlanID)
	if task, err := s.StartProtectionChange(ctx, change.PlanID, "accept"); err != nil || task.State != "succeeded" {
		t.Fatalf("plan %+v %v", task, err)
	}
	a.readLocations()
	if got := locationNames(a); !slices.Contains(got, "offsite/ssh") || !slices.Contains(got, "nas/station") || !slices.Contains(got, "inuse/ssh") {
		t.Fatalf("locations %q", got)
	}
	remove := func(name string) error {
		t.Helper()
		l, ok := a.locationOf(name)
		if !ok {
			t.Fatalf("no location %s in %q", name, locationNames(a))
		}
		ls := a.locs()
		ls.remove, ls.removeJ = name, job{}
		a.removeLocation(l)
		err := ls.removeJ.err
		a.readLocations() // what the server says now
		return err
	}
	// The server's own SSH host: gone from the server, not listed again.
	if err := remove("offsite"); err != nil {
		t.Fatal(err)
	}
	// Another machine: the server's record and this computer's.
	if err := remove("nas"); err != nil {
		t.Fatal(err)
	}
	if len(s.Stations()) != 0 {
		t.Fatalf("station still recorded here: %+v", s.Stations())
	}
	if got := locationNames(a); len(got) != 1 || got[0] != "inuse/ssh" {
		t.Fatalf("locations after removing two: %q", got)
	}
	targets, _ := os.ReadFile(filepath.Join(home, ".config/valise/targets.json"))
	if strings.Contains(string(targets), "offsite") || strings.Contains(string(targets), `"nas"`) || !strings.Contains(string(targets), "inuse") {
		t.Fatalf("server targets.json: %s", targets)
	}
	// In use by the plan: kept, and the reason is the server's.
	err = remove("inuse")
	if ue, ok := valise.As(err); !ok || ue.Code != "target_in_use" || !strings.Contains(ue.Suggestion, "保护计划") {
		t.Fatalf("in use: %v", err)
	}
	if got := locationNames(a); len(got) != 1 || got[0] != "inuse/ssh" || a.locs().remove != "inuse" {
		t.Fatalf("a refused removal changed the list or closed the dialog: %q", got)
	}
}
