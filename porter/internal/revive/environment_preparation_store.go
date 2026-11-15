package revive

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
	"valise/core/revive"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
)

const preparationSchema = "valise-environment-preparation/1"
const maxPreparationBytes = 96 << 20

// EnvironmentPreparation is a private intent, not a supplied execution plan.
// It exists before the first copy; reopening never completes a missing step.
type EnvironmentPreparation struct {
	Reuse              *preparationReuse             `json:"reuse,omitempty"`
	ManagedSource      *activationrecord.Source      `json:"managed_source,omitempty"`
	ArchiveMaterial    *environments.ProgramMaterial `json:"archive_material,omitempty"`
	Mode               environment.Mode              `json:"mode"`
	Claims             []preparationClaim            `json:"claims,omitempty"`
	CleanupReview      string                        `json:"cleanup_review,omitempty"`
	ProbeLease         revive.ManagedRoot            `json:"probe_lease,omitempty"`
	ProbeWorkspace     string                        `json:"probe_workspace,omitempty"`
	CancelFrom         string                        `json:"cancel_from,omitempty"`
	ProbeTracking      string                        `json:"probe_tracking,omitempty"`
	Schema             string                        `json:"schema"`
	OperationID        string                        `json:"operation_id"`
	Params             revive.EnvironmentPlanParams  `json:"params"`
	PreviewDigest      string                        `json:"preview_digest"`
	Plan               revive.Plan                   `json:"plan"`
	Native             managedruntime.NativeRef      `json:"native"`
	Directories        []revive.ManagedRoot          `json:"directories"`
	Manifest           []revive.ManifestEntry        `json:"manifest"`
	BindingDigest      string                        `json:"binding_digest"`
	State              string                        `json:"state"`
	FailureCode        string                        `json:"failure_code,omitempty"`
	Archive            string                        `json:"archive,omitempty"`
	OriginalMaterials  string                        `json:"original_materials,omitempty"`
	CandidateMaterials string                        `json:"candidate_materials,omitempty"`
	Original           environment.Ref               `json:"original"`
	Candidate          environment.Ref               `json:"candidate"`
	Probe              *isolated.ProbeResult         `json:"probe,omitempty"`
}

func preparationDigest(p *EnvironmentPreparation) string {
	legacy := environments.Digest(struct {
		Mode        environment.Mode
		Operation   string
		Params      revive.EnvironmentPlanParams
		Preview     string
		Plan        revive.Plan
		Native      managedruntime.NativeRef
		Directories []revive.ManagedRoot
		Manifest    []revive.ManifestEntry
	}{p.Mode, p.OperationID, p.Params, p.PreviewDigest, p.Plan, p.Native, p.Directories, p.Manifest})
	if p.ManagedSource == nil {
		return legacy
	}
	if p.Reuse != nil {
		return environments.Digest(struct {
			Legacy string
			Source *activationrecord.Source
			Reuse  string
		}{legacy, p.ManagedSource, reuseIntentDigest(p.Reuse)})
	}
	return environments.Digest(struct {
		Legacy string
		Source *activationrecord.Source
	}{legacy, p.ManagedSource})
}

func validPreparationSource(p *EnvironmentPreparation) bool {
	if p.ManagedSource == nil {
		return (p.Original == (environment.Ref{}) || p.Original == (environment.Ref{Slot: environment.A, Revision: 1})) && (p.Candidate == (environment.Ref{}) || p.Candidate == (environment.Ref{Slot: environment.B, Revision: 1})) && p.Native.Validate(p.Plan.Profile) == nil && p.ArchiveMaterial == nil
	}
	return p.ManagedSource.Valid(p.Params.Agent, p.OperationID) && p.Original == p.ManagedSource.Original && (p.Candidate.Slot == environment.A || p.Candidate.Slot == environment.B) && p.Candidate.Slot != p.Original.Slot && p.Candidate.Revision > 0 && p.Native == (managedruntime.NativeRef{}) && (p.ArchiveMaterial == nil || len(p.ArchiveMaterial.ArchiveSHA) == 64 && p.ArchiveMaterial.ArchiveBytes > 0)
}

func validPreparation(p *EnvironmentPreparation, agent identity.AgentRef, op string) bool {
	return (p.Mode == environment.Parallel || p.Mode == environment.Compact) && p.Schema == preparationSchema && p.Params.Agent == agent && p.OperationID == op && protection.ValidateAgent(agent) == nil && protection.ValidID(op) &&
		(p.ProbeLease.Inode == 0 || p.ProbeLease.ID == "probe-lease" && p.ProbeTracking == "bwrap-sync-fd/1" && p.ProbeWorkspace != "") &&
		(p.CleanupReview == "" || len(p.CleanupReview) == 64) &&
		validPreparationSource(p) && validPreparationReuse(p) && validPreparationClaims(p) && len(p.PreviewDigest) == 64 && p.BindingDigest == preparationDigest(p) &&
		p.Plan.InstanceID == agent.InstanceID && p.Plan.SnapshotID == p.Params.Input.Header.SnapshotID && p.Plan.Mode == revive.ModeRollback &&
		revive.ValidOpID(p.Plan.OpID) && !p.Plan.TestOnly && p.Plan.IsolatedTest == nil && p.Plan.Body.Action == revive.BodyKeep && !p.Plan.Carry &&
		(p.ProbeWorkspace == "" || filepath.Base(p.ProbeWorkspace) == p.ProbeWorkspace && strings.HasPrefix(p.ProbeWorkspace, "valise-isolated-") && len(p.ProbeWorkspace) < 96) &&
		(p.ProbeTracking == "" || p.ProbeTracking == "bwrap-sync-fd/1") &&
		(p.Archive == "" || filepath.Base(p.Archive) == p.Archive && len(p.Archive) < 96) &&
		(p.OriginalMaterials == "" || len(p.OriginalMaterials) == 64) && (p.CandidateMaterials == "" || len(p.CandidateMaterials) == 64) &&
		slices.Contains([]string{"capturing", "original_ready", "registering", "reusing", "data_ready", "building", "probing", "binding", "prepared", "failed", "cancelling", "cancelled", "cleaning", "cleaned"}, p.State)
}

