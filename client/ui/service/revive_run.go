package service

import (
	"archive/tar"
	"context"
	"fmt"
	"strings"

	"valise/client/ssh"
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
// confirmation of docs/ARCHITECTURE.md section 8.2).
func (s *Service) ExecuteRevive(ctx context.Context, rp *RevivePlan, fault string, confirm func(*revive.StageVerifyResult) bool) (*revive.SwapResult, error) {
	if len(rp.Blockers) > 0 {
		return nil, &rp.Blockers[0]
	}
	if len(rp.Nested) > 0 {
		return nil, valise.New("nested_instances", "目标目录里还嵌着其他 agent（"+strings.Join(rp.Nested, "、")+"），原地交换会把它们一起挪走，暂不支持",
			"可以单独恢复嵌在里面的 agent，或恢复到另一台服务器；恢复测试不受影响")
	}
	plan := rp.Plan
	// A daily key gets a one-time grant to run stage-write for this op.
	var begun struct {
		Grant string `json:"grant"`
	}
	if err := s.callOwner(ctx, rp.target, nil, revive.MethodStageBegin, revive.StageBeginParams{Plan: plan}, &begun); err != nil {
		return nil, err
	}
	back := func(why error) (*revive.SwapResult, error) {
		var res revive.SwapResult
		s.callOwner(ctx, rp.target, nil, revive.MethodResume, revive.ResumeParams{OpID: plan.OpID, Action: "back"}, &res)
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
	if !confirm(&ver) {
		return back(valise.New("cancelled", "已取消，活动目录未改动", "暂存目录保留，可用 valise prev drop 清理"))
	}
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
	report(ctx, StageSwap, 0, 0)
	var res revive.SwapResult
	err := s.callOwner(ctx, rp.target, faultEnv(fault), revive.MethodSwap, revive.OpParams{OpID: plan.OpID}, &res)
	if stopped && (err != nil || res.State == revive.StateRolledBack) {
		if s.callOwner(ctx, source, nil, revive.MethodService, revive.ServiceParams{Unit: rp.StopSource, Action: "start"}, nil) == nil {
			res.Message += "；已重新启动原服务器上的 " + rp.StopSource
		}
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
			if err := rp.vault.WriteFile(f, tw, rp.ident); err != nil {
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
	o, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	var res revive.SwapResult
	err = s.callOwner(ctx, o, faultEnv(fault), revive.MethodResume, revive.ResumeParams{OpID: op, Action: action}, &res)
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
