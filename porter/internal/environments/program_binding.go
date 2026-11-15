package environments

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/vault"
	porterenroll "valise/porter/internal/enroll"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/isolated"
)

const programBindingSchema = "valise-program-binding/1"

// ProgramBinding is private material metadata, not execution/health evidence.
// Its factories only accept a trusted source adapter's archive and manifest;
// no RPC accepts this record or BuildInput as supplied program materials.
type ProgramBinding struct {
	Schema    string               `json:"schema"`
	Agent     identity.AgentRef    `json:"agent"`
	Ref       environment.Ref      `json:"ref"`
	Origin    string               `json:"origin"`
	Source    *environment.Source  `json:"source,omitempty"`
	Profile   profile.Profile      `json:"profile"`
	Overrides []profile.Override   `json:"overrides"`
	Layout    Layout               `json:"layout"`
	Program   ProgramMaterial      `json:"program"`
	Runtime   *vault.RuntimeRecord `json:"runtime,omitempty"`
	Workspace string               `json:"workspace"`
	Paths     []string             `json:"paths"`
	Spec      isolated.Spec        `json:"spec"`
	Receipt   *Receipt             `json:"receipt,omitempty"`
}

type ProgramStore struct {
	Home string
	// Claim is an internal preparation callback. It persists ownership after
	// exclusive mkdir and before the first byte is written into this revision.
	Claim func(environment.Ref, string) error
}

func validProgramRef(ref environment.Ref) bool {
	return (ref.Slot == environment.A || ref.Slot == environment.B) && ref.Revision != 0
}

func (s ProgramStore) base(agent identity.AgentRef, ref environment.Ref) (string, error) {
	if protection.ValidateAgent(agent) != nil || !validProgramRef(ref) || !filepath.IsAbs(s.Home) || filepath.Clean(s.Home) != s.Home {
		return "", ErrProgram
	}
	return filepath.Join(s.Home, ".local/state/valise/programs", environment.Key(agent), string(ref.Slot), strconv.FormatUint(ref.Revision, 10)), nil
}

func (s ProgramStore) accepted(agent identity.AgentRef, p profile.Profile) error {
	binding, err := porteridentity.Read(s.Home, agent.InstanceID)
	if err != nil || binding.Agent == nil || binding.Agent.Ref != agent {
		return environment.ErrStale
	}
	dossier, accepted, err := porterenroll.ReadInstallation(s.Home, agent.InstanceID)
	if err != nil || dossier == nil || accepted == nil || dossier.InstallID != agent.InstallID || Digest(accepted) != Digest(p) {
		return ErrProgram
	}
	return nil
}

// BuildBound builds directly under the fixed candidate revision, then records
// the authenticated manifest. Partial failures are retained for the original
// operation's explicit cleanup; retry cannot silently replace the revision.
func (s ProgramStore) BuildBound(ctx context.Context, agent identity.AgentRef, ref environment.Ref, in BuildInput) (*Candidate, *ProgramBinding, error) {
	if err := s.accepted(agent, in.Profile); err != nil {
		return nil, nil, err
	}
	r, err := (Store{Home: s.Home}).Load(agent)
	if err != nil || r.Operation == nil || r.Operation.Phase != environment.Building || r.Operation.Candidate != ref || r.Operation.Snapshot != in.Target.Source.Snapshot {
		return nil, nil, environment.ErrStale
	}
	dir, err := s.reserve(agent, ref)
	if err != nil {
		return nil, nil, err
	}
	c, err := Build(ctx, dir, in)
	if err != nil {
		return nil, nil, err
	}
	_, paths, spec, err := prepareProgram(in)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyPrivateProgram(ctx, c.Workspace.ProgramDir(), in.Program, paths, in.Layout.Home); err != nil {
		return nil, nil, err
	}
	current, err := (Store{Home: s.Home}).Load(agent)
	if err != nil || current.Operation == nil || current.Operation.ID != r.Operation.ID || current.Operation.Phase != environment.Building || current.Operation.Candidate != ref {
		return nil, nil, environment.ErrStale
	}
	if err := s.accepted(agent, in.Profile); err != nil {
		return nil, nil, err
	}
	source := in.Target.Source
	b := &ProgramBinding{Schema: programBindingSchema, Agent: agent, Ref: ref, Origin: "snapshot", Source: &source, Profile: in.Profile, Overrides: in.Overrides,
		Layout: in.Layout, Program: in.Program, Runtime: in.Runtime, Workspace: filepath.Base(filepath.Dir(c.Workspace.ProgramDir())), Paths: paths, Spec: spec, Receipt: &c.Receipt}
	if err := s.save(b); err != nil {
		return nil, nil, err
	}
	return c, b, nil
}

// OriginalProgramInput comes from the installed-source adapter's read-only
// observation and independent archive. There is deliberately no SnapshotRef:
// copying the original programme does not create a backup of active data.
type OriginalProgramInput struct {
	Profile   profile.Profile
	Overrides []profile.Override
	Layout    Layout
	Program   ProgramMaterial
	Runtime   *vault.RuntimeRecord
	Archive   io.Reader
	MaxBytes  int64
}

