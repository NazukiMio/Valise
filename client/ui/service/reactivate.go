package service

import (
	"context"
	"strings"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/valise"
)

// ReactivateAgentRequest configures reactivating a migrated-out agent on its
// previous server either by switching back (stopping the current active target
// and recording a reverse migration_completed relation) or by coexisting.
type ReactivateAgentRequest struct {
	ConnectionID       string             `json:"connection_id"`
	InstanceID         string             `json:"instance_id"`
	Mode               string             `json:"mode"` // "switch_back" | "coexist"
	TargetConnectionID string             `json:"target_connection_id,omitempty"`
	TargetInstanceID   string             `json:"target_instance_id,omitempty"`
	TargetAgentRef     *identity.AgentRef `json:"target_agent_ref,omitempty"`
	StartService       bool               `json:"start_service,omitempty"`
	ConfirmBotConflict bool               `json:"confirm_bot_conflict,omitempty"`
}

// ReactivateAgentResult reports the new active generation and service states.
type ReactivateAgentResult struct {
	ConnectionID   string               `json:"connection_id"`
	InstanceID     string               `json:"instance_id"`
	Mode           string               `json:"mode"`
	Agent          identity.Agent       `json:"agent"`
	Relation       *identity.Relation   `json:"relation,omitempty"`
	TargetStopped  bool                 `json:"target_stopped,omitempty"`
	ServiceStarted bool                 `json:"service_started,omitempty"`
	ServiceStatus  *InstanceServiceInfo `json:"service_status,omitempty"`
}

// ReactivateAgent restores an active generation on a previously migrated-out
// server (either via switch_back or coexist).
func (s *Service) ReactivateAgent(ctx context.Context, req ReactivateAgentRequest) (*ReactivateAgentResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()

	host := strings.TrimSpace(req.ConnectionID)
	inst := strings.TrimSpace(req.InstanceID)
	mode := strings.TrimSpace(req.Mode)
	if host == "" || inst == "" {
		return nil, valise.New("request_invalid", "缺少服务器或实例标识", "选择要恢复接入的服务器和实例后重试")
	}

	switch mode {
	case enroll.ReactivateModeSwitchBack:
		return s.reactivateSwitchBack(ctx, host, inst, req)
	case enroll.ReactivateModeCoexist:
		return s.reactivateCoexist(ctx, host, inst, req)
	default:
		return nil, valise.New("reactivate_mode_invalid", "不支持的恢复模式："+mode, "只支持 switch_back（切回）或 coexist（双开共存）")
	}
}

