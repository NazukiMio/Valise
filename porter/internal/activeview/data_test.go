package activeview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/environments"
)

func dataFixture(t *testing.T) (Reader, *activationrecord.Record, *enroll.Record, *revive.Journal, string) {
	t.Helper()
	home := privateHome(t)
	root := filepath.Join(home, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	stat := st.Sys().(*syscall.Stat_t)
	registry := preparedRegistry(t)
	op := registry.Operation
	p := revive.Plan{OpID: vault.NewID("op", time.Now()), InstanceID: fixtureAgent.InstanceID, SnapshotID: op.Snapshot.SnapshotID, Mode: revive.ModeRollback, Body: revive.BodyPlan{Action: revive.BodyKeep}, Roots: []revive.RootPlan{{ID: "state", Path: root}}}
	parent := &activationrecord.Record{Plan: activationrecord.Plan{Agent: fixtureAgent, OperationID: op.ID, Snapshot: op.Snapshot, Original: op.Original, Candidate: op.Candidate, DataOpID: p.OpID}, DataPlanDigest: environments.Digest(p), DataManifestDigest: environments.Digest([]revive.ManifestEntry(nil))}
	m := &revive.ManagedDataRef{Agent: fixtureAgent, OperationID: op.ID, Snapshot: op.Snapshot, Original: op.Original, Candidate: op.Candidate, PlanDigest: parent.DataPlanDigest, ManifestDigest: parent.DataManifestDigest, OriginalRoots: []revive.ManagedRoot{{ID: "state", Device: uint64(stat.Dev), Inode: stat.Ino}}, CandidateRoots: []revive.ManagedRoot{{ID: "state", Device: uint64(stat.Dev), Inode: stat.Ino + 1}}}
	j := &revive.Journal{Plan: p, ManagedData: m, State: revive.StatePlanned, StageVerified: true, ManifestSaved: true, Manifest: []revive.ManifestEntry{}, Roots: []revive.JournalRoot{{ID: "state", Live: root, Stage: sibling(root, "stage", p.OpID, "state"), Prev: sibling(root, "prev", p.OpID, "state"), Failed: sibling(root, "failed", p.OpID, "state"), LiveExisted: true}}}
	dir := filepath.Join(home, ".local/state/valise/journal")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, p.OpID+".json")
	saveDataFixture(t, path, j)
	return Reader{Home: home}, parent, &enroll.Record{InstanceID: fixtureAgent.InstanceID, Roots: map[string]string{"state": root}}, j, path
}

func saveDataFixture(t *testing.T, path string, j *revive.Journal) {
	t.Helper()
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestActiveDataChecksDirectoryIncarnationWithoutDependingOnInactiveFiles(t *testing.T) {
	r, parent, record, j, path := dataFixture(t)
	if err := r.checkData(parent, record, parent.Plan.Original); err != nil {
		t.Fatal("unused candidate absence blocked current data", err)
	}
	root := record.Roots[profile.StateRoot]
	if err := os.Rename(root, root+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := r.checkData(parent, record, parent.Plan.Original); err == nil {
		t.Fatal("replacement current directory accepted")
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root+"-saved", root); err != nil {
		t.Fatal(err)
	}
	// The candidate's active descriptor is sufficient after acceptance, even
	// when the explicitly cleaned original no longer has a directory.
	j.ManagedData.OriginalRoots[0], j.ManagedData.CandidateRoots[0] = j.ManagedData.CandidateRoots[0], j.ManagedData.OriginalRoots[0]
	j.State = revive.StateRestored
	saveDataFixture(t, path, j)
	if err := r.checkData(parent, record, parent.Plan.Candidate); err != nil {
		t.Fatal("cleaned inactive state blocked active candidate", err)
	}
	if err := os.Rename(root, root+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root+"-saved", root); err != nil {
		t.Fatal(err)
	}
	if err := r.checkData(parent, record, parent.Plan.Candidate); err == nil {
		t.Fatal("current root alias accepted")
	}
}

func TestActiveDataRejectsChangedOrInsecureChildWithoutExposingJSON(t *testing.T) {
	for _, change := range []string{"profile", "scope", "manifest", "permissions", "hardlink", "unknown-field"} {
		t.Run(change, func(t *testing.T) {
			r, parent, record, j, path := dataFixture(t)
			var err error
			switch change {
			case "profile":
				j.Plan.Profile.ProfileVersion++
				saveDataFixture(t, path, j)
			case "scope":
				j.ManagedData.Agent.Generation = "another"
				saveDataFixture(t, path, j)
			case "manifest":
				j.Manifest = append(j.Manifest, revive.ManifestEntry{Path: "state/extra", SHA256: strings.Repeat("a", 64)})
				saveDataFixture(t, path, j)
			case "permissions":
				err = os.Chmod(path, 0644)
			case "hardlink":
				err = os.Link(path, path+".copy")
			case "unknown-field":
				b, _ := os.ReadFile(path)
				err = os.WriteFile(path, append([]byte(`{"fixture_private_content":1,`), b[1:]...), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := r.checkData(parent, record, environment.Ref{Slot: environment.A, Revision: 1}); err == nil {
				t.Fatal("inconsistent child accepted")
			}
		})
	}
}
