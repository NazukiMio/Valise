package enroll

import (
	"slices"
	"strings"
	"testing"
	"time"

	"valise/core/profile"
)

func TestValidRootIDAndToolName(t *testing.T) {
	for _, ok := range []string{"ext-notes", "obsidian", "a", "a1-b2-c3", strings.Repeat("a", 48)} {
		if err := ValidRootID(ok); err != nil {
			t.Errorf("ValidRootID(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "state", "body_patch", "Ext-Notes", "a_b", "-abc", "abc-", "a--b", strings.Repeat("a", 49)} {
		if err := ValidRootID(bad); err == nil {
			t.Errorf("ValidRootID(%q) should have failed", bad)
		}
	}

	for _, ok := range []string{"ffmpeg", "yt-dlp", "g++", "python3.12", "my_tool", strings.Repeat("x", 64)} {
		if err := ValidToolName(ok); err != nil {
			t.Errorf("ValidToolName(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/usr/bin/ffmpeg", "a/b", "my tool", "-flag", ".hidden", strings.Repeat("x", 65)} {
		if err := ValidToolName(bad); err == nil {
			t.Errorf("ValidToolName(%q) should have failed", bad)
		}
	}
}

func TestRecordFromPreservesDossierClosureFields(t *testing.T) {
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	prev := &Record{
		InstanceID:  "demo",
		DisplayName: "Traveler",
		LocalRoots: []LocalRoot{
			{ID: "ext-notes", Path: "/home/u/notes", Category: profile.CategoryMemory, Reason: "personal notes"},
		},
		ToolDeps: []ToolDep{
			{Name: "ffmpeg", Purpose: "audio transcode", Reason: "voice skill"},
		},
	}
	rep := &Report{
		InstanceID:     "demo",
		ProfileID:      "demo",
		ProfileVersion: 2,
		Roots:          []Root{{ID: "state", Path: "/home/u/.demo", Exists: true}},
		Previous:       prev,
	}
	rec := RecordFrom(rep, now)
	if rec.DisplayName != "Traveler" {
		t.Fatalf("DisplayName = %q, want Traveler", rec.DisplayName)
	}
	if rec.Roots["ext-notes"] != "/home/u/notes" {
		t.Fatalf("LocalRoot missing from Roots map: %+v", rec.Roots)
	}
	if len(rec.LocalRoots) != 1 || rec.LocalRoots[0].ID != "ext-notes" {
		t.Fatalf("LocalRoots = %+v", rec.LocalRoots)
	}
	if len(rec.ToolDeps) != 1 || rec.ToolDeps[0].Name != "ffmpeg" {
		t.Fatalf("ToolDeps = %+v", rec.ToolDeps)
	}

	// Dossier Put & Record round-trip, plus nil preservation vs explicit empty clearing.
	var d Dossier
	d.Put(rec, now)
	got := d.Record("demo")
	if got == nil || len(got.LocalRoots) != 1 || len(got.ToolDeps) != 1 || got.DisplayName != "Traveler" {
		t.Fatalf("Dossier.Record round-trip failed: %+v", got)
	}

	// Re-enroll with nil LocalRoots/ToolDeps preserves old values and re-populates Roots[id].
	reenroll := Record{
		InstanceID: "demo", ProfileID: "demo", ProfileVersion: 3,
		Roots: map[string]string{"state": "/home/u/.demo"},
	}
	d.Put(reenroll, now)
	got = d.Record("demo")
	if len(got.LocalRoots) != 1 || got.Roots["ext-notes"] != "/home/u/notes" || len(got.ToolDeps) != 1 {
		t.Fatalf("Dossier.Put did not preserve nil LocalRoots/ToolDeps: %+v", got)
	}

	// Explicit non-nil empty slices clear LocalRoots and ToolDeps.
	cleared := *got
	cleared.Roots = map[string]string{"state": "/home/u/.demo"}
	cleared.LocalRoots = []LocalRoot{}
	cleared.ToolDeps = []ToolDep{}
	d.Put(cleared, now)
	got = d.Record("demo")
	if len(got.LocalRoots) != 0 || len(got.ToolDeps) != 0 || !slices.Equal([]string{"state"}, func() []string {
		var ks []string
		for k := range got.Roots {
			ks = append(ks, k)
		}
		return ks
	}()) {
		t.Fatalf("Dossier.Put failed to clear explicit empty slices: %+v", got)
	}
}
