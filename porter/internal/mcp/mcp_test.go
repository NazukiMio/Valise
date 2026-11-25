package mcp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	_ "modernc.org/sqlite"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	cp "valise/core/protection"
	"valise/core/vault"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/kit"
	porterprotection "valise/porter/internal/protection"
	"valise/porter/internal/seal"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSQLite(t *testing.T, path, table, val string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE "+table+" (v TEXT); INSERT INTO "+table+" VALUES (?)", val); err != nil {
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

func setup(t *testing.T) (*client, string, string, string, *time.Time) {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, ".demo")
	extDir := filepath.Join(home, "external-notes")
	os.MkdirAll(extDir, 0o755)

	write(t, filepath.Join(state, "SOUL.md"), "soul")
	write(t, filepath.Join(state, "persona.md"), "custom persona")
	write(t, filepath.Join(state, ".env"), "API_KEY=secret")
	write(t, filepath.Join(state, "MEMORY.md"), "# Memory\n\n- fact one\n")
	write(t, filepath.Join(state, "notes/a.md"), "user notes")
	write(t, filepath.Join(state, "runtime.lock"), "1")
	write(t, filepath.Join(state, "tools/runtime.bin"), strings.Repeat("x", 4096))
	write(t, filepath.Join(state, "cache/blob.bin"), strings.Repeat("y", 4096))
	write(t, filepath.Join(state, "tmp/scratch.bin"), strings.Repeat("z", 4096))
	writeSQLite(t, filepath.Join(state, "db/core.sqlite"), "tokens", "core_token=secret")
	writeSQLite(t, filepath.Join(state, "plugins/mem.db"), "notes", "do_not_leak")
	writeSQLite(t, filepath.Join(state, "tools/excluded.db"), "t", "ignored_db")
	os.Symlink(extDir, filepath.Join(state, "ext_link"))

	p := profile.Profile{
		ID: "demo", ProfileVersion: 1, Status: "draft",
		Paths: []profile.PathRule{
			{Root: "state", Glob: ".env", Category: "secrets"},
			{Root: "state", Glob: "SOUL.md", Category: "identity"},
			{Root: "state", Glob: "MEMORY.md", Category: "memory"},
			{Root: "state", Glob: "db/*.sqlite", Category: "state"},
			{Root: "state", Glob: "cache/**", Category: "cache"},
			{Root: "state", Glob: "tmp/**", Category: "ignore"},
		},
		Body: &profile.Body{
			Paths: []profile.PathRef{{Root: "state", Glob: "tools/**"}},
		},
		Consistency: &profile.Consistency{
			SQLite: []profile.SQLiteRef{{Root: "state", Glob: "db/*.sqlite", SecretTables: []string{"tokens"}}},
		},
		Checkup: &profile.Checkup{
			MemoryFiles:            []profile.MemoryFile{{Root: "state", Glob: "MEMORY.md", WarnLines: 100}},
			MemoryMechanismMarkers: []profile.PathRef{{Root: "state", Glob: "MEMORY.md"}},
		},
		Handoff: &profile.Handoff{
			Targets:        []profile.HandoffTarget{{Root: "state", Glob: "MEMORY.md", Mode: "append_section", MaxLines: 10}},
			NewSessionHint: "send /new",
		},
	}
	rec := enroll.Record{
		InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1,
		Roots: map[string]string{"state": state}, LocalOverrides: []profile.Override{},
	}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}
	id, _ := age.GenerateX25519Identity()
	if _, _, err := seal.AddRecipient(seal.Paths{Home: home}, id.Recipient().String(), "test"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	env := &porterenroll.Env{Home: home, Vars: []string{"PATH=/usr/bin:/bin"}}
	tools := &Tools{
		Home: home, Instance: "demo", Version: "test",
		Env: func(context.Context) *porterenroll.Env { return env },
		Now: func() time.Time { return now },
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { Serve(context.Background(), inR, outW, tools); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	return &client{t: t, in: inW, out: bufio.NewScanner(outR)}, home, state, extDir, &now
}

func TestProtocolAndTools(t *testing.T) {
	c, home, state, extDir, now := setup(t)
	init := c.call("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t"}})
	if init["protocolVersion"] != "2025-03-26" {
		t.Fatalf("%v", init)
	}
	c.in.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))

	tools := c.call("tools/list", nil)["tools"].([]any)
	var toolNames []string
	for _, tl := range tools {
		m := tl.(map[string]any)
		name := m["name"].(string)
		toolNames = append(toolNames, name)
		for _, banned := range []string{"revive", "delete", "secret", "write", "read_file"} {
			if strings.Contains(name, banned) {
				t.Fatalf("tool %s exceeds MCP boundary", name)
			}
		}
		if name == "valise_propose_dossier" {
			rawSchema, _ := json.Marshal(m["inputSchema"])
			for _, want := range []string{`"identity"`, `"tool_dep"`, `"suggested_category"`, `"purpose"`} {
				if !strings.Contains(string(rawSchema), want) {
					t.Fatalf("valise_propose_dossier schema missing %s: %s", want, rawSchema)
				}
			}
		}
	}
	slices.Sort(toolNames)
	wantTools := []string{"valise_onboard_context", "valise_propose_dossier", "valise_seal", "valise_status"}
	if !slices.Equal(toolNames, wantTools) {
		t.Fatalf("tools/list = %v, want %v", toolNames, wantTools)
	}

	// Read-only calls never trigger autoSeal, stay compact, and log lightweight MCP audit timestamps.
	sc, text, isErr := c.tool("valise_status", map[string]any{})
	if isErr {
		t.Fatalf("%s", text)
	}
	if _, hasScan := sc["unclassified"]; hasScan {
		t.Fatalf("valise_status must stay slim without Scan fields: %s", text)
	}
	vs, _ := seal.Status(seal.Paths{Home: home})
	if len(vs.Snapshots) != 0 {
		t.Fatalf("autoSeal must be removed; read-only status created snapshots: %+v", vs.Snapshots)
	}
	if !strings.Contains(sc["protection_summary"].(string), "没有配置") {
		t.Fatalf("protection_summary = %s", text)
	}
	ks, err := kit.Status(home, agentkit.KitStatusParams{InstanceID: "demo"})
	if err != nil || ks.MCPCalls != 1 || ks.LastMCP == nil {
		t.Fatalf("read call not audited for LastMCP: %+v %v", ks, err)
	}

	// valise_onboard_context returns all 5 blocks including untracked_sqlite & external_symlinks, without reading table data.
	sc, text, isErr = c.tool("valise_onboard_context", map[string]any{})
	if isErr {
		t.Fatalf("onboard context failed: %s", text)
	}
	if strings.Contains(text, "API_KEY=secret") || strings.Contains(text, "do_not_leak") || strings.Contains(text, "user notes") {
		t.Fatalf("file/sqlite content leaked into onboard context: %s", text)
	}
	facts := sc["layout_facts"].(map[string]any)
	sqliteList := facts["untracked_sqlite"].([]any)
	if len(sqliteList) != 1 || sqliteList[0].(map[string]any)["path"] != "plugins/mem.db" {
		t.Fatalf("unexpected untracked_sqlite: %+v", sqliteList)
	}
	eff := sc["effective_rules"].(map[string]any)
	bodyPaths, _ := eff["profile_body_paths"].([]any)
	if len(bodyPaths) != 1 || bodyPaths[0].(map[string]any)["root"] != "state" || bodyPaths[0].(map[string]any)["glob"] != "tools/**" {
		t.Fatalf("unexpected profile_body_paths: %+v", eff["profile_body_paths"])
	}
	largestDirs := facts["largest_dirs"].([]any)
	hasNotesOrPlugins := false
	for _, entry := range largestDirs {
		pfx := entry.(map[string]any)["prefix"].(string)
		if pfx == "tools/" || pfx == "cache/" || pfx == "tmp/" {
			t.Fatalf("excluded category leaked into largest_dirs: %+v", largestDirs)
		}
		if pfx == "notes/" || pfx == "plugins/" {
			hasNotesOrPlugins = true
		}
	}
	if !hasNotesOrPlugins {
		t.Fatalf("expected full-tier dirs in largest_dirs, got: %+v", largestDirs)
	}
	extList := facts["external_symlinks"].([]any)
	if len(extList) != 1 || extList[0].(map[string]any)["link_path"] != "state:ext_link" {
		t.Fatalf("unexpected external_symlinks: %+v", extList)
	}
	vs, _ = seal.Status(seal.Paths{Home: home})
	if len(vs.Snapshots) != 0 {
		t.Fatalf("valise_onboard_context triggered seal: %+v", vs.Snapshots)
	}

	// valise_seal allows empty reason, writes KindAuto rolling leaf conservatively (soul + plan_warning) when no plan exists.
	sc, text, isErr = c.tool("valise_seal", map[string]any{})
	if isErr || sc["tier"] != vault.TierSoul || sc["kind"] != vault.KindAuto {
		t.Fatalf("unplanned seal failed: isErr=%v text=%s", isErr, text)
	}
	if _, ok := sc["plan_warning"].(string); !ok {
		t.Fatalf("missing plan_warning on legacy soul seal: %s", text)
	}
	sid := sc["snapshot_id"].(string)
	// Rate limit returns last_seal_id & timestamp.
	if _, errText, errFlag := c.tool("valise_seal", map[string]any{"reason": "again"}); !errFlag || !strings.Contains(errText, sid) || !strings.Contains(errText, "2026-10-31T12:00:00Z") {
		t.Fatalf("rate limit should cite snapshot id and timestamp: isErr=%v text=%q", errFlag, errText)
	}
	if audit, _ := os.ReadFile(seal.Paths{Home: home}.Audit()); !strings.Contains(string(audit), `"result":"rate_limited"`) {
		t.Fatalf("rate-limited attempt not audited: %s", audit)
	}

	// With an adopted full Protection Plan, valise_seal uses full tier and omits plan_warning.
	*now = now.Add(11 * time.Minute)
	writePlanFile(t, home, "demo", vault.TierFull)
	sc, text, isErr = c.tool("valise_seal", map[string]any{"reason": "before editing config"})
	if isErr || sc["tier"] != vault.TierFull || sc["plan_warning"] != nil {
		t.Fatalf("planned full seal unexpected: isErr=%v text=%s", isErr, text)
	}
	vs, _ = seal.Status(seal.Paths{Home: home})
	if len(vs.Snapshots) != 2 || vs.Snapshots[0].Kind != vault.KindAuto || vs.Snapshots[1].Kind != vault.KindAuto {
		t.Fatalf("expected 2 KindAuto rolling snapshots: %+v", vs.Snapshots)
	}
	for _, lf := range vs.Leaves {
		if vault.IsSavepointLeaf(lf.Leaf) {
			t.Fatalf("MCP seal must not write savepoints leaf: %+v", lf)
		}
	}

	// valise_propose_dossier: classify (including identity), sqlite, and external_ref validation + Decide persistence.
	sc, text, isErr = c.tool("valise_propose_dossier", map[string]any{"items": []map[string]any{
		{"kind": "classify", "root": "state", "path": "notes", "category": "memory", "reason": "user custom notes"},
		{"kind": "classify", "root": "state", "path": "persona.md", "category": "identity", "reason": "custom persona definition"},
		{"kind": "classify", "root": "state", "path": "runtime.lock", "category": "ignore", "reason": ""},
		{"kind": "classify", "root": "state", "path": "runtime.lock", "category": "ignore", "reason": "ephemeral runtime lock"},
		{"kind": "classify", "root": "state", "path": ".env", "category": "memory", "reason": "bad"},
		{"kind": "sqlite", "root": "state", "glob": "plugins/mem.db", "reason": "plugin memory db"},
		{"kind": "sqlite", "root": "state", "glob": "db/core.sqlite", "secret_tables": []string{}, "reason": "strip tables attempt"},
		{"kind": "external_ref", "path": state, "ref_kind": "external_dir", "reason": "inside root"},
		{"kind": "external_ref", "path": extDir, "ref_kind": "symlink_target", "reason": "external notes directory"},
	}})
	if isErr {
		t.Fatalf("propose_dossier failed: %s", text)
	}
	results := sc["results"].([]any)
	if len(results) != 9 ||
		!strings.Contains(results[0].(string), "notes/**") ||
		!strings.Contains(results[1].(string), "persona.md → identity") ||
		!strings.Contains(results[2].(string), "拒绝") ||
		!strings.Contains(results[3].(string), "降级") ||
		!strings.Contains(results[4].(string), "拒绝") ||
		!strings.Contains(results[5].(string), "sqlite:state:plugins/mem.db") ||
		!strings.Contains(results[6].(string), "secret_tables") ||
		!strings.Contains(results[7].(string), "拒绝") ||
		!strings.Contains(results[8].(string), "symlink_target") {
		t.Fatalf("propose_dossier results = %v", results)
	}
	ps, _ := kit.LoadProposals(home, "demo")
	if len(ps) != 5 {
		t.Fatalf("expected 5 pending proposals, got %+v", ps)
	}
	res, err := kit.Decide(home, agentkit.DecideParams{InstanceID: "demo", Accept: []string{"all"}})
	if err != nil || len(res.Accepted) != 4 || res.Remaining != 1 {
		t.Fatalf("Decide Accept=all = %+v %v", res, err)
	}
	if res, err = kit.Decide(home, agentkit.DecideParams{InstanceID: "demo", AcceptDemotions: []string{ps[2].ID}}); err != nil || len(res.Accepted) != 1 || res.Remaining != 0 {
		t.Fatalf("Decide demotion = %+v %v", res, err)
	}
	rec, _ := porterenroll.ReadRecord(home, "demo")
	if len(rec.LocalOverrides) != 3 || len(rec.LocalSQLite) != 1 || len(rec.ExternalRefs) != 1 {
		t.Fatalf("dossier updates missing: ov=%+v sql=%+v ext=%+v", rec.LocalOverrides, rec.LocalSQLite, rec.ExternalRefs)
	}

	// Propose tool_dep + external_ref with suggested_category, promote external_ref into local_roots,
	// and verify onboard_context exposes local_roots & tool_deps and classifies files in local_roots.
	promotedDir := filepath.Join(home, "personal-kb")
	write(t, filepath.Join(promotedDir, "guide.md"), "personal guide")
	sc, text, isErr = c.tool("valise_propose_dossier", map[string]any{"items": []map[string]any{
		{"kind": "external_ref", "path": promotedDir, "ref_kind": "external_dir", "suggested_category": "memory", "reason": "personal knowledge base"},
		{"kind": "tool_dep", "name": "ffmpeg", "purpose": "audio conversion", "reason": "voice skill"},
	}})
	if isErr {
		t.Fatalf("propose_dossier tool_dep/external_ref failed: %s", text)
	}
	ps2, _ := kit.LoadProposals(home, "demo")
	if len(ps2) != 2 {
		t.Fatalf("expected 2 proposals, got %+v", ps2)
	}
	if _, err := kit.Decide(home, agentkit.DecideParams{
		InstanceID:           "demo",
		Accept:               []string{ps2[1].ID},
		PromoteExternalRoots: []agentkit.ExternalRootPromotion{{ProposalID: ps2[0].ID}},
	}); err != nil {
		t.Fatalf("Decide promote + tool_dep failed: %v", err)
	}

	// After accepting plugins/mem.db, promoting personal-kb, and accepting tool_dep:
	// onboard context excludes plugins/mem.db from untracked_sqlite, classifies personal-kb via local_roots,
	// and lists local_roots & tool_deps in effective_rules.
	sc, text, isErr = c.tool("valise_onboard_context", map[string]any{})
	if isErr {
		t.Fatalf("%s", text)
	}
	eff2 := sc["effective_rules"].(map[string]any)
	if lr, ok := eff2["local_roots"].([]any); !ok || len(lr) != 1 {
		t.Fatalf("expected 1 local_root in effective_rules, got %+v", eff2["local_roots"])
	}
	if td, ok := eff2["tool_deps"].([]any); !ok || len(td) != 1 {
		t.Fatalf("expected 1 tool_dep in effective_rules, got %+v", eff2["tool_deps"])
	}
	for _, entry := range sc["layout_facts"].(map[string]any)["unclassified_dirs"].([]any) {
		if entry.(map[string]any)["root"] == "ext-personal-kb" {
			t.Fatalf("promoted local_root files should not be unclassified: %+v", entry)
		}
	}
	if got := sc["layout_facts"].(map[string]any)["untracked_sqlite"].([]any); len(got) != 0 {
		t.Fatalf("accepted local_sqlite still shown in untracked_sqlite: %+v", got)
	}
	for _, entry := range sc["layout_facts"].(map[string]any)["largest_dirs"].([]any) {
		if entry.(map[string]any)["prefix"] == "runtime.lock" {
			t.Fatalf("accepted ignore override still shown in largest_dirs: %+v", entry)
		}
	}

	// Hidden legacy tools stay compatible in Call() even though removed from tools/list.
	*now = now.Add(11 * time.Minute)
	if _, text, isErr := c.tool("valise_declare", map[string]any{"security_skills": []any{}, "approval_mode": "ask"}); isErr {
		t.Fatalf("hidden valise_declare failed: %s", text)
	}
	if sc, text, isErr := c.tool("valise_propose_enrollment", map[string]any{"items": []map[string]any{
		{"root": "state", "path": "notes/a.md", "category": "memory", "reason": "legacy compat"},
	}}); isErr || !strings.Contains(sc["results"].([]any)[0].(string), "notes/a.md") {
		t.Fatalf("hidden valise_propose_enrollment compat failed: %v %s", isErr, text)
	}
	if sc, text, isErr := c.tool("valise_checkup", map[string]any{}); isErr || sc["memory_mechanism"] != true {
		t.Fatalf("hidden valise_checkup failed: %v %s", isErr, text)
	}
	if sc, text, isErr := c.tool("valise_handoff_begin", map[string]any{}); isErr || sc["new_session_hint"] != "send /new" {
		t.Fatalf("hidden handoff_begin failed: %v %s", isErr, text)
	} else {
		hsid := sc["snapshot_id"].(string)
		write(t, filepath.Join(state, "MEMORY.md"), "# Memory\n\n- fact one\n\n## Handoff 2026-10-31\n- next\n")
		if sc, text, isErr := c.tool("valise_handoff_finish", map[string]any{"snapshot_id": hsid}); isErr || sc["next_for_user"] != "send /new" {
			t.Fatalf("hidden handoff_finish failed: %v %s", isErr, text)
		}
	}
}

