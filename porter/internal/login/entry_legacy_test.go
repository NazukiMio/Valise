package login

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"valise/core/enroll"
	"valise/core/login"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
)

func TestLegacyEntryAuthDoesNotImportMetadata(t *testing.T) {
	home := t.TempDir()
	dir := porterenroll.InstancesDir(home)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": filepath.Join(home, "state")}}
	for name, value := range map[string]any{"demo.json": rec, "demo.profile.json": profile.Profile{ID: "demo", ProfileVersion: 1}} {
		b, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := login.EntryAuthParams{InstanceID: "demo", Entry: profile.Entry{ID: "web", Port: 9000, Path: "/ui/", Auth: &profile.EntryAuth{TokenArgv: profile.Argv{"/bin/echo", "fixture-token"}, URLSuffix: "#token=${TOKEN}"}}}
	got, err := EntryAuth(context.Background(), &porterenroll.Env{Home: home}, home, p)
	if err != nil || got.Suffix != "/ui/#token=fixture-token" {
		t.Fatal("old unconverted record lost its token command", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo.json")); err != nil {
		t.Fatal("token query archived old metadata", err)
	}
	if _, err := os.Stat(porterenroll.DossiersDir(home)); !os.IsNotExist(err) {
		t.Fatal("token query converted metadata", err)
	}
}
