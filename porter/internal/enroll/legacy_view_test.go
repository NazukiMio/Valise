package enroll

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
	identitystore "valise/porter/internal/identity"
)

func oldPair(t *testing.T, home string, r enroll.Record, p profile.Profile) {
	t.Helper()
	if err := os.MkdirAll(InstancesDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{r.InstanceID + ".json": r, r.InstanceID + ".profile.json": p} {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(InstancesDir(home), name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func metadataState(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
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
	return out
}

func TestLegacyQueriesAreReadOnlyAndKeepUnboundIdentity(t *testing.T) {
	home := t.TempDir()
	p := profile.Profile{ID: "demo", ProfileVersion: 3}
	r := enroll.Record{InstanceID: "demo", ProfileID: p.ID, ProfileVersion: 3, Roots: map[string]string{"state": filepath.Join(home, "state")}}
	oldPair(t, home, r, p)
	before := metadataState(t, home)
	for range 2 {
		got, err := ReadRecord(home, "demo")
		if err != nil || got == nil || got.ProfileVersion != 3 {
			t.Fatal(got, err)
		}
		if got, err := ReadProfile(home, "demo"); err != nil || got.ProfileVersion != 3 {
			t.Fatal(got, err)
		}
		if got, err := ListRecords(home); err != nil || len(got) != 1 || InstallOf(home, "demo") != "demo" {
			t.Fatal(got, err)
		}
		if b, err := identitystore.Read(home, "demo"); err != nil || b.Agent != nil {
			t.Fatal("query created an identity", b, err)
		}
	}
	if after := metadataState(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("query changed files, modes or timestamps", before, after)
	}
}

func TestCommittedRecordWinsAndOldDuplicateCannotInfluenceSibling(t *testing.T) {
	home := t.TempDir()
	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	r := enroll.Record{InstanceID: "demo", ProfileID: p.ID, ProfileVersion: 1, Roots: map[string]string{"state": filepath.Join(home, "accepted")}}
	if _, err := WriteRecord(home, r, p); err != nil {
		t.Fatal(err)
	}
	stale, staleProfile := r, p
	stale.Roots = map[string]string{"state": filepath.Join(home, "stale")}
	stale.ProfileVersion, staleProfile.ProfileVersion = 99, 99
	oldPair(t, home, stale, staleProfile)
	named := enroll.Record{InstanceID: "demo-old", Name: "old", ProfileID: p.ID, ProfileVersion: 4, Roots: map[string]string{"state": filepath.Join(home, "old")}}
	namedProfile := p
	namedProfile.ProfileVersion = 4
	oldPair(t, home, named, namedProfile)
	before := metadataState(t, home)
	if got, err := ReadRecord(home, "demo"); err != nil || got.ProfileVersion != 1 || got.Roots["state"] != r.Roots["state"] {
		t.Fatal("old duplicate replaced accepted facts", got, err)
	}
	if got, err := ReadProfile(home, named.InstanceID); err != nil || got.ProfileVersion != 4 {
		t.Fatal("accepted ID's old duplicate contaminated sibling", got, err)
	}
	if got, err := ListRecords(home); err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if !reflect.DeepEqual(before, metadataState(t, home)) {
		t.Fatal("mixed listing rewrote accepted or legacy metadata")
	}
	// A retry of an explicit write finishes archiving an ignored duplicate;
	// it must preserve the current accepted profile instead of importing 99.
	if _, err := WriteRecord(home, r, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(InstancesDir(home), "demo.json")); !os.IsNotExist(err) {
		t.Fatal("explicit write did not finish archiving duplicate", err)
	}
	if got, err := ReadProfile(home, "demo"); err != nil || got.ProfileVersion != 1 {
		t.Fatal("explicit retry imported a stale profile", got, err)
	}
}

func TestExplicitLegacyWriteScopesConversionAndPreservesArchives(t *testing.T) {
	home := t.TempDir()
	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	r := enroll.Record{InstanceID: "demo", ProfileID: p.ID, ProfileVersion: 1, Roots: map[string]string{"state": filepath.Join(home, "state")}}
	oldPair(t, home, r, p)
	other, otherProfile := r, p
	other.InstanceID, other.ProfileID, otherProfile.ID = "other", "other", "other"
	oldPair(t, home, other, otherProfile)
	archive := filepath.Join(InstancesDir(home), "demo.legacy.json")
	if err := os.WriteFile(archive, []byte("previous archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetDisplayName(home, "demo", "New name"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(archive); err != nil || string(b) != "previous archive" {
		t.Fatal("conversion overwrote old archive", err)
	}
	if _, err := os.Stat(filepath.Join(InstancesDir(home), "demo.legacy.2.json")); err != nil {
		t.Fatal("new archive missing", err)
	}
	if _, err := os.Stat(DossierPath(home, "other")); !os.IsNotExist(err) {
		t.Fatal("conversion touched another installation", err)
	}
	// An ignored duplicate must not bring the Agent back after removal.
	oldPair(t, home, r, p)
	if err := RemoveRecord(home, "demo"); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadRecord(home, "demo"); err != nil || got != nil {
		t.Fatal("removed Agent returned from its duplicate", got, err)
	}
}

func TestBrokenCommittedMetadataCannotFallBackToOldCopy(t *testing.T) {
	for _, broken := range []string{"dossier", "profile"} {
		t.Run(broken, func(t *testing.T) {
			home := t.TempDir()
			p := profile.Profile{ID: "demo", ProfileVersion: 1}
			r := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": filepath.Join(home, "state")}}
			if _, err := WriteRecord(home, r, p); err != nil {
				t.Fatal(err)
			}
			oldPair(t, home, r, p)
			path := DossierPath(home, "demo")
			if broken == "profile" {
				path = dossierProfilePath(home, "demo")
			}
			if err := os.WriteFile(path, []byte("invalid committed metadata"), 0600); err != nil {
				t.Fatal(err)
			}
			before := metadataState(t, home)
			if _, err := ReadProfile(home, "demo"); err == nil {
				t.Fatal("broken committed profile replaced from old copy")
			}
			if broken == "dossier" {
				if _, err := ReadRecord(home, "demo"); err == nil {
					t.Fatal("broken committed record replaced from old copy")
				}
			}
			if !reflect.DeepEqual(before, metadataState(t, home)) {
				t.Fatal("query repaired broken metadata implicitly")
			}
		})
	}
}
