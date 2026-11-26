package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func TestFindAndRewriteHomeRefs(t *testing.T) {
	in := []byte("a: /home/old/.x/bin/valise-porter\nb: /home/older/z\nc: \"/home/old\"\nd: x/home/old/y\ne: /home/old2\n")
	hits := scanConfigHomeHits("state", "config.yaml", in, "/home/old", "/srv/new")
	if len(hits) != 2 {
		t.Fatalf("%+v", hits)
	}
	if hits[0].Line != 1 || !hits[0].IsValisePorter || hits[0].NewPath != "/srv/new/.x/bin/valise-porter" || hits[1].Line != 3 || hits[1].OldPath != "/home/old" {
		t.Fatalf("%+v", hits)
	}
	out, n := rewriteHomeRefs(in, "/home/old", "/srv/new")
	want := "a: /srv/new/.x/bin/valise-porter\nb: /home/older/z\nc: \"/srv/new\"\nd: x/home/old/y\ne: /home/old2\n"
	if n != 2 || string(out) != want {
		t.Fatalf("%d %q", n, out)
	}
}

func TestRemapHomeTarget(t *testing.T) {
	for _, c := range []struct {
		in, out string
		ok      bool
	}{
		{"/home/old", "/srv/new", true},
		{"/home/old/a/b", "/srv/new/a/b", true},
		{"/home/old/a/../b", "/srv/new/b", true},
		{"/home/old/a/../../etc/passwd", "/home/old/a/../../etc/passwd", false},
		{"/home/older/a", "/home/older/a", false},
		{"rel/x", "rel/x", false},
	} {
		got, ok := remapHomeTarget(c.in, "/home/old", "/srv/new")
		if got != c.out || ok != c.ok {
			t.Fatalf("%s -> %s %v (want %s %v)", c.in, got, ok, c.out, c.ok)
		}
	}
}

func TestHomeScanScopeAndValidUTF8(t *testing.T) {
	prof := &profile.Profile{
		Detect: &profile.Detect{MarkersAny: []string{"custom.json", "auth.json"}},
		Paths:  []profile.PathRule{{Root: "state", Glob: "cordis.patch.yml", Category: profile.CategoryConfig}},
	}
	names := profileConfigNames(prof)
	if names["auth.json"] {
		t.Fatal("credential marker auth.json must not enter profileConfigNames")
	}
	if !shouldScanHomeFile(vault.File{Root: "state", Path: "cordis.patch.yml", Category: profile.CategoryConfig, Size: 10}, names) {
		t.Fatal("expected CategoryConfig yml file to be scanned")
	}
	if !shouldScanHomeFile(vault.File{Root: "state", Path: "custom.json", Category: profile.CategorySecrets, Size: 10}, names) {
		t.Fatal("expected top-level state marker config classified as secrets to be scanned")
	}
	if !shouldScanHomeFile(vault.File{Root: "state", Path: "config.yaml", Category: profile.CategorySecrets, Size: 10}, names) {
		t.Fatal("expected top-level state config.yaml classified as secrets to be scanned")
	}
	if shouldScanHomeFile(vault.File{Root: "state", Path: "auth.json", Category: profile.CategorySecrets, Size: 10}, names) {
		t.Fatal("auth.json must not be scanned")
	}
	if shouldScanHomeFile(vault.File{Root: "state", Path: "sub/config.yaml", Category: profile.CategorySecrets, Size: 10}, names) {
		t.Fatal("nested CategorySecrets file must not be scanned")
	}
	if shouldScanHomeFile(vault.File{Root: "workspace", Path: "config.yaml", Category: "unclassified", Size: 10}, names) {
		t.Fatal("non-state root unclassified file must not be scanned")
	}
	if shouldScanHomeFile(vault.File{Root: "state", Path: "workspace/config.yaml", Category: "unclassified", Size: 10}, names) {
		t.Fatal("workspace file should not be scanned")
	}
	if shouldScanHomeFile(vault.File{Root: "state", Path: "a/node_modules/config.yaml", Category: "unclassified", Size: 10}, names) {
		t.Fatal("nested node_modules file should not be scanned")
	}
	if shouldScanHomeFile(vault.File{Root: "state", Path: "sessions/config.yaml", Category: profile.CategorySessions, Size: 10}, names) {
		t.Fatal("sessions file should not be scanned")
	}
	if validUTF8([]byte("ok\x00bad")) {
		t.Fatal("validUTF8 must reject NUL bytes")
	}
}

