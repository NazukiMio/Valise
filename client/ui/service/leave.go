package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"valise/client/config"
	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/kittools"
	"valise/core/vaultrpc"
)

// Removing a server (docs/ARCHITECTURE.md 2.3): only from this computer,
// or also cleaning what Valise put on the server. The backup library always
// stays; this computer keeps the server's name as a removed server.

// Cleanup items, in the order they run.
const (
	LeaveTimers = "timers"
	LeaveTools  = "tools"
	LeaveClouds = "clouds"
	LeaveSkills = "skills"
	LeaveMark   = "mark" // not a choice: always written when cleaning
	LeavePorter = "porter"
	LeavePasses = "passes"
)

// LeaveOrder is the cleanup order: tasks first, then tools and skills,
// then the passes and porter.
var LeaveOrder = []string{LeaveTimers, LeaveTools, LeaveClouds, LeaveSkills, LeaveMark, LeavePorter, LeavePasses}

// LeftTool is an enhanced tool Valise installed on the server.
type LeftTool struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Binary string `json:"binary"`
}

// LeftSkills are the protection skills Valise installed into one agent.
type LeftSkills struct {
	Instance string   `json:"instance"`
	Label    string   `json:"label,omitempty"`
	Skills   []string `json:"skills"`
}

// ServerLeftovers is what Valise put on a server, as last read.
type ServerLeftovers struct {
	ReadAt     time.Time    `json:"read_at"`
	ServerName string       `json:"server_name,omitempty"`
	Porter     bool         `json:"porter"`
	Passes     []string     `json:"passes,omitempty"` // daily, admin
	Timers     []string     `json:"timers,omitempty"` // instances with a timed backup
	Tools      []LeftTool   `json:"tools,omitempty"`
	Clouds     []string     `json:"clouds,omitempty"` // cloud locations with the server's login
	Skills     []LeftSkills `json:"skills,omitempty"`
}

// Has reports whether item has anything to clean.
func (l *ServerLeftovers) Has(item string) bool {
	switch item {
	case LeaveTimers:
		return len(l.Timers) > 0
	case LeaveTools:
		return len(l.Tools) > 0
	case LeaveClouds:
		return len(l.Clouds) > 0
	case LeaveSkills:
		return len(l.Skills) > 0
	case LeaveMark, LeavePorter:
		return l.Porter
	case LeavePasses:
		return len(l.Passes) > 0
	}
	return false
}

// localPasses are the passes this computer holds for host.
func (s *Service) localPasses(host string) []string {
	var out []string
	if s.HasDailyKey(host) {
		out = append(out, dailyKeyName)
	}
	if s.HasAdminKey(host) {
		out = append(out, adminKeyName)
	}
	return out
}

