// Package service is the business layer shared by the valise CLI and the
// desktop UI. The UI is a thin binding over it; the CLI must work without it.
package service

import (
	"os"
	"path/filepath"

	"valise/client/config"
	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/valise"
)

// Service holds client-wide state.
type Service struct {
	Dirs    config.Dirs
	Config  config.Config
	Version string
	// Home is the local user's home, for ~/.ssh/config.
	Home string
	// SSHConfigPath defaults to ~/.ssh/config.
	SSHConfigPath string
	Release       porter.Release
}

// New loads config.json from dirs.Data.
func New(dirs config.Dirs, version string) (*Service, error) {
	cfg, err := config.Load(dirs.Data)
	if err != nil {
		return nil, valise.New("config_invalid", "无法读取客户端配置 config.json", "检查 "+filepath.Join(dirs.Data, config.ConfigFile)+" 的 JSON 格式").WithDetail(err.Error())
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Service{
		Dirs: dirs, Config: cfg, Version: version, Home: home,
		SSHConfigPath: filepath.Join(home, ".ssh", "config"),
		Release:       porter.Release{PublicKey: porter.ReleasePublicKey},
	}, nil
}

// SSHConfig parses the user's ssh_config.
func (s *Service) SSHConfig() (*ssh.Config, error) {
	c, err := ssh.ParseFile(s.SSHConfigPath, s.Home)
	if err != nil {
		return nil, valise.New("ssh_config_invalid", "无法解析 SSH 配置文件", "检查 "+s.SSHConfigPath+" 的语法").WithDetail(err.Error())
	}
	return c, nil
}

func (s *Service) transport(host string) (*ssh.Transport, ssh.HostConfig, error) {
	c, err := s.SSHConfig()
	if err != nil {
		return nil, ssh.HostConfig{}, err
	}
	hc := c.Lookup(host)
	t, err := ssh.New(host, ssh.Options{Binary: s.Config.SSHBinary, ControlDir: s.Dirs.Cache, HostConfig: &hc})
	if err != nil {
		return nil, hc, valise.New("bad_host", "服务器别名无效："+host, "使用 ~/.ssh/config 中的 Host 别名，或 user@host")
	}
	return t, hc, nil
}
