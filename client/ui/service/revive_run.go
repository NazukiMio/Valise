package service

import (
	"archive/tar"
	"context"
	"fmt"
	"strings"
	"time"

	"valise/client/ssh"
	"valise/core/identity"
	"valise/core/revive"
	"valise/core/valise"
)

func faultEnv(fault string) []string {
	if fault == "" {
		return nil
	}
	return []string{revive.FaultEnv + "=" + fault}
}

// tarName is the stage path of a snapshot file: "<swap root>/<rel>".
func tarName(plan revive.Plan, root, path string) string {
	for _, r := range plan.Roots {
		if r.ID == root && r.SwapRoot != "" {
			return r.SwapRoot + "/" + r.Rel + "/" + path
		}
	}
	return root + "/" + path
}

// ExecuteRevive stages, verifies and swaps. confirm is asked with the stage
// verification result before anything live is touched (the second
// confirmation of docs/ARCHITECTURE.md section 8.2). A plan whose source is
// unverified is refused here; use ExecuteReviveConfirmed.
func (s *Service) ExecuteRevive(ctx context.Context, rp *RevivePlan, fault string, confirm func(*revive.StageVerifyResult) bool) (*revive.SwapResult, error) {
	return s.ExecuteReviveConfirmed(ctx, rp, fault, ReviveConfirm{Swap: confirm})
}

