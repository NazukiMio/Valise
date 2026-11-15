package server

import (
	"context"
	"encoding/json"
	"errors"

	"valise/core/environment"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/porter/internal/environments"
)

func init() {
	methods[environment.MethodSettingsGet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[environment.SettingsParams](raw)
		if err != nil {
			return nil, err
		}
		if err := s.validateAgent(ctx, p.Agent); err != nil {
			return nil, err
		}
		v, err := (environments.Store{Home: s.cfg.Home}).Settings(p.Agent)
		return v, settingsError(err)
	}
	methods[environment.MethodSettingsSet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		if err := s.require(rpc.TierOwner, environment.MethodSettingsSet); err != nil {
			return nil, err
		}
		p, err := decode[environment.SetSettingsParams](raw)
		if err != nil {
			return nil, err
		}
		if err := s.validateAgent(ctx, p.Agent); err != nil {
			return nil, err
		}
		v, err := (environments.Store{Home: s.cfg.Home}).SetSettings(p)
		return v, settingsError(err)
	}
}

func settingsError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, environments.ErrConflict) {
		return valise.New("environment_settings_changed", "恢复设置已经变化，尚未保存这次修改", "重新读取设置后再选择并保存")
	}
	if errors.Is(err, environment.ErrState) {
		return valise.New("environment_settings_busy", "当前恢复状态不允许更改模式", "先处理未完成的恢复；若没有恢复在进行，请重新读取设置")
	}
	return valise.New("environment_settings_unreadable", "无法读取或保存这个 Agent 的恢复设置", "检查服务器连接和 Valise 状态目录权限后重新读取").WithDetail(err.Error())
}
