package service

import (
	"context"
	"slices"

	"valise/client/config"
	"valise/client/ssh"
	"valise/core/enroll"
	"valise/core/valise"
)

// Servers returns the recorded servers (SSH aliases).
func (s *Service) Servers() ([]string, error) { return config.LoadServers(s.Dirs.Data) }

// AddServers records aliases; already recorded ones are skipped. It returns
// the aliases that were added.
func (s *Service) AddServers(aliases ...string) ([]string, error) {
	list, err := s.Servers()
	if err != nil {
		return nil, err
	}
	var added []string
	for _, a := range aliases {
		if _, err := ssh.New(a, ssh.Options{}); err != nil {
			return nil, valise.New("bad_host", "服务器别名无效："+a, "使用 ~/.ssh/config 中的 Host 别名，或 user@host")
		}
		if !slices.Contains(list, a) {
			list = append(list, a)
			added = append(added, a)
		}
	}
	return added, config.SaveServers(s.Dirs.Data, list)
}

// RemoveServer forgets an alias. Nothing on the server changes.
func (s *Service) RemoveServer(alias string) (bool, error) {
	list, err := s.Servers()
	if err != nil {
		return false, err
	}
	i := slices.Index(list, alias)
	if i < 0 {
		return false, nil
	}
	return true, config.SaveServers(s.Dirs.Data, slices.Delete(list, i, i+1))
}

// ImportableHosts lists the concrete Host aliases of ~/.ssh/config that are
// not recorded yet.
func (s *Service) ImportableHosts() ([]string, error) {
	c, err := s.SSHConfig()
	if err != nil {
		return nil, err
	}
	list, err := s.Servers()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, h := range c.Hosts() {
		if !slices.Contains(list, h) {
			out = append(out, h)
		}
	}
	return out, nil
}

// Instances lists the instances enrolled on host.
func (s *Service) Instances(ctx context.Context, host string) ([]enroll.InstanceSummary, error) {
	var res enroll.InstanceListResult
	err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, enroll.MethodInstanceList, struct{}{}, &res)
	})
	return res.Instances, err
}
