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

func TestValidLocalEntryAndApply(t *testing.T) {
	e := LocalEntry{
		ID:          " webapp-ui ",
		DisplayName: " Web App ",
		Port:        7860,
		Path:        "",
		Hint:        "Run\n  demo webapp\tfirst",
		Reason:      "Detected UI",
	}
	if err := ValidLocalEntry(&e); err != nil {
		t.Fatalf("ValidLocalEntry valid: %v", err)
	}
	if e.ID != "webapp-ui" || e.DisplayName != "Web App" || e.Path != "/" || e.Hint != "Run demo webapp first" {
		t.Fatalf("normalized LocalEntry = %+v", e)
	}
	for _, bad := range []LocalEntry{
		{ID: "../bad", DisplayName: "x", Port: 8080},
		{ID: "bad_id", DisplayName: "x", Port: 8080},
		{ID: "ok", DisplayName: "", Port: 8080},
		{ID: "ok", DisplayName: "x", Port: 0},
		{ID: "ok", DisplayName: "x", Port: 70000},
		{ID: "ok", DisplayName: "x", Port: 8080, Path: "no-slash"},
		{ID: "ok", DisplayName: "x", Port: 8080, Path: "/?token=abc"},
		{ID: "ok", DisplayName: "x", Port: 8080, Path: "/a#frag"},
		{ID: "ok", DisplayName: "x", Port: 8080, Path: "/../etc"},
		{ID: "ok", DisplayName: "x", Port: 8080, Hint: "bad\x00ctrl"},
	} {
		c := bad
		if err := ValidLocalEntry(&c); err == nil {
			t.Errorf("ValidLocalEntry(%+v) should have failed", bad)
		}
	}

	prof := &profile.Profile{
		Entries: []profile.Entry{
			{ID: "official", Port: 9000, Path: "/", Prepare: &profile.EntryPrepare{Steps: []profile.Argv{{"true"}}}},
			{ID: "other", Port: 9001, Path: "/"},
		},
	}
	rec := &Record{
		InstanceID: "demo",
		LocalEntries: []LocalEntry{
			{ID: "official", DisplayName: "Dup ID", Port: 7000, Path: "/"},
			{ID: "shadow-port", DisplayName: "Dup Port", Port: 9000, Path: "/"},
			{ID: "webapp", DisplayName: "WebApp", Port: 7860, Path: "/ui", Hint: "start webapp"},
		},
	}
	shadowed := rec.ApplyLocalEntries(prof)
	if !slices.Equal(shadowed, []string{"official", "shadow-port"}) {
		t.Fatalf("shadowed = %v", shadowed)
	}
	if len(prof.Entries) != 3 || prof.Entries[2].ID != "webapp" || prof.Entries[2].Host != "127.0.0.1" ||
		prof.Entries[2].AgentHint != "start webapp" || prof.Entries[2].LocalSource != "agent_dossier" {
		t.Fatalf("Entries after ApplyLocalEntries = %+v", prof.Entries)
	}

	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	var d Dossier
	d.Put(*rec, now)
	got := d.Record("demo")
	if len(got.LocalEntries) != 3 {
		t.Fatalf("Dossier round-trip LocalEntries = %+v", got.LocalEntries)
	}
	// Preserve on nil, clear on non-nil empty.
	d.Put(Record{InstanceID: "demo", Roots: map[string]string{"state": "/tmp/s"}}, now)
	if len(d.Record("demo").LocalEntries) != 3 {
		t.Fatalf("nil LocalEntries should preserve")
	}
	d.Put(Record{InstanceID: "demo", Roots: map[string]string{"state": "/tmp/s"}, LocalEntries: []LocalEntry{}}, now)
	if len(d.Record("demo").LocalEntries) != 0 {
		t.Fatalf("empty LocalEntries should clear")
	}
}
