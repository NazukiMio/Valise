package stationsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailureNamesTheCause(t *testing.T) {
	st := Station{Name: "nas", Host: "nas-test"}
	for stderr, want := range map[string]string{
		"ssh: Could not resolve hostname nas-test: Name or service not known": "不认识主机名 nas-test",
		"nas@1.2.3.4: Permission denied (publickey).":                         "拒绝了登录",
		"Host key verification failed.":                                       "还没有确认过 nas-test",
		"ssh: connect to host 1.2.3.4 port 22: Connection refused":            "连不上 nas-test",
	} {
		ue := failure(st, "同步到 nas 失败", stderr)
		if !strings.Contains(ue.Reason, want) || ue.Code != UnreachableCode {
			t.Errorf("%q → %q (%s)", stderr, ue.Reason, ue.Code)
		}
	}
	if failure(st, "x", "rsync: write failed: No space left on device").Code == UnreachableCode {
		t.Error("a full disk is not unreachable")
	}
}

// The listing format a TrueNAS sftp-server printed (2026-11-02): links "?",
// owner names, full paths.
func TestParseLongList(t *testing.T) {
	out := `sftp> -ls -ln "/mnt/test/nas/valise-vault/"
drwxr-xr-x    ? nas      nas             6 Oct 31 10:22 /mnt/test/nas/valise-vault/ubuntu-vm
-rw-------    ? nas      nas         10565 Nov  1 08:19 /mnt/test/nas/valise-vault/TREE
Can't ls: "/x" not found`
	es := parseLongList(out)
	if len(es) != 2 || !es[0].Dir || es[1].Size != 10565 || es[1].Path != "/mnt/test/nas/valise-vault/TREE" {
		t.Fatalf("%+v", es)
	}
}

func TestSFTPOptions(t *testing.T) {
	got := strings.Join(sftpOptions([]string{"-i", "/k", "-p", "2222", "-l", "nas"}), " ")
	if got != "-i /k -P 2222 -o User=nas" {
		t.Fatal(got)
	}
}

func TestOldRsyncFallsBack(t *testing.T) {
	if !rsyncMissing(1, "rsync: unrecognized option `--mkpath'") || rsyncMissing(23, "rsync: some files could not be transferred") {
		t.Fatal("old rsync detection")
	}
}

func TestWhyNoRsync(t *testing.T) {
	// macOS openrsync: the reason comes first, then many lines of usage.
	out := "rsync: unrecognized option `--mkpath'\nusage: rsync [-0468B]\n\t[-f filter]\n\t[--backup-dir=dir]\n\t[--checksum-seed=NUM]\n\t[--compare-dest=dir]\n\t[--read-batch=file] [--remove-source-files] [--rsync-path=program]\n"
	if got := whyNoRsync(out); got != "这里的 rsync 版本太旧" {
		t.Fatal(got)
	}
}

func TestValidRels(t *testing.T) {
	for _, ok := range []string{"snapshots/s_X.age", "index/a.json"} {
		if err := ValidRels([]string{ok}); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "/etc/passwd", "../x", "a/../../x", "a//b", "a/./b", "a\\b", ".", "a/..", "a\nb", `a"b`,
		"snapshots/*.age", "index/s_?.json", "packs/[a]/x.pack", "packs/a/x].pack",
		" snapshots/a.age", "snapshots/a.age ", "\tsnapshots/a.age", "#snapshots/a.age", ";snapshots/a.age",
	} {
		if err := ValidRels([]string{bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

type captureRunner struct {
	argv  [][]string
	batch string
}

func (c *captureRunner) Run(_ context.Context, _ time.Duration, argv ...string) (int, []byte, []byte, error) {
	c.argv = append(c.argv, argv)
	for i, a := range argv {
		if a == "-b" && i+1 < len(argv) {
			b, _ := os.ReadFile(argv[i+1])
			c.batch = string(b)
		}
	}
	return 0, nil, nil, nil
}

func TestDeleteFilesCommands(t *testing.T) {
	st := Station{Name: "nas", Host: "nas", Tmp: filepath.Join(t.TempDir(), "tmp")}
	c := &captureRunner{}
	rels := []string{"snapshots/s_A.age", "index/s_A.json"}
	if err := (&sftpRemote{run: c, st: st}).DeleteFiles(context.Background(), "nas:/data/box/demo/x", rels); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.batch, `-rm "/data/box/demo/x/snapshots/s_A.age"`) || !strings.Contains(c.batch, `-rm "/data/box/demo/x/index/s_A.json"`) {
		t.Fatalf("sftp batch: %q", c.batch)
	}
	c = &captureRunner{}
	if err := (&rsyncRemote{run: c, st: st}).DeleteFiles(context.Background(), "nas:/data/x", rels); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(c.argv[0], " ")
	if !strings.Contains(joined, "--delete") || !strings.Contains(joined, "--include /index/s_A.json") || !strings.HasSuffix(joined, "nas:/data/x/") {
		t.Fatalf("rsync argv: %s", joined)
	}
	if err := (&rsyncRemote{run: c, st: st}).DeleteFiles(context.Background(), "nas:/data/x", []string{"../x"}); err == nil {
		t.Fatal("escape accepted")
	}
	if err := (&rcloneRemote{run: c, st: st}).DeleteFiles(context.Background(), "r:x", []string{"/abs"}); err == nil {
		t.Fatal("abs accepted")
	}
}
