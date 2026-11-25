package service

import (
	"context"
	"strings"

	"valise/core/enroll"
	"valise/core/valise"
)

// UpdateDossier updates an enrolled instance's dossier extensions (local_roots
// and tool_deps) on host using an owner-tier RPC, and invalidates cached agent
// observations for host on success.
func (s *Service) UpdateDossier(ctx context.Context, host string, p enroll.DossierUpdateParams) (*enroll.Record, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	host = strings.TrimSpace(host)
	p.InstanceID = strings.TrimSpace(p.InstanceID)
	if host == "" || p.InstanceID == "" {
		return nil, valise.New("request_invalid", "缺少服务器或实例标识", "指定服务器和实例后重试")
	}
	op, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	if op.found != nil && !op.found.Supports(enroll.DossierCapability) {
		return nil, valise.New("unsupported_capability", "服务器上的 porter 不支持直接编辑实例档案的附加目录与工具依赖", "先在服务器设置中更新 porter 后重试")
	}
	var res enroll.DossierUpdateResult
	if err := s.callOwner(ctx, op, nil, enroll.MethodDossierUpdate, p, &res); err != nil {
		if rpcMissing(err) {
			return nil, valise.New("unsupported_capability", "服务器上的 porter 不支持直接编辑实例档案的附加目录与工具依赖", "先在服务器设置中更新 porter 后重试")
		}
		return nil, err
	}
	s.invalidateAgents(host)
	return &res.Record, nil
}

// InstanceDossier reads an enrolled instance's full record (dossier view) over
// a daily-tier porter session.
func (s *Service) InstanceDossier(ctx context.Context, host, instance string) (*enroll.Record, error) {
	host = strings.TrimSpace(host)
	instance = strings.TrimSpace(instance)
	if host == "" || instance == "" {
		return nil, valise.New("request_invalid", "缺少服务器或实例标识", "指定服务器和实例后重试")
	}
	var res enroll.InstanceGetResult
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, enroll.MethodInstanceGet, enroll.InstanceGetParams{InstanceID: instance}, &res)
	})
	if err != nil {
		return nil, err
	}
	if res.Record == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+instance, "先运行 valise enroll <服务器> --accept")
	}
	return res.Record, nil
}
