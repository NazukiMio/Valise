package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
)

// PostMigrateReport summarizes post-migration follow-ups after a migration
// succeeds: companion tool gaps, unpromoted external refs, remapped local
// roots, best-effort protection plan carry-over, and MCP rewire probe status.
type PostMigrateReport struct {
	ToolPresent         []string             `json:"tool_present,omitempty"`
	ToolMissing         []enroll.ToolDep     `json:"tool_missing,omitempty"`
	ExternalRefs        []enroll.ExternalRef `json:"external_refs,omitempty"`
	LocalRoots          []enroll.LocalRoot   `json:"local_roots,omitempty"`
	ProtectionAttempted bool                 `json:"protection_attempted,omitempty"`
	ProtectionCarried   bool                 `json:"protection_carried,omitempty"`
	ProtectionNote      string               `json:"protection_note,omitempty"`
	MCPChecked          bool                 `json:"mcp_checked,omitempty"`
	MCPWired            bool                 `json:"mcp_wired,omitempty"`
	MCPCanWire          bool                 `json:"mcp_can_wire,omitempty"`
	MCPNeedsRewire      bool                 `json:"mcp_needs_rewire,omitempty"`
	MCPNote             string               `json:"mcp_note,omitempty"`
	// Home-path follow-ups (docs: migration across accounts).
	RemappedSymlinks        int                       `json:"remapped_symlinks,omitempty"`
	RewrittenConfigPaths    int                       `json:"rewritten_config_paths,omitempty"`
	RemainingConfigHomeHits int                       `json:"remaining_config_home_hits,omitempty"`
	MCPAutoRan              bool                      `json:"mcp_auto_ran,omitempty"`
	MCPWireOutcome          string                    `json:"mcp_wire_outcome,omitempty"` // "wired" or "failed"
	MCPWireResult           *KitStepOutcome           `json:"mcp_wire_result,omitempty"`
	MCPServiceRestart       *KitServiceRestartOutcome `json:"mcp_service_restart,omitempty"`
}

// RewireMigratedMCP re-runs the harness's MCP add command (pointing at the
// target server's valise-porter path) and restarts its managed service unit
// when one is configured, without overwriting skill files.
func (s *Service) RewireMigratedMCP(ctx context.Context, host, instance string) (*KitPlan, error) {
	return s.RewireMCP(ctx, host, instance, true)
}

// readSourceProtection best-effort fetches the source instance's accepted
// protection plan during migration planning so the user can choose to carry it
// over. Failures never block planning.
func (s *Service) readSourceProtection(ctx context.Context, rp *RevivePlan, fromHost, instanceID string) {
	var ref *identity.AgentRef
	if rp.sourceAgent != nil {
		copyRef := *rp.sourceAgent
		ref = &copyRef
	} else if instanceID != "" {
		var binding identity.Binding
		if s.call(ctx, fromHost, identity.MethodGet, identity.GetParams{InstanceID: instanceID}, &binding) == nil &&
			binding.IdentityState == identity.Bound && binding.Agent != nil {
			copyRef := binding.Agent.Ref
			ref = &copyRef
		}
	}
	if ref == nil {
		return
	}
	view, err := s.GetProtection(ctx, fromHost, protection.GetParams{AgentRef: *ref})
	if err != nil || view.Mode != "accepted" || view.ServerPlan == nil {
		return
	}
	desired := cloneProtection(view.ServerPlan.Desired)
	rp.sourceProtectionDesired = &desired
	rp.SourceProtection = summarizeSourceProtection(desired)
}