func TestStageManifestCoversRewrittenConfig(t *testing.T) {
	rp := &RevivePlan{files: []vault.File{{Root: "state", Path: "config.yaml", Size: 3, SHA256: "aa", Category: "config"}},
		rewritten: map[string]*rewrittenFile{"state:config.yaml": {data: []byte("xy"), orig: []byte("abc"), newSHA: "bb", origSHA: "aa"}}}
	man := stageManifest(rp)
	if len(man) != 2 || man[0].Path != "state/config.yaml"+homeBakSuffix || man[0].Size != 3 || man[1].Size != 2 || man[1].SHA256 != "bb" {
		t.Fatalf("%+v", man)
	}
}

func TestEmptyDirsRoundTripAndHomeRewrite(t *testing.T) {
	s, home, _, _ := sealedWorld(t)
	ctx := context.Background()
	state := filepath.Join(home, ".demo")
	os.MkdirAll(filepath.Join(state, "notes/empty/inner"), 0o755)
	os.WriteFile(filepath.Join(state, "demo.json"), []byte("mcp: /oldhome/bin/valise-porter\nsecret: topsecret\n"), 0o644)
	rep, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(state, "notes/empty"))
	os.WriteFile(filepath.Join(state, "demo.json"), []byte("changed"), 0o644)
	rp, err := s.PlanRevive(ctx, ReviveOptions{Target: "box", SnapshotID: rep.SnapshotID, Mode: revive.ModeRollback, AllowDraft: true}, pass)
	if err != nil || len(rp.Blockers) != 0 {
		t.Fatalf("%+v %v", rp, err)
	}
	if len(rp.dirs) == 0 {
		t.Fatalf("empty directories not carried: %+v", rp.dirs)
	}
	rp.sourceHome, rp.targetFacts.Home = "/oldhome", "/newhome"
	s.planHomePaths(rp, rp.snapshotHeader, &rp.Plan.Profile)
	if !rp.HomeChanged || len(rp.ConfigHomeHits) != 1 || !rp.ConfigHomeHits[0].IsValisePorter || rp.ConfigHomeHits[0].Line != 1 {
		t.Fatalf("%+v", rp.ConfigHomeHits)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "demo.json")); strings.Contains(strings.Join([]string{rp.ConfigHomeHits[0].OldPath, rp.ConfigHomeHits[0].NewPath}, ""), "topsecret") || len(b) == 0 {
		t.Fatal("hits must carry paths only")
	}
	c := proceedConfirm()
	c.RewriteConfigHomePaths = true
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", c)
	if err != nil || res.State != revive.StateRestored {
		t.Fatalf("%+v %v", res, err)
	}
	if st, err := os.Stat(filepath.Join(state, "notes/empty/inner")); err != nil || !st.IsDir() {
		t.Fatalf("empty dir not restored: %v", err)
	}
	if got := readFile(t, filepath.Join(state, "demo.json")); got != "mcp: /newhome/bin/valise-porter\nsecret: topsecret\n" {
		t.Fatalf("%q", got)
	}
	if got := readFile(t, filepath.Join(state, "demo.json"+homeBakSuffix)); got != "mcp: /oldhome/bin/valise-porter\nsecret: topsecret\n" {
		t.Fatalf("%q", got)
	}
	if rp.RewrittenConfigPaths != 1 || rp.remainingConfigHomeHits() != 0 {
		t.Fatalf("%+v", rp)
	}
}