func (s *Service) reactivateSwitchBack(ctx context.Context, host, inst string, req ReactivateAgentRequest) (*ReactivateAgentResult, error) {
	targetConn := strings.TrimSpace(req.TargetConnectionID)
	targetInst := strings.TrimSpace(req.TargetInstanceID)
	var targetRef *identity.AgentRef
	if req.TargetAgentRef != nil {
		cp := *req.TargetAgentRef
		targetRef = &cp
	}

	// Resolve or verify the current active target via a fresh AgentReader read.
	reader := NewAgentReader(s)
	list, err := reader.ListAgents(ctx, AgentListRequest{})
	if err != nil {
		return nil, err
	}
	if targetConn == "" || targetInst == "" || targetRef == nil {
		for _, item := range list.Items {
			if item.ConnectionID == host && item.Instance.InstanceID == inst && item.MigratedTo != nil {
				if targetConn == "" {
					targetConn = item.MigratedTo.ConnectionID
				}
				if targetInst == "" {
					targetInst = item.MigratedTo.AgentRef.InstanceID
				}
				if targetRef == nil {
					cp := item.MigratedTo.AgentRef
					targetRef = &cp
				}
				break
			}
		}
	}
	if targetConn == "" || targetInst == "" || targetRef == nil {
		return nil, valise.New("switch_target_missing", "无法定位当前活跃的目标服务器或目标实例", "确认目标服务器在线后刷新 Agent 列表重试")
	}
	if targetConn == host && targetInst == inst {
		return nil, valise.New("switch_from_same_instance", "不能从同一个实例切回自身", "选择迁移后的目标实例进行切回")
	}

	// Verify target server observation is fresh and matches targetRef exactly.
	targetMatched := false
	for _, obs := range list.Observations {
		if obs.ConnectionID != targetConn || obs.Status != "fresh" || obs.Value == nil {
			continue
		}
		for _, item := range *obs.Value {
			if item.Instance.InstanceID == targetInst && item.AgentRef != nil && *item.AgentRef == *targetRef {
				targetMatched = true
				break
			}
		}
	}
	if !targetMatched {
		return nil, valise.New("switch_target_mismatch", "无法核实新服务器 "+targetConn+" 上的目标实例世代", "刷新 Agent 列表确认目标服务器在线且世代未变化后重试")
	}

	// Stop target service if present and not verified inactive/failed, and verify stopped.
	targetStopped := false
	if svcInfo, err := s.ServiceStatus(ctx, targetConn, targetInst); err == nil && svcInfo != nil {
		if svcInfo.HasUnit && svcInfo.Active != "inactive" && svcInfo.Active != "failed" {
			stopped, stopErr := s.ControlInstanceService(ctx, targetConn, targetInst, "stop")
			if stopErr != nil {
				return nil, stopErr
			}
			if stopped == nil || (stopped.Active != "inactive" && stopped.Active != "failed") {
				return nil, valise.New("target_stop_failed", "新服务器 "+targetConn+" 上的服务 "+svcInfo.Unit+" 尚未停止", "确认新机服务已停止后再切回")
			}
		}
		targetStopped = true
	} else if err != nil {
		// If profile has no service or cannot be loaded, fail closed when ServiceStatus fails unexpectedly.
		if ue, ok := valise.As(err); !ok || ue.Code != "service_not_declared" {
			return nil, err
		}
		targetStopped = true
	}

	// Call instance_reactivate on the old server with SwitchFrom = targetRef.
	res, err := s.callReactivate(ctx, host, enroll.ReactivateParams{
		InstanceID:        inst,
		Mode:              enroll.ReactivateModeSwitchBack,
		SwitchFrom:        targetRef,
		SourceDisposition: identity.StoppedVerified,
	})
	if err != nil {
		return nil, err
	}

	out := &ReactivateAgentResult{
		ConnectionID:  host,
		InstanceID:    inst,
		Mode:          enroll.ReactivateModeSwitchBack,
		Agent:         res.Agent,
		Relation:      res.Relation,
		TargetStopped: targetStopped,
	}
	if req.StartService {
		if svcInfo, err := s.ServiceStatus(ctx, host, inst); err == nil && svcInfo != nil && svcInfo.HasUnit {
			started, startErr := s.ControlInstanceService(ctx, host, inst, "start")
			if startErr != nil {
				s.invalidateAgents(host)
				s.invalidateAgents(targetConn)
				return out, startErr
			}
			out.ServiceStarted = started.Active == "active"
			out.ServiceStatus = started
		}
	}
	s.invalidateAgents(host)
	s.invalidateAgents(targetConn)
	return out, nil
}

func (s *Service) reactivateCoexist(ctx context.Context, host, inst string, req ReactivateAgentRequest) (*ReactivateAgentResult, error) {
	if !req.ConfirmBotConflict {
		return nil, valise.New("bot_conflict_confirm_required",
			"双开共存会使新旧两台服务器上的 Agent 同时处于活跃状态，可能引发 Bot 凭据或消息渠道路由冲突",
			"确认已理解双活冲突风险并勾选确认后再继续")
	}

	res, err := s.callReactivate(ctx, host, enroll.ReactivateParams{
		InstanceID: inst,
		Mode:       enroll.ReactivateModeCoexist,
	})
	if err != nil {
		return nil, err
	}

	out := &ReactivateAgentResult{
		ConnectionID: host,
		InstanceID:   inst,
		Mode:         enroll.ReactivateModeCoexist,
		Agent:        res.Agent,
	}
	if req.StartService {
		if svcInfo, err := s.ServiceStatus(ctx, host, inst); err == nil && svcInfo != nil && svcInfo.HasUnit {
			started, startErr := s.ControlInstanceService(ctx, host, inst, "start")
			if startErr != nil {
				s.invalidateAgents(host)
				return out, startErr
			}
			out.ServiceStarted = started.Active == "active"
			out.ServiceStatus = started
		}
	}
	s.invalidateAgents(host)
	return out, nil
}

func (s *Service) callReactivate(ctx context.Context, host string, params enroll.ReactivateParams) (*enroll.ReactivateResult, error) {
	op, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	if op.found != nil && !op.found.Supports(enroll.ReactivateCapability) {
		return nil, valise.New("unsupported_capability", "服务器上的 porter 不支持恢复切回或双开（instance-reactivate/1）", "先在服务器设置中更新 porter 后重试")
	}
	var res enroll.ReactivateResult
	if err := s.callOwner(ctx, op, nil, enroll.MethodReactivate, params, &res); err != nil {
		if rpcMissing(err) {
			return nil, valise.New("unsupported_capability", "服务器上的 porter 不支持恢复切回或双开（instance-reactivate/1）", "先在服务器设置中更新 porter 后重试")
		}
		return nil, err
	}
	return &res, nil
}
