package service

import (
	"context"
	"os"
	"slices"

	"valise/client/config"
	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/enroll"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// ServerList returns the recorded servers with how they are reached.
func (s *Service) ServerList() ([]config.Server, error) { return config.LoadServerList(s.Dirs.Data) }

// Servers returns the recorded server names.
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
	err := s.withSession(ctx, host, func(c caller) error {
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
	// Doing is told what is happening now within a step, in plain words,
	// before it may take a while (connecting, waiting for a password or the
	// user's SSH agent, downloading).
	Doing func(step, what string)
}

func (o SetupOptions) step(st SetupStep) {
	if o.Step != nil {
		o.Step(st)
	}
}

func (o SetupOptions) doing(step, what string) {
	if o.Doing != nil {
		o.Doing(step, what)
	}
}

// SetupServer sets up a server reached through the user's own SSH setup (or
// re-runs setup on one added by hand, as an admin operation): check it,
// deploy porter when missing, register this client's key, name it, and
// record it. It stops at the first failing step and returns its error.
func (s *Service) SetupServer(ctx context.Context, host string, o SetupOptions) error {
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return err
	}
	defer done()
	if err := s.setupOn(ctx, t, ssh.HostConfig{Alias: host}, o); err != nil {
		return err
	}
	if s.Tiered(host) {
		return nil
	}
	if _, err := s.AddServers(host); err != nil {
		return err
	}
	o.step(SetupStep{Step: SetupRecord, OK: true})
	return nil
}

// setupOn runs the setup steps over a full login t. Registering this
// client's key is skipped while there is no key yet: it is registered
// before the first seal (the daily key may register the first recipient).
func (s *Service) setupOn(ctx context.Context, t *ssh.Transport, hc ssh.HostConfig, o SetupOptions) error {
	host := t.Host()
	o.doing(SetupCheck, "连接服务器，检查系统、架构与 porter")
	doc, err := s.doctorOn(ctx, t, hc, false)
	if err != nil {
		return err
	}
	o.step(SetupStep{Step: SetupCheck, OK: true, Detail: doc.Platform.String()})
	if doc.Problem != nil {
		if o.Deploy == (DeploySource{}) {
			return valise.New("setup_need_porter", "服务器上还没有可用的 porter："+doc.Problem.Reason,
				"选择 porter 的来源：--release <版本>（从 GitHub Release 下载并校验）或 --local <文件>")
		}
		if o.Deploy.LocalPath != "" {
			o.doing(SetupDeploy, "核对并上传本地构建的 porter")
		} else {
			o.doing(SetupDeploy, "从 GitHub Release 下载 porter "+o.Deploy.ReleaseTag+"，核对签名与 SHA-256 后上传")
		}
		rep, err := s.deployOn(ctx, t, o.Deploy)
		if err != nil {
			return err
		}
		o.step(SetupStep{Step: SetupDeploy, OK: true, Detail: rep.Porter.Info.Version})
	} else {
		o.step(SetupStep{Step: SetupDeploy, OK: true, Detail: "已有 porter " + doc.PorterVersion})
	}
	o.doing(SetupRegister, "连接 porter，登记这台电脑")
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return err
	}
	c, _, err := ssh.Dial(ctx, t, found.Path, s.Version)
	if err != nil {
		return err
	}
	defer c.Close()
	if info, err := s.KeyShow(); err == nil {
		label := o.KeyLabel
		if label == "" {
			label, _ = os.Hostname()
		}
		if err := c.Call(ctx, vaultrpc.MethodRecipientAdd, vaultrpc.RecipientAddParams{Recipient: info.Recipient, Label: label}, nil); err != nil {
			return err
		}
		o.step(SetupStep{Step: SetupRegister, OK: true})
	} else {
		o.step(SetupStep{Step: SetupRegister, OK: true, Detail: "还没有加密钥匙，第一次建立备份前再登记"})
	}
	name := o.Name
	if name == "" {
		name = host
	}
	if err := c.Call(ctx, vaultrpc.MethodServerNameSet, vaultrpc.ServerNameSetParams{Name: name}, nil); err != nil {
		ue, ok := valise.As(err)
		if !ok || ue.Code != "server_name_set" {
			return err
		}
		o.step(SetupStep{Step: SetupName, OK: true, Detail: ue.Reason})
	} else {
		o.step(SetupStep{Step: SetupName, OK: true, Detail: name})
	}
	return nil
}
