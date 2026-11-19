// Package kit is porter's side of agent-kit: classification proposals,
// handoff checks, MCP rate limits and skill installation
// (docs/AGENT_KIT.md section 8).
package kit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"valise/core/agentkit"
)

// MinInterval is the shortest gap between two agent seals or checkups.
const MinInterval = 10 * time.Minute

// State is the per-instance MCP state: rate limits, the last checkup's
// findings and the open handoff.
type State struct {
	LastSeal     time.Time     `json:"last_seal,omitzero"`
	LastSealID   string        `json:"last_seal_id,omitempty"`
	LastCheckup  time.Time     `json:"last_checkup,omitzero"`
	LastFindings []string      `json:"last_findings,omitempty"`
	Handoff      *HandoffState `json:"handoff,omitempty"`
	// Declaration is the agent's latest valise_declare.
	Declaration *agentkit.Declaration `json:"declaration,omitempty"`
}

// HandoffState is recorded by valise_handoff_begin.
type HandoffState struct {
	SnapshotID string         `json:"snapshot_id"`
	Started    time.Time      `json:"started"`
	Targets    []TargetBefore `json:"targets"`
}

// TargetBefore is a handoff target as it was at begin.
type TargetBefore struct {
	Root     string `json:"root"`
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	SHA256   string `json:"sha256,omitempty"`
	Size     int64  `json:"size"`
	MaxLines int    `json:"max_lines"`
}

func statePath(home, instance string) string {
	return filepath.Join(home, ".local", "state", "valise", "mcp", instance+".json")
}

// LoadState reads the MCP state; a missing file is an empty state.
func LoadState(home, instance string) (*State, error) {
	s := &State{}
	b, err := os.ReadFile(statePath(home, instance))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	return s, json.Unmarshal(b, s)
}

// SaveState writes the MCP state atomically (0600).
func SaveState(home, instance string, s *State) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return writeAtomic(statePath(home, instance), b)
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func newID(prefix string) string {
	var b [6]byte
	rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// findingIDs lists the ids of a checkup.
func findingIDs(r *agentkit.CheckupResult) []string {
	ids := []string{}
	for _, f := range r.Findings {
		ids = append(ids, f.ID)
	}
	return ids
}
