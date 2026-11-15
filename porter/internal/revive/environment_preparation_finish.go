package revive

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

// FinishEnvironmentPreparation consumes only the exact parent-bound stage.
// The source remains running throughout; only Activate may stop it later.
func (r *Runner) FinishEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op string) (*EnvironmentPreparation, error) {
	return r.finishEnvironmentPreparation(ctx, agent, op, managedruntime.Controller{Home: r.Store.Home})
}

func (r *Runner) finishEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op string, control managedruntime.Controller) (*EnvironmentPreparation, error) {
	unlock, err := (seal.Paths{Home: r.Store.Home}).LockFor("environment_prepare_finish")
	if err != nil {
		return nil, err
	}
	defer unlock()
	rec, err := loadPreparation(r.Store.Home, agent, op)
	if err != nil {
		return nil, err
	}
	if rec.State != "data_ready" {
		return rec, nil
	}
	// An incomplete upload may be completed within this operation. Do not mark
	// the intent consumed until the exact immutable manifest has verified.
	j, err := r.Store.Load(rec.Plan.OpID)
	if err != nil {
		return rec, err
	}
	if err := checkManagedData(r.Store, j, environment.Building); err != nil {
		return rec, err
	}
	if err := checkManagedStage(j); err != nil {
		return rec, err
	}
	verified, err := StageVerify(r.Store, revive.StageVerifyParams{OpID: rec.Plan.OpID, Manifest: rec.Manifest})
	if err != nil {
		return rec, err
	}
	if len(verified.Problems) != 0 {
		return rec, preparationError("environment_prepare_stage")
	}
	in, check, err := r.preparationInput(ctx, rec, control)
	if err != nil {
		return rec, err
	}
	rec.State = "building"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	fail := func(code string) (*EnvironmentPreparation, error) {
		rec.State, rec.FailureCode = "failed", code
		if err := savePreparation(r.Store.Home, rec, false); err != nil {
			return rec, err
		}
		return rec, preparationError(code)
	}
	programs := r.preparationPrograms(rec)
	original, err := programs.Load(agent, rec.Original)
	if err != nil {
		return fail("environment_prepare_original")
	}
	dir, err := preparationWorkspace(r.Store.Home, rec, false)
	if err != nil {
		return fail("environment_prepare_workspace")
	}
	material, err := r.managedPreparationArchive(rec, original)
	if err != nil {
		return fail("environment_prepare_source")
	}
	f, err := openPreparationArchive(dir, rec.Archive, material)
	if err != nil {
		return fail("environment_prepare_source")
	}
	defer f.Close()
	in.Program, in.Archive, in.Runtime = material, f, rec.Params.Input.Header.Runtime
	in.Layout = original.Layout
	in.Target.Source.LayoutDigest = environments.LayoutDigest(in.Layout, in.Overrides)
	if in.Baseline != nil {
		in.Baseline.Source.LayoutDigest = in.Target.Source.LayoutDigest
	}
	candidate, binding, err := programs.BuildBound(ctx, agent, rec.Candidate, in)
	if err != nil {
		return fail("environment_prepare_candidate")
	}
	rec.CandidateMaterials = environments.Digest(binding)
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	// A probe gets its own programme tree and HOME. Its writes never enter the
	// formal candidate or the sibling data stage that will be exchanged.
	if _, err := f.Seek(0, 0); err != nil {
		return fail("environment_prepare_source")
	}
	probe, err := environments.Build(ctx, dir, in)
	if err != nil {
		return fail("environment_prepare_probe")
	}
	rec.ProbeWorkspace = filepath.Base(filepath.Dir(probe.Workspace.ProgramDir()))
	rec.ProbeTracking = "bwrap-sync-fd/1"
	if err := r.claimPreparation(rec, "probe", "", filepath.Dir(probe.Workspace.ProgramDir()), nil); err != nil {
		return fail("environment_prepare_probe")
	}

	rec.State = "probing"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	proof, err := isolated.GatewayProbe(ctx, probe.Workspace, probe.Spec, check, func(st os.FileInfo) error {
		stat, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			return managedDataError()
		}
		rec.ProbeLease = revive.ManagedRoot{ID: "probe-lease", Device: uint64(stat.Dev), Inode: stat.Ino}
		return savePreparation(r.Store.Home, rec, false)
	})
	rec.Probe = &proof
	if err != nil || !proof.Verified() {
		return fail("environment_prepare_gateway")
	}
	if _, _, err := r.preparationInput(ctx, rec, control); err != nil {
		return fail("environment_prepare_source")
	}
	if err := programs.Verify(ctx, agent, rec.Candidate, rec.CandidateMaterials); err != nil {
		return fail("environment_prepare_candidate")
	}
	oldWorkspace, oldSpec, err := programs.Open(ctx, agent, rec.Original, rec.OriginalMaterials)
	if err != nil {
		return fail("environment_prepare_original")
	}
	bindings, err := ManagedBindings(rec.Plan, in.Layout)
	if err != nil {
		return fail("environment_prepare_candidate")
	}
	var oldDef managedruntime.Definition
	if rec.ManagedSource != nil {
		oldDef, err = (managedruntime.Controller{Home: r.Store.Home}).Remembered(agent, rec.ManagedSource.Definition)
	} else {
		oldDef, err = managedruntime.Compile(agent, oldWorkspace, oldSpec, bindings)
	}
	if err != nil {
		return fail("environment_prepare_original")
	}
	newDef, err := managedruntime.Compile(agent, candidate.Workspace, candidate.Spec, bindings)
	if err != nil {
		return fail("environment_prepare_candidate")
	}
	rec.State = "binding"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	store := environments.Store{Home: r.Store.Home}
	registry, err := store.Load(agent)
	if err != nil {
		return fail("environment_prepare_registration")
	}
	if _, err := store.Update(agent, registry.Revision, func(v environment.Registry) (environment.Registry, error) { return v.Prepared(op) }); err != nil {
		return fail("environment_prepare_registration")
	}
	activation := ActivationPlan{Agent: agent, OperationID: op, Snapshot: registry.Operation.Snapshot, Original: rec.Original, Candidate: rec.Candidate, DataOpID: rec.Plan.OpID,
		OriginalMaterials: rec.OriginalMaterials, CandidateMaterials: rec.CandidateMaterials, OriginalProfile: rec.Plan.Profile, CandidateProfile: rec.Plan.Profile, NativeOriginal: &rec.Native, NativeSourceMaterials: rec.OriginalMaterials}
	if rec.ManagedSource != nil {
		activation.NativeOriginal, activation.NativeSourceMaterials = nil, ""
		activation.ManagedSourceDigest = environments.Digest(rec.ManagedSource)
		if oldDef.Digest() != rec.ManagedSource.Definition {
			return fail("environment_prepare_source")
		}
	}
	a := NewBoundActivationRunner(r)
	a.runtime = control
	if _, err := a.prepareLocked(ctx, activation, oldDef, newDef); err != nil {
		return fail("environment_prepare_activation")
	}
	rec.State = "prepared"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	return rec, nil
}

