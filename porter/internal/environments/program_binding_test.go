package environments

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	porterenroll "valise/porter/internal/enroll"
)

func bindingFixture(t *testing.T) (ProgramStore, identity.AgentRef, BuildInput, []byte) {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	in := fixtureInput(t, []byte("private executable"))
	archive, err := io.ReadAll(in.Archive)
	if err != nil {
		t.Fatal(err)
	}
	in.Archive = bytes.NewReader(archive)
	_, agent, err := porterenroll.AcceptRecord(home, enroll.Record{InstanceID: "fixture", ProfileID: "fixture", ProfileVersion: in.Profile.ProfileVersion,
		Roots: map[string]string{"state": filepath.Join(home, ".fixture"), "workspace": filepath.Join(home, ".fixture/work")}, EnrolledAt: time.Now().UTC()}, in.Profile)
	if err != nil {
		t.Fatal(err)
	}
	in.Target.Source.Agent = agent.Ref
	return ProgramStore{Home: home}, agent.Ref, in, archive
}

func originalInput(in BuildInput, archive []byte) OriginalProgramInput {
	return OriginalProgramInput{Profile: in.Profile, Overrides: in.Overrides, Layout: in.Layout, Program: in.Program, Archive: bytes.NewReader(archive), MaxBytes: in.MaxBytes}
}

