package environments

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/vault"
)

const maxBindingBytes = 64 << 20

func ownedDirectory(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 {
		return ErrProgram
	}
	return nil
}

func (s ProgramStore) directories(agent identity.AgentRef, ref environment.Ref, create bool) (string, error) {
	base, err := s.base(agent, ref)
	if err != nil {
		return "", err
	}
	current := s.Home
	rel, _ := filepath.Rel(s.Home, base)
	for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
		if part != "" {
			current = filepath.Join(current, part)
		}
		if create {
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
		}
		if err := ownedDirectory(current); err != nil {
			return "", err
		}
	}
	return base, nil
}

func (s ProgramStore) reserve(agent identity.AgentRef, ref environment.Ref) (string, error) {
	base, err := s.base(agent, ref)
	if err != nil {
		return "", err
	}
	// A revision is never overwritten, including an incomplete prior build.
	if _, err := os.Lstat(base); !errors.Is(err, os.ErrNotExist) {
		return "", environment.ErrState
	}
	parent := filepath.Dir(base)
	current := s.Home
	if err := ownedDirectory(current); err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(s.Home, parent)
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		if err := ownedDirectory(current); err != nil {
			return "", err
		}
	}
	if err := os.Mkdir(base, 0700); err != nil {
		return "", err
	}
	if s.Claim != nil {
		if err := s.Claim(ref, base); err != nil {
			return "", err
		}
	}
	return base, nil
}

// RevisionPath only derives a fixed private resource for trusted controllers.
// It does not create a directory or accept a supplied path.
func (s ProgramStore) RevisionPath(agent identity.AgentRef, ref environment.Ref) (string, error) {
	return s.base(agent, ref)
}

func validProgramBinding(b *ProgramBinding) bool {
	if b.Schema != programBindingSchema || !validProgramRef(b.Ref) || b.Workspace != filepath.Base(b.Workspace) ||
		!strings.HasPrefix(b.Workspace, "valise-isolated-") || len(b.Workspace) > 96 || b.Program.SourceDigest == "" || len(b.Program.SourceDigest) > 512 ||
		len(b.Program.Files) == 0 || len(b.Program.Files) > 500_000 {
		return false
	}
	if b.Origin == "installed_original" {
		if b.Ref != (environment.Ref{Slot: environment.A, Revision: 1}) || b.Source != nil || b.Receipt != nil {
			return false
		}
	} else if b.Origin == "snapshot" {
		if b.Source == nil || b.Receipt == nil || !b.Receipt.CandidateBuilt || Digest(b.Source) != Digest(b.Receipt.Source) ||
			!b.Source.Verified || protection.ValidateAgent(b.Source.Agent) != nil || !vault.ValidID(b.Source.Snapshot.VaultID) || !vault.ValidID(b.Source.Snapshot.SnapshotID) || b.Source.Time.IsZero() || vault.TierCategories[b.Source.Tier] == nil ||
			b.Source.ProfileDigest != Digest(b.Profile) || b.Source.LayoutDigest != LayoutDigest(b.Layout, b.Overrides) || b.Source.ProgramDigest != b.Program.SourceDigest ||
			b.Receipt.ProgramSHA256 != b.Program.ArchiveSHA {
			return false
		}
	} else {
		return false
	}
	if r := b.Runtime; r != nil {
		if r.Format != vault.FormatRuntime || r.Home.Path != b.Layout.Home || r.OS != b.Program.OS || r.Arch != b.Program.Arch || r.Installation == nil || Digest(r.Installation.ProfileDoc) != Digest(b.Profile) {
			return false
		}
	}
	budget := int64(1)
	for _, f := range b.Program.Files {
		if f.Size < 0 || f.Size > 1<<40-budget {
			return false
		}
		budget += f.Size
	}
	in := BuildInput{Profile: b.Profile, Overrides: b.Overrides, Layout: b.Layout, Program: b.Program, MaxBytes: budget}
	in.Target.Source.ProfileDigest, in.Target.Source.LayoutDigest, in.Target.Source.ProgramDigest = Digest(b.Profile), LayoutDigest(b.Layout, b.Overrides), b.Program.SourceDigest
	_, paths, spec, err := prepareProgram(in)
	if err != nil || !slices.Equal(paths, b.Paths) || Digest(spec) != Digest(b.Spec) {
		return false
	}
	graph, err := newProgramGraph(b.Program, paths, b.Layout.Home)
	if err != nil {
		return false
	}
	entry, directory, err := graph.resolve(strings.TrimPrefix(spec.Argv[0], b.Layout.Home+"/"))
	return err == nil && !directory && graph.nodes[entry].Mode&0100 != 0
}

// Load is metadata-only. Current integrity requires Verify, not just this read.
func (s ProgramStore) Load(agent identity.AgentRef, ref environment.Ref) (*ProgramBinding, error) {
	base, err := s.directories(agent, ref, false)
	if err != nil {
		return nil, err
	}
	f, err := privateFile(filepath.Join(base, "binding.json"), false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() > maxBindingBytes {
		return nil, ErrProgram
	}
	b, err := io.ReadAll(io.LimitReader(f, maxBindingBytes+1))
	if err != nil {
		return nil, err
	}
	var record ProgramBinding
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&record); err != nil {
		return nil, ErrProgram
	}
	var extra any
	if d.Decode(&extra) != io.EOF || record.Agent != agent || record.Ref != ref || !validProgramBinding(&record) {
		return nil, ErrProgram
	}
	return &record, nil
}

func (s ProgramStore) save(b *ProgramBinding) error {
	if !validProgramBinding(b) {
		return ErrProgram
	}
	base, err := s.directories(b.Agent, b.Ref, false)
	if err != nil {
		return err
	}
	data, err := json.Marshal(b)
	if err != nil || len(data) > maxBindingBytes {
		return ErrProgram
	}
	f, err := os.CreateTemp(base, ".binding-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Link is exclusive; it must never replace a binding from this revision.
	if err := os.Link(f.Name(), filepath.Join(base, "binding.json")); err != nil {
		return err
	}
	if err := os.Remove(f.Name()); err != nil {
		return err
	}
	d, err := os.Open(base)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