func (s ProgramStore) BuildOriginal(ctx context.Context, agent identity.AgentRef, in OriginalProgramInput) (*isolated.Workspace, *ProgramBinding, error) {
	ref := environment.Ref{Slot: environment.A, Revision: 1}
	if ctx == nil || in.Archive == nil {
		return nil, nil, ErrBuild
	}
	if err := s.accepted(agent, in.Profile); err != nil {
		return nil, nil, err
	}
	// Only the first, not-yet-imported environment may use installed facts.
	if _, err := (Store{Home: s.Home}).Load(agent); !errors.Is(err, os.ErrNotExist) {
		return nil, nil, environment.ErrState
	}
	input := BuildInput{Profile: in.Profile, Overrides: in.Overrides, Layout: in.Layout, Program: in.Program, Archive: in.Archive, MaxBytes: in.MaxBytes}
	input.Target.Source.ProfileDigest, input.Target.Source.ProgramDigest, input.Target.Source.LayoutDigest = Digest(in.Profile), in.Program.SourceDigest, LayoutDigest(in.Layout, in.Overrides)
	roots, paths, spec, err := prepareProgram(input)
	if err != nil {
		return nil, nil, err
	}
	dir, err := s.reserve(agent, ref)
	if err != nil {
		return nil, nil, err
	}
	w, err := isolated.NewWorkspaceForHome(dir, in.Layout.Home)
	if err != nil {
		return nil, nil, err
	}
	if err := w.SetProgramPaths(paths); err != nil {
		return nil, nil, err
	}
	home, err := os.OpenRoot(w.HomeDir())
	if err != nil {
		return nil, nil, err
	}
	for _, rel := range roots {
		if err := home.MkdirAll(rel, 0700); err != nil {
			home.Close()
			return nil, nil, err
		}
	}
	home.Close()
	root, err := os.OpenRoot(w.ProgramDir())
	if err != nil {
		return nil, nil, err
	}
	err = unpackProgram(ctx, root, in.Program, in.Archive)
	root.Close()
	if err != nil {
		return nil, nil, err
	}
	if err := verifyPrivateProgram(ctx, w.ProgramDir(), in.Program, paths, in.Layout.Home); err != nil {
		return nil, nil, err
	}
	if err := syncTree(w.ProgramDir()); err != nil {
		return nil, nil, err
	}
	b := &ProgramBinding{Schema: programBindingSchema, Agent: agent, Ref: ref, Origin: "installed_original", Profile: in.Profile, Overrides: in.Overrides, Layout: in.Layout,
		Program: in.Program, Runtime: in.Runtime, Workspace: filepath.Base(filepath.Dir(w.ProgramDir())), Paths: paths, Spec: spec}
	if err := s.save(b); err != nil {
		return nil, nil, err
	}
	return w, b, nil
}

// Verify is the concrete persisted-material adapter for ActivationRunner. It
// rechecks bytes; Load alone never implies current programme integrity.
func (s ProgramStore) Verify(ctx context.Context, agent identity.AgentRef, ref environment.Ref, expected string) error {
	if ctx == nil {
		return ErrProgram
	}
	b, err := s.Load(agent, ref)
	if err != nil {
		return err
	}
	if Digest(b) != expected {
		return ErrProgram
	}
	if err := s.accepted(agent, b.Profile); err != nil {
		return err
	}
	r, err := (Store{Home: s.Home}).Load(agent)
	if err != nil {
		return err
	}
	index := 0
	if ref.Slot == environment.B {
		index = 1
	}
	if r.Slots[index].Ref != ref || r.Slots[index].State == environment.Empty {
		return environment.ErrStale
	}
	dir, _ := s.base(agent, ref)
	w, err := isolated.OpenWorkspace(filepath.Join(dir, b.Workspace), b.Layout.Home, b.Paths)
	if err != nil {
		return err
	}
	return verifyPrivateProgram(ctx, w.ProgramDir(), b.Program, b.Paths, b.Layout.Home)
}

// Open verifies and reconstructs a persisted private workspace for a trusted
// controller. It is not an arbitrary path/command interface.
func (s ProgramStore) Open(ctx context.Context, agent identity.AgentRef, ref environment.Ref, expected string) (*isolated.Workspace, isolated.Spec, error) {
	if err := s.Verify(ctx, agent, ref, expected); err != nil {
		return nil, isolated.Spec{}, err
	}
	b, err := s.Load(agent, ref)
	if err != nil || Digest(b) != expected {
		return nil, isolated.Spec{}, ErrProgram
	}
	dir, _ := s.base(agent, ref)
	w, err := isolated.OpenWorkspace(filepath.Join(dir, b.Workspace), b.Layout.Home, b.Paths)
	return w, b.Spec, err
}
