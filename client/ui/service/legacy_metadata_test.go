package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
)

func savedMetadata(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range []string{".config/valise", ".local/state/valise"} {
		err := filepath.WalkDir(filepath.Join(home, dir), func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			var digest [32]byte
			if info.Mode().IsRegular() {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				digest = sha256.Sum256(b)
			}
			rel, _ := filepath.Rel(home, path)
			out[rel] = fmt.Sprintf("%s:%d:%x", info.Mode(), info.ModTime().UnixNano(), digest)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestLegacyMetadataQueriesOverFakeSSHDoNotImportOrAdvertiseNative(t *testing.T) {
	s, home := enrollWorld(t)
	dir := filepath.Join(home, ".config/valise/instances")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	r := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": filepath.Join(home, ".demo")}}
	p := profile.Profile{ID: "demo", ProfileVersion: 1, Status: profile.StatusDraft, NativeCLI: []string{"/bin/cat"}}
	for name, value := range map[string]any{"demo.json": r, "demo.profile.json": p} {
		b, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	before := savedMetadata(t, home)
	for range 2 {
		var result enroll.InstanceGetResult
		if err := s.call(ctx, "box", enroll.MethodInstanceGet, enroll.InstanceGetParams{InstanceID: "demo"}, &result); err != nil || result.Record == nil {
			t.Fatal(result, err)
		}
		rows, err := s.Instances(ctx, "box")
		if err != nil || len(rows) != 1 || rows[0].InstallID != "demo" || rows[0].Identity == nil || rows[0].Identity.IdentityState != identity.LegacyUnbound || rows[0].NativeCLI {
			t.Fatal("legacy query accepted metadata or advertised an unavailable executor", rows, err)
		}
	}
	if result, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo"}); err != nil || result.Report.Previous == nil {
		t.Fatal("read-only enrollment lost old facts", result, err)
	}
	if after := savedMetadata(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only RPCs changed files, modes or timestamps", before, after)
	}
	// Explicit acceptance still upgrades the format and establishes identity.
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Assign: []string{"state:notes/**=memory"}, Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo.legacy.json")); err != nil {
		t.Fatal("accepted conversion did not preserve the old record", err)
	}
	rows, err := s.Instances(ctx, "box")
	if err != nil || len(rows) != 1 || rows[0].Identity == nil || rows[0].Identity.IdentityState != identity.Bound {
		t.Fatal("explicit acceptance failed to bind identity", rows, err)
	}
	accepted := rows[0].Identity.Agent.Ref
	oldArchive := readFile(t, filepath.Join(dir, "demo.legacy.json"))
	// A partial old conversion can leave a duplicate beside committed facts.
	// Explicitly accepting again finishes that archive without importing it.
	p.ProfileVersion = 99
	for name, value := range map[string]any{"demo.json": r, "demo.profile.json": p} {
		b, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Assign: []string{"state:notes/**=memory"}, Accept: true}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dir, "demo.legacy.json")) != oldArchive {
		t.Fatal("explicit retry overwrote prior archive")
	}
	if _, err := os.Stat(filepath.Join(dir, "demo.legacy.2.json")); err != nil {
		t.Fatal("retry did not archive the duplicate", err)
	}
	rows, err = s.Instances(ctx, "box")
	if err != nil || len(rows) != 1 || rows[0].ProfileVersion != 1 || rows[0].Identity == nil || rows[0].Identity.Agent == nil || rows[0].Identity.Agent.Ref != accepted {
		t.Fatal("retry imported old profile or changed generation", rows, err)
	}
}