// ExecuteReviveConfirmed is ExecuteRevive with every human confirmation
// supplied by the in-process adapter (GUI, TUI or interactive CLI).
func (s *Service) ExecuteReviveConfirmed(ctx context.Context, rp *RevivePlan, fault string, confirm ReviveConfirm) (*revive.SwapResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	if confirm.BackupCurrent && rp.Plan.Mode != revive.ModeRollback {
		return nil, valise.New("backup_first_unavailable", "只有恢复当前实例可以选择先备份当前状态", "重新查看恢复计划")
	}
	if len(rp.Blockers) > 0 {
		return nil, &rp.Blockers[0]
	}
	if len(rp.Nested) > 0 {
		return nil, valise.New("nested_instances", "目标目录里还嵌着其他 agent（"+strings.Join(rp.Nested, "、")+"），原地交换会把它们一起挪走，暂不支持",
			"可以单独恢复嵌在里面的 agent，或恢复到另一台服务器；恢复测试不受影响")
	}
	if confirm.Swap == nil {
		return nil, valise.New("confirmation_required", "缺少交换前的确认", "从 Valise 界面或终端发起恢复")
	}
	plan := rp.Plan
	if err := s.requireRecoveryIdentityCapability(ctx, rp); err != nil {
		return nil, err
	}
	// Before anything is staged: an unverified source needs the person's
	// choice, and "backup first" must have sealed the current state.
	if err := s.settleUnverifiedSource(ctx, rp, confirm.UnverifiedSource); err != nil {
		return nil, err
	}
	if confirm.BackupCurrent && rp.PreRestoreBackup == nil {
		if err := s.backupBeforeRestore(ctx, rp, rp.Plan.InstanceID); err != nil {
			return nil, err
		}
	}
	// A daily key gets a one-time grant to run stage-write for this op.
	var begun struct {
		Grant string `json:"grant"`
	}
	if err := s.callOwner(ctx, rp.target, nil, revive.MethodStageBegin, revive.StageBeginParams{Plan: plan}, &begun); err != nil {
		return nil, err
	}
	back := func(why error) (*revive.SwapResult, error) {
		var res revive.SwapResult
		settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := s.callOwner(settle, rp.target, nil, revive.MethodResume, revive.ResumeParams{OpID: plan.OpID, Action: "back"}, &res); err != nil {
			return &res, valise.New("recovery_pending", "停止请求尚未核实，原操作需要查询", "检查连接后核实原操作，不要重新发送恢复请求")
		}
		return &res, why
	}
	if err := s.streamStage(ctx, rp, begun.Grant); err != nil {
		return back(err)
	}
	var man []revive.ManifestEntry
	for _, f := range rp.files {
		man = append(man, revive.ManifestEntry{Path: tarName(plan, f.Root, f.Path), Size: f.Size, SHA256: f.SHA256,
			Category: f.Category, SQLite: rp.sqlite[f.Root+":"+f.Path]})
	}
	report(ctx, StageVerify, 0, 0)
	var ver revive.StageVerifyResult
	if err := s.callOwner(ctx, rp.target, nil, revive.MethodStageVerify, revive.StageVerifyParams{OpID: plan.OpID, Manifest: man}, &ver); err != nil {
		return back(err)
	}
	if len(ver.Problems) > 0 {
		return back(valise.New("stage_verify_failed", fmt.Sprintf("暂存校验发现 %d 个问题，已退回，活动目录未改动", len(ver.Problems)), "检查网络与服务器磁盘后重试").
			WithDetail(strings.Join(ver.Problems, "\n")))
	}
	if !confirm.Swap(&ver) {
		return back(valise.New("cancelled", "已取消，活动目录未改动", "暂存目录保留，可用 valise prev drop 清理"))
	}
	var checked revive.StageVerifyResult
	if err := s.callOwner(ctx, rp.target, nil, revive.MethodStageVerify, revive.StageVerifyParams{OpID: plan.OpID, Manifest: man}, &checked); err != nil {
		return back(err)
	}
	if len(checked.Problems) > 0 {
		return back(valise.New("stage_verify_failed", "确认期间暂存材料发生变化，未交换活动目录", "重新准备恢复材料"))
	}
	operation, done, accessErr := s.restoreOperationPorter(ctx, rp.Target, plan)
	if accessErr != nil {
		return back(accessErr)
	}
	defer done()
	// From here on nothing may interrupt the swap (docs/UI.md section 6):
	// a cancelled caller (Ctrl-C, a closed window) no longer cancels it.
	ctx = context.WithoutCancel(ctx)
	stopped := false
	var source *ownerPorter
	if rp.StopSource != "" {
		// Stopping the source's service is an owner operation there.
		var err error
		if source, err = s.ownerPorter(ctx, rp.From); err == nil {
			err = s.callOwner(ctx, source, nil, revive.MethodService, revive.ServiceParams{Unit: rp.StopSource, Action: "stop"}, nil)
		}
		if err != nil {
			return back(valise.New("source_stop_failed", "无法停止原服务器上的 "+rp.StopSource, "手动停止后重试；独占凭据不能两边同时使用").WithDetail(err.Error()))
		}
		stopped = true
	}
	// Disposition comes only from this execution's actual results; there is
	// no option for a person or automation to assert it.
	disposition := ""
	if plan.Identity != nil {
		disposition = identity.Unresolved
		switch {
		case plan.Mode == revive.ModeClone, plan.Mode == revive.ModeMigrate && rp.SourceLost:
			disposition = identity.ExclusiveOmitted // excluded while planning; the target rechecks its stage
		case plan.Mode == revive.ModeMigrate && stopped && rp.sourceStatus:
			var st struct {
				State string `json:"state"`
			}
			if s.callOwner(ctx, source, nil, revive.MethodService, revive.ServiceParams{Unit: rp.StopSource, Action: "status"}, &st) == nil &&
				(st.State == "inactive" || st.State == "failed") {
				disposition = identity.StoppedVerified
			}
		}
	}
	if plan.Mode == revive.ModeMigrate && rp.StopSource != "" && disposition != identity.StoppedVerified {
		if stopped {
			s.callOwner(ctx, source, nil, revive.MethodService, revive.ServiceParams{Unit: rp.StopSource, Action: "start"}, nil)
		}
		return back(valise.New("source_stop_unverified", "无法核实原实例已经停用，未启动目标实例", "检查原服务状态并重新准备迁移"))
	}
	report(ctx, StageSwap, 0, 0)
	var res revive.SwapResult
	err := s.callOwner(ctx, operation, faultEnv(fault), revive.MethodSwap, revive.OpParams{OpID: plan.OpID, SourceDisposition: disposition}, &res)
	if stopped && (err != nil || res.State == revive.StateRolledBack) {
		if s.callOwner(ctx, source, nil, revive.MethodService, revive.ServiceParams{Unit: rp.StopSource, Action: "start"}, nil) == nil {
			res.Message += "；已重新启动原服务器上的 " + rp.StopSource
		}
	}
	if err == nil && rp.SourceLost && plan.Mode == revive.ModeMigrate && res.State != revive.StateRolledBack {
		s.recordLostMigration(LostMigration{From: rp.From, Instance: rp.Plan.InstanceID, Target: rp.Target, OpID: plan.OpID, SnapshotID: plan.SnapshotID, At: time.Now().UTC()})
	}
	return &res, err
}