func summarizeSourceProtection(d protection.Desired) *SourceProtectionSummary {
	enabled := d.Schedule.Enabled != nil && *d.Schedule.Enabled
	sched := describeScheduleSummary(d.Schedule)
	var targets []string
	for _, cp := range d.ServerCopies {
		if cp.Enabled != nil && *cp.Enabled && cp.TargetRef.ID != "" {
			targets = append(targets, cp.TargetRef.ID)
		}
	}
	var parts []string
	if enabled {
		if sched != "" {
			parts = append(parts, "定时自动备份："+sched)
		} else {
			parts = append(parts, "已启用定时自动备份")
		}
	} else {
		parts = append(parts, "未启用定时自动备份")
	}
	if d.Scope.Tier != "" {
		parts = append(parts, "档位 "+tierSummaryLabel(d.Scope.Tier))
	}
	if d.Period.Unit != "" {
		parts = append(parts, "周期 "+periodUnitSummaryLabel(d.Period.Unit))
	}
	if d.Period.KeepAutoPeriods > 0 {
		parts = append(parts, fmt.Sprintf("保留 %d 个自动周期", d.Period.KeepAutoPeriods))
	}
	if len(targets) > 0 {
		parts = append(parts, "同步目标 "+strings.Join(targets, "、"))
	}
	return &SourceProtectionSummary{
		Enabled:         enabled,
		Schedule:        sched,
		PeriodUnit:      d.Period.Unit,
		Tier:            d.Scope.Tier,
		Targets:         targets,
		KeepAutoPeriods: d.Period.KeepAutoPeriods,
		Summary:         strings.Join(parts, "；"),
	}
}

func describeScheduleSummary(sc protection.Schedule) string {
	if sc.Enabled == nil || !*sc.Enabled {
		return ""
	}
	tz := sc.Timezone
	switch sc.EffectiveCadence() {
	case "hourly":
		return fmt.Sprintf("每 %d 小时（%s）", sc.EveryHours, tz)
	case "weekly":
		return fmt.Sprintf("每周 %s %s（%s）", strings.Join(sc.Weekdays, ","), sc.DailyAt, tz)
	case "monthly":
		return fmt.Sprintf("每月 %d 日 %s（%s）", sc.DayOfMonth, sc.DailyAt, tz)
	default:
		if sc.DailyAt == "" {
			return ""
		}
		return fmt.Sprintf("每天 %s（%s）", sc.DailyAt, tz)
	}
}

func tierSummaryLabel(tier string) string {
	switch tier {
	case vault.TierSoul:
		return "轻装"
	case vault.TierSoulSecrets:
		return "标准"
	case vault.TierFull:
		return "完整"
	case vault.TierRaw:
		return "原始"
	default:
		return tier
	}
}

func periodUnitSummaryLabel(unit string) string {
	switch unit {
	case "week":
		return "每周"
	case "month":
		return "每月"
	case "quarter":
		return "每季"
	default:
		return unit
	}
}

// buildPostMigrateReport assembles the post-migration report after a migration
// swap has already succeeded. Nothing inside may fail or downgrade the restore.
func (s *Service) buildPostMigrateReport(ctx context.Context, t RestoreTask, rp *RevivePlan, carryProtection bool) *PostMigrateReport {
	report := &PostMigrateReport{}
	if rp.ToolCheck != nil {
		report.ToolPresent = append([]string(nil), rp.ToolCheck.Present...)
		report.ToolMissing = append([]enroll.ToolDep(nil), rp.ToolCheck.Missing...)
	}
	if rp.Dossier != nil && len(rp.Dossier.ExternalRefs) > 0 {
		report.ExternalRefs = append([]enroll.ExternalRef(nil), rp.Dossier.ExternalRefs...)
	}
	if len(rp.Plan.LocalRoots) > 0 {
		report.LocalRoots = append([]enroll.LocalRoot(nil), rp.Plan.LocalRoots...)
	}

	if carryProtection && rp.sourceProtectionDesired != nil {
		report.ProtectionAttempted = true
		if t.TargetAgent == nil {
			report.ProtectionCarried = false
			report.ProtectionNote = "目标实例尚未建立可核实的世代身份绑定，未能自动沿用原保护计划，请在目标服务器手动配置"
		} else {
			carried, note := s.carryOverProtection(ctx, t.Target, *t.TargetAgent, t.OpID, *rp.sourceProtectionDesired)
			report.ProtectionCarried = carried
			report.ProtectionNote = note
		}
	}

	report.RemappedSymlinks, report.RewrittenConfigPaths = rp.RemappedSymlinks, rp.RewrittenConfigPaths
	report.RemainingConfigHomeHits = rp.remainingConfigHomeHits()
	if rp.carryMCP && rp.Plan.Profile.AgentKit != nil && len(rp.Plan.Profile.AgentKit.MCPAddArgv) > 0 {
		report.MCPAutoRan = true
		restartSvc := !rp.DataOnly && rp.Plan.Unit != ""
		kp, err := s.RewireMCP(ctx, t.Target, t.Instance, restartSvc)
		if kp != nil {
			report.MCPWireResult = kp.MCPWire
			report.MCPServiceRestart = kp.ServiceRestart
		} else if err != nil {
			report.MCPWireResult = &KitStepOutcome{Attempted: true, ExitCode: -1, Err: errText1(err)}
		}
		if err == nil && kp != nil && kp.MCPWire != nil && kp.MCPWire.OK {
			report.MCPWireOutcome = "wired"
		} else {
			report.MCPWireOutcome = "failed"
		}
	}
	s.probeMigratedMCP(ctx, t, rp, report)
	if report.MCPWireOutcome == "wired" {
		report.MCPNeedsRewire = false
		report.MCPNote = "已执行 Valise MCP 配置命令"
	} else if report.MCPWireOutcome == "failed" {
		report.MCPNeedsRewire = true
		report.MCPNote = "自动重新接入 MCP 没有成功，可在管理页重试"
	}
	return report
}

