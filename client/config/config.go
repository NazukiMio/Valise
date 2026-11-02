// Package config holds client settings and data directories.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ConfigFile is the settings file inside the data directory.
const ConfigFile = "config.json"

// Config is the client settings file.
type Config struct {
	// SSHBinary is the ssh(1) to run. Tests point it at fake-ssh.
	SSHBinary string `json:"ssh_binary,omitempty"`
	// AdminWindowMinutes is how long an unlocked admin key stays usable
	// (1–120, default 15; docs/ARCHITECTURE.md 2.1).
	AdminWindowMinutes int `json:"admin_window_minutes,omitempty"`
	// Repos are local folders escape pods are copied to (docs/
	// ARCHITECTURE.md section 7, "本机仓库"), shared by all servers.
	Repos []Repo `json:"repos,omitempty"`
}

// Repo is a local folder used as a repository.
type Repo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Default returns the default settings.
func Default() Config { return Config{SSHBinary: "ssh"} }

// Dirs are the client's directories.
type Dirs struct {
	// Data holds keystore.age, recipient.txt and config.json.
	Data string
	// Cache holds ControlMaster sockets.
	Cache string
}

// DefaultDirs returns os.UserConfigDir()/valise and os.UserCacheDir()/valise.
func DefaultDirs() (Dirs, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Dirs{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Dirs{}, err
	}
	return Dirs{Data: filepath.Join(cfg, "valise"), Cache: filepath.Join(cache, "valise")}, nil
}

// Load reads config.json from dir; a missing file yields defaults.
func Load(dir string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.SSHBinary == "" {
		c.SSHBinary = "ssh"
	}
	return c, nil
}

// SetAdminWindow records admin_window_minutes in config.json, keeping the
// file's other settings.
func SetAdminWindow(dir string, minutes int) error {
	return setKey(dir, "admin_window_minutes", minutes)
}

// SetRepos replaces the repository list in config.json.
func SetRepos(dir string, repos []Repo) error { return setKey(dir, "repos", repos) }

// setKey merges one key into config.json, keeping the others.
func setKey(dir, key string, value any) error {
	doc := map[string]any{}
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &doc); err != nil {
			return err
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	doc[key] = value
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeAtomic(dir, ConfigFile, append(out, '\n'))
}