// streamStage decrypts the snapshot in memory and streams it as tar into
// `porter stage-write`. Plaintext exists only in this process and in the
// target's stage directory.
func (s *Service) streamStage(ctx context.Context, rp *RevivePlan, grant string) error {
	remote := ssh.Quote(rp.target.path) + " stage-write --op " + rp.Plan.OpID
	if grant != "" {
		remote += " --grant " + grant
	}
	cmd, err := rp.target.t.Command(remote)
	if err != nil {
		return err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	var done int64
	report(ctx, StageStage, 0, rp.Bytes)
	tw := tar.NewWriter(&countingWriter{ctx: ctx, stage: StageStage, done: &done, total: rp.Bytes, w: in})
	werr := func() error {
		for _, f := range rp.files {
			h := &tar.Header{Name: tarName(rp.Plan, f.Root, f.Path), Mode: int64(f.Mode & 0o7777), Size: f.Size, ModTime: f.MTime, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			if err := rp.writeRestoreFile(f, tw); err != nil {
				return err
			}
		}
		for _, l := range rp.links {
			if err := tw.WriteHeader(&tar.Header{Name: tarName(rp.Plan, l.Root, l.Path), Typeflag: tar.TypeSymlink, Linkname: l.Target, Format: tar.FormatPAX}); err != nil {
				return err
			}
		}
		return tw.Close()
	}()
	in.Close()
	if err := cmd.Wait(); err != nil || werr != nil {
		return valise.New("stage_write_failed", "把快照写入目标服务器的暂存目录时失败，活动目录未改动", "检查网络与目标服务器磁盘后重试").
			WithDetail(fmt.Sprintf("%v %v %s", werr, err, stderr.String()))
	}
	return nil
}

// ReviveResume continues or backs out an unfinished operation.
func (s *Service) ReviveResume(ctx context.Context, host, op, action, fault string) (*revive.SwapResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	o, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	var res revive.SwapResult
	var j revive.Journal
	if err = s.callOwner(ctx, o, nil, revive.MethodJournal, revive.OpParams{OpID: op}, &j); err != nil {
		return nil, err
	}
	accessPlan := j.Plan
	if action == "back" && (j.State == revive.StatePlanned || j.State == revive.StateStopped) && !j.NPMInstalled && !j.ServiceInstalled {
		accessPlan.Body.Action = revive.BodyKeep
		accessPlan.InstallService = false
	}
	err = s.callRestoreOperation(ctx, host, accessPlan, faultEnv(fault), revive.MethodResume, revive.ResumeParams{OpID: op, Action: action}, &res)
	return &res, err
}

// PrevList lists kept pre-swap states on host.
func (s *Service) PrevList(ctx context.Context, host string) (*revive.PrevListResult, error) {
	var res revive.PrevListResult
	err := s.withSession(ctx, host, func(c caller) error { return c.Call(ctx, revive.MethodPrevList, struct{}{}, &res) })
	return &res, err
}

// PrevDrop deletes the leftovers of one finished operation (explicit).
func (s *Service) PrevDrop(ctx context.Context, host, op string) ([]string, error) {
	var res map[string][]string
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, revive.MethodPrevDrop, revive.OpParams{OpID: op}, &res)
	})
	return res["removed"], err
}

// OldList lists superseded instances on host.
func (s *Service) OldList(ctx context.Context, host string) (*revive.OldListResult, error) {
	var res revive.OldListResult
	err := s.withSession(ctx, host, func(c caller) error { return c.Call(ctx, revive.MethodOldList, struct{}{}, &res) })
	return &res, err
}