func (r *Runner) preparationInput(ctx context.Context, rec *EnvironmentPreparation, control managedruntime.Controller) (environments.BuildInput, isolated.ProbeCheck, error) {
	var empty environments.BuildInput
	var check isolated.ProbeCheck
	settings, err := (environments.Store{Home: r.Store.Home}).Settings(rec.Params.Agent)
	if err != nil || settings.Mode != rec.Mode || !settings.Managed || !settings.Busy {
		return empty, check, preparationError("environment_prepare_changed")
	}
	j, err := r.Store.Load(rec.Plan.OpID)
	if err != nil {
		return empty, check, err
	}
	if checkManagedData(r.Store, j, environment.Building) != nil || environments.Digest(j.Plan) != environments.Digest(rec.Plan) || j.ManagedData.OperationID != rec.OperationID || j.ManagedData.Original != rec.Original || j.ManagedData.Candidate != rec.Candidate {
		return empty, check, managedDataError()
	}
	facts, err := planDirectoryFacts(rec.Plan.Roots)
	if err != nil || !slices.Equal(facts, rec.Directories) {
		return empty, check, preparationError("environment_prepare_changed")
	}
	// The ephemeral metadata view only reuses the authenticated source reader;
	// the persisted child remains a formal, parent-bound data journal.
	meta := *j
	meta.Plan = rec.Plan
	meta.Plan.IsolatedTest = &rec.Params.Input
	in, source, check, err := r.environmentTestInput(ctx, &meta)
	if err != nil {
		return empty, check, planningInputError(err)
	}
	if rec.ManagedSource != nil {
		if _, err := r.managedPreparationUnchanged(ctx, rec, control, true); err != nil {
			return empty, check, err
		}
		return in, check, nil
	}
	ref, _, err := control.InspectNative(ctx, rec.Plan.Profile, profile.ServiceUnit{Scope: "user", Unit: rec.Native.Unit})
	if err != nil || ref != rec.Native {
		return empty, check, preparationError("environment_prepare_source")
	}
	programs := environments.ProgramStore{Home: r.Store.Home}
	if err := programs.Verify(ctx, rec.Params.Agent, rec.Original, rec.OriginalMaterials); err != nil {
		return empty, check, preparationError("environment_prepare_source")
	}
	binding, err := programs.Load(rec.Params.Agent, rec.Original)
	if err != nil {
		return empty, check, err
	}
	observed, err := environments.ObserveInstalledNPM(ctx, source)
	if err != nil || environments.Digest(observed.Dependencies) != environments.Digest(rec.Params.Input.Header.Runtime.Dependencies) || environments.Digest(observed.Material.Files) != environments.Digest(binding.Program.Files) || observed.Material.SourceDigest != binding.Program.SourceDigest {
		return empty, check, preparationError("environment_prepare_source")
	}
	after, _, err := control.InspectNative(ctx, rec.Plan.Profile, profile.ServiceUnit{Scope: "user", Unit: rec.Native.Unit})
	if err != nil || after != rec.Native {
		return empty, check, preparationError("environment_prepare_source")
	}
	return in, check, nil
}

func openPreparationArchive(dir, name string, material environments.ProgramMaterial) (*os.File, error) {
	if filepath.Base(name) != name || name == "" {
		return nil, managedDataError()
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 || st.Size() != material.ArchiveBytes {
		f.Close()
		return nil, managedDataError()
	}
	return f, nil
}
