package stationsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/vault"
)

type execRunner struct{}

func (execRunner) Run(ctx context.Context, timeout time.Duration, argv ...string) (int, []byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode(), []byte(out.String()), []byte(errb.String()), nil
	}
	return 0, []byte(out.String()), []byte(errb.String()), err
}

// rcloneStation is a real rclone with a "local" remote: no login involved.
func rcloneStation(t *testing.T) (Station, string) {
	t.Helper()
	bin, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not installed")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "rclone.conf")
	os.WriteFile(conf, []byte("[valise-t]\ntype = local\n"), 0o600)
	cloud := filepath.Join(dir, "cloud")
	return Station{Name: "cloud", Kind: MethodRclone, Host: "valise-t", Tmp: filepath.Join(dir, "tmp"), Rclone: bin, RcloneConfig: conf}, cloud
}

func leafFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "leaf")
	pack := []byte("pack bytes")
	id := vault.SHA256Hex(pack)
	for rel, body := range map[string]string{
		vault.PackPath(id): string(pack), "index/s_1.json": "{}", "snapshots/s_1.age": "x", "snapshots/s_1.meta.json": "{}",
		"epochs/1.json": "{}", "keys/dedupe.1.age": "k", "VAULT": "{}", "PERIOD": "{}", "packs/aa/tmp-1": "partial",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(body), 0o600)
	}
	return dir
}

func TestRcloneRemote(t *testing.T) {
	st, cloud := rcloneStation(t)
	leaf := leafFixture(t)
	ctx := context.Background()
	dest := st.Dest(cloud + "/box/demo/2026-11")
	method, _, err := With(ctx, execRunner{}, st, func(r Remote) error {
		if err := r.Probe(ctx, dest); err != nil {
			return err
		}
		if list, err := r.List(ctx, dest); err != nil || len(list) != 0 {
			t.Fatalf("missing base: %v %v", list, err)
		}
		if err := r.SyncLeaf(ctx, leaf, dest); err != nil {
			return err
		}
		// Append-only: a changed local file is not uploaded again.
		os.WriteFile(filepath.Join(leaf, "VAULT"), []byte("changed"), 0o600)
		if err := r.SyncLeaf(ctx, leaf, dest); err != nil {
			return err
		}
		list, err := r.List(ctx, dest)
		if err != nil {
			return err
		}
		files, deleting := LeafEntries(list)
		if deleting || len(files) != 8 || files[0].Stamp == "" {
			t.Fatalf("%+v", files)
		}
		got, err := ReadLeaf(ctx, r, dest, files, t.TempDir(), true, nil)
		if err != nil {
			return err
		}
		if string(got.Small["VAULT"]) != "{}" || len(got.Hashed) != 1 || len(IntegrityProblems(got)) != 0 {
			t.Fatalf("%+v", got)
		}
		return r.DeleteDir(ctx, st.Dest(cloud+"/box/demo"), "2026-11")
	})
	if err != nil || method != MethodRclone {
		t.Fatal(method, err)
	}
	if _, err := os.Stat(filepath.Join(cloud, "box", "demo", "2026-11")); !os.IsNotExist(err) {
		t.Fatalf("leaf left on the cloud: %v", err)
	}
}

// IntegrityProblems is the no-key check of a reading.
func IntegrityProblems(r *LeafReading) []vault.Problem {
	var out []vault.Problem
	for _, p := range vault.IntegrityCheck(r.Materials, func(rel string) ([]byte, error) { return r.Small[rel], nil }) {
		if !strings.Contains(p.Where, "index") && !strings.Contains(p.Where, "meta") {
			out = append(out, p)
		}
	}
	return out
}
