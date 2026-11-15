package revive

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
)

func nestedManagedWorld(t *testing.T) *managedWorld {
	t.Helper()
	w := newWorld(t)
	w.p.Roots = []profile.Root{{ID: "workspace", Default: "${STATE_DIR}/workspace"}, {ID: "deep", Default: "${STATE_DIR}/workspace/deep"}, {ID: "empty", Default: "${STATE_DIR}/empty"}}
	w.p.Paths = append(w.p.Paths, profile.PathRule{Root: "workspace", Glob: "**", Category: "memory"}, profile.PathRule{Root: "deep", Glob: "**", Category: "memory"})
	paths := map[string]string{"state": w.state, "workspace": filepath.Join(w.state, "workspace"), "deep": filepath.Join(w.state, "workspace/deep"), "empty": filepath.Join(w.state, "empty")}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(w.home, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(paths["workspace"], "old.md"), "old workspace", 0600)
	write(t, filepath.Join(paths["deep"], "old.md"), "old deep", 0600)
	_, agent, err := porterenroll.AcceptRecord(w.home, enroll.Record{InstanceID: "demo", ProfileID: w.p.ID, ProfileVersion: w.p.ProfileVersion, Roots: paths, EnrolledAt: time.Now().UTC()}, w.p)
	if err != nil {
		t.Fatal(err)
	}
	store := environments.Store{Home: w.home}
	r, err := store.Update(agent.Ref, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(agent.Ref) })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := identity.SnapshotRef{VaultID: vault.NewID("v", time.Now()), SnapshotID: vault.NewID("s", time.Now())}
	r, err = store.Update(agent.Ref, r.Revision, func(r environment.Registry) (environment.Registry, error) {
		return r.Begin(strings.Repeat("c", 32), snapshot)
	})
	if err != nil {
		t.Fatal(err)
	}
	p := w.plan()
	p.Roots, err = revive.CanonicalRoots(paths)
	if err != nil {
		t.Fatal(err)
	}
	p.Tier, p.SnapshotID, p.Carry, p.Unit, p.Body = vault.TierFull, snapshot.SnapshotID, false, "", revive.BodyPlan{Action: revive.BodyKeep}
	scope := revive.ManagedDataRef{Agent: agent.Ref, OperationID: r.Operation.ID, Snapshot: snapshot, Original: r.Operation.Original, Candidate: r.Operation.Candidate}
	return &managedWorld{world: w, p: p, scope: scope}
}

func beginNestedStage(t *testing.T, w *managedWorld) *revive.Journal {
	t.Helper()
	files := []snapFile{{"SOUL.md", "restored"}, {"workspace/task.md", "target workspace"}, {"workspace/deep/item.md", "target deep"}}
	var body bytes.Buffer
	tw := tar.NewWriter(&body)
	for _, f := range files {
		category := "memory"
		if f.path == "SOUL.md" {
			category = "identity"
		}
		w.manifest = append(w.manifest, revive.ManifestEntry{Path: "state/" + f.path, Size: int64(len(f.content)), SHA256: vault.SHA256Hex([]byte(f.content)), Category: category})
		if err := tw.WriteHeader(&tar.Header{Name: "state/" + f.path, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(f.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	j, err := BeginManagedData(context.Background(), w.r.Env, w.r.Store, w.p, w.scope, w.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StageWrite(w.r.Store, w.p.OpID, &body); err != nil {
		t.Fatal(err)
	}
	proof, err := StageVerify(w.r.Store, revive.StageVerifyParams{OpID: w.p.OpID, Manifest: w.manifest})
	if err != nil || len(proof.Problems) != 0 {
		t.Fatal(proof, err)
	}
	return j
}

func TestNestedManagedRootsExchangeOnceAndPreserveEmptyRoots(t *testing.T) {
	w := nestedManagedWorld(t)
	j := beginNestedStage(t, w)
	if len(j.Roots) != 1 || len(j.ManagedData.OriginalRoots) != 1 || len(j.ManagedData.CandidateRoots) != 1 || len(j.ManagedData.LogicalRoots) != 4 {
		t.Fatal("logical roots became separate swaps", j)
	}
	if st, err := os.Stat(filepath.Join(j.Roots[0].Stage, "empty")); err != nil || !st.IsDir() {
		t.Fatal("empty declared root missing", err)
	}
	bindings, err := ManagedBindings(w.p, environments.Layout{Home: w.home, Roots: map[string]string{"state": w.state, "workspace": filepath.Join(w.state, "workspace"), "deep": filepath.Join(w.state, "workspace/deep"), "empty": filepath.Join(w.state, "empty")}})
	if err != nil || len(bindings) != 1 || bindings[0].Host != w.state || bindings[0].Guest != w.state {
		t.Fatal("overlapping runtime mounts", bindings, err)
	}
	w.originalDown(t)
	result, err := w.r.ExchangeManagedData(context.Background(), w.p.OpID)
	if err != nil || result.State != revive.StateRestored || len(result.Prev) != 1 {
		t.Fatal(result, err)
	}
	if read(t, filepath.Join(w.state, "workspace/task.md")) != "target workspace" || read(t, filepath.Join(w.state, "workspace/deep/item.md")) != "target deep" {
		t.Fatal("nested target files misplaced")
	}
	if _, err := os.Stat(filepath.Join(w.state, "workspace/old.md")); !os.IsNotExist(err) {
		t.Fatal("old scope content survived", err)
	}
	w.candidateDown(t)
	result, err = w.r.RevertManagedData(context.Background(), w.p.OpID)
	if err != nil || result.State != revive.StateRolledBack {
		t.Fatal(result, err)
	}
	if read(t, filepath.Join(w.state, "workspace/old.md")) != "old workspace" || read(t, filepath.Join(w.state, "workspace/deep/old.md")) != "old deep" {
		t.Fatal("original nested data not preserved")
	}
}

func TestNestedManagedRootReplacementAndMappingTamperingRefused(t *testing.T) {
	for _, where := range []string{"before-exchange", "retained-original", "intermediate-link", "noncanonical-map"} {
		t.Run(where, func(t *testing.T) {
			w := nestedManagedWorld(t)
			if where == "noncanonical-map" {
				for i := range w.p.Roots {
					if w.p.Roots[i].ID == "deep" {
						w.p.Roots[i].SwapRoot, w.p.Roots[i].Rel = "workspace", "deep"
					}
				}
				if _, err := BeginManagedData(context.Background(), w.r.Env, w.r.Store, w.p, w.scope, nil); err == nil {
					t.Fatal("chained mapping accepted")
				}
				return
			}
			j := beginNestedStage(t, w)
			root := filepath.Join(w.state, "workspace")
			if where == "retained-original" {
				w.originalDown(t)
				if _, err := w.r.ExchangeManagedData(context.Background(), w.p.OpID); err != nil {
					t.Fatal(err)
				}
				w.candidateDown(t)
				root = filepath.Join(j.Roots[0].Prev, "workspace")
			}
			if err := os.Rename(root, root+"-kept"); err != nil {
				t.Fatal(err)
			}
			if where == "intermediate-link" {
				if err := os.Symlink(root+"-kept", root); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			if where == "retained-original" {
				if _, err := w.r.RevertManagedData(context.Background(), w.p.OpID); err == nil {
					t.Fatal("replaced original logical root reported restored")
				}
			} else {
				if err := checkManagedData(w.r.Store, j, environment.Building); err == nil {
					t.Fatal("changed logical root accepted")
				}
			}
		})
	}
}
