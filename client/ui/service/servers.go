package service

import (
	"context"
	"os"
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

// Server setup steps (docs/ARCHITECTURE.md section 3, "添加服务器").
const (
	SetupCheck    = "check"
	SetupDeploy   = "deploy"
	SetupRegister = "register"
	SetupName     = "name"
	SetupRecord   = "record"
)

// SetupStep reports one step of adding a server.
type SetupStep struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// SetupOptions configure adding a server. Deploy is used only when the
// server has no usable porter; Name defaults to the alias.
type SetupOptions struct {
	Deploy   DeploySource
	Name     string
	KeyLabel string
	Step     func(SetupStep)
}

// SetupServer adds a server: check it, deploy porter when missing, register
// this client's key, name it, and record it. It stops at the first failing
// step and returns that step's error.
func (s *Service) SetupServer(ctx context.Context, host string, o SetupOptions) error {
	step := func(st SetupStep) {
		if o.Step != nil {
			o.Step(st)
		}
	}
	doc, err := s.Doctor(ctx, host)
	if err != nil {
		return err
	}
	step(SetupStep{Step: SetupCheck, OK: true, Detail: doc.Platform.String()})
	if doc.Problem != nil {
		if o.Deploy == (DeploySource{}) {
			return valise.New("setup_need_porter", "服务器上还没有可用的 porter："+doc.Problem.Reason,
				"选择 porter 的来源：--release <版本>（从 GitHub Release 下载并校验）或 --local <文件>")
		}
		rep, err := s.DeployPorter(ctx, host, o.Deploy)
		if err != nil {
			return err
		}
		step(SetupStep{Step: SetupDeploy, OK: true, Detail: rep.Porter.Info.Version})
	} else {
		step(SetupStep{Step: SetupDeploy, OK: true, Detail: "已有 porter " + doc.PorterVersion})
	}
	label := o.KeyLabel
	if label == "" {
		if h, err := os.Hostname(); err == nil {
			label = h
		}
	}
	if _, _, err := s.KeyRegister(ctx, host, label); err != nil {
		return err
	}
	step(SetupStep{Step: SetupRegister, OK: true})
	name := o.Name
	if name == "" {
		name = host
	}
	if err := s.ServerNameSet(ctx, host, name); err != nil {
		ue, ok := valise.As(err)
		if !ok || ue.Code != "server_name_set" {
			return err
		}
		step(SetupStep{Step: SetupName, OK: true, Detail: ue.Reason})
	} else {
		step(SetupStep{Step: SetupName, OK: true, Detail: name})
	}
	if _, err := s.AddServers(host); err != nil {
		return err
	}
	step(SetupStep{Step: SetupRecord, OK: true})
	return nil
}
