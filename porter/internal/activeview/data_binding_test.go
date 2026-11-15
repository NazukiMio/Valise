package activeview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

func initialDataFixture(t *testing.T) (Reader, identity.AgentRef, environments.InitialData, environments.Store) {
	t.Helper()
	home := privateHome(t)
	path := filepath.Join(home, ".fixture")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{ID: "fixture", ProfileVersion: 1, Service: &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "fixture.service"}}}}
	_, a, err := porterenroll.AcceptRecord(home, enroll.Record{InstanceID: "fixture", ProfileID: p.ID, ProfileVersion: p.ProfileVersion, Roots: map[string]string{"state": path}, EnrolledAt: time.Now().UTC()}, p)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := st.Sys().(*syscall.Stat_t)
	b := environments.InitialData{Schema: "valise-initial-data/1", Agent: a.Ref, Ref: environment.Ref{Slot: environment.A, Revision: 1},
		OperationID: strings.Repeat("a", 32), Materials: strings.Repeat("b", 64), ProfileDigest: environments.Digest(p), Roots: map[string]string{"state": path},
		Directories: []revive.ManagedRoot{{ID: "state", Device: uint64(stat.Dev), Inode: stat.Ino}},
		Native: managedruntime.NativeRef{Unit: "fixture.service", Definition: strings.Repeat("c", 64), Executable: filepath.Join(home, "original-engine"), ExecutableSHA256: strings.Repeat("d", 64), MainPID: 2, KernelStart: "1",
			ControlGroup: fmt.Sprintf("/user.slice/user-%d.slice/user@%d.service/app.slice/fixture.service", os.Getuid(), os.Getuid())}}
	s := environments.InitialDataStore{Home: home}
	if _, err := s.Load(a.Ref); !os.IsNotExist(err) {
		t.Fatal("initial read initialized metadata", err)
	}
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	store := environments.Store{Home: home}
	if _, err := store.Update(a.Ref, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(a.Ref) }); err != nil {
		t.Fatal(err)
	}
	return Reader{Home: home}, a.Ref, b, store
}

func TestInitialNativeDataRemainsReadableWithoutProgrammeOrService(t *testing.T) {
	r, agent, anchor, store := initialDataFixture(t)
	ctx := context.Background()
	check := func() {
		t.Helper()
		b, err := r.ReadData(ctx, agent)
		if err != nil || b == nil || !b.Native || b.Ref != anchor.Ref || b.Program != nil || b.ProgramIssue == "" {
			t.Fatal("data backup depended on missing programme/service", b, err)
		}
		if err := r.VerifyProgram(ctx, b); err == nil {
			t.Fatal("missing programme passed separate source verification")
		}
		if err := r.ValidateData(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	check() // registry created; Begin not yet committed
	reg, _ := store.Load(agent)
	reg, err := store.Update(agent, reg.Revision, func(v environment.Registry) (environment.Registry, error) {
		return v.Begin(anchor.OperationID, identity.SnapshotRef{VaultID: vault.NewID("v", time.Now()), SnapshotID: vault.NewID("s", time.Now())})
	})
	if err != nil {
		t.Fatal(err)
	}
	check() // failed during data/programme preparation
	if _, err := store.Update(agent, reg.Revision, func(v environment.Registry) (environment.Registry, error) {
		return v.CancelPreparation(anchor.OperationID)
	}); err != nil {
		t.Fatal(err)
	}
	check() // cancelled with materials retained
	if err := (environments.InitialDataStore{Home: r.Home}).Save(anchor); err == nil {
		t.Fatal("anchor replaced while registry exists")
	}
	root := anchor.Roots["state"]
	if err := os.Rename(root, root+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if b, err := r.ReadData(ctx, agent); err == nil || b != nil {
		t.Fatal("new directory borrowed old native binding", b, err)
	}
}

func TestInitialDataCannotBypassFormalParentOrChangedRegistry(t *testing.T) {
	r, agent, anchor, store := initialDataFixture(t)
	b, err := r.ReadData(context.Background(), agent)
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := store.Load(agent)
	if _, err := store.Update(agent, reg.Revision, func(v environment.Registry) (environment.Registry, error) { return v.SetMode(environment.Compact) }); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateData(context.Background(), b); err == nil {
		t.Fatal("stale binding survived concurrent settings change")
	}
	dir := filepath.Join(r.Home, ".local/state/valise/activations", environment.Key(agent))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, anchor.OperationID+".json"), []byte(`{"state":"running"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := r.ReadData(context.Background(), agent); err == nil || b != nil {
		t.Fatal("damaged formal parent fell back to initial anchor", b, err)
	}
}
