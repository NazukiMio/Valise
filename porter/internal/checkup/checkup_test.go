package checkup

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ids(r *agentkit.CheckupResult) string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.ID)
	}
	return strings.Join(out, ",")
}

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// A long JSONL session with usage, a bloated memory file, no marker.
func TestJSONLSessionsAndMemory(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	start := now.AddDate(0, 0, -20)
	for i := 0; i <= 20; i++ {
		day := start.AddDate(0, 0, i)
		tok := 100
		if i >= 18 { // the last three days grow
			tok = (i - 17) * 40_000
		}
		fmt.Fprintf(&b, `{"ts":%q,"usage":{"in":%d,"out":%d}}`+"\n", day.Format(time.RFC3339), tok, 0)
	}
	write(t, filepath.Join(root, "sessions/long.jsonl"), b.String())
	write(t, filepath.Join(root, "sessions/plain.jsonl"), `{"text":"hi"}`+"\n")
	write(t, filepath.Join(root, "MEMORY.md"), strings.Repeat("line\n", 801))
	rec := &enroll.Record{InstanceID: "demo", Roots: map[string]string{"state": root}}
	p := &profile.Profile{Checkup: &profile.Checkup{
		Sessions:               &profile.SessionsSpec{Root: "state", Glob: "sessions/*.jsonl", Format: "jsonl", UsageFields: []string{"usage.in", "usage.out"}, TimeField: "ts"},
		MemoryFiles:            []profile.MemoryFile{{Root: "state", Glob: "MEMORY.md", WarnLines: 800}},
		MemoryMechanismMarkers: []profile.PathRef{{Root: "state", Glob: "memories/*.md"}},
	}}
	r := Run(rec, p, now)
	if got := ids(r); got != "long_session,cost_growth,memory_bloat,no_memory_mechanism" {
		t.Fatalf("findings %s %+v", got, r.Findings)
	}
	if r.SessionCount != 2 || r.Current == nil || r.Current.ID != "sessions/long.jsonl" {
		t.Fatalf("%+v", r)
	}
	var long, plain agentkit.SessionStat
	for _, s := range r.Sessions {
		if s.ID == "sessions/long.jsonl" {
			long = s
		} else {
			plain = s
		}
	}
	if long.Estimated || long.Days < 19.9 || long.Events != 21 || !plain.Estimated {
		t.Fatalf("long %+v plain %+v", long, plain)
	}
	if last := r.Daily[len(r.Daily)-1]; last.Day != "2026-10-31" || last.Tokens != 120_000 {
		t.Fatalf("daily %+v", r.Daily)
	}
}

