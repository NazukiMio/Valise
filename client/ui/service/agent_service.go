package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"valise/client/porter"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/valise"
)

// InstanceServiceInfo reports the declared and observed systemd service unit
// state for one enrolled instance.
type InstanceServiceInfo struct {
	Instance    string   `json:"instance"`
	ProfileID   string   `json:"profile_id"`
	Unit        string   `json:"unit,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	LoadState   string   `json:"load_state,omitempty"`
	Active      string   `json:"active_state,omitempty"`
	Enabled     string   `json:"unit_file_state,omitempty"`
	HasUnit     bool     `json:"has_unit"`
	InstallArgv []string `json:"install_argv,omitempty"`
	Stale       bool     `json:"stale,omitempty"`
}

var (
	serviceControlSettle = 5 * time.Second
	serviceControlPoll   = 350 * time.Millisecond
)

// ServiceStatus returns the effective profile's service declaration merged
// with the live systemd unit state observed on host.
func (s *Service) ServiceStatus(ctx context.Context, host, instance string) (*InstanceServiceInfo, error) {
	info, _, err := s.serviceTarget(ctx, host, instance)
	return info, err
}

func (s *Service) serviceTarget(ctx context.Context, host, instance string) (*InstanceServiceInfo, profile.Profile, error) {
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, profile.Profile{}, err
	}
	p := target.Profile.ForAgent(target.Record.Name)
	info := &InstanceServiceInfo{
		Instance:  instance,
		ProfileID: p.ID,
		Stale:     target.Record.ProfileVersion != p.ProfileVersion,
	}
	if p.Service != nil {
		if len(p.Service.Candidates) > 0 {
			info.Unit = p.Service.Candidates[0].Unit
			info.Scope = p.Service.Candidates[0].Scope
		}
		if len(p.Service.InstallArgv) > 0 {
			info.InstallArgv = append([]string(nil), p.Service.InstallArgv...)
		}
	}
	list, err := s.Instances(ctx, host)
	if err != nil {
		return nil, p, err
	}
	applyLiveService(info, list, instance)
	return info, p, nil
}

func applyLiveService(info *InstanceServiceInfo, list []enroll.InstanceSummary, instance string) {
	for _, in := range list {
		if in.InstanceID != instance || len(in.Service) == 0 {
			continue
		}
		sv := in.Service[0]
		if info.Unit != "" {
			for _, cand := range in.Service {
				if cand.Unit == info.Unit {
					sv = cand
					break
				}
			}
		}
		if sv.Unit != "" {
			info.Unit = sv.Unit
		}
		if sv.Scope != "" {
			info.Scope = sv.Scope
		}
		info.LoadState = sv.LoadState
		info.Active = sv.Active
		info.Enabled = sv.Enabled
		info.HasUnit = sv.Exists()
		return
	}
}

func isRPCInvalidParams(err error) bool {
	var re *rpc.Error
	return errors.As(err, &re) && re.Code == rpc.CodeInvalidParams
}

// ControlInstanceService runs start, stop, restart or status on the instance's
// declared systemd user service via owner-tier RPC, then polls live state so
// the returned status reflects actual service health rather than command exit.
func (s *Service) ControlInstanceService(ctx context.Context, host, instance, action string) (*InstanceServiceInfo, error) {
	action = strings.TrimSpace(action)
	switch action {
	case "start", "stop", "restart", "status":
	default:
		return nil, valise.New("service_action_invalid", "不支持的服务操作："+action, "只支持 start、stop、restart 或 status")
	}
	info, err := s.ServiceStatus(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	if info.Unit == "" {
		return nil, valise.New("service_not_declared", "这个 Agent 没有声明可管理的后台服务单元", "如需启动或停止它，请在命令行与向导里按该 Harness 的方式操作")
	}
	if action != "status" && info.LoadState != "loaded" {
		return nil, valise.New("service_not_installed", "服务器上尚未安装或加载服务 "+info.Unit, "先在服务卡片中安装该服务单元，再启动、停止或重启")
	}
	stopOp := s.BeginOperation()
	defer stopOp()
	op, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	if err := s.callOwner(ctx, op, nil, revive.MethodService, revive.ServiceParams{Unit: info.Unit, Action: action}, nil); err != nil {
		if action == "restart" && isRPCInvalidParams(err) {
			// Older porters only understood stop/start/status.
			if err := s.callOwner(ctx, op, nil, revive.MethodService, revive.ServiceParams{Unit: info.Unit, Action: "stop"}, nil); err != nil {
				return nil, err
			}
			if err := s.callOwner(ctx, op, nil, revive.MethodService, revive.ServiceParams{Unit: info.Unit, Action: "start"}, nil); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	if action != "status" {
		s.invalidateAgents(host)
		s.settleServiceInfo(ctx, host, instance, action, info)
		return info, nil
	}
	if list, err := s.Instances(ctx, host); err == nil {
		applyLiveService(info, list, instance)
	}
	return info, nil
}

func (s *Service) settleServiceInfo(ctx context.Context, host, instance, action string, info *InstanceServiceInfo) {
	deadline := time.Now().Add(serviceControlSettle)
	for {
		list, err := s.Instances(ctx, host)
		if err != nil {
			return
		}
		applyLiveService(info, list, instance)
		if serviceSettled(action, info.Active) || time.Now().After(deadline) {
			return
		}
		wait := serviceControlPoll
		if rem := time.Until(deadline); rem < wait {
			wait = max(rem, 10*time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func serviceSettled(action, active string) bool {
	switch action {
	case "start", "restart":
		return active == "active" || active == "failed"
	case "stop":
		return active == "inactive" || active == "failed" || active == ""
	default:
		return true
	}
}

// InstallInstanceService runs the profile's declared service.install_argv over
// a fresh admin terminal (with optional local PTY via InstallIO), then reads
// live ServiceStatus separately from the installer's exit code.
func (s *Service) InstallInstanceService(ctx context.Context, host, instance string, tio InstallIO) (int, *InstanceServiceInfo, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	info, p, err := s.serviceTarget(ctx, host, instance)
	if err != nil {
		return -1, nil, err
	}
	if info.Stale {
		return -1, info, valise.New("profile_stale", "本机与服务器的 harness 资料版本不同，没有安装服务", "先在设置中重新接入该实例，再安装服务")
	}
	if p.Service == nil || len(p.Service.InstallArgv) == 0 {
		return -1, info, valise.New("service_install_not_declared", "这个 Agent 的资料没有声明后台服务安装命令", "按该 Harness 的官方文档在命令行与向导中手动安装服务")
	}
	if err := s.RequireGate(ctx, host, instance); err != nil {
		return -1, info, err
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return -1, info, err
	}
	defer done()
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return -1, info, err
	}
	var binDirs []string
	if p.Detect != nil {
		binDirs = p.Detect.BinDirs
	}
	code, runErr := runTTY(ctx, t, found.Path, binDirs, p.Service.InstallArgv, tio)
	s.invalidateAgents(host)
	updated, statusErr := s.ServiceStatus(ctx, host, instance)
	if statusErr == nil && updated != nil {
		info = updated
	}
	if runErr != nil {
		return code, info, runErr
	}
	if statusErr != nil {
		return code, info, statusErr
	}
	return code, info, nil
}
