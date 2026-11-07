package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"valise/core/stationsync"
	"valise/core/vault"
)

// verification-state.json (contracts 5.3; docs/ARCHITECTURE.md 7.1.1):
// hashed packs per place, reused while size and stamp are unchanged, and
// the latest result of each check. Paths and problem kinds only, never
// file contents.

const verificationFile = "verification-state.json"

// verifierVersion changes when the checking rules change; older evidence
// is then shown as stale.
const verifierVersion = "copy-verify/1"

// CopyEvidence is one recorded check (contracts 5.2).
type CopyEvidence struct {
	Location        string    `json:"location_ref"`
	Subject         string    `json:"subject"` // the leaf
	Check           string    `json:"check"`   // period_material | integrity
	Result          string    `json:"result"`  // passed | failed | unknown
	CheckedAt       time.Time `json:"checked_at"`
	SourceRevision  string    `json:"source_revision,omitempty"`
	MaterialDigest  string    `json:"material_digest,omitempty"`
	VerifierVersion string    `json:"verifier_version"`
	Error           string    `json:"error,omitempty"`
}

type verificationState struct {
	// Packs: "host|place|leaf" → path → hashed pack.
	Packs map[string]map[string]vault.Material `json:"packs"`
	// Evidence: "host|place|leaf|check" → latest.
	Evidence map[string]CopyEvidence `json:"evidence"`
}

func (s *Service) loadVerification() *verificationState {
	st := &verificationState{}
	if b, err := os.ReadFile(filepath.Join(s.Dirs.Data, verificationFile)); err == nil {
		json.Unmarshal(b, st)
	}
	if st.Packs == nil {
		st.Packs = map[string]map[string]vault.Material{}
	}
	if st.Evidence == nil {
		st.Evidence = map[string]CopyEvidence{}
	}
	return st
}

func placeKey(host, where, leaf string) string { return host + "|" + where + "|" + leaf }

func (v *verificationState) known(host, where, leaf string) []vault.Material {
	var out []vault.Material
	for _, m := range v.Packs[placeKey(host, where, leaf)] {
		out = append(out, m)
	}
	vault.SortMaterials(out)
	return out
}

func (v *verificationState) lookup(host, where, leaf string) stationsync.KnownPack {
	at := v.Packs[placeKey(host, where, leaf)]
	return func(rel string, size int64, stamp string) (string, bool) {
		m, ok := at[rel]
		if !ok || m.Size != size || m.Stamp != stamp || stamp == "" {
			return "", false
		}
		return m.SHA256, true
	}
}

// remember keeps the hashed packs read at a place; a pack whose hash does
// not match its name is never kept.
func (v *verificationState) remember(host, where, leaf string, list []vault.Material) {
	k := placeKey(host, where, leaf)
	at := map[string]vault.Material{}
	for _, m := range list {
		if vault.IsPackPath(m.Path) && m.SHA256 != "" && m.Stamp != "" && m.SHA256 == vault.PackIDOf(m.Path) {
			at[m.Path] = m
		}
	}
	v.Packs[k] = at
}

func (s *Service) saveVerification(v *verificationState, host string, rep *CopyReport) {
	for _, c := range rep.Checks {
		if c.State == CopyUnchecked {
			continue
		}
		material, integrity := "unknown", "unknown"
		switch c.State {
		case CopyComplete:
			material, integrity = "passed", "passed"
		case CopyListed:
			material = "passed"
		case CopySelf:
			integrity = "passed"
		case CopyIncomplete, CopyAbsent, CopyDeleting:
			material = "failed"
			if len(c.Problems) > 0 {
				integrity = "failed"
			}
		}
		if rep.Basis == "" && material != "failed" {
			material = "unknown"
		}
		for check, result := range map[string]string{"period_material": material, "integrity": integrity} {
			v.Evidence[placeKey(host, c.Where, rep.Leaf)+"|"+check] = CopyEvidence{Location: host + "|" + c.Where, Subject: rep.Leaf, Check: check,
				Result: result, CheckedAt: c.CheckedAt, SourceRevision: rep.Basis, VerifierVersion: verifierVersion, Error: c.Reason}
		}
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	_ = os.WriteFile(filepath.Join(s.Dirs.Data, verificationFile), append(b, '\n'), 0o600)
}

// CopyEvidenceFor returns the latest recorded checks of a place's period.
func (s *Service) CopyEvidenceFor(host, where, leaf string) []CopyEvidence {
	v := s.loadVerification()
	var out []CopyEvidence
	for _, check := range []string{"period_material", "integrity"} {
		if e, ok := v.Evidence[placeKey(host, where, leaf)+"|"+check]; ok {
			out = append(out, e)
		}
	}
	return out
}