func TestSQLiteSessionsAndPileup(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, content TEXT, timestamp REAL, token_count INTEGER)`)
	old := float64(now.AddDate(0, 0, -200).Unix())
	big := strings.Repeat("x", 4000)
	for i := 0; i < 50; i++ {
		db.Exec(`INSERT INTO messages (session_id, content, timestamp, token_count) VALUES ('cold', ?, ?, NULL)`, big, old+float64(i))
	}
	db.Exec(`INSERT INTO messages (session_id, content, timestamp, token_count) VALUES ('hot', 'hello', ?, 7)`, float64(now.Unix()-60))
	db.Exec(`CREATE TABLE usage (session_id TEXT, input_tokens INTEGER, output_tokens INTEGER, last_seen REAL)`)
	db.Exec(`INSERT INTO usage VALUES ('cold', 900, 100, ?)`, old)
	db.Close()
	write(t, filepath.Join(root, "memories/MEMORY.md"), "fact\n")
	rec := &enroll.Record{InstanceID: "demo", Roots: map[string]string{"state": root}}
	p := &profile.Profile{Checkup: &profile.Checkup{
		Sessions: &profile.SessionsSpec{Root: "state", Glob: "state.db", Format: "sqlite", SQLite: &profile.SessionTable{
			Table: "messages", SessionColumn: "session_id", TimeColumn: "timestamp", TimeUnit: "s", TokensColumn: "token_count", BytesColumns: []string{"content"}}},
		MemoryMechanismMarkers: []profile.PathRef{{Root: "state", Glob: "memories/MEMORY.md"}},
		Thresholds:             &profile.Thresholds{PileupBytes: 100_000},
	}}
	r := Run(rec, p, now)
	if got := ids(r); got != "session_pileup" {
		t.Fatalf("findings %s %+v notes %v", got, r.Findings, r.Notes)
	}
	if r.Current == nil || r.Current.ID != "hot" || r.Current.Tokens != 7 || r.Current.Estimated {
		t.Fatalf("current %+v", r.Current)
	}
	if r.ColdBytes != 200_000 || !r.MemoryMechanism {
		t.Fatalf("%+v", r)
	}
	if last := r.Daily[len(r.Daily)-1]; last.Tokens != 7 {
		t.Fatalf("daily %+v", r.Daily)
	}
}

func TestUndeclaredSessions(t *testing.T) {
	rec := &enroll.Record{InstanceID: "demo", Roots: map[string]string{"state": t.TempDir()}}
	p := &profile.Profile{Checkup: &profile.Checkup{Sessions: &profile.SessionsSpec{Root: "state", Glob: "x.db", Format: "sqlite"}}}
	r := Run(rec, p, now)
	if r.SessionsDeclared || len(r.Notes) != 1 || ids(r) != "no_memory_mechanism" {
		t.Fatalf("%+v", r)
	}
}

func TestUsageTableReplacesEstimates(t *testing.T) {
	root := t.TempDir()
	db, _ := sql.Open("sqlite", filepath.Join(root, "state.db"))
	db.Exec(`CREATE TABLE messages (session_id TEXT, content TEXT, timestamp REAL, token_count INTEGER)`)
	db.Exec(`INSERT INTO messages VALUES ('a', 'hi', ?, NULL)`, float64(now.Unix()-30))
	db.Exec(`CREATE TABLE usage (session_id TEXT, input_tokens INTEGER, output_tokens INTEGER, last_seen REAL)`)
	db.Exec(`INSERT INTO usage VALUES ('a', 12000, 400, ?), ('a', 100, 0, ?)`, float64(now.Unix()-20), float64(now.Unix()-10))
	db.Close()
	rec := &enroll.Record{InstanceID: "demo", Roots: map[string]string{"state": root}}
	p := &profile.Profile{Checkup: &profile.Checkup{Sessions: &profile.SessionsSpec{Root: "state", Glob: "state.db", Format: "sqlite",
		SQLite: &profile.SessionTable{Table: "messages", SessionColumn: "session_id", TimeColumn: "timestamp", TimeUnit: "s", TokensColumn: "token_count",
			BytesColumns: []string{"content"}, Usage: &profile.UsageTable{Table: "usage", SessionColumn: "session_id",
				TokensColumns: []string{"input_tokens", "output_tokens"}, TimeColumn: "last_seen", TimeUnit: "s"}}}}}
	r := Run(rec, p, now)
	if r.Current == nil || r.Current.Tokens != 12500 || r.Current.Estimated {
		t.Fatalf("%+v %v", r.Current, r.Notes)
	}
	if last := r.Daily[len(r.Daily)-1]; last.Tokens != 12500 {
		t.Fatalf("daily %+v", r.Daily)
	}
}

func TestTokensFromJSON(t *testing.T) {
	root := t.TempDir()
	db, _ := sql.Open("sqlite", filepath.Join(root, "a.sqlite"))
	db.Exec(`CREATE TABLE ev (session_id TEXT, created_at INTEGER, event_json TEXT)`)
	db.Exec(`INSERT INTO ev VALUES ('s', ?, '{"type":"message","message":{"usage":{"totalTokens":1500}}}'), ('s', ?, '{"type":"session"}')`,
		now.UnixMilli()-2000, now.UnixMilli()-1000)
	db.Close()
	rec := &enroll.Record{InstanceID: "demo", Roots: map[string]string{"state": root}}
	p := &profile.Profile{Checkup: &profile.Checkup{Sessions: &profile.SessionsSpec{Root: "state", Glob: "a.sqlite", Format: "sqlite",
		SQLite: &profile.SessionTable{Table: "ev", SessionColumn: "session_id", TimeColumn: "created_at", TimeUnit: "ms",
			BytesColumns: []string{"event_json"}, TokensJSON: &profile.JSONField{Column: "event_json", Path: "message.usage.totalTokens"}}}}}
	r := Run(rec, p, now)
	if r.Current == nil || r.Current.Tokens != 1500 || r.Current.Estimated || r.Current.Days > 0.001 {
		t.Fatalf("%+v %v", r.Current, r.Notes)
	}
}