func initialRegistry(t *testing.T, s ProgramStore, agent identity.AgentRef) environment.Registry {
	t.Helper()
	r, err := (Store{Home: s.Home}).Update(agent, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(agent) })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProgramBindingsPersistOriginalAndCandidateWithoutCopyingActiveData(t *testing.T) {
	s, agent, in, archive := bindingFixture(t)
	active := filepath.Join(s.Home, ".fixture/SOUL.md")
	if err := os.MkdirAll(filepath.Dir(active), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte("active data unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	w, old, err := s.BuildOriginal(context.Background(), agent, originalInput(in, archive))
	if err != nil {
		t.Fatal(err)
	}
	if old.Source != nil || old.Receipt != nil || old.Origin != "installed_original" {
		t.Fatal("original programme fabricated a snapshot", old)
	}
	if _, err := os.Stat(filepath.Join(w.HomeDir(), ".fixture/SOUL.md")); !os.IsNotExist(err) {
		t.Fatal("copied active data into programme template")
	}
	r := initialRegistry(t, s, agent)
	r, err = (Store{Home: s.Home}).Update(agent, r.Revision, func(r environment.Registry) (environment.Registry, error) {
		return r.Begin(strings.Repeat("a", 32), in.Target.Source.Snapshot)
	})
	if err != nil {
		t.Fatal(err)
	}
	c, next, err := s.BuildBound(context.Background(), agent, r.Operation.Candidate, in)
	if err != nil || next.Source == nil || next.Source.Snapshot != in.Target.Source.Snapshot || !next.Receipt.CandidateBuilt {
		t.Fatal(next, err)
	}
	fresh := ProgramStore{Home: s.Home}
	for _, b := range []*ProgramBinding{old, next} {
		if err := fresh.Verify(context.Background(), agent, b.Ref, Digest(b)); err != nil {
			t.Fatal("reopened programme failed verification", err)
		}
		opened, spec, err := fresh.Open(context.Background(), agent, b.Ref, Digest(b))
		if err != nil || Digest(spec) != Digest(b.Spec) || filepath.Base(filepath.Dir(opened.ProgramDir())) != b.Workspace {
			t.Fatal("reopen lost binding", err)
		}
	}
	if !strings.HasPrefix(c.Workspace.ProgramDir(), filepath.Join(s.Home, ".local/state/valise/programs")) {
		t.Fatal("programme outside private revision")
	}
	if b, _ := os.ReadFile(active); string(b) != "active data unchanged" {
		t.Fatal("programme preparation changed active data")
	}
	in.Archive = bytes.NewReader(archive)
	if _, _, err := s.BuildBound(context.Background(), agent, next.Ref, in); err == nil {
		t.Fatal("overwrote immutable candidate revision")
	}
}

func TestProgrammeVerificationRejectsChangedOrAliasedMaterials(t *testing.T) {
	for _, change := range []string{"content", "extra_file", "extra_dir", "mode", "hardlink", "symlink", "home_alias", "workspace_alias"} {
		t.Run(change, func(t *testing.T) {
			s, agent, in, archive := bindingFixture(t)
			w, b, err := s.BuildOriginal(context.Background(), agent, originalInput(in, archive))
			if err != nil {
				t.Fatal(err)
			}
			initialRegistry(t, s, agent)
			file := filepath.Join(w.ProgramDir(), ".fixture/program/agent")
			switch change {
			case "content":
				err = os.WriteFile(file, []byte("Private executable"), 0700)
			case "extra_file":
				err = os.WriteFile(filepath.Join(filepath.Dir(file), "extra"), []byte("extra"), 0600)
			case "extra_dir":
				err = os.Mkdir(filepath.Join(filepath.Dir(file), "extra"), 0700)
			case "mode":
				err = os.Chmod(file, 0600)
			case "hardlink":
				err = os.Link(file, filepath.Join(t.TempDir(), "link"))
			case "symlink":
				err = os.Rename(file, file+".moved")
				if err == nil {
					err = os.Symlink("/etc/passwd", file)
				}
			case "home_alias":
				err = os.Rename(w.HomeDir(), w.HomeDir()+".moved")
				if err == nil {
					err = os.Symlink(w.HomeDir()+".moved", w.HomeDir())
				}
			case "workspace_alias":
				dir := filepath.Dir(w.ProgramDir())
				err = os.Rename(dir, dir+".moved")
				if err == nil {
					err = os.Symlink(dir+".moved", dir)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(agent, b.Ref); err != nil {
				t.Fatal("metadata read should retain the historical binding", err)
			}
			if err := s.Verify(context.Background(), agent, b.Ref, Digest(b)); err == nil {
				t.Fatal("changed programme accepted", change)
			}
		})
	}
}

func TestProgramBindingReadsDoNotCreateStateAndOldRefsCannotRunNewRevisions(t *testing.T) {
	s, agent, in, archive := bindingFixture(t)
	if _, err := s.Load(agent, environment.Ref{Slot: environment.A, Revision: 1}); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Home, ".local")); !os.IsNotExist(err) {
		t.Fatal("missing read initialized store")
	}
	_, old, err := s.BuildOriginal(context.Background(), agent, originalInput(in, archive))
	if err != nil {
		t.Fatal(err)
	}
	r := initialRegistry(t, s, agent)
	store := Store{Home: s.Home}
	update := func(fn func(environment.Registry) (environment.Registry, error)) {
		t.Helper()
		var err error
		r, err = store.Update(agent, r.Revision, fn)
		if err != nil {
			t.Fatal(err)
		}
	}
	op := strings.Repeat("a", 32)
	update(func(r environment.Registry) (environment.Registry, error) {
		return r.Begin(op, in.Target.Source.Snapshot)
	})
	_, next, err := s.BuildBound(context.Background(), agent, r.Operation.Candidate, in)
	if err != nil {
		t.Fatal(err)
	}
	update(func(r environment.Registry) (environment.Registry, error) { return r.Prepared(op) })
	update(func(r environment.Registry) (environment.Registry, error) { return r.StopOriginal(op) })
	update(func(r environment.Registry) (environment.Registry, error) { return r.OriginalStopped(op) })
	update(func(r environment.Registry) (environment.Registry, error) { return r.StartCandidate(op) })
	update(func(r environment.Registry) (environment.Registry, error) { return r.GatewayPassed(op) })
	update(func(r environment.Registry) (environment.Registry, error) { return r.ConfirmUsable(op, next.Ref) })
	update(func(r environment.Registry) (environment.Registry, error) {
		return r.Begin(strings.Repeat("b", 32), in.Target.Source.Snapshot)
	})
	if err := s.Verify(context.Background(), agent, old.Ref, Digest(old)); err == nil {
		t.Fatal("old A reference accepted after reuse")
	}
	if _, err := s.Load(agent, old.Ref); err != nil {
		t.Fatal("history should stay readable", err)
	}
	if err := s.Verify(context.Background(), agent, next.Ref, Digest(next)); err != nil {
		t.Fatal("active B lost verification", err)
	}
}