func writePlanFile(t *testing.T, home, instance, tier string) {
	t.Helper()
	enabled, copyOn := true, true
	plan := cp.ServerPlan{
		SchemaVersion: cp.Schema,
		AgentRef:      identity.AgentRef{ServerUID: "srv-1", InstallID: instance, InstanceID: instance, Generation: "1"},
		Revision:      1,
		Desired: cp.Desired{
			Scope:    cp.Scope{Kind: "preset", Tier: tier},
			Schedule: cp.Schedule{Enabled: &enabled, DailyAt: "03:30", Timezone: "Asia/Shanghai"},
			Period:   cp.Period{Unit: "month", Timezone: "Asia/Shanghai", KeepAutoPeriods: 3},
			ServerCopies: []cp.ServerCopy{{
				TargetRef:       cp.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: "srv-1"},
				Enabled:         &copyOn,
				MinimumInterval: "24h",
			}},
		},
		AcceptedAt: time.Now().UTC(),
	}
	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".config", "valise", "protection", instance+".json"), string(b))
	if lp, lerr := (porterprotection.Store{Home: home}).LoadPlan(instance); lerr != nil || lp == nil {
		t.Fatalf("LoadPlan failed: lp=%+v err=%v", lp, lerr)
	}
}

// The case the review found (F10): a target is configured and older pods
// reached it, only the newest is still on the server.
func TestProtectionSummary(t *testing.T) {
	now := time.Date(2026, 11, 3, 13, 0, 0, 0, time.UTC)
	r := &protectionRaw{Targets: []string{"nas"}, LastSeal: &vault.Meta{SnapshotID: "s_new"}, SyncedTo: []string{},
		LastOffsite: &offsitePod{SnapshotID: "s_old", CreatedAt: now, Targets: []string{"nas"}}, WaitingSync: 1}
	got := protection(r)
	for _, want := range []string{"已配置 1 个同步位置（nas）", "还只在服务器上", "1 个在等同步", "别处最新的一份", "本机文件夹"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q lacks %q", got, want)
		}
	}
}

