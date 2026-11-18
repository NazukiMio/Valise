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
	// (1–240, default 30; docs/ARCHITECTURE.md 2.1).
	AdminWindowMinutes int `json:"admin_window_minutes,omitempty"`
	// Repos are local folders escape pods are copied to (docs/
	// ARCHITECTURE.md section 7, "本机仓库"), shared by all servers.
	Repos []Repo `json:"repos,omitempty"`
	// Clouds are cloud drives logged in on this computer (docs/
	// ARCHITECTURE.md 7.3, "这台电脑登录"); the authorization stays in
	// Valise's rclone config, never here.
	Clouds []Cloud `json:"clouds,omitempty"`
	// StationCertHours is how long a server's station certificate lasts
	// (1–720, default 168; renewed when Valise opens).
	StationCertHours int `json:"station_cert_hours,omitempty"`
	// RedDismissDays is how long a dismissed red health item stays off the
	// overview and home (30–365, default 30; docs/UI.md 9.5).
	RedDismissDays int `json:"red_dismiss_days,omitempty"`
	// ChatEntryOff hides the terminal UI's 对话 tab (docs/UI.md 9.5).
	ChatEntryOff bool `json:"chat_entry_off,omitempty"`
	// Takeover lists the servers in takeover mode (default off; docs/
	// docs/SERVICE_CONTRACTS.md 16.F2): the terminal UI runs a fix
	// that needs sudo itself, with sudo -n, after advanced mode is on.
	Takeover map[string]bool `json:"takeover,omitempty"`
	// DetachedTasks is the settings' 分离模式 (default off): when on and the
	// server declares backup-runs/2, a planned backup is handed to the
	// server's one-shot unit.
	DetachedTasks bool `json:"detached_tasks,omitempty"`
	// DebugLog enables or disables structured client debug logging when
	// explicitly configured (nil uses the version-based default unless
	// VALISE_DEBUG is set).
	DebugLog *bool `json:"debug_log,omitempty"`
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

// Cloud is a cloud drive logged in on this computer: the location name
// (its rclone remote is valise-<name>) and the signed list's cloud id.
type Cloud struct {
	Name  string `json:"name"`
	Cloud string `json:"cloud"`
}

// SetClouds replaces the cloud list in config.json.
func SetClouds(dir string, clouds []Cloud) error { return setKey(dir, "clouds", clouds) }

// SetRepos replaces the repository list in config.json.
func SetRepos(dir string, repos []Repo) error { return setKey(dir, "repos", repos) }

// SetChatEntryOff records whether the 对话 tab is hidden.
func SetChatEntryOff(dir string, off bool) error { return setKey(dir, "chat_entry_off", off) }

// SetRedDismissDays records how long a red dismissal lasts.
func SetRedDismissDays(dir string, days int) error { return setKey(dir, "red_dismiss_days", days) }

// SetDetachedTasks records the 分离模式 setting.
func SetDetachedTasks(dir string, on bool) error { return setKey(dir, "detached_tasks", on) }

// SetDebugLog records debug_log in config.json, keeping the file's other
// settings.
func SetDebugLog(dir string, on bool) error { return setKey(dir, "debug_log", on) }

// SetTakeover turns takeover mode on or off for one server, keeping the
// other servers' settings. Off removes the server from the list.
func SetTakeover(dir, host string, on bool) error {
	if host == "" {
		return errors.New("takeover: empty server name")
	}
	c, err := Load(dir)
	if err != nil {
		return err
	}
	m := map[string]bool{}
	for h, v := range c.Takeover {
		if v {
			m[h] = true
		}
	}
	if on {
		m[host] = true
	} else {
		delete(m, host)
	}
	return setKey(dir, "takeover", m)
}

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
