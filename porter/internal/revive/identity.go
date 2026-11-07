package revive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
	identitystore "valise/porter/internal/identity"
)

func identityFailure() error {
	return valise.New("recovery_identity_conflict", "恢复身份上下文或隔离核实结果与操作不一致", "重新核对恢复计划与源服务状态，不要将未知结果标为已完成迁移")
}

// RecordDisposition is called only on the owner swap path. stopped_verified is
// the trusted client's report after its real source stop/status calls, not a
// human approval. Target-side exclusion is independently checked on the stage.
func (r *Runner) RecordDisposition(op, disposition string) error {
	j, err := r.Store.Load(op)
	if err != nil {
		return err
	}
	if j.Plan.Identity == nil {
		if disposition != "" {
			return identityFailure()
		}
		return nil
	}
	if j.State != revive.StatePlanned || !j.StageVerified {
		return identityFailure()
	}
	c := j.Plan.Identity
	if c.Mode != j.Plan.Mode || c.SourceSnapshot.SnapshotID != j.Plan.SnapshotID || c.SourceSnapshot.VaultID == "" {
		return identityFailure()
	}
	if disposition == "" {
		disposition = identity.Unresolved
	}
	switch disposition {
	case identity.StoppedVerified:
		if j.Plan.Mode != revive.ModeMigrate {
			return identityFailure()
		}
	case identity.ExclusiveOmitted:
		if j.Plan.Mode != revive.ModeClone {
			return identityFailure()
		}
		if err := verifyExclusiveOmitted(j); err != nil {
			return err
		}
	case identity.Unresolved:
	default:
		return identityFailure()
	}
	j.RecoveryDisposition = disposition
	return r.Store.Save(j)
}

func verifyExclusiveOmitted(j *revive.Journal) error {
	for _, ref := range j.Plan.Profile.ExclusiveSecrets {
		var root *revive.RootPlan
		for i := range j.Plan.Roots {
			if j.Plan.Roots[i].ID == ref.Root {
				root = &j.Plan.Roots[i]
				break
			}
		}
		if root == nil {
			return identityFailure()
		}
		stageID, rel := root.ID, ""
		if root.SwapRoot != "" {
			stageID, rel = root.SwapRoot, root.Rel
		}
		stage := ""
		for _, jr := range j.Roots {
			if jr.ID == stageID {
				stage = filepath.Join(jr.Stage, filepath.FromSlash(rel))
				break
			}
		}
		if stage == "" {
			return identityFailure()
		}
		err := filepath.WalkDir(stage, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == stage || d.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(stage, path)
			if err != nil {
				return err
			}
			if profile.MatchGlob(ref.Glob, filepath.ToSlash(relative)) {
				return identityFailure()
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) persistRecoveryFacts(ctx context.Context, j *revive.Journal) error {
	if j.Plan.Record == nil {
		return nil
	}
	rec := *j.Plan.Record
	porterenroll.Refresh(ctx, r.Env, &j.Plan.Profile, &rec)
	if _, err := porterenroll.WriteRecord(r.Env.Home, rec, j.Plan.Profile); err != nil {
		return err
	}
	for _, id := range j.Plan.Supersedes {
		sup := enroll.Supersession{By: rec.InstanceID, OpID: j.Plan.OpID, At: time.Now().UTC()}
		if err := porterenroll.MarkSuperseded(r.Env.Home, id, sup); err != nil {
			return err
		}
	}
	for _, dir := range []string{porterenroll.DossiersDir(r.Env.Home), filepath.Dir(porterenroll.DossiersDir(r.Env.Home))} {
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = f.Sync()
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// finalizeIdentity runs only after actual health success. File-only recovery
// keeps its old facts behavior and cannot produce a successful migration edge.
func (r *Runner) finalizeIdentity(ctx context.Context, j *revive.Journal) error {
	if j.Plan.Identity == nil || j.State != revive.StateHealthy {
		return r.persistRecoveryFacts(ctx, j)
	}
	provenance := *j.Plan.Identity
	if provenance.Mode != j.Plan.Mode || provenance.SourceSnapshot.SnapshotID != j.Plan.SnapshotID {
		return identityFailure()
	}
	disposition := j.RecoveryDisposition
	if disposition == "" {
		disposition = identity.Unresolved
	}
	var roots map[string]string
	var install, digest string
	if j.Plan.Mode == revive.ModeRollback {
		if j.RecoveryTarget == nil {
			b, err := identitystore.Read(r.Env.Home, j.Plan.InstanceID)
			if err != nil {
				return err
			}
			if b.Agent == nil {
				return nil
			}
			j.RecoveryTarget = b.Agent
			if err := r.Store.Save(j); err != nil {
				return err
			}
		}
		roots, install, digest = j.RecoveryTarget.Roots, j.RecoveryTarget.Ref.InstallID, j.RecoveryTarget.ProfileDigest
	} else {
		if j.Plan.Record == nil {
			return identityFailure()
		}
		roots, install = j.Plan.Record.Roots, j.Plan.Profile.ID
		raw, err := json.Marshal(j.Plan.Profile)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		digest = hex.EncodeToString(sum[:])
	}
	_, _, err := identitystore.CompleteRecovery(r.Env.Home, identitystore.RecoveryInput{OperationRef: j.Plan.OpID, Context: provenance, SourceDisposition: disposition, InstallID: install, InstanceID: j.Plan.InstanceID, Roots: roots, ProfileDigest: digest}, func() error { return r.persistRecoveryFacts(ctx, j) })
	return err
}

// SourceStatus reports only a known inactive/failed source service state.
// Empty/unknown status can never be promoted to isolation evidence.
func SourceStatus(ctx context.Context, e *porterenroll.Env, unit string) (string, error) {
	if !unitRe.MatchString(unit) {
		return "", identityFailure()
	}
	state := unitState(ctx, e, unit)
	if state != "inactive" && state != "failed" {
		return state, valise.New("source_stop_unverified", "无法核实源服务已经停止", "检查原服务器服务状态后重试；未知状态不能视为已隔离")
	}
	return strings.TrimSpace(state), nil
}
