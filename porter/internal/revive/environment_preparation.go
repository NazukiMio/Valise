package revive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

func preparationError(code string) error {
	reason, next := "新环境尚未准备完成", "查看本次准备记录；原 Agent 仍可继续使用"
	switch code {
	case "environment_prepare_changed":
		reason, next = "准备条件已经改变，没有继续准备新环境", "重新读取原 Agent、备份和恢复设置后再查看条件"
	case "environment_prepare_space":
		reason, next = "磁盘空间不足以保留原环境并准备新环境", "释放空间后重新查看条件；不会自动删除当前环境或改用直通"
	case "environment_prepare_entries":
		reason, next = "所选内容包含当前正式准备适配尚不支持的条目", "仍可校验备份文件，等待此类内容的准备适配"
	case "environment_prepare_source":
		reason, next = "原程序在准备期间发生变化，不能作为本次回退环境", "保留当前环境；核实程序更新后重新准备"
	}
	return valise.New(code, reason, next)
}

// BeginEnvironmentPreparation is a trusted management controller, not an RPC
// accepting arbitrary plans. The caller freezes the human-reviewed preview;
// every executable fact is independently derived again on this server.
func (r *Runner) BeginEnvironmentPreparation(ctx context.Context, p revive.EnvironmentPreparationParams) (*EnvironmentPreparation, error) {
	return r.beginEnvironmentPreparation(ctx, p.EnvironmentPlanParams, p.PreviewDigest, p.OperationID, managedruntime.Controller{Home: r.Store.Home}, p.AllowUntested)
}

