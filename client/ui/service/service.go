// Package service is the business layer shared by the valise CLI and the
// desktop UI. The UI is a thin binding over it; the CLI must work without it.
package service

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"valise/client/config"
	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/valise"
)

// Service holds client-wide state.
type Service struct {
	// RiskAccepted lets this process's operations run without an escape
	// pod elsewhere: the user accepted the risk for this one operation
	// (docs/INSTALL.md 4.2). Only the CLI sets it, for one command.
	RiskAccepted bool
	// locking is set while backup locks are being made (recovery_lock.go).
	locking atomic.Bool
	// recovering is set in recovery mode: background work waits
	// (docs/ARCHITECTURE.md 3.2).
	recovering atomic.Bool
	Dirs       config.Dirs
	Config     config.Config
	Version    string
	// Home is the local user's home, for ~/.ssh/config.
	Home string
	// SSHConfigPath defaults to ~/.ssh/config.
	SSHConfigPath string
	Release       porter.Release
	// AdminAuth is set by the frontend to ask for the admin passphrase.
	AdminAuth AdminAuth
	// OwnerAuth is set by the frontend to ask for the Valise passphrase
	// (the owner proof, owner.go).
	OwnerAuth OwnerAuth
	// PasswordAuth is set by a frontend without a terminal to answer
	// OpenSSH's password and passphrase prompts (askpass_relay.go).
	PasswordAuth PasswordAuth
	// Keyscan replaces ssh-keyscan in tests.
	Keyscan Keyscan
	// NoTerminalLogin is set by full-screen frontends: a temporary admin
	// login cannot ask for the server password on the terminal, so it is
	// refused with a suggestion instead.
	NoTerminalLogin bool

	agent    adminAgent
	full     advanced
	owner    ownerCache
	askRelay relayState

	readersMu            sync.Mutex
	agentsReader         *AgentReader
	journeyReader        *JourneyReader
	journeyReposDigest   string
	protectionController *ProtectionController
	// ProtectionReview is a trusted, in-process human confirmation callback.
	ProtectionReview ProtectionReview
	backupController *BackupController
	BackupReview     BackupReview
	localTasks       *LocalTasks
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
