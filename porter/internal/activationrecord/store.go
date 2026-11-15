// Package activationrecord owns private activation metadata. It executes no runtime actions.
package activationrecord

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
	"valise/porter/internal/managedruntime"
)

const Schema = "valise-activation/1"

// Plan is supplied by the trusted preparation controller, never
// decoded as an event or an arbitrary-command request.
type Plan struct {
	ManagedSourceDigest   string                    `json:"managed_source_digest,omitempty"`
	NativeSourceMaterials string                    `json:"native_source_materials,omitempty"`
	Agent                 identity.AgentRef         `json:"agent"`
	OperationID           string                    `json:"operation_id"`
	Snapshot              identity.SnapshotRef      `json:"snapshot"`
	Original              environment.Ref           `json:"original"`
	Candidate             environment.Ref           `json:"candidate"`
	DataOpID              string                    `json:"data_op_id"`
	OriginalMaterials     string                    `json:"original_materials"`
	CandidateMaterials    string                    `json:"candidate_materials"`
	OriginalProfile       profile.Profile           `json:"original_profile"`
	CandidateProfile      profile.Profile           `json:"candidate_profile"`
	NativeOriginal        *managedruntime.NativeRef `json:"native_original,omitempty"`
}

type Fact struct {
	IntentAt    time.Time                          `json:"intent_at"`
	FinishedAt  time.Time                          `json:"finished_at,omitempty"`
	Command     *managedruntime.ActionResult       `json:"command,omitempty"`
	Gateway     *managedruntime.GatewayObservation `json:"gateway,omitempty"`
	DataState   string                             `json:"data_state,omitempty"`
	FailureCode string                             `json:"failure_code,omitempty"`
}

type Record struct {
	Schema              string          `json:"schema"`
	Plan                Plan            `json:"plan"`
	OriginalDefinition  string          `json:"original_definition"`
	CandidateDefinition string          `json:"candidate_definition"`
	DataPlanDigest      string          `json:"data_plan_digest"`
	DataManifestDigest  string          `json:"data_manifest_digest"`
	EnableOnBoot        bool            `json:"enable_on_boot"`
	BindingDigest       string          `json:"binding_digest"`
	State               string          `json:"state"`
	FailureCode         string          `json:"failure_code,omitempty"`
	IssueCode           string          `json:"issue_code,omitempty"`
	Facts               map[string]Fact `json:"facts"`
	Cleanup             *Cleanup        `json:"cleanup,omitempty"`
}

func Digest(r *Record) string {
	b, _ := protection.Digest(struct {
		Plan                                        Plan
		Original, Candidate, DataPlan, DataManifest string
		Enable                                      bool
	}{r.Plan, r.OriginalDefinition, r.CandidateDefinition, r.DataPlanDigest, r.DataManifestDigest, r.EnableOnBoot})
	return b
}

func Valid(r *Record, agent identity.AgentRef, op string) bool {
	if r.Plan.ManagedSourceDigest != "" && (len(r.Plan.ManagedSourceDigest) != 64 || r.Plan.NativeOriginal != nil || r.Plan.NativeSourceMaterials != "") {
		return false
	}
	if r.Plan.NativeSourceMaterials != "" && (r.Plan.NativeOriginal == nil || r.Plan.NativeSourceMaterials != r.Plan.OriginalMaterials) {
		return false
	}
	if r.Plan.NativeOriginal != nil && r.Plan.NativeOriginal.Validate(r.Plan.OriginalProfile) != nil {
		return false
	}
	return r.Schema == Schema && r.Plan.Agent == agent && r.Plan.OperationID == op && protection.ValidateAgent(agent) == nil &&
		protection.ValidID(op) && revive.ValidOpID(r.Plan.DataOpID) && r.BindingDigest == Digest(r) &&
		len(r.OriginalDefinition) == 64 && len(r.CandidateDefinition) == 64 && len(r.Plan.OriginalMaterials) == 64 && len(r.Plan.CandidateMaterials) == 64 &&
		len(r.DataPlanDigest) == 64 && len(r.DataManifestDigest) == 64 &&
		len(r.Facts) <= 32 && validCleanup(r) && (r.State == "ready" || r.State == "running" || r.State == "awaiting_human" || r.State == "needs_review" || r.State == "reverting" || r.State == "rolled_back" || r.State == "cancelled")
}

func Dir(home string, agent identity.AgentRef, create bool) (string, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || protection.ValidateAgent(agent) != nil {
		return "", recordError()
	}
	current := home
	for _, part := range []string{"", ".local", "state", "valise", "activations", environment.Key(agent)} {
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
			return "", recordError()
		}
	}
	return current, nil
}

func Load(home string, agent identity.AgentRef, op string) (*Record, error) {
	if !protection.ValidID(op) {
		return nil, recordError()
	}
	dir, err := Dir(home, agent, false)
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
	if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 || st.Size() > 2<<20 {
		return nil, recordError()
	}
	b, err := io.ReadAll(io.LimitReader(f, 2<<20+1))
	if err != nil {
		return nil, err
	}
	var r Record
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !Valid(&r, agent, op) {
		return nil, recordError()
	}
	return &r, nil
}

func Save(home string, r *Record) error {
	if !Valid(r, r.Plan.Agent, r.Plan.OperationID) {
		return recordError()
	}
	dir, err := Dir(home, r.Plan.Agent, true)
	if err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > 2<<20 {
		return recordError()
	}
	f, err := os.CreateTemp(dir, ".activation-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, r.Plan.OperationID+".json")); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func recordError() error {
	return valise.New("managed_environment_required", "这些目录属于原来的并行恢复操作，不能单独切换或清理", "回到原恢复记录核实环境状态，再处理切回或清理")
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
