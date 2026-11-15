package enroll

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
)

// One dossier per installation: agents share the profile copy; a named
// agent gets its effective profile; legacy records move in once and their
// files are kept under another name; removing the last agent removes the
// dossier.
func TestDossier(t *testing.T) {
	home := t.TempDir()
	p := profile.Profile{ID: "demo", ProfileVersion: 2,
		Service: &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "demo.service"}}},
		Detect:  &profile.Detect{Named: []profile.Named{{Glob: "${HOME}/.demo/profiles/*", Env: map[string]string{"DEMO_HOME": "${STATE_DIR}"}, Service: "shared"}}}}

	// A legacy record from before dossiers.
	dir := InstancesDir(home)
	os.MkdirAll(dir, 0o700)
	old := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": "/h/.demo"},
		Service: []enroll.ServiceState{{Unit: "demo.service", Active: "active"}}}
	b, _ := json.Marshal(old)
	os.WriteFile(filepath.Join(dir, "demo.json"), b, 0o600)
	pb, _ := json.Marshal(profile.Profile{ID: "demo", ProfileVersion: 1})
	os.WriteFile(filepath.Join(dir, "demo.profile.json"), pb, 0o600)

	got, err := ReadRecord(home, "demo")
	if err != nil || got == nil || got.Roots["state"] != "/h/.demo" || got.Service[0].Unit != "demo.service" {
		t.Fatalf("old record not readable: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo.json")); err != nil {
		t.Fatal("query renamed legacy record")
	}
	if _, err := os.Stat(DossierPath(home, "demo")); !os.IsNotExist(err) {
		t.Fatal("query materialized a dossier", err)
	}

	// Re-enrolling with the new profile, then a named agent.
	old.ProfileVersion = 2
	if _, err := WriteRecord(home, old, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo.legacy.json")); err != nil {
		t.Fatal("explicit write did not retain legacy record")
	}
	bot := enroll.Record{InstanceID: "demo-bot", ProfileID: "demo", ProfileVersion: 2, Name: "bot", Env: map[string]string{"DEMO_HOME": "/h/.demo/profiles/bot"},
		Roots: map[string]string{"state": "/h/.demo/profiles/bot"}, Service: []enroll.ServiceState{{Unit: "demo.service", Active: "active"}}}
	if _, err := WriteRecord(home, bot, p); err != nil {
		t.Fatal(err)
	}
	ds, _ := allDossiers(home)
	if len(ds) != 1 || len(ds[0].Agents) != 2 {
		t.Fatalf("%+v", ds)
	}
	bp, err := ReadProfile(home, "demo-bot")
	if err != nil || bp.Service == nil || bp.Provenance != nil {
		t.Fatalf("named agent's profile: %+v %v", bp, err)
	}
	if r, _ := ReadRecord(home, "demo-bot"); r.Name != "bot" || r.Env["DEMO_HOME"] == "" || r.Provenance.Kind != "" {
		t.Fatalf("%+v", r)
	}
	if list, _ := ListRecords(home); len(list) != 2 {
		t.Fatalf("%+v", list)
	}

	// Cleanup: the dossier stays while an agent is left, and the legacy
	// file does not bring a removed agent back.
	if err := RemoveRecord(home, "demo"); err != nil {
		t.Fatal(err)
	}
	if r, _ := ReadRecord(home, "demo"); r != nil {
		t.Fatal("removed agent came back")
	}
	if err := RemoveRecord(home, "demo-bot"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DossierPath(home, "demo")); !os.IsNotExist(err) {
		t.Fatal("empty dossier kept")
	}
}

// Legacy records of one installation: the default agent's profile copy is
// the installation's, even when a named agent's (stripped) copy sorts
// first.
func TestDossierMigrationPrefersDefault(t *testing.T) {
	home := t.TempDir()
	dir := InstancesDir(home)
	os.MkdirAll(dir, 0o700)
	put := func(id, name string, p profile.Profile) {
		b, _ := json.Marshal(enroll.Record{InstanceID: id, ProfileID: "demo", ProfileVersion: 3, Name: name, Roots: map[string]string{"state": "/x/" + id}})
		os.WriteFile(filepath.Join(dir, id+".json"), b, 0o600)
		pb, _ := json.Marshal(p)
		os.WriteFile(filepath.Join(dir, id+".profile.json"), pb, 0o600)
	}
	full := profile.Profile{ID: "demo", ProfileVersion: 3, Service: &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "demo.service"}}}}
	put("demo-a", "a", profile.Profile{ID: "demo", ProfileVersion: 3}) // stripped
	put("demo", "", full)
	p, err := ReadProfile(home, "demo")
	if err != nil || p.Service == nil {
		t.Fatalf("installation copy lost the service: %+v %v", p, err)
	}
}

func TestReadInstallationDoesNotMigrateAndRejectsAmbiguousOwner(t *testing.T) {
	home := t.TempDir()
	legacy := InstancesDir(home)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	r := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": filepath.Join(home, ".demo")}}
	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	rb, _ := json.Marshal(r)
	pb, _ := json.Marshal(p)
	for name, b := range map[string][]byte{"demo.json": rb, "demo.profile.json": pb} {
		if err := os.WriteFile(filepath.Join(legacy, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if d, _, err := ReadInstallation(home, "demo"); err != nil || d != nil {
		t.Fatal("metadata collection migrated a legacy registration", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "demo.json")); err != nil {
		t.Fatal("metadata collection renamed a legacy record")
	}
	if _, err := WriteRecord(home, r, p); err != nil {
		t.Fatal(err)
	}
	d, _, err := ReadInstallation(home, "demo")
	if err != nil || d == nil {
		t.Fatal(err)
	}
	// One instance appearing in two dossiers cannot choose an origin based on
	// directory iteration order. No source profile is read from an arbitrary id.
	d.InstallID = "other"
	if err := writeDossier(home, d); err != nil {
		t.Fatal(err)
	}
	if d, _, err := ReadInstallation(home, "demo"); err == nil || d != nil {
		t.Fatal("ambiguous installation silently picked a parent")
	}
}
