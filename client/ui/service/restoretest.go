package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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
	At       time.Time `json:"at"`
	Snapshot string    `json:"snapshot"`
	Tier     string    `json:"tier"`
	Created  time.Time `json:"created"`
	Files    int       `json:"files"`
	Bytes    int64     `json:"bytes"`
	OK       bool      `json:"ok"`
	// Step names the step that failed, Problem says why.
	Step    string `json:"step,omitempty"`
	Problem string `json:"problem,omitempty"`
	// Downtime is what a real restore would add: the plan's estimate.
	Downtime string   `json:"downtime,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
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
			return nil, valise.New("no_snapshot", instance+" 还没有逃生舱，没有可以测试的", "先建立一个逃生舱")
		}
		snapshot = newest.SnapshotID
	}
	// Nothing is switched, so a profile whose restore is not verified yet
	// can still be tested.
	rp, err := s.planRevive(ctx, ReviveOptions{Target: host, SnapshotID: snapshot, Mode: revive.ModeRollback, AllowDraft: true}, "")
	if err != nil {
		return nil, err
	}
	res := &RestoreTest{At: time.Now().UTC(), Snapshot: snapshot, Tier: rp.Snapshot.Tier, Created: rp.Snapshot.CreatedAt,
		Files: rp.Files, Bytes: rp.Bytes, Downtime: rp.Downtime, Warnings: rp.Warnings}
	defer func() { s.recordRestoreTest(host, instance, res) }()
	fail := func(step string, err error) (*RestoreTest, error) {
		res.Step, res.Problem = step, errText1(err)
		return res, nil
	}
	if len(rp.Blockers) > 0 {
		return fail(TestServer, &rp.Blockers[0])
	}
	if err := s.unpackLocal(ctx, rp); err != nil {
		return fail(TestLocal, err)
	}
	if err := s.unpackServer(ctx, rp); err != nil {
		return fail(TestServer, err)
	}
	res.OK = true
	return res, nil
}

// unpackLocal writes every file into a private temporary folder, checks
// its SHA-256, and removes the folder.
func (s *Service) unpackLocal(ctx context.Context, rp *RevivePlan) error {
	tmp, err := s.privateTemp()
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
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
		err = rp.vault.WriteFile(f, io.MultiWriter(out, h), rp.ident)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return valise.New("unpack_failed", "解不开 "+f.Root+":"+f.Path, "换一个更早的逃生舱再测试").WithDetail(err.Error())
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
			return valise.New("unpack_mismatch", f.Root+":"+f.Path+" 解开后与记录不一致", "换一个更早的逃生舱再测试")
		}
		done += f.Size
		report(ctx, TestLocal, done, rp.Bytes)
	}
	return nil
}

// unpackServer stages the snapshot on the server, has porter check every
// file (and SQLite database), then discards the stage: the operation never
// reaches the swap.
func (s *Service) unpackServer(ctx context.Context, rp *RevivePlan) error {
	var begun struct {
		Grant string `json:"grant"`
	}
	if err := s.callOwner(ctx, rp.target, nil, revive.MethodStageBegin, revive.StageBeginParams{Plan: rp.Plan}, &begun); err != nil {
		return err
	}
	discard := func() error {
		var res revive.SwapResult
		return s.callOwner(context.WithoutCancel(ctx), rp.target, nil, revive.MethodResume, revive.ResumeParams{OpID: rp.Plan.OpID, Action: "discard"}, &res)
	}
	err := s.streamStage(ctx, rp, begun.Grant)
	if err == nil {
		var man []revive.ManifestEntry
		for _, f := range rp.files {
			man = append(man, revive.ManifestEntry{Path: tarName(rp.Plan, f.Root, f.Path), Size: f.Size, SHA256: f.SHA256,
				Category: f.Category, SQLite: rp.sqlite[f.Root+":"+f.Path]})
		}
		report(ctx, StageVerify, 0, 0)
		var ver revive.StageVerifyResult
		if err = s.callOwner(ctx, rp.target, nil, revive.MethodStageVerify, revive.StageVerifyParams{OpID: rp.Plan.OpID, Manifest: man}, &ver); err == nil && len(ver.Problems) > 0 {
			err = valise.New("stage_verify_failed", fmt.Sprintf("服务器上解开后有 %d 个问题", len(ver.Problems)), "换一个更早的逃生舱再测试").
				WithDetail(ver.Problems[0])
		}
	}
	if derr := discard(); err == nil && derr != nil {
		err = valise.New("discard_failed", "测试通过，但服务器上的临时目录没有删掉", "在空间清理里删除").WithDetail(errText1(derr))
	}
	return err
}

func (s *Service) loadRestoreTests() map[string]map[string]RestoreTest {
	out := map[string]map[string]RestoreTest{}
	if b, err := os.ReadFile(filepath.Join(s.Dirs.Data, restoreTestsFile)); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func (s *Service) recordRestoreTest(host, instance string, t *RestoreTest) {
	all := s.loadRestoreTests()
	if all[host] == nil {
		all[host] = map[string]RestoreTest{}
	}
	all[host][instance] = *t
	b, _ := json.MarshalIndent(all, "", "  ")
	_ = os.WriteFile(filepath.Join(s.Dirs.Data, restoreTestsFile), append(b, '\n'), 0o600)
}

// LastRestoreTest is the latest restore test of instance on host.
func (s *Service) LastRestoreTest(host, instance string) (RestoreTest, bool) {
	t, ok := s.loadRestoreTests()[host][instance]
	return t, ok
}
