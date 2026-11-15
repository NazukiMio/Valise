package service

import (
	"context"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
)

func (s *Service) GetEnvironmentSettings(ctx context.Context, host string, agent identity.AgentRef) (environment.Settings, error) {
	var v environment.Settings
	if err := protection.ValidateAgent(agent); err != nil {
		return v, err
	}
	if err := s.call(ctx, host, environment.MethodSettingsGet, environment.SettingsParams{Agent: agent}, &v); err != nil {
		return v, err
	}
	return v, validateEnvironmentSettings(v, agent)
}

func validateEnvironmentSettings(v environment.Settings, agent identity.AgentRef) error {
	if v.Agent != agent || v.Validate() != nil {
		return valise.New("environment_settings_invalid", "服务器返回的恢复设置与当前 Agent 不一致", "重新读取 Agent 信息与恢复设置后再继续")
	}
	return nil
}

func (s *Service) SaveEnvironmentSettings(ctx context.Context, host string, current environment.Settings, mode environment.Mode) (environment.Settings, error) {
	var v environment.Settings
	if err := current.Validate(); err != nil {
		return v, err
	}
	if mode != environment.Parallel && mode != environment.Compact && mode != environment.Direct {
		return v, environment.ErrState
	}
	o, err := s.ownerPorter(ctx, host)
	if err != nil {
		return v, err
	}
	p := environment.SetSettingsParams{Agent: current.Agent, Expected: current.Digest, Mode: mode}
	if err := s.callOwner(ctx, o, nil, environment.MethodSettingsSet, p, &v); err != nil {
		return v, err
	}
	if err := validateEnvironmentSettings(v, current.Agent); err != nil {
		return v, err
	}
	if v.Mode != mode {
		return v, valise.New("environment_settings_invalid", "服务器未确认所选恢复模式", "重新读取设置，核实后再保存")
	}
	return v, nil
}

func (s *Service) InstanceEnvironmentSettings(ctx context.Context, host, instance string) (environment.Settings, error) {
	var binding identity.Binding
	if err := s.call(ctx, host, identity.MethodGet, identity.GetParams{InstanceID: instance}, &binding); err != nil {
		return environment.Settings{}, err
	}
	if binding.IdentityState != identity.Bound || binding.Agent == nil {
		return environment.Settings{}, valise.New("identity_unbound", "这个 Agent 尚未完成身份接入，无法确定恢复设置", "在 Agent 的管理与设置中重新接入后继续")
	}
	return s.GetEnvironmentSettings(ctx, host, binding.Agent.Ref)
}

// Until formal activation is exposed, a direct plan must never masquerade as
// parallel recovery. Managed roots cannot be handed back to the old executor.
func requireDirectRestore(v environment.Settings) error {
	if v.Managed {
		return valise.New("environment_managed", "这个 Agent 已使用独立运行环境，不能通过旧恢复流程交换目录", "使用对应的环境恢复入口；当前构建尚未开放此入口")
	}
	if v.Mode != environment.Direct {
		return valise.New("environment_parallel_unavailable", "已选择并行恢复，但当前构建尚未接通这个 Agent 的正式切换", "等待并行恢复适配完成；若决定使用直接恢复，请先在 Agent 的管理与设置中明确改为直通模式")
	}
	return nil
}
