package revive

import (
	"context"
	"os"
	"slices"

	"valise/core/environment"
	"valise/core/revive"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/activeview"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

func (r *Runner) inspectManagedEnvironmentPlan(ctx context.Context, p revive.EnvironmentPlanParams, control managedruntime.Controller, settings environment.Settings) (*environmentPlanFacts, error) {
	reader := activeview.Reader{Home: r.Store.Home, Control: control}
	current, err := reader.Read(ctx, p.Agent)
	if err != nil || current == nil || current.Native != nil || !current.Current.Running || current.Current.PendingJob {
		return nil, planError("environment_plan_native")
	}
	registry := current.Registry
	if registry.Operation == nil || !environment.OperationFinished(registry.Operation.Phase) {
		return nil, planError("environment_plan_busy")
	}
	index := 1
	if registry.Active == environment.B {
		index = 0
	}
	target := registry.Slots[index]
	if (target.State != environment.Empty && target.State != environment.Stopped) || target.Revision == ^uint64(0) {
		return nil, planError("environment_plan_reuse_pending")
	}
	next := target.Ref
	next.Revision++
	d, accepted, err := porterenroll.ReadInstallation(r.Store.Home, p.Agent.InstanceID)
	if err != nil || d == nil || accepted == nil || d.Record(p.Agent.InstanceID) == nil || d.Record(p.Agent.InstanceID).Name != "" {
		return nil, planError("environment_plan_input")
	}
	record := d.Record(p.Agent.InstanceID)
	roots, err := revive.CanonicalRoots(record.Roots)
	if err != nil {
		return nil, planError("environment_plan_input")
	}
	directories, err := planDirectoryFacts(roots)
	if err != nil {
		return nil, err
	}
	plan := revive.Plan{Mode: revive.ModeRollback, InstanceID: p.Agent.InstanceID, SnapshotID: p.Input.Header.SnapshotID, Tier: p.Input.Header.Tier, Profile: *accepted, Overrides: record.LocalOverrides, Roots: roots, IsolatedTest: &p.Input}
	in, _, check, err := r.environmentTestInput(ctx, &revive.Journal{Plan: plan})
	if err != nil {
		return nil, planningInputError(err)
	}
	if check.Offline != nil {
		return nil, activationError("activation_readiness_unsupported")
	}
	program := current.Program
	if program.Runtime == nil || p.Input.Header.Runtime == nil || environments.Digest(program.Runtime.Dependencies) != environments.Digest(p.Input.Header.Runtime.Dependencies) || environments.Digest(program.Runtime.Installation) != environments.Digest(p.Input.Header.Runtime.Installation) || program.Program.SourceDigest != in.Target.Source.ProgramDigest {
		return nil, planError("environment_plan_source")
	}
	data, err := r.composeEnvironmentTest(ctx, &revive.Journal{Plan: plan}, &in)
	if err != nil {
		return nil, err
	}
	space, err := planEnvironmentSpace(r.Store.Home, roots, program.Program, data)
	if err != nil {
		return nil, err
	}
	reuse, reuseItems, reuseDigest, err := r.inspectInactiveReuse(ctx, registry, current.Ref, control)
	if err != nil {
		return nil, err
	}
	parent, err := loadActivation(r.Store.Home, p.Agent, current.Operation.ID)
	if err != nil {
		return nil, err
	}
	source := &activationrecord.Source{Schema: "valise-preparation-source/1", Agent: p.Agent, Original: current.Ref, Owner: current.Operation, ParentDigest: activationrecord.Digest(parent), Materials: environments.Digest(program), Definition: current.Definition.Digest(), Observed: current.Current}
	source.RetiredNative = parent.Plan.NativeOriginal
	if parent.Plan.ManagedSourceDigest != "" {
		previous, err := activationrecord.LoadSource(r.Store.Home, p.Agent, parent.Plan.OperationID)
		if err != nil || environments.Digest(previous) != parent.Plan.ManagedSourceDigest {
			return nil, planError("environment_plan_source")
		}
		source.RetiredNative = previous.RetiredNative
	}
	final, err := reader.Read(ctx, p.Agent)
	if err != nil || final.Ref != current.Ref || environments.Digest(final.Registry) != environments.Digest(registry) || final.Definition.Digest() != source.Definition || environments.Digest(final.Program) != source.Materials || environments.Digest(final.Current) != environments.Digest(source.Observed) {
		return nil, planError("environment_plan_changed")
	}
	lastSettings, err := (environments.Store{Home: r.Store.Home}).Settings(p.Agent)
	lastDirectories, rootErr := planDirectoryFacts(roots)
	if err != nil || lastSettings.Digest != settings.Digest || rootErr != nil || !slices.Equal(directories, lastDirectories) {
		return nil, planError("environment_plan_changed")
	}
	// A pre-existing revision, even when metadata says empty, cannot be
	// silently taken over by the next builder's exclusive reservation.
	path, err := (environments.ProgramStore{Home: r.Store.Home}).RevisionPath(p.Agent, next)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return nil, planError("environment_plan_changed")
	}
	v := &revive.EnvironmentPlan{Agent: p.Agent, Snapshot: in.Target.Source.Snapshot, CreatedAt: p.Input.Header.CreatedAt, Tier: p.Input.Header.Tier, Mode: settings.Mode, ManagedSource: true, Original: &current.Ref, Candidate: &next,
		InputDigest: environments.Digest(p), NativeRunning: true, NativeUnit: current.Definition.Name(), EnableOnBoot: current.Current.UnitFileState == "enabled", ProgramMatches: true,
		ProgramFiles: len(program.Program.Files), Files: len(data.Entries), LogicalRoots: len(roots), Space: space}
	for _, root := range roots {
		if root.SwapRoot == "" {
			v.PhysicalRoots++
		}
	}
	for _, file := range program.Program.Files {
		v.ProgramBytes += file.Size
	}
	for _, file := range data.Entries {
		if file.File != nil {
			v.Bytes += file.File.Size
		}
	}
	if p.Input.Baseline != nil {
		v.Baseline = &environment.DataOrigin{Snapshot: in.Baseline.Source.Snapshot, Time: p.Input.Baseline.Header.CreatedAt}
	}
	if d.Provenance.NPM != nil {
		v.ProgramVersion = d.Provenance.NPM.Version
	}
	v.ProfileUntested = !accepted.EnvironmentTested(v.ProgramVersion)
	if reuse != nil {
		v.Reuse = &revive.EnvironmentReuse{Target: reuse.Target, Items: reuseItems}
	}
	v.Digest = environments.Digest(struct {
		Input       string
		Registry    environment.Registry
		Source      *activationrecord.Source
		Next        environment.Ref
		Directories []revive.ManagedRoot
		Reuse       string
	}{v.InputDigest, registry, source, next, directories, reuseDigest})
	return &environmentPlanFacts{View: v, Plan: plan, Managed: source, Registry: &registry, Next: next, Material: program.Program, Data: data, Directories: directories, Reuse: reuse}, nil
}