func TestStatusIncludesProtectionPlan(t *testing.T) {
	c, home, _, _, _ := setup(t)
	writePlanFile(t, home, "demo", vault.TierFull)

	sc, text, isErr := c.tool("valise_status", map[string]any{})
	if isErr {
		t.Fatalf("%s", text)
	}
	pp, ok := sc["protection_plan"].(map[string]any)
	if !ok {
		t.Fatalf("missing protection_plan in status: %+v", sc)
	}
	if pp["adopted"] != true || pp["enabled"] != true || pp["tier"] != vault.TierFull || pp["period_unit"] != "month" ||
		pp["keep_auto_periods"] != float64(3) || !strings.Contains(pp["schedule"].(string), "03:30") {
		t.Fatalf("unexpected protection_plan: %+v", pp)
	}
}

func TestSummarizePlanCadences(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		sc   cp.Schedule
		want string
	}{
		{
			name: "disabled",
			sc:   cp.Schedule{Enabled: &no, Timezone: "UTC"},
			want: "off",
		},
		{
			name: "daily",
			sc:   cp.Schedule{Enabled: &yes, DailyAt: "03:30", Timezone: "Asia/Shanghai"},
			want: "03:30 (Asia/Shanghai)",
		},
		{
			name: "hourly without daily_at",
			sc:   cp.Schedule{Enabled: &yes, Cadence: "hourly", EveryHours: 6, Timezone: "UTC"},
			want: "every 6h (UTC)",
		},
		{
			name: "hourly with minute offset",
			sc:   cp.Schedule{Enabled: &yes, Cadence: "hourly", EveryHours: 3, DailyAt: "00:20", Timezone: "UTC"},
			want: "every 3h at :20 (UTC)",
		},
		{
			name: "weekly",
			sc:   cp.Schedule{Enabled: &yes, Cadence: "weekly", Weekdays: []string{"Mon", "Thu"}, DailyAt: "02:00", Timezone: "Asia/Shanghai"},
			want: "Mon,Thu 02:00 (Asia/Shanghai)",
		},
		{
			name: "monthly",
			sc:   cp.Schedule{Enabled: &yes, Cadence: "monthly", DayOfMonth: 10, DailyAt: "05:00", Timezone: "UTC"},
			want: "day 10 05:00 (UTC)",
		},
	}
	for _, tc := range cases {
		got := summarizePlan(&cp.ServerPlan{
			Desired: cp.Desired{
				Scope:    cp.Scope{Kind: "preset", Tier: vault.TierFull},
				Schedule: tc.sc,
			},
		})
		if got.Schedule != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got.Schedule, tc.want)
		}
	}
}
