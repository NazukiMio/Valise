package activationrecord

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/porter/internal/managedruntime"
)

// Source anchors the unchanged current environment before a later preparation
// has a formal parent. It grants no execution, deletion or acceptance right.
type Source struct {
	Schema        string                     `json:"schema"`
	Agent         identity.AgentRef          `json:"agent"`
	OperationID   string                     `json:"operation_id"`
	Original      environment.Ref            `json:"original"`
	Owner         environment.Operation      `json:"owner"`
	ParentDigest  string                     `json:"parent_digest"`
	Materials     string                     `json:"materials"`
	Definition    string                     `json:"definition"`
	Observed      managedruntime.Observation `json:"observed"`
	RetiredNative *managedruntime.NativeRef  `json:"retired_native,omitempty"`
}

func (s *Source) Valid(a identity.AgentRef, op string) bool {
	return s != nil && s.Schema == "valise-preparation-source/1" && s.Agent == a && protection.ValidateAgent(a) == nil && s.OperationID == op && protection.ValidID(op) && protection.ValidID(s.Owner.ID) && s.Owner.ID != op &&
		(s.Original == s.Owner.Original || s.Original == s.Owner.Candidate) && s.Original.Revision > 0 && environment.OperationFinished(s.Owner.Phase) && len(s.ParentDigest) == 64 && len(s.Materials) == 64 && len(s.Definition) == 64 && s.Observed.Running && !s.Observed.PendingJob
}

func (s *Source) Owns(op environment.Operation) bool {
	if s == nil {
		return false
	}
	owner := s.Owner
	owner.OriginalCleaned, owner.CandidateCleaned = false, false
	op.OriginalCleaned, op.CandidateCleaned = false, false
	return owner == op
}

func LoadSource(home string, a identity.AgentRef, op string) (*Source, error) {
	if !protection.ValidID(op) {
		return nil, recordError()
	}
	dir, err := Dir(home, a, false)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, op+".source.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 || st.Size() > 64<<10 {
		return nil, recordError()
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil {
		return nil, err
	}
	var source Source
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&source) != nil || d.Decode(&extra) != io.EOF || !source.Valid(a, op) {
		return nil, recordError()
	}
	return &source, nil
}

func SaveSource(home string, source Source) error {
	if !source.Valid(source.Agent, source.OperationID) {
		return recordError()
	}
	parent, err := Load(home, source.Agent, source.Owner.ID)
	if err != nil || Digest(parent) != source.ParentDigest {
		return recordError()
	}
	if source.Owner.Snapshot != parent.Plan.Snapshot || source.Owner.Original != parent.Plan.Original || source.Owner.Candidate != parent.Plan.Candidate {
		return recordError()
	}
	material, definition := parent.Plan.OriginalMaterials, parent.OriginalDefinition
	if source.Original == parent.Plan.Candidate {
		material, definition = parent.Plan.CandidateMaterials, parent.CandidateDefinition
	}
	if source.Materials != material || source.Definition != definition {
		return recordError()
	}
	dir, err := Dir(home, source.Agent, true)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, source.OperationID+".source.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := LoadSource(home, source.Agent, source.OperationID)
		old, _ := json.Marshal(existing)
		if readErr == nil && bytes.Equal(old, raw) {
			return nil
		}
		return recordError()
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return syncDirectory(dir)
}