// ServerLeftovers reads what Valise put on host with the user pass, and
// remembers it for when the server cannot be reached later.
func (s *Service) ServerLeftovers(ctx context.Context, host string) (*ServerLeftovers, error) {
	l := &ServerLeftovers{ReadAt: time.Now().UTC(), Passes: s.localPasses(host)}
	files, err := KitFiles()
	if err != nil {
		return nil, err
	}
	err = s.withSession(ctx, host, func(c caller) error {
		l.Porter = true
		var st vaultrpc.StatusResult
		if err := c.Call(ctx, vaultrpc.MethodStatus, struct{}{}, &st); err != nil {
			return err
		}
		l.ServerName = st.ServerName
		for _, t := range st.Targets {
			if t.Kind == "rclone" {
				l.Clouds = append(l.Clouds, t.Name)
			}
		}
		var list enroll.InstanceListResult
		if err := c.Call(ctx, enroll.MethodInstanceList, struct{}{}, &list); err != nil {
			return err
		}
		for _, in := range list.Instances {
			var ts vaultrpc.TimerStatus
			if err := c.Call(ctx, vaultrpc.MethodTimerStatus, vaultrpc.TimerStatusParams{InstanceID: in.InstanceID}, &ts); err != nil {
				return err
			}
			if ts.Enabled {
				l.Timers = append(l.Timers, in.InstanceID)
			}
			var ks agentkit.KitStatusResult
			if err := c.Call(ctx, agentkit.MethodKitStatus, agentkit.KitStatusParams{InstanceID: in.InstanceID, Files: files}, &ks); err != nil {
				return err
			}
			var names []string
			for _, sk := range ks.Skills {
				if sk.State != agentkit.SkillMissing {
					names = append(names, sk.Skill)
				}
			}
			if len(names) > 0 {
				l.Skills = append(l.Skills, LeftSkills{Instance: in.InstanceID, Label: in.Name, Skills: names})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if tools, err := s.KitTools(ctx, host); err == nil {
		for _, t := range tools {
			if t.Present && t.Ours && t.Tier == kittools.TierEnhanced {
				l.Tools = append(l.Tools, LeftTool{ID: t.ID, Name: t.Name, Binary: t.Check.Binary})
			}
		}
	}
	s.saveLeftovers(host, l)
	return l, nil
}

func (s *Service) leftoversPath() string { return filepath.Join(s.Dirs.Data, "server-leftovers.json") }

func (s *Service) saveLeftovers(host string, l *ServerLeftovers) {
	all := map[string]*ServerLeftovers{}
	if b, err := os.ReadFile(s.leftoversPath()); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	all[host] = l
	b, _ := json.MarshalIndent(all, "", "  ")
	_ = writeAtomicFile(s.leftoversPath(), b)
}

// LastLeftovers is the last read of what Valise put on host, nil when
// never read; the passes are this computer's own and always current.
func (s *Service) LastLeftovers(host string) *ServerLeftovers {
	all := map[string]*ServerLeftovers{}
	if b, err := os.ReadFile(s.leftoversPath()); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	l := all[host]
	if l != nil {
		l.Passes = s.localPasses(host)
	}
	return l
}

// RemovedServer is a server removed from Valise whose backup library stays.
type RemovedServer struct {
	Name       string    `json:"name"`
	ServerName string    `json:"server_name,omitempty"`
	RemovedAt  time.Time `json:"removed_at"`
	// Cleaned: Valise's things on the server were removed; otherwise Left
	// lists what stayed there, as last read.
	Cleaned bool             `json:"cleaned"`
	Left    *ServerLeftovers `json:"left,omitempty"`
	// Server is the record to add it again with (no secrets).
	Server *config.Server `json:"server,omitempty"`
}

func (s *Service) removedPath() string { return filepath.Join(s.Dirs.Data, "removed-servers.json") }

// RemovedServers lists the removed servers, newest first.
func (s *Service) RemovedServers() ([]RemovedServer, error) {
	b, err := os.ReadFile(s.removedPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []RemovedServer
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	slices.SortFunc(list, func(a, b RemovedServer) int { return b.RemovedAt.Compare(a.RemovedAt) })
	return list, nil
}

func (s *Service) saveRemoved(list []RemovedServer) error {
	b, _ := json.MarshalIndent(list, "", "  ")
	return writeAtomicFile(s.removedPath(), b)
}

// unmarkRemoved drops host from the removed servers when it is added again.
func (s *Service) unmarkRemoved(host string) {
	list, err := s.RemovedServers()
	if err != nil || len(list) == 0 {
		return
	}
	if i := slices.IndexFunc(list, func(r RemovedServer) bool { return r.Name == host }); i >= 0 {
		_ = s.saveRemoved(slices.Delete(list, i, i+1))
	}
}

// RemoveServerHere removes host from this computer only: the list entry,
// its passes' files here and its known_hosts entry. Nothing on the server
// changes; left is what stays there (nil when never read).
func (s *Service) RemoveServerHere(host string, left *ServerLeftovers) error {
	return s.removeHere(host, left, false)
}

func (s *Service) removeHere(host string, left *ServerLeftovers, cleaned bool) error {
	srv, ok, err := config.FindServer(s.Dirs.Data, host)
	if err != nil {
		return err
	}
	rec := RemovedServer{Name: host, RemovedAt: time.Now().UTC(), Cleaned: cleaned}
	if !cleaned {
		rec.Left = left
	}
	if left != nil {
		rec.ServerName = left.ServerName
	}
	if ok {
		copySrv := srv
		rec.Server = &copySrv
	}
	list, _ := s.RemovedServers()
	list = slices.DeleteFunc(list, func(r RemovedServer) bool { return r.Name == host })
	if err := s.saveRemoved(append(list, rec)); err != nil {
		return err
	}
	if ok && srv.Valise() {
		s.forgetServer(host)
		if err := s.dropKnownHost(knownHostsField(srv.HostName, srv.Port), host); err != nil {
			return err
		}
	} else if _, err := s.RemoveServer(host); err != nil {
		return err
	}
	for _, name := range []string{dailyKeyName, adminKeyName} {
		for _, f := range []string{name, name + ".pub"} {
			if err := os.Remove(s.keyPath(host, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	_ = os.Remove(config.KeyDir(s.Dirs.Data, host)) // only when empty
	s.invalidateAgents(host)
	return nil
}

// dropKnownHost removes field's lines from Valise's known_hosts unless
// another recorded server still uses the same address.
func (s *Service) dropKnownHost(field, except string) error {
	list, _ := config.LoadServerList(s.Dirs.Data)
	for _, o := range list {
		if o.Name != except && o.Valise() && knownHostsField(o.HostName, o.Port) == field {
			return nil
		}
	}
	path := filepath.Join(s.Dirs.Data, config.KnownHostsFile)
	old, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var keep []string
	for _, l := range strings.Split(strings.TrimRight(string(old), "\n"), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == field {
			continue
		}
		keep = append(keep, l)
	}
	out := strings.Join(keep, "\n")
	if out != "" {
		out += "\n"
	}
	return writeAtomicFile(path, []byte(out))
}

// writeAtomicFile writes a private file through a temporary one.
func writeAtomicFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ForgetRemoved drops a removed server's record from this computer; no
// backup anywhere is touched (docs/ARCHITECTURE.md 2.3).
func (s *Service) ForgetRemoved(host string) error {
	list, err := s.RemovedServers()
	if err != nil {
		return err
	}
	list = slices.DeleteFunc(list, func(r RemovedServer) bool { return r.Name == host })
	if err := s.saveRemoved(list); err != nil {
		return err
	}
	all := map[string]*ServerLeftovers{}
	if b, err := os.ReadFile(s.leftoversPath()); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	if _, ok := all[host]; ok {
		delete(all, host)
		b, _ := json.MarshalIndent(all, "", "  ")
		return writeAtomicFile(s.leftoversPath(), b)
	}
	return nil
}
