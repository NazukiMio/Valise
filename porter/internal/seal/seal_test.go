package seal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"math/rand"

	"filippo.io/age"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

type world struct {
	p     Paths
	id    *age.X25519Identity
	state string
	db    *sql.DB
	env   *porterenroll.Env
}

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

// newWorld enrolls a fixture instance (harness names here are fixtures).
func newWorld(t *testing.T) *world {
	t.Helper()
	home := t.TempDir()
	w := &world{p: Paths{Home: home}, state: filepath.Join(home, ".demo"),
		env: &porterenroll.Env{Home: home, Vars: []string{"PATH=/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin"}}}
	write(t, filepath.Join(w.state, "SOUL.md"), "soul")
	write(t, filepath.Join(w.state, ".env"), "API_KEY=secret")
	write(t, filepath.Join(w.state, "workspace/notes.md"), "notes")
	write(t, filepath.Join(w.state, "logs/x.log"), "log")
	write(t, filepath.Join(w.state, "mystery.bin"), "?")
	session := make([]byte, 300<<10)
	rand.New(rand.NewSource(7)).Read(session)
	write(t, filepath.Join(w.state, "sessions/a.jsonl"), string(session))
	os.Symlink("SOUL.md", filepath.Join(w.state, "soul-link"))
	os.Symlink("../SOUL.md", filepath.Join(w.state, "logs/ignored-link"))
	os.Symlink("SOUL.md", filepath.Join(w.state, "unclassified-link"))
	db, err := sql.Open("sqlite", filepath.Join(w.state, "agent.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"PRAGMA journal_mode=wal", "CREATE TABLE auth_store(k TEXT)", "CREATE TABLE sessions(v TEXT)",
		"INSERT INTO auth_store VALUES('TOPSECRET-TOKEN')", "INSERT INTO sessions VALUES('hello session')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	w.db = db // kept open so the -wal file exists while sealing
	t.Cleanup(func() { db.Close() })

	p := profile.Profile{ID: "demo", ProfileVersion: 2, Status: "draft",
		Paths: []profile.PathRule{
			{Root: "state", Glob: ".env", Category: "secrets"},
			{Root: "state", Glob: "SOUL.md", Category: "identity"},
			{Root: "state", Glob: "sessions/**", Category: "sessions"},
			{Root: "state", Glob: "agent.sqlite", Category: "state"},
			{Root: "state", Glob: "workspace/**", Category: "workspace"},
			{Root: "state", Glob: "logs/**", Category: "ignore"},
			{Root: "state", Glob: "soul-link", Category: "identity"},
		},
		Consistency: &profile.Consistency{SQLite: []profile.SQLiteRef{{Root: "state", Glob: "agent.sqlite", SecretTables: []string{"auth_store", "missing_table"}}}},
	}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 2, Roots: map[string]string{"state": w.state}}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}
	w.id, _ = age.GenerateX25519Identity()
	if _, _, err := AddRecipient(w.p, w.id.Recipient().String(), "test key"); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) seal(t *testing.T, tier string) *vaultrpc.SealReport {
	t.Helper()
	rep, err := Seal(context.Background(), w.env, w.p, vaultrpc.SealParams{InstanceID: "demo", Tier: tier, Reason: "test"}, vaultrpc.InitiatorClient)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// leaf returns the demo instance's only leaf.
func (w *world) leaf(t *testing.T) LeafInfo {
	t.Helper()
	all, err := Leaves(w.p)
	if err != nil || len(all) != 1 {
		t.Fatalf("leaves %v %v", all, err)
	}
	return all[0]
}

func (w *world) restore(t *testing.T, sid string) map[string][]byte {
	t.Helper()
	st, err := Status(w.p)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := w.p.LeafDir(st.LeafOf(sid))
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, files, err := v.Snapshot(sid, w.id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range files.Files {
		var b bytes.Buffer
		if err := v.WriteFile(f, &b, w.id); err != nil {
			t.Fatal(err)
		}
		out[f.Root+":"+f.Path] = b.Bytes()
	}
	for _, s := range files.Symlinks {
		out["link "+s.Root+":"+s.Path] = []byte(s.Target)
	}
	return out
}

func dbRows(t *testing.T, content []byte, table string) int {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.sqlite")
	os.WriteFile(p, content, 0o600)
	db, _ := sql.Open("sqlite", p)
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSealTiers(t *testing.T) {
	w := newWorld(t)
	soul := w.seal(t, vault.TierSoul)
	if soul.Epoch != 1 || !soul.NewEpoch || soul.UnclassifiedCount != 1 || len(soul.SQLite) != 1 ||
		len(soul.SQLite[0].StrippedTables) != 1 || soul.SQLite[0].StrippedTables[0] != "auth_store" {
		t.Fatalf("%+v", soul)
	}
	got := w.restore(t, soul.SnapshotID)
	if _, ok := got["state:.env"]; ok {
		t.Fatal("soul tier contains a secrets file")
	}
	if _, ok := got["state:workspace/notes.md"]; ok {
		t.Fatal("soul tier contains workspace")
	}
	if string(got["state:SOUL.md"]) != "soul" || string(got["link state:soul-link"]) != "SOUL.md" {
		t.Fatalf("%v", got)
	}
	for k := range got {
		if strings.Contains(k, "ignored-link") || strings.Contains(k, "unclassified-link") {
			t.Fatalf("symlink outside the tier was recorded: %s", k)
		}
		if strings.HasSuffix(k, "-wal") || strings.HasSuffix(k, "-shm") || strings.Contains(k, "logs/") {
			t.Fatalf("unexpected %s", k)
		}
	}
	db := got["state:agent.sqlite"]
	if len(db) < 20 || db[18] != 2 || db[19] != 2 {
		t.Fatal("the WAL database's copy is not in WAL mode")
	}
	if dbRows(t, db, "auth_store") != 0 || dbRows(t, db, "sessions") != 1 || bytes.Contains(db, []byte("TOPSECRET")) {
		t.Fatal("secret table not stripped from the soul copy")
	}
	// The live database is untouched.
	var n int
	w.db.QueryRow("SELECT count(*) FROM auth_store").Scan(&n)
	if n != 1 {
		t.Fatal("live database was modified")
	}

	ss := w.seal(t, vault.TierSoulSecrets)
	got = w.restore(t, ss.SnapshotID)
	if string(got["state:.env"]) != "API_KEY=secret" || dbRows(t, got["state:agent.sqlite"], "auth_store") != 1 || len(ss.SQLite[0].StrippedTables) != 0 {
		t.Fatal("soul-secrets must hold secrets and the unstripped database")
	}
	// Unknown means sensitive: unclassified files and symlinks travel with
	// the secrets layer, never with soul.
	if string(got["state:mystery.bin"]) != "?" || string(got["link state:unclassified-link"]) != "SOUL.md" || ss.Categories["unclassified"].Files != 1 {
		t.Fatalf("soul-secrets lacks unclassified files: %v", ss.Categories)
	}
	if _, ok := got["state:logs/x.log"]; ok {
		t.Fatal("soul-secrets contains ignored files")
	}
	full := w.seal(t, vault.TierFull)
	if string(w.restore(t, full.SnapshotID)["state:workspace/notes.md"]) != "notes" {
		t.Fatal("full tier lacks workspace")
	}
	raw := w.seal(t, vault.TierRaw)
	got = w.restore(t, raw.SnapshotID)
	if string(got["state:logs/x.log"]) != "log" || string(got["link state:logs/ignored-link"]) != "../SOUL.md" ||
		string(got["state:mystery.bin"]) != "?" || dbRows(t, got["state:agent.sqlite"], "auth_store") != 1 || len(raw.SQLite[0].StrippedTables) != 0 {
		t.Fatalf("raw must hold every file, unstripped: %v", raw.Categories)
	}
	for k := range got {
		if strings.HasSuffix(k, "-wal") || strings.HasSuffix(k, "-shm") {
			t.Fatalf("raw sealed a SQLite sidecar instead of the copy: %s", k)
		}
	}

	// Unchanged instance: every payload blob is reused. Header growth is
	// independent of payload dedupe; only index/head/meta may be added.
	again := w.seal(t, vault.TierSoul)
	var index vault.Index
	indexBytes, err := os.ReadFile(filepath.Join(w.leaf(t).Dir, vault.IndexPath(again.SnapshotID)))
	if err != nil || json.Unmarshal(indexBytes, &index) != nil || len(index.Blobs) != 0 || again.NewEpoch {
		t.Fatalf("unchanged payload was written again: %d blobs, %v", len(index.Blobs), err)
	}
	var headerBytes int64
	for _, name := range []string{vault.IndexPath(again.SnapshotID), vault.SnapshotPath(again.SnapshotID), vault.MetaPath(again.SnapshotID)} {
		st, err := os.Stat(filepath.Join(w.leaf(t).Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		headerBytes += st.Size()
	}
	if again.AddedBytes != headerBytes {
		t.Fatal("unchanged backup added more than its own metadata")
	}
	if probs, err := vault.VerifyNoKey(w.leaf(t).Dir); err != nil || len(probs) != 0 {
		t.Fatal(probs, err)
	}
	audit, _ := os.ReadFile(w.p.Audit())
	if strings.Count(string(audit), `"op":"seal"`) != 5 || strings.Contains(string(audit), "secret") {
		t.Fatalf("audit: %s", audit)
	}
}

func TestSealErrors(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	code := func(err error) string {
		ue, _ := valise.As(err)
		if ue == nil {
			return ""
		}
		return ue.Code
	}
	if _, err := Seal(ctx, w.env, w.p, vaultrpc.SealParams{InstanceID: "demo", Tier: "everything"}, "client"); code(err) != "tier_invalid" {
		t.Fatal(err)
	}
	if _, err := Seal(ctx, w.env, w.p, vaultrpc.SealParams{InstanceID: "nope", Tier: "soul"}, "client"); code(err) != "instance_not_enrolled" {
		t.Fatal(err)
	}
	unlock, err := w.p.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Seal(ctx, w.env, w.p, vaultrpc.SealParams{InstanceID: "demo", Tier: "soul"}, "client"); code(err) != "vault_busy" {
		t.Fatal(err)
	}
	unlock()
	os.Remove(w.p.Recipients())
	if _, err := Seal(ctx, w.env, w.p, vaultrpc.SealParams{InstanceID: "demo", Tier: "soul"}, "client"); code(err) != "no_recipients" {
		t.Fatal(err)
	}
	porterenroll.MarkSuperseded(w.p.Home, "demo", enroll.Supersession{By: "demo-2", OpID: "op_x"})
	if _, err := Seal(ctx, w.env, w.p, vaultrpc.SealParams{InstanceID: "demo", Tier: "soul"}, "client"); code(err) != "instance_superseded" {
		t.Fatal(err)
	}
}

func TestBodyPatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	w := newWorld(t)
	body := filepath.Join(w.state, "agent-src")
	write(t, filepath.Join(body, "main.py"), "print(1)\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "i"}} {
		if b, err := exec.Command("git", append([]string{"-C", body}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(b))
		}
	}
	rec, _ := porterenroll.ReadRecord(w.p.Home, "demo")
	rec.Provenance = enroll.Provenance{Kind: "git", Git: &enroll.GitRecord{Path: body}}
	prof, _ := porterenroll.ReadProfile(w.p.Home, "demo")
	porterenroll.WriteRecord(w.p.Home, *rec, *prof)

	if rep := w.seal(t, vault.TierSoul); rep.BodyPatch {
		t.Fatal("clean work tree produced a patch")
	}
	write(t, filepath.Join(body, "main.py"), "print(2)\n")
	rep := w.seal(t, vault.TierSoul)
	patch := w.restore(t, rep.SnapshotID)["valise:body.patch"]
	if !rep.BodyPatch || !bytes.Contains(patch, []byte("+print(2)")) {
		t.Fatalf("%+v %s", rep, patch)
	}
}

func TestVaultOps(t *testing.T) {
	w := newWorld(t)
	if b, _ := os.ReadFile(w.p.Recipients()); !strings.Contains(string(b), "# test key (added ") {
		t.Fatalf("label comment missing: %s", b)
	}
	if _, _, err := AddRecipient(w.p, w.id.Recipient().String(), "two\nlines"); err == nil {
		t.Fatal("multi-line label accepted")
	}
	if st, err := Status(w.p); err != nil || st.Exists || len(st.Recipients) != 1 {
		t.Fatal(st, err)
	}
	rep := w.seal(t, vault.TierSoul)
	st, err := Status(w.p)
	if err != nil || !st.Exists || len(st.Snapshots) != 1 || st.Snapshots[0].SnapshotID != rep.SnapshotID {
		t.Fatal(st, err)
	}
	leaf := w.leaf(t).Leaf
	if leaf != rep.Leaf || !strings.HasPrefix(leaf, "demo/") {
		t.Fatal(leaf, rep.Leaf)
	}
	files, err := Files(w.p, leaf)
	if err != nil || len(files.Files) < 7 {
		t.Fatal(files, err)
	}
	for _, f := range files.Files {
		fh, err := OpenVaultFile(w.p, leaf, f.Path)
		if err != nil {
			t.Fatal(err)
		}
		fh.Close()
	}
	for _, bad := range []string{"../.config/valise/recipients.txt", "VAULT/../../x", "packs/tmp-1"} {
		if _, err := OpenVaultFile(w.p, leaf, bad); err == nil {
			t.Errorf("%s served", bad)
		}
	}
	for _, bad := range []string{"../x", "demo/../../.config", "demo", "-/../x"} {
		if _, err := OpenVaultFile(w.p, bad, "VAULT"); err == nil {
			t.Errorf("leaf %s served", bad)
		}
	}
	if res, err := Verify(w.p); err != nil || len(res.Problems) != 0 {
		t.Fatal(res, err)
	}
	for _, bad := range []vaultrpc.Target{
		{Name: "Nas", Kind: "rsync", Dest: "nas:/x"},
		{Name: "nas", Kind: "ftp", Dest: "nas:/x"},
		{Name: "nas", Kind: "rclone", Dest: "nas:/x"},
		{Name: "nas", Kind: "rsync", Dest: "nas:relative"},
		{Name: "nas", Kind: "rsync", Dest: "-e:/x"},
		{Name: "nas", Kind: "rsync", Dest: "nas:/x;rm"},
	} {
		if err := SetTarget(w.p, bad); err == nil && !strings.Contains(bad.Dest, ";") {
			t.Errorf("%+v accepted", bad)
		}
	}
	if err := SetTarget(w.p, vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "nas-test:/mnt/test/nas/valise-vault/"}); err != nil {
		t.Fatal(err)
	}
	st, _ = Status(w.p)
	if len(st.Targets) != 1 || st.Targets[0].LastOK != nil {
		t.Fatal(st.Targets)
	}
}

func TestTreePeriods(t *testing.T) {
	w := newWorld(t)
	t.Cleanup(func() { now = time.Now })
	base := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) // a Wednesday
	now = func() time.Time { return base }
	if err := SetPeriodSetting(w.p, "demo", vault.PeriodSetting{Unit: "day", TZ: "UTC"}); err == nil {
		t.Fatal("bad unit accepted")
	}
	if err := SetPeriodSetting(w.p, "demo", vault.PeriodSetting{Unit: vault.UnitWeek, TZ: "Asia/Shanghai"}); err != nil {
		t.Fatal(err)
	}
	first := w.sealAuto(t)
	if first.Leaf != "demo/2026-10-26_2026-11-01" {
		t.Fatal(first.Leaf)
	}
	if _, err := os.Stat(filepath.Join(w.p.Vaults(), vault.TreeFile)); err == nil {
		t.Fatal("TREE written before the server has a name")
	}
	if err := SetServerName(w.p, "box"); err != nil {
		t.Fatal(err)
	}
	if err := SetServerName(w.p, "other"); err == nil {
		t.Fatal("server name changed")
	}
	if _, err := os.Stat(filepath.Join(w.p.Vaults(), vault.TreeFile)); err != nil {
		t.Fatal("TREE missing after naming")
	}
	now = func() time.Time { return base.Add(8 * 24 * time.Hour) }
	second := w.sealAuto(t)
	if second.Leaf != "demo/2026-11-02_2026-11-08" || second.NewEpoch == false {
		t.Fatalf("%s new epoch %v", second.Leaf, second.NewEpoch)
	}
	st, err := Status(w.p)
	if err != nil || len(st.Leaves) != 2 || !st.Leaves[1].Current || st.Leaves[0].Current || st.ServerName != "box" {
		t.Fatalf("%+v %v", st.Leaves, err)
	}
	if st.LeafOf(first.SnapshotID) != first.Leaf || len(w.restore(t, first.SnapshotID)) == 0 {
		t.Fatal("first snapshot not found in its leaf")
	}
	if _, err := DeleteLeaf(w.p, second.Leaf, "client"); err == nil {
		t.Fatal("deleted the current leaf")
	}
	// An interrupted deletion: the leaf is listed as deleting, cannot be
	// pulled, and deleting again finishes it.
	os.WriteFile(filepath.Join(w.p.Vaults(), first.Leaf, vault.DeletingFile), []byte("{}"), 0o600)
	os.RemoveAll(filepath.Join(w.p.Vaults(), first.Leaf, vault.SnapshotsDir))
	st, _ = Status(w.p)
	if !st.Leaves[0].Deleting || len(st.Snapshots) != 1 {
		t.Fatalf("%+v", st.Leaves)
	}
	if _, err := Files(w.p, first.Leaf); err == nil {
		t.Fatal("files of a deleting leaf served")
	}
	res, err := DeleteLeaf(w.p, first.Leaf, "client")
	if err != nil || res.Bytes == 0 {
		t.Fatal(res, err)
	}
	if st, _ = Status(w.p); len(st.Leaves) != 1 {
		t.Fatalf("%+v", st.Leaves)
	}
	if res, err := Verify(w.p); err != nil || len(res.Problems) != 0 {
		t.Fatal(res, err)
	}
	log, err := SealLog(w.p, "demo")
	if err != nil || len(log.Entries) != 2 || log.Entries[1].SnapshotID != second.SnapshotID || log.Entries[0].Reason != "test" || log.Entries[0].Initiator != vaultrpc.InitiatorTimer {
		t.Fatalf("%+v %v", log, err)
	}
	audit, _ := os.ReadFile(w.p.Audit())
	if !strings.Contains(string(audit), `"op":"leaf_delete"`) {
		t.Fatal("leaf deletion not audited")
	}
}

func TestLegacyVaultReadable(t *testing.T) {
	w := newWorld(t)
	rep := w.seal(t, vault.TierSoul)
	// Move the leaf to where the pre-tree vault lived.
	os.MkdirAll(filepath.Dir(w.p.LegacyVault()), 0o700)
	if err := os.Rename(filepath.Join(w.p.Vaults(), rep.Leaf), w.p.LegacyVault()); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(w.p.LegacyVault(), vault.PeriodFile))
	st, err := Status(w.p)
	if err != nil || len(st.Leaves) != 1 || st.Leaves[0].Leaf != vault.LegacyLeaf || st.LeafOf(rep.SnapshotID) != vault.LegacyLeaf {
		t.Fatalf("%+v %v", st.Leaves, err)
	}
	if len(w.restore(t, rep.SnapshotID)) == 0 {
		t.Fatal("legacy snapshot unreadable")
	}
	if _, err := Files(w.p, vault.LegacyLeaf); err != nil {
		t.Fatal(err)
	}
	next := w.seal(t, vault.TierSoul)
	if next.Leaf == vault.LegacyLeaf {
		t.Fatal("new seals must go to the tree")
	}
}
