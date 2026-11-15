package service

import (
	"context"
	"time"

	"valise/core/revive"
	"valise/core/valise"
)

// CleanupRestoreTest only reconciles the original test. It never restarts it,
// switches directories or completes a real restore.
func (s *Service) CleanupRestoreTest(ctx context.Context, host, instance, op string) (*RestoreTest, error) {
	unlock, err := s.restoreTestLock(host, instance)
	if err != nil {
		return nil, err
	}
	defer unlock()
	all, err := s.readRestoreTests()
	if err != nil {
		return nil, err
	}
	t, ok := all[host][instance]
	if !ok || t.OpID != op || !restoreTestPending(t) {
		return nil, valise.New("restore_test_changed", "原测试记录已改变", "重新打开健康页，查看最新的测试记录")
	}
	if err := s.discardRestoreTest(ctx, host, instance, &t); err != nil {
		return &t, err
	}
	t.OK, t.State, t.Problem = false, "cancelled", "原测试已收尾；未把中断的测试记为通过"
	return &t, s.recordRestoreTest(host, instance, &t)
}

func (s *Service) discardRestoreTest(ctx context.Context, host, instance string, t *RestoreTest) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := s.cleanLocalRestoreTest(t.OpID); err != nil {
		return err
	}
	trust, err := s.restoreTrust(host)
	if err != nil {
		return err
	}
	if t.TrustDigest == "" || trust != t.TrustDigest {
		return valise.New("restore_test_changed", "服务器连接身份已改变", "检查服务器设置后再核实原测试")
	}
	o, err := s.ownerPorter(ctx, host)
	if err != nil {
		return err
	}
	read := func() (*revive.ReviewResult, error) {
		var result revive.ReviewResult
		err := s.callOwner(ctx, o, nil, revive.MethodReview, revive.OpParams{OpID: t.OpID}, &result)
		return &result, err
	}
	r, err := read()
	if e, ok := valise.As(err); ok && e.Code == "op_unknown" {
		t.Cleanup = "removed"
		return nil
	}
	if err != nil {
		return err
	}
	j := r.Journal
	if t.Depth == "isolated" {
		t.Environment = j.EnvironmentTest
	}
	if j.Plan.OpID != t.OpID || j.Plan.InstanceID != instance || j.Plan.SnapshotID != t.Snapshot || j.Plan.Mode != revive.ModeRollback || j.Plan.Unit != "" || j.Plan.InstallService || j.Plan.Body.Action != revive.BodyKeep || j.Plan.Carry || j.Plan.Identity != nil {
		return valise.New("restore_test_changed", "服务器上的操作不符合原文件测试记录", "保留现场，查看原操作；不能把恢复操作当作测试清理")
	}
	var result revive.SwapResult
	switch {
	case r.Complete && j.State == revive.StateRolledBack && len(r.Kept) == 0:
		t.Cleanup = "removed"
		return nil
	case j.State == revive.StatePlanned:
		err = s.callOwner(ctx, o, nil, revive.MethodResume, revive.ResumeParams{OpID: t.OpID, Action: "discard"}, &result)
	case !r.Complete && j.State == revive.StateRolledBack && len(r.Kept) == 0:
		err = s.callOwner(ctx, o, nil, revive.MethodResumeChecked, revive.CheckedResumeParams{OpID: t.OpID, Action: "back", Digest: r.Digest}, &result)
	default:
		return valise.New("restore_test_changed", "原操作已不处于可清理的测试状态", "保留现场并查看原操作，不要重新发起测试")
	}
	if err != nil {
		return err
	}
	r, err = read()
	if err != nil {
		return err
	}
	if !r.Complete || r.Journal.State != revive.StateRolledBack || len(r.Kept) != 0 {
		return valise.New("restore_test_cleanup", "测试暂存尚未清理完成", "检查服务器连接后再次核实并清理")
	}
	t.Cleanup = "removed"
	return nil
}
