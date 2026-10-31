package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// ServersFile lists the recorded servers: SSH aliases only, never
// addresses or credentials (docs/ARCHITECTURE.md section 3).
const ServersFile = "servers.json"

type serversDoc struct {
	Servers []string `json:"servers"`
}

// LoadServers returns the recorded server aliases in order.
func LoadServers(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, ServersFile))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var d serversDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.Servers == nil {
		d.Servers = []string{}
	}
	return d.Servers, nil
}

// SaveServers writes the list atomically.
func SaveServers(dir string, servers []string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(serversDoc{Servers: slices.Clip(servers)}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".servers-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, ServersFile))
}
