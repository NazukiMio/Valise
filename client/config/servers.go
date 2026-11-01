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

// ServersFile lists the recorded servers (docs/ARCHITECTURE.md section 3).
// It never holds passwords or key material.
const ServersFile = "servers.json"

// SSHConfigFile is Valise's own ssh_config, rendered from servers.json for
// servers added by hand and passed to OpenSSH with -F. The user's
// ~/.ssh/config is never modified.
const SSHConfigFile = "ssh_config"

// KnownHostsFile holds the host keys the user confirmed for those servers.
const KnownHostsFile = "known_hosts"

// Server sources.
const (
	// SourceValise is a server added by hand: Valise's ssh_config, the daily
	// key, and the optional admin key.
	SourceValise = "valise"
	// SourceSSHConfig is an alias from the user's ~/.ssh/config; connection
	// and keys are the user's own.
	SourceSSHConfig = "ssh_config"
)

// Login methods for a temporary admin login on a SourceValise server.
const (
	LoginPassword = "password"
	LoginKey      = "key"
)

// Server is one recorded server.
type Server struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	// HostName, Port and User: SourceValise only.
	HostName string `json:"hostname,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	// Login is how a temporary admin login authenticates; LoginKey names
	// the user's own private key file (path only).
	Login    string `json:"login,omitempty"`
	LoginKey string `json:"login_key,omitempty"`
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$`)
	userRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,31}$`)
)

// Validate checks the fields that end up in ssh_config and on the ssh
// command line.
func (s Server) Validate() error {
	switch {
	case !nameRe.MatchString(s.Name):
		return fmt.Errorf("别名只能用字母、数字、点、下划线和连字符（不能以符号开头）")
	case !s.Valise():
		return nil
	case !hostRe.MatchString(s.HostName):
		return fmt.Errorf("地址无效：%q", s.HostName)
	case s.Port < 0 || s.Port > 65535:
		return fmt.Errorf("端口无效：%d", s.Port)
	case s.User != "" && !userRe.MatchString(s.User):
		return fmt.Errorf("用户名无效：%q", s.User)
	case s.Login != LoginPassword && s.Login != LoginKey:
		return fmt.Errorf("登录方式只能是密码或私钥文件")
	case s.Login == LoginKey && !filepath.IsAbs(s.LoginKey):
		return fmt.Errorf("私钥文件要写完整路径")
	}
	return nil
}

// Valise reports whether the server uses Valise's own keys.
func (s Server) Valise() bool { return s.Source == SourceValise }

type serversDoc struct {
	Servers []json.RawMessage `json:"servers"`
}

// LoadServerList returns the recorded servers in order. Entries of the
// earlier format (bare alias strings) read as SourceSSHConfig.
func LoadServerList(dir string) ([]Server, error) {
	b, err := os.ReadFile(filepath.Join(dir, ServersFile))
	if errors.Is(err, os.ErrNotExist) {
		return []Server{}, nil
	}
	if err != nil {
		return nil, err
	}
	var d serversDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	out := []Server{}
	for _, raw := range d.Servers {
		var alias string
		if json.Unmarshal(raw, &alias) == nil {
			out = append(out, Server{Name: alias, Source: SourceSSHConfig})
			continue
		}
		var s Server
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		if s.Source == "" {
			s.Source = SourceSSHConfig
		}
		out = append(out, s)
	}
	return out, nil
}

// LoadServers returns the recorded server names in order.
func LoadServers(dir string) ([]string, error) {
	list, err := LoadServerList(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(list))
	for i, s := range list {
		names[i] = s.Name
	}
	return names, nil
}

// FindServer returns the recorded server with that name, if any.
func FindServer(dir, name string) (Server, bool, error) {
	list, err := LoadServerList(dir)
	if err != nil {
		return Server{}, false, err
	}
	for _, s := range list {
		if s.Name == name {
			return s, true, nil
		}
	}
	return Server{}, false, nil
}

// SaveServerList writes servers.json and re-renders Valise's ssh_config.
func SaveServerList(dir string, servers []Server) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(struct {
		Servers []Server `json:"servers"`
	}{servers}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(dir, ServersFile, append(b, '\n')); err != nil {
		return err
	}
	return writeAtomic(dir, SSHConfigFile, []byte(RenderSSHConfig(dir, servers)))
}

// SaveServers records bare aliases from ~/.ssh/config, keeping the details
// of servers already recorded.
func SaveServers(dir string, names []string) error {
	old, err := LoadServerList(dir)
	if err != nil {
		return err
	}
	byName := map[string]Server{}
	for _, s := range old {
		byName[s.Name] = s
	}
	list := make([]Server, 0, len(names))
	for _, n := range names {
		s, ok := byName[n]
		if !ok {
			s = Server{Name: n, Source: SourceSSHConfig}
		}
		list = append(list, s)
	}
	return SaveServerList(dir, list)
}

// RenderSSHConfig is Valise's ssh_config for the SourceValise servers. Keys
// are passed on the command line per access level, never here, so a
// temporary admin login cannot fall back to the daily key.
func RenderSSHConfig(dir string, servers []Server) string {
	var b strings.Builder
	b.WriteString("# Generated by Valise from servers.json; edits are overwritten.\n")
	for _, s := range servers {
		if !s.Valise() || s.Validate() != nil {
			continue
		}
		port := s.Port
		if port == 0 {
			port = 22
		}
		fmt.Fprintf(&b, "\nHost %s\n  HostName %s\n  Port %s\n", s.Name, s.HostName, strconv.Itoa(port))
		if s.User != "" {
			fmt.Fprintf(&b, "  User %s\n", s.User)
		}
		fmt.Fprintf(&b, "  UserKnownHostsFile %q\n  StrictHostKeyChecking yes\n  UpdateHostKeys no\n", filepath.Join(dir, KnownHostsFile))
	}
	return b.String()
}

// KeyDir holds a server's daily and admin keys.
func KeyDir(dir, server string) string { return filepath.Join(dir, "keys", server) }

func writeAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}
