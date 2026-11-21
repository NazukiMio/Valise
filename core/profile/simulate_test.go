package profile

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSimulateClassify(t *testing.T) {
	d := mustParse(t, "p.yaml", baseYAML)
	sim := SimulateClassify(d.Profile, []SampleEntry{
		{"state", ".env"},
		{"state", "memories/a.md"},
		{"state", "agent.sqlite"},
		{"state", "agent.sqlite-wal"},
		{"state", "channels/x"},
		{"state", "weird.bin"},
	})
	if sim.Total != 6 || sim.Counts[CategorySecrets] != 1 || sim.Counts[Unclassified] != 4 {
		t.Fatalf("%+v", sim.Counts)
	}
	byPath := map[string]SimulatedMatch{}
	for _, m := range sim.Matches {
		byPath[m.Path] = m
	}
	if m := byPath["agent.sqlite"]; !m.SQLite || !slices.Equal(m.SecretTables, []string{"auth_store"}) {
		t.Errorf("sqlite: %+v", m)
	}
	if m := byPath["agent.sqlite-wal"]; m.SidecarOf != "agent.sqlite" {
		t.Errorf("sidecar: %+v", m)
	}
	if !byPath["channels/x"].ExclusiveSecret {
		t.Error("exclusive")
	}
	if m := byPath[".env"]; slices.Contains(m.InTiers, "soul") || !slices.Contains(m.InTiers, "soul-secrets") {
		t.Errorf("tiers: %v", m.InTiers)
	}
	if len(sim.Unclassified) != 4 || len(sim.UnusedRules) == 0 {
		t.Errorf("%+v", sim)
	}
}

func TestLoadSampleEntries(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "s.txt")
	os.WriteFile(f, []byte("# c\n\n./a/b\nworkspace:/c\n"), 0o600)
	got, err := LoadSampleEntries(f)
	if err != nil || len(got) != 2 || got[0] != (SampleEntry{"state", "a/b"}) || got[1] != (SampleEntry{"workspace", "c"}) {
		t.Fatalf("%v %v", got, err)
	}
	os.WriteFile(f, []byte("../x\n"), 0o600)
	if _, err := LoadSampleEntries(f); err == nil {
		t.Error("want error for ..")
	}
	sub := t.TempDir()
	os.MkdirAll(filepath.Join(sub, "d"), 0o700)
	os.WriteFile(filepath.Join(sub, "d", "e"), nil, 0o600)
	got, err = LoadSampleEntries(sub)
	if err != nil || len(got) != 1 || got[0].Path != "d/e" {
		t.Fatalf("%v %v", got, err)
	}
}
