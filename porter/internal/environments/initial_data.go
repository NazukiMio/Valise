package environments

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/porter/internal/managedruntime"
)

// InitialData is a read-only anchor for the still-native A before a formal
// parent exists. It conveys no permission to stop, swap, start or clean up.
type InitialData struct {
	Schema        string                   `json:"schema"`
	Agent         identity.AgentRef        `json:"agent"`
	Ref           environment.Ref          `json:"ref"`
	OperationID   string                   `json:"operation_id"`
	Materials     string                   `json:"materials"`
	ProfileDigest string                   `json:"profile_digest"`
	Native        managedruntime.NativeRef `json:"native"`
	Roots         map[string]string        `json:"roots"`
	Directories   []revive.ManagedRoot     `json:"directories"`
}

type InitialDataStore struct{ Home string }

func validInitialData(b *InitialData, a identity.AgentRef) bool {
	if b.Schema != "valise-initial-data/1" || b.Agent != a || protection.ValidateAgent(a) != nil ||
		b.Ref != (environment.Ref{Slot: environment.A, Revision: 1}) || !protection.ValidID(b.OperationID) ||
		len(b.Materials) != 64 || len(b.ProfileDigest) != 64 || len(b.Roots) == 0 || len(b.Directories) != len(b.Roots) {
		return false
	}
	seen := map[string]bool{}
	for _, d := range b.Directories {
		path := b.Roots[d.ID]
		if seen[d.ID] || d.Inode == 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return false
		}
		seen[d.ID] = true
	}
	return true
}

func (s InitialDataStore) dir(create bool) (string, error) {
	if !filepath.IsAbs(s.Home) || filepath.Clean(s.Home) != s.Home {
		return "", environment.ErrState
	}
	current := s.Home
	for _, part := range []string{"", ".local", "state", "valise", "initial-data"} {
		current = filepath.Join(current, part)
		if create {
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
		}
		if err := ownedDirectory(current); err != nil {
			return "", err
		}
	}
	st, err := os.Lstat(current)
	if err != nil || st.Mode().Perm()&0077 != 0 {
		return "", environment.ErrState
	}
	return current, nil
}

func (s InitialDataStore) Load(a identity.AgentRef) (*InitialData, error) {
	dir, err := s.dir(false)
	if err != nil {
		return nil, err
	}
	f, err := privateFile(filepath.Join(dir, environment.Key(a)+".json"), false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() > 1<<20 {
		return nil, environment.ErrState
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	var b InitialData
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&b) != nil || d.Decode(&extra) != io.EOF || !validInitialData(&b, a) {
		return nil, environment.ErrState
	}
	return &b, nil
}

// Save is called under the server mutation lock after building independent A
// and before creating its registry. Only explicit release permits replacement.
func (s InitialDataStore) Save(b InitialData) error {
	if !validInitialData(&b, b.Agent) {
		return environment.ErrState
	}
	if _, err := (Store{Home: s.Home}).Load(b.Agent); !errors.Is(err, os.ErrNotExist) {
		return environment.ErrState
	}
	dir, err := s.dir(true)
	if err != nil {
		return err
	}
	data, err := json.Marshal(b)
	if err != nil || len(data) > 1<<20 {
		return environment.ErrState
	}
	f, err := os.CreateTemp(dir, ".initial-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, environment.Key(b.Agent)+".json")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