// OldCleanup removes a superseded instance's kept directories and record
// (explicit).
func (s *Service) OldCleanup(ctx context.Context, host, instance string) ([]string, error) {
	var res map[string][]string
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, revive.MethodOldCleanup, revive.OldCleanupParams{InstanceID: instance}, &res)
	})
	return res["removed"], err
}

// CleanupBatchRequest selects pre-swap leftover ops and superseded instances
// to remove in one authenticated batch on a server.
type CleanupBatchRequest struct {
	PrevOps      []string
	OldInstances []string
}

// CleanupBatchFailure records one item in a batch cleanup that failed.
type CleanupBatchFailure struct {
	Kind   string // "prev" or "old"
	Target string
	Err    error
}

// CleanupBatchResult summarizes what a batch cleanup removed and what failed.
type CleanupBatchResult struct {
	Removed  []string
	PrevDone int
	OldDone  int
	Failures []CleanupBatchFailure
}

// CleanupBatch removes multiple superseded instances and pre-swap directories
// inside a single session, always prompting once for PIN or Valise passphrase
// before opening the batch when the client has a keystore.
func (s *Service) CleanupBatch(ctx context.Context, host string, req CleanupBatchRequest) (*CleanupBatchResult, error) {
	olds := dedupNonEmpty(req.OldInstances)
	prevs := dedupNonEmpty(req.PrevOps)
	if len(olds) == 0 && len(prevs) == 0 {
		return &CleanupBatchResult{}, nil
	}
	stopOp := s.BeginOperation()
	defer stopOp()
	if s.HasKey() {
		if err := s.verifyOwnerFresh(host); err != nil {
			return nil, err
		}
	}
	res := &CleanupBatchResult{}
	seenRemoved := map[string]bool{}
	recordRemoved := func(items []string) {
		for _, p := range items {
			if p != "" && !seenRemoved[p] {
				seenRemoved[p] = true
				res.Removed = append(res.Removed, p)
			}
		}
	}
	err := s.withSession(ctx, host, func(c caller) error {
		// Clean superseded instances first: their Superseded.OpID leftovers are
		// removed once, and any overlapping PrevDrop afterwards is a no-op.
		for _, inst := range olds {
			if err := ctx.Err(); err != nil {
				return err
			}
			var rpcRes map[string][]string
			err := c.Call(ctx, revive.MethodOldCleanup, revive.OldCleanupParams{InstanceID: inst}, &rpcRes)
			recordRemoved(rpcRes["removed"])
			if err != nil {
				if isBatchAbortError(err) {
					return err
				}
				res.Failures = append(res.Failures, CleanupBatchFailure{Kind: "old", Target: inst, Err: err})
				continue
			}
			res.OldDone++
		}
		for _, op := range prevs {
			if err := ctx.Err(); err != nil {
				return err
			}
			var rpcRes map[string][]string
			err := c.Call(ctx, revive.MethodPrevDrop, revive.OpParams{OpID: op}, &rpcRes)
			recordRemoved(rpcRes["removed"])
			if err != nil {
				if isBatchAbortError(err) {
					return err
				}
				res.Failures = append(res.Failures, CleanupBatchFailure{Kind: "prev", Target: op, Err: err})
				continue
			}
			res.PrevDone++
		}
		return nil
	})
	return res, err
}

func dedupNonEmpty(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func isBatchAbortError(err error) bool {
	ue, ok := valise.As(err)
	if !ok {
		return false
	}
	switch ue.Code {
	case "cancelled", "admin_locked", "owner_locked", "pin_locked", "pin_wrong", "pin_destroyed", "passphrase_wrong", "admin_passphrase_wrong":
		return true
	}
	return false
}

// KeyUnregister removes a recipient from host. Snapshots already sealed stay
// decryptable by that key.
func (s *Service) KeyUnregister(ctx context.Context, host, recipient string) ([]string, bool, error) {
	var res struct {
		Added        bool     `json:"added"`
		Fingerprints []string `json:"fingerprints"`
	}
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, revive.MethodRecipientRm, revive.RecipientRemoveParams{Recipient: recipient}, &res)
	})
	return res.Fingerprints, res.Added, err
}