// carryOverProtection applies the source protection Desired onto the migrated
// target agent ref, filtering ServerCopies to targets that exist on host.
// It is authorized by the human's explicit in-process ReviveConfirm.CarryProtection
// choice and uses an execution-scoped ProtectionController sharing the durable store.
func (s *Service) carryOverProtection(ctx context.Context, host string, targetAgent identity.AgentRef, opID string, srcDesired protection.Desired) (bool, string) {
	desired := cloneProtection(srcDesired)
	targetNames := map[string]bool{}
	if st, err := s.VaultStatus(ctx, host); err == nil && st != nil {
		for _, tg := range st.Targets {
			if tg.Name != "" {
				targetNames[tg.Name] = true
			}
		}
	}
	filtered := make([]protection.ServerCopy, 0, len(desired.ServerCopies))
	var dropped []string
	for _, cp := range desired.ServerCopies {
		if cp.TargetRef.ID != "" && targetNames[cp.TargetRef.ID] {
			cp.TargetRef.AuthorityID = targetAgent.ServerUID
			filtered = append(filtered, cp)
		} else if cp.TargetRef.ID != "" {
			dropped = append(dropped, cp.TargetRef.ID)
		}
	}
	desired.ServerCopies = filtered

	var expectedRev protection.Revision
	if view, err := s.GetProtection(ctx, host, protection.GetParams{AgentRef: targetAgent}); err == nil {
		switch {
		case view.Mode == "accepted" && view.ServerPlan != nil:
			expectedRev = view.ServerPlan.Revision
		case view.Mode == "superseded" && view.SupersededPlan != nil:
			expectedRev = view.SupersededPlan.Revision
		}
	}

	ctrl, err := NewProtectionController(ProtectionControllerConfig{
		StateDir: s.Dirs.Data,
		RPC:      s.call,
		Review: func(_ context.Context, h string, fc protection.FrozenChange) (bool, error) {
			return h == host && fc.Request.AgentRef == targetAgent, nil
		},
	})
	if err != nil {
		return false, "未能初始化保护计划沿用控制器：" + errText1(err) + "（请在目标服务器手动配置保护计划）"
	}

	frozen, err := ctrl.PrepareProtectionChange(ctx, host, protection.PrepareParams{
		AgentRef:         targetAgent,
		ExpectedRevision: expectedRev,
		Desired:          desired,
	})
	if err != nil {
		return false, "未能准备沿用原保护计划：" + errText1(err) + "（可在目标服务器手动配置）"
	}

	task, err := ctrl.StartProtectionChange(ctx, frozen.PlanID, "migrate-carry-"+opID)
	if err != nil {
		return false, "未能应用原保护计划：" + errText1(err) + "（可在目标服务器手动配置）"
	}
	for i := 0; i < 5 && (task.State == "running" || task.State == "reconciling"); i++ {
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
		if ctx.Err() != nil {
			break
		}
		if next, gerr := ctrl.GetTask(ctx, task.TaskRef); gerr == nil {
			task = next
		}
	}
	if task.State == "succeeded" {
		if len(dropped) > 0 {
			return true, "已沿用原保护计划；目标服务器尚未配置同步目标（" + strings.Join(dropped, "、") + "），已自动跳过，后续可在新服务器补充"
		}
		return true, "已沿用原保护计划"
	}
	detail := task.State
	if task.Error != nil && task.Error.Reason != "" {
		detail = task.Error.Reason
	}
	return false, "保护计划沿用未完成（" + detail + "），请在目标服务器检查并重新设置"
}

