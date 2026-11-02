package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Stations Valise manages (docs/ARCHITECTURE.md section 7): stations.json
// in the data directory; each also gets a Host block in Valise's
// ssh_config under StationAlias.

const StationsFile = "stations.json"

// Station is a station Valise reaches with its own keys.
type Station struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port,omitempty"`
	User    string `json:"user"`
	Path    string `json:"path"`
	// Login and LoginKey: the one login that installs the keys.
	Login    string `json:"login,omitempty"`
	LoginKey string `json:"login_key,omitempty"`
	// What the station offers to limit server keys, found when added:
	// rrsync, else the path of sftp-server.
	RRsync     bool   `json:"rrsync,omitempty"`
	SFTPServer string `json:"sftp_server,omitempty"`
	// AnySource leaves certificates without a source address (servers
	// whose address changes).
	AnySource bool `json:"any_source,omitempty"`
}

var (
	stationNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	stationPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
)

// StationAlias is the station's Host in Valise's ssh_config.
func StationAlias(name string) string { return "valise-station-" + name }

// Validate checks what ends up in ssh_config and on command lines.
func (s Station) Validate() error {
	switch {
	case !stationNameRe.MatchString(s.Name) || len(s.Name) > 32:
		return errors.New("name")
	case !hostRe.MatchString(s.Address):
		return errors.New("address")
	case s.Port < 0 || s.Port > 65535:
		return errors.New("port")
	case !userRe.MatchString(s.User):
		return errors.New("user")
	case !stationPathRe.MatchString(s.Path) || strings.Contains(s.Path, ".."):
		return errors.New("path")
	}
	return nil
}

// PortOr22 is the SSH port.
func (s Station) PortOr22() int {
	if s.Port == 0 {
		return 22
	}
	return s.Port
}

// LoadStations reads stations.json (missing: none).
func LoadStations(dir string) ([]Station, error) {
	b, err := os.ReadFile(filepath.Join(dir, StationsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Stations []Station `json:"stations"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return f.Stations, nil
}

// SaveStations writes stations.json and re-renders Valise's ssh_config.
func SaveStations(dir string, list []Station) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(struct {
		Stations []Station `json:"stations"`
	}{list}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(dir, StationsFile, append(b, '\n')); err != nil {
		return err
	}
	servers, err := LoadServerList(dir)
	if err != nil {
		return err
	}
	return writeAtomic(dir, SSHConfigFile, []byte(RenderSSHConfig(dir, servers)))
}

// renderStations appends the stations' Host blocks.
func renderStations(b *strings.Builder, dir string) {
	list, _ := LoadStations(dir)
	for _, s := range list {
		if s.Validate() != nil {
			continue
		}
		fmt.Fprintf(b, "\nHost %s\n  HostName %s\n  Port %s\n  User %s\n", StationAlias(s.Name), s.Address, strconv.Itoa(s.PortOr22()), s.User)
		fmt.Fprintf(b, "  UserKnownHostsFile %q\n  StrictHostKeyChecking yes\n  UpdateHostKeys no\n", filepath.Join(dir, KnownHostsFile))
	}
}

// StationKeyDir holds the client's key for a station.
func StationKeyDir(dir, station string) string {
	return filepath.Join(dir, "keys", "_stations", station)
}
