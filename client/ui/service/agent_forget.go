package service

import (
	"context"
	"strings"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/valise"
)

// ForgetInstance removes an enrolled instance's Valise metadata on host
// (dossier, timer, protection plan, proposals and identity binding) without
// touching its backups, live directories or .valise-prev-* leftovers.
func (s *Service) ForgetInstance(ctx context.Context, host, instance string) (*enroll.InstanceForgetResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	host = strings.TrimSpace(host)
	instance = strings.TrimSpace(instance)
	if host == "" || instance == "" {
		return nil, valise.New("request_invalid", "缺少服务器或实例标识", "指定服务器和要移出的实例后重试")
	}

	// Best-effort lookup of the instance's AgentRef before forgetting so any
	// local client protection binding for this generation is cleaned up too.
	var boundRef *identity.AgentRef
	if list, err := s.Instances(ctx, host); err == nil {
		for _, in := range list {
			if in.InstanceID == instance && in.Identity != nil && in.Identity.IdentityState == identity.Bound && in.Identity.Agent != nil {
				ref := in.Identity.Agent.Ref
				boundRef = &ref
				break
			}
		}
	}
	if boundRef == nil {
		if refs, err := s.agentRefs(ctx, host, []string{instance}); err == nil {
			if ref, ok := refs[instance]; ok {
				boundRef = &ref
			}
		}
	}

	op, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	if op.found != nil && !op.found.Supports(enroll.ForgetCapability) {
		return nil, valise.New("unsupported_capability", "服务器上的 porter 不支持移出实例记录", "先在服务器设置中更新 porter 后重试")
	}
	var res enroll.InstanceForgetResult
	if err := s.callOwner(ctx, op, nil, enroll.MethodInstanceForget, enroll.InstanceForgetParams{InstanceID: instance}, &res); err != nil {
		if rpcMissing(err) {
			return nil, valise.New("unsupported_capability", "服务器上的 porter 不支持移出实例记录", "先在服务器设置中更新 porter 后重试")
		}
		return nil, err
	}
	s.invalidateAgents(host)
	if boundRef != nil {
		_ = s.bindingStore().Delete(ctx, *boundRef)
	}
	return &res, nil
}