func (s *Service) probeMigratedMCP(ctx context.Context, t RestoreTask, rp *RevivePlan, report *PostMigrateReport) {
	if rp.Plan.Profile.AgentKit != nil && len(rp.Plan.Profile.AgentKit.MCPAddArgv) > 0 {
		report.MCPCanWire = true
	}
	homeChanged := rp.sourceHome != "" && rp.targetFacts.Home != "" && rp.sourceHome != rp.targetFacts.Home
	st, err := s.KitStatus(ctx, t.Target, t.Instance)
	if err != nil {
		if homeChanged {
			report.MCPNeedsRewire = true
			report.MCPNote = fmt.Sprintf("未能探测目标 MCP 状态，且目标主目录已从 %s 变为 %s，原 MCP 配置里的 valise-porter 路径可能已失效，建议重新接入 MCP", rp.sourceHome, rp.targetFacts.Home)
		} else {
			report.MCPNote = "未能探测目标服务器的 MCP 状态，迁移后请在管理页确认"
		}
		return
	}
	report.MCPChecked = true
	report.MCPWired = st.LastMCP != nil || st.MCPCalls > 0
	if homeChanged {
		report.MCPNeedsRewire = true
		report.MCPNote = fmt.Sprintf("目标服务器主目录已从 %s 变为 %s，原 MCP 配置里的 valise-porter 路径可能已失效，建议重新接入 MCP", rp.sourceHome, rp.targetFacts.Home)
		return
	}
	if !report.MCPWired {
		report.MCPNeedsRewire = true
		if report.MCPCanWire {
			report.MCPNote = "目标服务器尚未检测到 Valise MCP 连通记录，可一键重新接入 MCP 并重启服务"
		} else {
			report.MCPNote = "目标服务器尚未检测到 Valise MCP 连通记录，请按引导在目标上重新配置 MCP 连接"
		}
		return
	}
	report.MCPNote = "目标服务器已检测到 Valise MCP 连通记录"
}

// resolveMigratedTargetAgent best-effort reads the bound target agent identity
// after a migration swap has succeeded so protection carry-over can target it.
func (s *Service) resolveMigratedTargetAgent(ctx context.Context, host, instanceID string) *identity.AgentRef {
	if host == "" || instanceID == "" {
		return nil
	}
	var binding identity.Binding
	if err := s.call(ctx, host, identity.MethodGet, identity.GetParams{InstanceID: instanceID}, &binding); err != nil {
		return nil
	}
	if binding.IdentityState != identity.Bound || binding.Agent == nil {
		return nil
	}
	ref := binding.Agent.Ref
	return &ref
}

// finalizeMigratedMember builds the PostMigrateReport for a single migrated
// member after ExecuteReviveConfirmed has succeeded (used by RunBundleRestore
// and RunHarnessMove so bundle and one-click move match single-agent migrate).
func (s *Service) finalizeMigratedMember(ctx context.Context, target, from, mode string, rp *RevivePlan, carryProtection bool) *PostMigrateReport {
	if mode != revive.ModeMigrate || rp == nil {
		return nil
	}
	pmCtx, pmCancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer pmCancel()
	synthTask := RestoreTask{
		OpID:        rp.Plan.OpID,
		Target:      target,
		From:        from,
		Instance:    rp.Plan.InstanceID,
		Mode:        mode,
		State:       "succeeded",
		TargetAgent: s.resolveMigratedTargetAgent(pmCtx, target, rp.Plan.InstanceID),
	}
	return s.buildPostMigrateReport(pmCtx, synthTask, rp, carryProtection)
}
