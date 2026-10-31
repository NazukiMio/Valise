package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/kit"
	"valise/porter/internal/seal"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// client drives Serve over pipes like a harness would.
type client struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Scanner
	id  int
}

func (c *client) call(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	c.in.Write(append(b, '\n'))
	if !c.out.Scan() {
		c.t.Fatalf("no response to %s", method)
	}
	var resp map[string]any
	json.Unmarshal(c.out.Bytes(), &resp)
	if resp["error"] != nil {
		c.t.Fatalf("%s: %v", method, resp["error"])
	}
	return resp["result"].(map[string]any)
}

func (c *client) tool(name string, args any) (map[string]any, string, bool) {
	c.t.Helper()
	r := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	sc, _ := r["structuredContent"].(map[string]any)
	return sc, text, r["isError"] == true
}

func setup(t *testing.T) (*client, string, string, *time.Time) {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, ".demo")
	write(t, filepath.Join(state, "SOUL.md"), "soul")
	write(t, filepath.Join(state, ".env"), "API_KEY=secret")
	write(t, filepath.Join(state, "MEMORY.md"), "# Memory\n\n- fact one\n")
	write(t, filepath.Join(state, "notes/a.md"), "user notes")
	write(t, filepath.Join(state, "runtime.lock"), "1")
	p := profile.Profile{ID: "demo", ProfileVersion: 1, Status: "draft",
		Paths: []profile.PathRule{
			{Root: "state", Glob: ".env", Category: "secrets"},
			{Root: "state", Glob: "SOUL.md", Category: "identity"},
			{Root: "state", Glob: "MEMORY.md", Category: "memory"},
		},
		Checkup: &profile.Checkup{MemoryFiles: []profile.MemoryFile{{Root: "state", Glob: "MEMORY.md", WarnLines: 100}},
			MemoryMechanismMarkers: []profile.PathRef{{Root: "state", Glob: "MEMORY.md"}}},
		Handoff: &profile.Handoff{Targets: []profile.HandoffTarget{{Root: "state", Glob: "MEMORY.md", Mode: "append_section", MaxLines: 10}},
			NewSessionHint: "send /new"},
	}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": state}, LocalOverrides: []profile.Override{}}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}
	id, _ := age.GenerateX25519Identity()
	if _, _, err := seal.AddRecipient(seal.Paths{Home: home}, id.Recipient().String(), "test"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	env := &porterenroll.Env{Home: home, Vars: []string{"PATH=/usr/bin:/bin"}}
	tools := &Tools{Home: home, Instance: "demo", Version: "test", Env: func(context.Context) *porterenroll.Env { return env },
		Now: func() time.Time { return now }}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { Serve(context.Background(), inR, outW, tools); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	return &client{t: t, in: inW, out: bufio.NewScanner(outR)}, home, state, &now
}

func TestProtocolAndTools(t *testing.T) {
	c, home, state, now := setup(t)
	init := c.call("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t"}})
	if init["protocolVersion"] != "2025-03-26" {
		t.Fatalf("%v", init)
	}
	c.in.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	tools := c.call("tools/list", nil)["tools"].([]any)
	if len(tools) != 6 {
		t.Fatalf("%d tools", len(tools))
	}
	for _, tl := range tools {
		name := tl.(map[string]any)["name"].(string)
		for _, banned := range []string{"revive", "delete", "secret", "write", "read_file"} {
			if strings.Contains(name, banned) {
				t.Fatalf("tool %s exceeds the MCP capability boundary", name)
			}
		}
	}

	// Seal, then the rate limit.
	if _, text, isErr := c.tool("valise_seal", map[string]any{"reason": ""}); !isErr || !strings.Contains(text, "原因") {
		t.Fatalf("empty reason accepted: %s", text)
	}
	sc, text, isErr := c.tool("valise_seal", map[string]any{"reason": "about to edit SOUL.md"})
	if isErr || !strings.HasPrefix(sc["snapshot_id"].(string), "s_") {
		t.Fatalf("%s", text)
	}
	if _, text, isErr := c.tool("valise_seal", map[string]any{"reason": "again"}); !isErr || !strings.Contains(text, "10 分钟") {
		t.Fatalf("rate limit missing: %s", text)
	}
	if audit, _ := os.ReadFile(seal.Paths{Home: home}.Audit()); !strings.Contains(string(audit), `"result":"rate_limited"`) {
		t.Fatalf("rate-limited attempt not audited: %s", audit)
	}
	*now = now.Add(11 * time.Minute)
	if _, text, isErr := c.tool("valise_seal", map[string]any{"reason": "later"}); isErr {
		t.Fatalf("%s", text)
	}
	audit, _ := os.ReadFile(seal.Paths{Home: home}.Audit())
	if !strings.Contains(string(audit), `"initiator":"agent-mcp"`) || !strings.Contains(string(audit), "about to edit SOUL.md") {
		t.Fatalf("audit: %s", audit)
	}

	// Status: last seal, unclassified hints, no contents.
	sc, text, _ = c.tool("valise_status", map[string]any{})
	if sc["last_seal"] == nil || len(sc["unclassified"].([]any)) == 0 || strings.Contains(text, "API_KEY=secret") || strings.Contains(text, "user notes") {
		t.Fatalf("%s", text)
	}

	// Checkup runs, then is rate limited.
	sc, text, isErr = c.tool("valise_checkup", map[string]any{})
	if isErr || sc["memory_mechanism"] != true {
		t.Fatalf("%s", text)
	}
	if _, _, isErr := c.tool("valise_checkup", map[string]any{}); !isErr {
		t.Fatal("checkup not rate limited")
	}

	// Proposals: validated by code; a demotion is flagged; secrets are protected.
	sc, text, _ = c.tool("valise_propose_enrollment", map[string]any{"items": []map[string]any{
		{"root": "state", "path": "notes", "category": "memory", "reason": "the user's notes"},
		{"root": "state", "path": "runtime.lock", "category": "ignore", "reason": "lock file"},
		{"root": "state", "path": ".env", "category": "memory", "reason": "x"},
		{"root": "state", "path": "../outside", "category": "memory", "reason": "x"},
		{"root": "state", "path": "missing.md", "category": "memory", "reason": "x"},
	}})
	results := sc["results"].([]any)
	if len(results) != 5 || !strings.Contains(results[0].(string), "notes/**") || !strings.Contains(results[1].(string), "降级") ||
		!strings.Contains(results[2].(string), "拒绝") || !strings.Contains(results[3].(string), "拒绝") || !strings.Contains(results[4].(string), "拒绝") {
		t.Fatalf("%s", text)
	}
	ps, _ := kit.LoadProposals(home, "demo")
	if len(ps) != 2 {
		t.Fatalf("%+v", ps)
	}
	for _, p := range ps {
		if p.Rule.Glob == "notes/**" && p.Unclassified != 1 {
			t.Fatalf("unclassified files moving into soul not counted: %+v", p)
		}
	}
	// "all" never accepts a demotion.
	res, err := kit.Decide(home, agentkit.DecideParams{InstanceID: "demo", Accept: []string{"all"}})
	if err != nil || len(res.Accepted) != 1 || res.Remaining != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	demo := ps[1].ID
	if ps[0].Demotion {
		demo = ps[0].ID
	}
	if res, err = kit.Decide(home, agentkit.DecideParams{InstanceID: "demo", AcceptDemotions: []string{demo}}); err != nil || len(res.Accepted) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	rec, _ := porterenroll.ReadRecord(home, "demo")
	if len(rec.LocalOverrides) != 2 {
		t.Fatalf("%+v", rec.LocalOverrides)
	}

	// Handoff: an edit above the new section is refused; a dated appended
	// section within the limit passes.
	*now = now.Add(11 * time.Minute)
	sc, text, isErr = c.tool("valise_handoff_begin", map[string]any{})
	if isErr || sc["new_session_hint"] != "send /new" {
		t.Fatalf("%s", text)
	}
	sid := sc["snapshot_id"].(string)
	mem := filepath.Join(state, "MEMORY.md")
	write(t, mem, "# Memory (edited)\n\n- fact one\n## Handoff 2026-10-31\n- next: x\n")
	if _, text, isErr := c.tool("valise_handoff_finish", map[string]any{"snapshot_id": sid}); !isErr || !strings.Contains(text, "原有内容被修改") {
		t.Fatalf("%s", text)
	}
	write(t, mem, "# Memory\n\n- fact one\n"+strings.Repeat("- x\n", 12))
	if _, text, isErr := c.tool("valise_handoff_finish", map[string]any{"snapshot_id": sid}); !isErr || !strings.Contains(text, "标题") && !strings.Contains(text, "超过") {
		t.Fatalf("%s", text)
	}
	write(t, mem, "# Memory\n\n- fact one\n\n## Handoff 2026-10-31\n- in progress: tidy notes\n- next: review\n")
	sc, text, isErr = c.tool("valise_handoff_finish", map[string]any{"snapshot_id": sid})
	if isErr || sc["next_for_user"] != "send /new" {
		t.Fatalf("%s", text)
	}
	if _, _, isErr := c.tool("valise_handoff_finish", map[string]any{"snapshot_id": sid}); !isErr {
		t.Fatal("finished twice")
	}
}
