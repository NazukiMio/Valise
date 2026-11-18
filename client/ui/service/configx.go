package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"valise/client/config"
	"valise/core/valise"
)

// Exporting and importing the client's records (docs/ARCHITECTURE.md
// section 3.1): the server list, the host keys the user confirmed for
// those servers, and the settings. Never a key: the daily and admin keys
// stay on this computer, the encryption key travels only in the recovery
// kit.

// ConfigFormat names the export file format.
const ConfigFormat = "valise-config"

// ConfigVersion is the current export format version.
const ConfigVersion = 1

// ConfigExport is the export file, plain JSON.
type ConfigExport struct {
	Format     string          `json:"format"`
	Version    int             `json:"version"`
	ExportedAt time.Time       `json:"exported_at"`
	Servers    []config.Server `json:"servers"`
	KnownHosts []string        `json:"known_hosts"`
	Settings   ConfigSettings  `json:"settings"`
}

// ConfigSettings are the exported settings; machine-specific ones (the ssh
// binary) are left out.
type ConfigSettings struct {
	AdminWindowMinutes int `json:"admin_window_minutes,omitempty"`
}

// ConfigExportResult says what went into the file.
type ConfigExportResult struct {
	Path       string `json:"path"`
	Servers    int    `json:"servers"`
	KnownHosts int    `json:"known_hosts"`
}

// ExportConfig writes the records to path (a new file).
func (s *Service) ExportConfig(path string) (*ConfigExportResult, error) {
	list, err := config.LoadServerList(s.Dirs.Data)
	if err != nil {
		return nil, err
	}
	doc := ConfigExport{Format: ConfigFormat, Version: ConfigVersion, ExportedAt: time.Now().UTC(), Servers: list,
		KnownHosts: []string{}, Settings: ConfigSettings{AdminWindowMinutes: s.configuredAdminWindow()}}
	known, _ := os.ReadFile(filepath.Join(s.Dirs.Data, config.KnownHostsFile))
	for _, srv := range list {
		if srv.Valise() {
			_, lines := parseHostKeys(known, knownHostsField(srv.HostName, srv.Port))
			doc.KnownHosts = append(doc.KnownHosts, lines...)
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, valise.New("config_export_exists", path+" 已经存在", "换一个文件名；Valise 不覆盖已有的文件")
	}
	if err != nil {
		return nil, valise.New("config_export_failed", "无法写入 "+path, "检查路径和权限").WithDetail(err.Error())
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	return &ConfigExportResult{Path: path, Servers: len(list), KnownHosts: len(doc.KnownHosts)}, nil
}

// ConfigImportResult says what was taken from the file.
type ConfigImportResult struct {
	Added []string `json:"added"`
	// NeedKeys are added servers that need this computer's own daily key
	// (valise server rekey); NeedSSHConfig are aliases that must exist in
	// this computer's ~/.ssh/config.
	NeedKeys      []string `json:"need_keys,omitempty"`
	NeedSSHConfig []string `json:"need_ssh_config,omitempty"`
	Same          []string `json:"same,omitempty"`
	// Skipped already exist here with different content; they are left as
	// they are (no overwrite).
	Skipped  []string `json:"skipped,omitempty"`
	Settings string   `json:"settings,omitempty"`
}

// ImportConfig adds the servers of an export file that this computer does
// not have, with their host keys. Existing servers are never overwritten.
func (s *Service) ImportConfig(path string) (*ConfigImportResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, valise.New("config_import_unreadable", "读不了 "+path, "检查路径").WithDetail(err.Error())
	}
	var doc ConfigExport
	if err := json.Unmarshal(b, &doc); err != nil || doc.Format != ConfigFormat {
		return nil, valise.New("config_import_invalid", path+" 不是 Valise 导出的配置文件", "选择用「导出 Valise 配置」生成的 .json 文件")
	}
	if doc.Version > ConfigVersion {
		return nil, valise.New("config_import_newer", "这个配置文件来自更新版本的 Valise", "先升级这台电脑上的 Valise")
	}
	for _, srv := range doc.Servers {
		if err := srv.Validate(); err != nil {
			return nil, valise.New("config_import_invalid", "配置文件里的服务器 "+srv.Name+" 无效："+err.Error(), "重新导出一次")
		}
	}
	list, err := config.LoadServerList(s.Dirs.Data)
	if err != nil {
		return nil, err
	}
	res := &ConfigImportResult{Added: []string{}}
	known, _ := os.ReadFile(filepath.Join(s.Dirs.Data, config.KnownHostsFile))
	for _, srv := range doc.Servers {
		if srv.Source != config.SourceValise {
			srv.Source = config.SourceSSHConfig
		}
		if i := slices.IndexFunc(list, func(x config.Server) bool { return x.Name == srv.Name }); i >= 0 {
			if list[i] == srv {
				res.Same = append(res.Same, srv.Name)
			} else {
				res.Skipped = append(res.Skipped, srv.Name)
			}
			continue
		}
		if srv.Valise() {
			field := knownHostsField(srv.HostName, srv.Port)
			// Keys already confirmed on this computer for that address win.
			if have, _ := parseHostKeys(known, field); len(have) == 0 {
				_, lines := parseHostKeys([]byte(strings.Join(doc.KnownHosts, "\n")), field)
				if len(lines) > 0 {
					if err := s.writeKnownHosts(field, lines); err != nil {
						return nil, err
					}
					known, _ = os.ReadFile(filepath.Join(s.Dirs.Data, config.KnownHostsFile))
				}
			}
			res.NeedKeys = append(res.NeedKeys, srv.Name)
		} else {
			res.NeedSSHConfig = append(res.NeedSSHConfig, srv.Name)
		}
		list = append(list, srv)
		res.Added = append(res.Added, srv.Name)
	}
	if len(res.Added) > 0 {
		if err := config.SaveServerList(s.Dirs.Data, list); err != nil {
			return nil, err
		}
	}
	local := s.configuredAdminWindow()
	if w := doc.Settings.AdminWindowMinutes; w >= 1 && w <= 240 && w != local {
		if local == 0 {
			if err := s.SetAdminWindow(w); err != nil {
				return nil, err
			}
			res.Settings = "管理窗口时长设为 " + strconv.Itoa(w) + " 分钟"
		} else {
			res.Settings = "保留这台电脑的管理窗口时长（" + strconv.Itoa(local) + " 分钟）"
		}
	}
	return res, nil
}