func (r *Runner) beginEnvironmentPreparation(ctx context.Context, p revive.EnvironmentPlanParams, preview, op string, control managedruntime.Controller, allowUntested bool) (*EnvironmentPreparation, error) {
	unlock, err := (seal.Paths{Home: r.Store.Home}).LockFor("environment_preparation")
	if err != nil {
		return nil, err
	}
	defer unlock()
	if old, err := loadPreparation(r.Store.Home, p.Agent, op); err == nil {
		if environments.Digest(old.Params) != environments.Digest(p) || old.PreviewDigest != preview {
			return nil, preparationError("environment_prepare_changed")
		}
		return old, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	facts, err := r.inspectEnvironmentPlan(ctx, p, control)
	if err != nil {
		return nil, err
	}
	if facts.View.Digest != preview {
		return nil, preparationError("environment_prepare_changed")
	}
	// The management adapter supplies only the human's specific exception.
	if !allowUntested && facts.View.ProfileUntested {
		return nil, valise.New("environment_profile_untested", "此版本的并行恢复尚未完成实际验收，没有开始准备", "查看此版本的恢复影响，并明确允许未核实能力后再准备")
	}
	for _, budget := range facts.View.Space {
		if budget.FreeBytes < budget.NeedBytes {
			return nil, preparationError("environment_prepare_space")
		}
	}
	manifest, err := preparationManifest(facts.Plan.Roots, facts.Data, p.Input)
	if err != nil {
		return nil, err
	}
	plan := facts.Plan
	plan.OpID, plan.Body, plan.IsolatedTest = vault.NewID("op", time.Now()), revive.BodyPlan{Action: revive.BodyKeep}, nil
	if facts.Managed != nil {
		return r.beginManagedEnvironmentPreparation(ctx, p, preview, op, facts, plan, manifest, control)
	}
	rec := &EnvironmentPreparation{Schema: preparationSchema, Mode: facts.View.Mode, OperationID: op, Params: p, PreviewDigest: preview, Plan: plan, Native: facts.Native,
		Directories: facts.Directories, Manifest: manifest, State: "capturing"}
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
	// Freeze checks the full tree before and after copying; it contains no data.
	archive, err := environments.FreezeInstalledNPM(ctx, dir, facts.Source, p.Input.Header.Runtime)
	if err != nil {
		return fail("environment_prepare_source")
	}
	defer archive.File.Close()
	rec.Archive = filepath.Base(archive.File.Name())
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	layout := facts.Source.Layout
	layout.RuntimeExecutable = archive.RuntimeExecutable
	originalWorkspace, original, err := r.preparationPrograms(rec).BuildOriginal(ctx, p.Agent, environments.OriginalProgramInput{
		Profile: plan.Profile, Overrides: plan.Overrides, Layout: layout, Program: archive.Material, Runtime: p.Input.Header.Runtime, Archive: archive.File, MaxBytes: 16 << 30})
	if err != nil {
		return fail("environment_prepare_original")
	}
	if err := isolated.PrepareNetwork(originalWorkspace); err != nil {
		return fail("environment_prepare_original")
	}
	rec.OriginalMaterials, rec.State = environments.Digest(original), "original_ready"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	// Copying may take time. No registry may claim A until these live facts still
	// match the exact preview, including source bytes and directory identities.
	final, err := r.inspectEnvironmentPlan(ctx, p, control)
	if err != nil || final.View.Digest != preview {
		return fail("environment_prepare_source")
	}
	// A failed/cancelled preparation may retain its registry before a formal
	// activation parent exists. Keep the unchanged native data readable without
	// treating a private programme copy as proof that it is running.
	if err := (environments.InitialDataStore{Home: r.Store.Home}).Save(environments.InitialData{
		Schema: "valise-initial-data/1", Agent: p.Agent, Ref: original.Ref, OperationID: op,
		Materials: rec.OriginalMaterials, ProfileDigest: environments.Digest(plan.Profile),
		Native: rec.Native, Roots: original.Layout.Roots, Directories: rec.Directories,
	}); err != nil {
		return fail("environment_prepare_registration")
	}
	rec.State = "registering"
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
	}
	store := environments.Store{Home: r.Store.Home}
	registry, err := store.Update(p.Agent, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(p.Agent) })
	if err != nil {
		return fail("environment_prepare_registration")
	}
	registry, err = store.Update(p.Agent, registry.Revision, func(v environment.Registry) (environment.Registry, error) { return v.Begin(op, facts.View.Snapshot) })
	if err != nil {
		return fail("environment_prepare_registration")
	}
	rec.Original, rec.Candidate = registry.Operation.Original, registry.Operation.Candidate
	if err := savePreparation(r.Store.Home, rec, false); err != nil {
		return rec, err
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

func preparationManifest(roots []revive.RootPlan, data environment.DataPlan, input environment.TestInput) ([]revive.ManifestEntry, error) {
	sqlite := map[identity.SnapshotRef]map[string]bool{}
	add := func(vaultID string, h vault.Snapshot) {
		paths := map[string]bool{}
		for _, note := range h.SQLite {
			paths[note.Root+"/"+note.Path] = true
		}
		sqlite[identity.SnapshotRef{VaultID: vaultID, SnapshotID: h.SnapshotID}] = paths
	}
	add(input.VaultID, input.Header)
	if input.Baseline != nil {
		add(input.Baseline.VaultID, input.Baseline.Header)
	}
	var out []revive.ManifestEntry
	for _, entry := range data.Entries {
		if entry.File == nil || entry.Link != nil {
			return nil, preparationError("environment_prepare_entries")
		}
		root, path := entry.Root, entry.Path
		for _, r := range roots {
			if r.ID == root && r.SwapRoot != "" {
				root, path = r.SwapRoot, filepath.ToSlash(filepath.Join(r.Rel, path))
				break
			}
		}
		f := entry.File
		out = append(out, revive.ManifestEntry{Path: root + "/" + path, Size: f.Size, SHA256: f.SHA256, Category: f.Category, SQLite: sqlite[entry.Source][entry.Root+"/"+entry.Path]})
	}
	slices.SortFunc(out, func(a, b revive.ManifestEntry) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	for i := 1; i < len(out); i++ {
		if out[i].Path == out[i-1].Path {
			return nil, preparationError("environment_prepare_entries")
		}
	}
	return out, nil
}

func preparationWorkspace(home string, p *EnvironmentPreparation, create bool) (string, error) {
	dir, err := preparationDir(home, p.Params.Agent, false)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, p.OperationID)
	if create {
		if err := os.Mkdir(path, 0700); err != nil {
			return "", err
		}
	}
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return "", managedDataError()
	}
	return path, nil
}

// QueryEnvironmentPreparation only reads private metadata; a lost connection
// during copying/registration/probing never authorizes another execution.
func (r *Runner) QueryEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op string) (*EnvironmentPreparation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return loadPreparation(r.Store.Home, agent, op)
}
