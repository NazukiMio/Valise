package revive

import (
	"context"
	"path/filepath"
	"slices"

	"valise/core/environment"
	"valise/core/revive"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/activeview"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

func (r *Runner) beginManagedEnvironmentPreparation(ctx context.Context, p revive.EnvironmentPlanParams, preview, op string, facts *environmentPlanFacts, plan revive.Plan, manifest []revive.ManifestEntry, control managedruntime.Controller) (*EnvironmentPreparation, error) {
	source := *facts.Managed
	source.OperationID = op
	rec := &EnvironmentPreparation{Schema: preparationSchema, Mode: facts.View.Mode, OperationID: op, Params: p, PreviewDigest: preview, Plan: plan,
		ManagedSource: &source, Original: source.Original, Candidate: facts.Next, OriginalMaterials: source.Materials, Directories: facts.Directories, Manifest: manifest, State: "capturing", Reuse: facts.Reuse}
	rec.BindingDigest = preparationDigest(rec)
	if err := savePreparation(r.Store.Home, rec, true); err != nil {
		return nil, err
	}
	fail := func(code string) (*EnvironmentPreparation, error) {
		rec.State, rec.FailureCode = "failed", code
		if err := savePreparation(r.Store.Home, rec, false); err != nil {
			return rec, err
		}
		return rec, preparationError(code)
	}
	dir, err := preparationWorkspace(r.Store.Home, rec, true)
	if err != nil {
		return fail("environment_prepare_workspace")
	}
	if err := r.claimPreparation(rec, "workspace", "", dir, nil); err != nil {
		return fail("environment_prepare_workspace")
	}
	archive, err := (environments.ProgramStore{Home: r.Store.Home}).ArchiveBound(ctx, dir, p.Agent, source.Original, source.Materials, p.Input.Header.Runtime)
	if err != nil {
		return fail("environment_prepare_source")
	}
	defer archive.File.Close()
	rec.Archive, rec.ArchiveMaterial, rec.State = filepath.Base(archive.File.Name()), &archive.Material, "original_ready"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	final, err := r.inspectEnvironmentPlan(ctx, p, control)
	if err != nil || final.View.Digest != preview {
		return fail("environment_prepare_source")
	}
	if err := activationrecord.SaveSource(r.Store.Home, source); err != nil {
		return fail("environment_prepare_registration")
	}
	rec.State = "registering"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	store := environments.Store{Home: r.Store.Home}
	registry, err := store.Update(p.Agent, facts.Registry.Revision, func(v environment.Registry) (environment.Registry, error) { return v.Begin(op, facts.View.Snapshot) })
	if err != nil || registry.Operation.Original != rec.Original || registry.Operation.Candidate != rec.Candidate {
		return fail("environment_prepare_registration")
	}
	if rec.Reuse != nil {
		rec.State = "reusing"
		if err := savePreparation(r.Store.Home, rec, false); err != nil {
			return rec, err
		}
		if err := r.executePreparationReuse(ctx, rec, control); err != nil {
			return fail("environment_prepare_reuse")
		}
	}
	scope := revive.ManagedDataRef{Agent: p.Agent, OperationID: op, Snapshot: facts.View.Snapshot, Original: rec.Original, Candidate: rec.Candidate}
	if _, err := BeginManagedData(ctx, r.Env, r.Store, plan, scope, manifest, func(id string, fact revive.ManagedRoot) error {
		path, err := preparationResource(r.Store.Home, rec, "stage", id)
		if err != nil {
			return err
		}
		return r.claimPreparation(rec, "stage", id, path, &fact)
	}); err != nil {
		return fail("environment_prepare_stage")
	}
	rec.State = "data_ready"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	return rec, nil
}

func (r *Runner) managedPreparationUnchanged(ctx context.Context, p *EnvironmentPreparation, control managedruntime.Controller, frozenProcess bool) (*activeview.Binding, error) {
	source := p.ManagedSource
	if source == nil || !source.Valid(p.Params.Agent, p.OperationID) {
		return nil, preparationError("environment_prepare_changed")
	}
	current, err := (activeview.Reader{Home: r.Store.Home, Control: control}).Read(ctx, p.Params.Agent)
	if err != nil || current == nil || current.Native != nil || current.Ref != source.Original || current.Definition.Digest() != source.Definition || environments.Digest(current.Program) != source.Materials || current.Current.PendingJob || !current.Current.Running && !current.Current.Stopped {
		return nil, preparationError("environment_prepare_source")
	}
	if frozenProcess && environments.Digest(current.Current) != environments.Digest(source.Observed) {
		return nil, preparationError("environment_prepare_source")
	}
	if op := current.Registry.Operation; op != nil && op.ID == p.OperationID {
		if op.Original != p.Original || op.Candidate != p.Candidate || op.Phase != environment.Building && op.Phase != environment.Prepared && op.Phase != environment.Cancelled {
			return nil, preparationCleanupError("environment_prepare_already_switching")
		}
		stored, err := activationrecord.LoadSource(r.Store.Home, p.Params.Agent, p.OperationID)
		if err != nil || environments.Digest(stored) != environments.Digest(source) {
			return nil, preparationError("environment_prepare_changed")
		}
	} else if !source.Owns(current.Operation) {
		return nil, preparationError("environment_prepare_changed")
	}
	parent, err := loadActivation(r.Store.Home, p.Params.Agent, source.Owner.ID)
	if err != nil || activationrecord.Digest(parent) != source.ParentDigest {
		return nil, preparationError("environment_prepare_changed")
	}
	directories, err := planDirectoryFacts(p.Plan.Roots)
	if err != nil || !slices.Equal(directories, p.Directories) {
		return nil, preparationError("environment_prepare_changed")
	}
	return current, nil
}

func (r *Runner) managedPreparationArchive(rec *EnvironmentPreparation, original *environments.ProgramBinding) (environments.ProgramMaterial, error) {
	if rec.ManagedSource == nil {
		return original.Program, nil
	}
	m := rec.ArchiveMaterial
	if m == nil || environments.Digest(m.Files) != environments.Digest(original.Program.Files) || m.SourceDigest != original.Program.SourceDigest || m.OS != original.Program.OS || m.Arch != original.Program.Arch {
		return environments.ProgramMaterial{}, preparationError("environment_prepare_source")
	}
	return *m, nil
}

func managedPreparationParentMatches(p *EnvironmentPreparation, a *ActivationRecord) bool {
	if p.ManagedSource != nil {
		return a.Plan.NativeOriginal == nil && a.OriginalDefinition == p.ManagedSource.Definition && a.Plan.Original == p.Original && a.Plan.OriginalMaterials == p.ManagedSource.Materials
	}
	return a.Plan.NativeOriginal != nil && *a.Plan.NativeOriginal == p.Native
}