func preparationDir(home string, agent identity.AgentRef, create bool) (string, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || protection.ValidateAgent(agent) != nil {
		return "", managedDataError()
	}
	current := home
	for _, part := range []string{"", ".local", "state", "valise", "environment-preparations", environment.Key(agent)} {
		if part != "" {
			current = filepath.Join(current, part)
		}
		if create {
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
		}
		st, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 {
			return "", managedDataError()
		}
		if part == environment.Key(agent) && st.Mode().Perm()&0077 != 0 {
			return "", managedDataError()
		}
	}
	return current, nil
}

func loadPreparation(home string, agent identity.AgentRef, op string) (*EnvironmentPreparation, error) {
	if !protection.ValidID(op) {
		return nil, managedDataError()
	}
	dir, err := preparationDir(home, agent, false)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, op+".json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 || st.Size() > maxPreparationBytes {
		return nil, managedDataError()
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPreparationBytes+1))
	if err != nil {
		return nil, err
	}
	var p EnvironmentPreparation
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return nil, managedDataError()
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !validPreparation(&p, agent, op) {
		return nil, managedDataError()
	}
	return &p, nil
}

func savePreparation(home string, p *EnvironmentPreparation, exclusive bool) error {
	if !validPreparation(p, p.Params.Agent, p.OperationID) {
		return managedDataError()
	}
	dir, err := preparationDir(home, p.Params.Agent, true)
	if err != nil {
		return err
	}
	// A second operation must not claim a half-copied first A revision.
	if exclusive {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				if reusablePreparationWorkspace(home, p, entry) {
					continue
				}
				// An interrupted atomic metadata write cannot own copied materials:
				// the committed intent necessarily precedes any copy. Leave the private
				// temporary file untouched; it is neither a second operation nor data.
				st, err := entry.Info()
				if err != nil {
					return err
				}
				owner, ok := st.Sys().(*syscall.Stat_t)
				if strings.HasPrefix(entry.Name(), ".preparation-") && len(entry.Name()) < 96 && st.Mode().IsRegular() && st.Mode().Perm()&0077 == 0 && ok && owner.Uid == uint32(os.Getuid()) && owner.Nlink == 1 && st.Size() <= maxPreparationBytes {
					continue
				}
				return planError("environment_plan_busy")
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			old, err := loadPreparation(home, p.Params.Agent, id)
			if err != nil || id == p.OperationID || old.State != "cleaned" && !completedPreparationForManagedSource(home, p, old) {
				return planError("environment_plan_busy")
			}
		}
	}
	data, err := json.Marshal(p)
	if err != nil || len(data) > maxPreparationBytes {
		return managedDataError()
	}
	f, err := os.CreateTemp(dir, ".preparation-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	path := filepath.Join(dir, p.OperationID+".json")
	if exclusive {
		err = os.Link(f.Name(), path)
	} else {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return err
	}
	if exclusive {
		if err = os.Remove(f.Name()); err != nil {
			return err
		}
	}
	return syncDataDirs(dir)
}

// A retained workspace is allowed only by its persisted creation claim and
// this preparation's reviewed reuse scope, never merely by its directory name.
func reusablePreparationWorkspace(home string, next *EnvironmentPreparation, entry os.DirEntry) bool {
	if !entry.IsDir() || !protection.ValidID(entry.Name()) {
		return false
	}
	old, err := loadPreparation(home, next.Params.Agent, entry.Name())
	if err != nil || !completedPreparationForManagedSource(home, next, old) {
		return false
	}
	path, err := preparationWorkspace(home, old, false)
	if err != nil {
		return false
	}
	for _, claim := range old.Claims {
		if claim.Kind == "workspace" && claim.Root == "" && !claim.Removed {
			fact, err := managedRootIdentity(claim.Identity.ID, path)
			return err == nil && fact == claim.Identity
		}
	}
	return false
}

func completedPreparationForManagedSource(home string, next, old *EnvironmentPreparation) bool {
	if next.ManagedSource == nil {
		return false
	}
	if next.Reuse != nil {
		for _, owner := range next.Reuse.Owners {
			if owner.Operation == old.OperationID {
				return true
			}
		}
	}
	if old.State != "prepared" {
		return false
	}
	registry, err := (environments.Store{Home: home}).Load(next.Params.Agent)
	if err != nil {
		return false
	}
	ops := slices.Clone(registry.History)
	if registry.Operation != nil {
		ops = append(ops, *registry.Operation)
	}
	for _, op := range ops {
		if op.ID != old.OperationID || op.Phase != environment.Accepted || !op.Confirmed || !op.OriginalCleaned || op.Original != old.Original || op.Candidate != old.Candidate {
			continue
		}
		parent, err := loadActivation(home, next.Params.Agent, op.ID)
		return err == nil && parent.Cleanup != nil && parent.Cleanup.Complete
	}
	return false
}
