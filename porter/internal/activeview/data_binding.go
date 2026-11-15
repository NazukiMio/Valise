package activeview

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/porter/internal/activationrecord"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
)

// DataBinding proves which data tree is active, independently of programme,
// service or gateway health. Private programme evidence is checked separately.
// Callers hold the server mutation lock for the full backup and Validate again
// before committing; no RPC accepts this object as supplied authority.
type DataBinding struct {
	Registry      environment.Registry
	Ref           environment.Ref
	Operation     environment.Operation
	Record        *enroll.Record
	Profile       *profile.Profile
	Program       *environments.ProgramBinding
	Materials     string
	Native        bool
	ProgramIssue  string
	parent        *activationrecord.Record
	initial       *environments.InitialData
	candidate     bool
	profileDigest string
	rootDigest    string
	registryHash  string
	parentHash    string
	initialHash   string
}

func (r Reader) ReadData(ctx context.Context, agent identity.AgentRef) (*DataBinding, error) {
	if ctx == nil || protection.ValidateAgent(agent) != nil || !filepath.IsAbs(r.Home) || filepath.Clean(r.Home) != r.Home {
		return nil, unavailable()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(r.Home, ".local/state/valise/environments", environment.Key(agent)+".json")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, unavailable()
	}
	reg, err := (environments.Store{Home: r.Home}).Load(agent)
	if err != nil {
		return nil, unavailable()
	}
	ref, err := stableRef(reg)
	if err != nil {
		return nil, err
	}
	b := &DataBinding{Registry: reg, Ref: ref, registryHash: environments.Digest(reg)}
	b.Operation, b.parent, b.candidate, err = r.recordFor(reg, ref)
	if err != nil {
		b.initial, err = r.initialFor(reg, ref)
		if err != nil {
			return nil, unavailable()
		}
		b.initialHash, b.Materials, b.Native = environments.Digest(b.initial), b.initial.Materials, true
		if reg.Operation != nil {
			b.Operation = *reg.Operation
		}
	} else {
		b.parentHash = environments.Digest(b.parent)
		b.Materials = b.parent.Plan.OriginalMaterials
		if b.candidate {
			b.Materials = b.parent.Plan.CandidateMaterials
		}
		b.Native = !b.candidate && b.parent.Plan.NativeOriginal != nil && (b.Operation.Phase == environment.Prepared || b.Operation.Phase == environment.Cancelled)
	}
	id, err := porteridentity.Read(r.Home, agent.InstanceID)
	if err != nil || id.IdentityState != identity.Bound || id.Agent == nil || id.Agent.Ref != agent {
		return nil, unavailable()
	}
	d, p, err := porterenroll.ReadInstallation(r.Home, agent.InstanceID)
	if err != nil || d == nil || p == nil || d.InstallID != agent.InstallID || environments.Digest(p) != id.Agent.ProfileDigest {
		return nil, unavailable()
	}
	b.Record, b.Profile = d.Record(agent.InstanceID), p
	b.profileDigest, b.rootDigest = id.Agent.ProfileDigest, environments.Digest(id.Agent.Roots)
	if b.Record == nil || b.Record.Superseded != nil || environments.Digest(b.Record.Roots) != b.rootDigest {
		return nil, unavailable()
	}
	if err := r.checkBoundData(b); err != nil {
		return nil, unavailable()
	}
	b.Program, err = (environments.ProgramStore{Home: r.Home}).Load(agent, ref)
	if err != nil || environments.Digest(b.Program) != b.Materials || environments.Digest(p.ForAgent(b.Record.Name)) != environments.Digest(b.Program.Profile) {
		b.Program, b.ProgramIssue = nil, "active_program_unverified"
	}
	if err := r.ValidateData(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (r Reader) initialFor(reg environment.Registry, ref environment.Ref) (*environments.InitialData, error) {
	if ref != (environment.Ref{Slot: environment.A, Revision: 1}) || len(reg.History) != 0 {
		return nil, unavailable()
	}
	b, err := (environments.InitialDataStore{Home: r.Home}).Load(reg.Agent)
	if err != nil {
		return nil, err
	}
	if op := reg.Operation; op != nil {
		if op.ID != b.OperationID || op.Original != ref || op.Phase != environment.Building && op.Phase != environment.Cancelled {
			return nil, unavailable()
		}
	}
	// Never bypass an existing or damaged formal record. Even a saved stop
	// intent must remain authoritative, regardless of the registry's last phase.
	path := filepath.Join(r.Home, ".local/state/valise/activations", environment.Key(reg.Agent), b.OperationID+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, unavailable()
	}
	return b, nil
}

func (r Reader) checkBoundData(b *DataBinding) error {
	if b.parent != nil {
		return r.checkData(b.parent, b.Record, b.Ref)
	}
	i := b.initial
	if i == nil || i.ProfileDigest != b.profileDigest || environments.Digest(i.Roots) != environments.Digest(b.Record.Roots) || i.Native.Validate(b.Profile.ForAgent(b.Record.Name)) != nil {
		return unavailable()
	}
	for _, dir := range i.Directories {
		if checkDirectoryChain(i.Roots[dir.ID]) != nil || matchDirectory(i.Roots[dir.ID], dir) != nil {
			return unavailable()
		}
	}
	return nil
}

// ValidateData rechecks metadata and actual root identities, without invoking
// services, executing an interpreter, repairing or hashing programme contents.
func (r Reader) ValidateData(ctx context.Context, b *DataBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a := b.Registry.Agent
	reg, err := (environments.Store{Home: r.Home}).Load(a)
	if err != nil || environments.Digest(reg) != b.registryHash {
		return changing()
	}
	if b.parent != nil {
		p, err := activationrecord.Load(r.Home, a, b.parent.Plan.OperationID)
		if err != nil || environments.Digest(p) != b.parentHash {
			return changing()
		}
	} else {
		i, err := r.initialFor(reg, b.Ref)
		if err != nil || environments.Digest(i) != b.initialHash {
			return changing()
		}
	}
	id, err := porteridentity.Read(r.Home, a.InstanceID)
	d, p, factsErr := porterenroll.ReadInstallation(r.Home, a.InstanceID)
	if err != nil || factsErr != nil || id.IdentityState != identity.Bound || id.Agent == nil || id.Agent.Ref != a || id.Agent.ProfileDigest != b.profileDigest || environments.Digest(id.Agent.Roots) != b.rootDigest ||
		d == nil || d.InstallID != a.InstallID || !sameRuntimeRecord(b.Record, d.Record(a.InstanceID)) || environments.Digest(p) != b.profileDigest || r.checkBoundData(b) != nil {
		return changing()
	}
	return ctx.Err()
}

// VerifyProgram is separate so a damaged programme does not prevent a data
// backup. A successful check does not claim service or gateway health.
func (r Reader) VerifyProgram(ctx context.Context, b *DataBinding) error {
	if b.Program == nil || b.ProgramIssue != "" {
		return unavailable()
	}
	if err := (environments.ProgramStore{Home: r.Home}).Verify(ctx, b.Registry.Agent, b.Ref, b.Materials); err != nil {
		return err
	}
	if b.Native {
		expected := b.Materials
		if b.parent != nil {
			expected = b.parent.Plan.NativeSourceMaterials
		}
		if expected == "" {
			return unavailable()
		}
		if err := environments.VerifyOriginalSource(ctx, b.Program, expected); err != nil {
			return err
		}
	}
	return r.ValidateData(ctx, b)
}
