package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"valise/core/identity"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
)

// A restore test (docs/UI.md 9.5 健康) unpacks an escape pod on both sides
// without switching anything: here into a private temporary folder, and on
// the server into a stage directory through the same path a restore takes;
// every file is checked, then both copies are deleted.

const restoreTestsFile = "restore-tests.json"

// RestoreTest is the outcome of one restore test.
type RestoreTest struct {
	Depth          string               `json:"depth,omitempty"`
	SourceLocation *journey.LocationRef `json:"source_location,omitempty"`
	HeaderDigest   string               `json:"header_digest,omitempty"`
	AgentRef       *identity.AgentRef   `json:"agent_ref,omitempty"`
	LocalVerified  bool                 `json:"local_verified,omitempty"`
	ServerVerified bool                 `json:"server_verified,omitempty"`
	State          string               `json:"state,omitempty"`
	OpID           string               `json:"op_id,omitempty"`
	VaultID        string               `json:"vault_id,omitempty"`
	TrustDigest    string               `json:"trust_digest,omitempty"`
	Cleanup        string               `json:"cleanup,omitempty"`
	At             time.Time            `json:"at"`
	Snapshot       string               `json:"snapshot"`
	Tier           string               `json:"tier"`
	Created        time.Time            `json:"created"`
	Files          int                  `json:"files"`
	Bytes          int64                `json:"bytes"`
	OK             bool                 `json:"ok"`
	// Step names the step that failed, Problem says why.
	Step    string `json:"step,omitempty"`
	Problem string `json:"problem,omitempty"`
	// Downtime is what a real restore would add: the plan's estimate.
	Downtime string   `json:"downtime,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	// Transfer is what pulling the pod to this computer took.
	Transfer *SnapshotPull `json:"transfer,omitempty"`
}

// Restore test steps, for progress and for a failed result.
const (
	TestLocal  = "local"  // unpack on this computer
	TestServer = "server" // unpack into the server's stage directory
)

// RunRestoreTest tests snapshot (the instance's newest when "") of
// instance on host. It needs the Valise passphrase (asked through
// OwnerAuth when locked) and temporary space on both sides.
func (s *Service) RunRestoreTest(ctx context.Context, host, instance, snapshot string) (*RestoreTest, error) {
	return s.runRestoreTest(ctx, host, instance, snapshot, nil)
}

func (s *Service) runRestoreTest(ctx context.Context, host, instance, snapshot string, selected *SelectedRestoreTest) (*RestoreTest, error) {
	unlock, err := s.restoreTestLock(host, instance)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if old, ok := s.LastRestoreTest(host, instance); ok && restoreTestPending(old) {
		return &old, valise.New("restore_test_pending", "上次测试的暂存尚未核实", "先核实并清理原测试，再开始新的测试")
	}
	if snapshot == "" {
		st, err := s.VaultStatus(ctx, host)
		if err != nil {
			return nil, err
		}
		var newest *vault.Meta
		for i, m := range st.Snapshots {
			if m.InstanceID == instance && (newest == nil || m.CreatedAt.After(newest.CreatedAt)) {
				newest = &st.Snapshots[i]
			}
		}
		if newest == nil {
			return nil, valise.New("no_snapshot", instance+" 还没有备份，没有可以测试的", "先建立一个备份")
		}
		snapshot = newest.SnapshotID
	}
	// Nothing is switched, so a profile whose restore is not verified yet
	// can still be tested.
	caps, err := s.BackupCapabilities(ctx, host)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(caps, revive.ReviewCapability) {
		return nil, valise.New("unsupported_capability", "此服务器的 porter 尚不支持测试结果核实", "在服务器设置中更新 porter 后再测试")
	}
	o := ReviveOptions{Target: host, SnapshotID: snapshot, Mode: revive.ModeRollback, AllowDraft: true, AllowSnapshotProfile: true, testOnly: true}
	if selected != nil {
		o.Source = selected.Source.Spec
		if selected.Source.Copy.LocationRef.Kind == "server_vault" {
			o.From = selected.Source.Copy.LocationRef.ID
		}
	}
	if err := s.currentRestoreRoots(ctx, &o, instance); err != nil {
		return nil, err
	}
	rp, err := s.planRevive(ctx, o, "")
	if err != nil {
		return nil, err
	}
	defer rp.Close()
	if selected != nil {
		if err := selected.verify(rp); err != nil {
			return nil, err
		}
	}
	if err := currentRestorePlan(rp, instance, o.Roots); err != nil {
		return nil, err
	}
	res := &RestoreTest{At: time.Now().UTC(), Snapshot: snapshot, Tier: rp.Snapshot.Tier, Created: rp.Snapshot.CreatedAt,
		Files: rp.Files, Bytes: rp.Bytes, Downtime: rp.Downtime, Warnings: rp.Warnings, Transfer: rp.Transfer}
	res.State, res.Cleanup, res.OpID, res.VaultID = "running", "not_needed", rp.Plan.OpID, rp.vault.Desc.VaultID
	if selected != nil {
		res.Depth = selected.Depth
		location := selected.Source.Copy.LocationRef
		res.SourceLocation, res.HeaderDigest = &location, selected.Source.Copy.HeaderDigest
	}
	if slices.Contains(caps, identity.Capability) {
		var binding identity.Binding
		if err := s.call(ctx, host, identity.MethodGet, identity.GetParams{InstanceID: instance}, &binding); err != nil {
			return nil, err
		}
		if binding.Agent != nil {
			ref := binding.Agent.Ref
			res.AgentRef = &ref
		}
	}
	res.TrustDigest, err = s.restoreTrust(host)
	if err != nil {
		return nil, err
	}
	if err := s.recordRestoreTest(host, instance, res); err != nil {
		return nil, err
	}
	s.LogStage("restore_test", "plan", host, res.State, "")
	fail := func(step string, err error) (*RestoreTest, error) {
		res.State = "failed"
		if ctx.Err() == context.Canceled {
			res.State = "cancelled"
		}
		if res.Cleanup == "pending" {
			res.State = "needs_cleanup"
		}
		res.Step, res.Problem = step, errText1(err)
		s.LogStage("restore_test", step, host, res.State, safeDebugErrCode(err))
		return res, s.recordRestoreTest(host, instance, res)
	}
	if len(rp.Blockers) > 0 {
		return fail(TestServer, &rp.Blockers[0])
	}
	s.LogStage("restore_test", TestLocal, host, "running", "")
	if err := s.unpackLocal(ctx, rp); err != nil {
		if e, ok := valise.As(err); ok && e.Code == "restore_test_local_cleanup" {
			res.Cleanup = "pending"
		}
		return fail(TestLocal, err)
	}
	res.LocalVerified = true
	res.Cleanup = "pending"
	s.LogStage("restore_test", TestLocal, host, "passed", "")
	if err := s.recordRestoreTest(host, instance, res); err != nil {
		return res, err
	}
	if err := s.unpackServer(ctx, rp, res); err != nil {
		return fail(TestServer, err)
	}
	res.OK, res.State = true, "passed"
	s.LogStage("restore_test", "finish", host, "passed", "")
	return res, s.recordRestoreTest(host, instance, res)
}

// unpackLocal writes every file into a private temporary folder, checks
// its SHA-256, and removes the folder.
func (s *Service) unpackLocal(ctx context.Context, rp *RevivePlan) (err error) {
	tmp, err := s.localRestoreTestDir(rp.Plan.OpID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(tmp), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Dir(tmp)); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return valise.New("restore_test_local_cleanup", "本机测试暂存的位置不是私有目录", "检查缓存目录后再次测试")
	}
	if err := os.Mkdir(tmp, 0700); err != nil {
		return err
	}
	defer func() {
		if cleanup := s.cleanLocalRestoreTest(rp.Plan.OpID); cleanup != nil {
			err = cleanup
		}
	}()
	var done int64
	report(ctx, TestLocal, 0, rp.Bytes)
	for i, f := range rp.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := filepath.Join(tmp, fmt.Sprintf("%06d", i))
		out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		h := sha256.New()
		err = rp.writeRestoreFile(f, io.MultiWriter(out, h))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return valise.New("unpack_failed", "解不开 "+f.Root+":"+f.Path, "换一个更早的备份再测试").WithDetail(err.Error())
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
			return valise.New("unpack_mismatch", f.Root+":"+f.Path+" 解开后与记录不一致", "换一个更早的备份再测试")
		}
		done += f.Size
		report(ctx, TestLocal, done, rp.Bytes)
	}
	return nil
}

// unpackServer stages the snapshot on the server, has porter check every
// file (and SQLite database), then discards the stage: the operation never
// reaches the swap.
func (s *Service) unpackServer(ctx context.Context, rp *RevivePlan, result *RestoreTest) error {
	var begun struct {
		Grant string `json:"grant"`
	}
	report(ctx, TestServer, 0, rp.Bytes)
	s.LogStage("restore_test", TestServer, rp.Target, "running", "")
	if err := s.callOwner(ctx, rp.target, nil, revive.MethodStageBegin, revive.StageBeginParams{Plan: rp.Plan}, &begun); err != nil {
		_ = s.discardRestoreTest(context.WithoutCancel(ctx), rp.Target, rp.Plan.InstanceID, result)
		return err
	}
	err := s.streamStage(ctx, rp, begun.Grant)
	if err == nil {
		var man []revive.ManifestEntry
		for _, f := range rp.files {
			man = append(man, revive.ManifestEntry{Path: tarName(rp.Plan, f.Root, f.Path), Size: f.Size, SHA256: f.SHA256,
				Category: f.Category, SQLite: rp.sqlite[f.Root+":"+f.Path]})
		}
		report(ctx, StageVerify, 0, 0)
		s.LogStage("restore_test", StageVerify, rp.Target, "running", "")
		var ver revive.StageVerifyResult
		if err = s.callOwner(ctx, rp.target, nil, revive.MethodStageVerify, revive.StageVerifyParams{OpID: rp.Plan.OpID, Manifest: man}, &ver); err == nil && len(ver.Problems) > 0 {
			err = valise.New("stage_verify_failed", fmt.Sprintf("服务器上解开后有 %d 个问题", len(ver.Problems)), "换一个更早的备份再测试").
				WithDetail(ver.Problems[0])
		}
		if err == nil {
			result.ServerVerified = true
		}
	}
	report(ctx, "test_cleanup", 0, 0)
	s.LogStage("restore_test", "test_cleanup", rp.Target, "running", "")
	if derr := s.discardRestoreTest(context.WithoutCancel(ctx), rp.Target, rp.Plan.InstanceID, result); derr != nil {
		err = valise.New("discard_failed", "测试暂存尚未完成清理", "检查连接后点“核实并清理”，不要重新发起测试")
	}
	return err
}
