package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"

	"valise/client/keystore"
	"valise/client/ssh"
	core "valise/core/recovery"
	"valise/core/valise"
)

// Finding the Valise passphrase again (docs/ARCHITECTURE.md 3.2).

// PorterCheck is the early scan of a server's porter: only a reminder,
// since a replaced porter could lie; the sudo command checks for real.
type PorterCheck struct {
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Expected string `json:"expected,omitempty"` // "" when this computer has no record
	Match    bool   `json:"match"`
}

// RecoveryMode reports whether recovery mode is on.
func (s *Service) RecoveryMode() bool { return s.recovering.Load() }

// SetRecoveryMode turns recovery mode on or off: background work such as
// automatic copies waits while it is on.
func (s *Service) SetRecoveryMode(on bool) { s.recovering.Store(on) }

// CheckPorter reads porter's version and SHA-256 over the user pass and
// compares it with what this computer recorded when it deployed porter.
func (s *Service) CheckPorter(ctx context.Context, host string) (*PorterCheck, error) {
	t, _, err := s.transport(host)
	if err != nil {
		return nil, err
	}
	f, err := s.porterOn(ctx, t, s.Tiered(host))
	if err != nil {
		return nil, err
	}
	c := &PorterCheck{Version: f.Info.Version, SHA256: f.Info.SelfSHA256}
	if d, ok := s.LastDeploy(host); ok {
		c.Expected = d.SHA256
		c.Match = d.SHA256 == c.SHA256
	}
	return c, nil
}

// RecoveryRun is one request to find the passphrase again. The one-time
// identity lives only in memory.
type RecoveryRun struct {
	Host    string
	ID      string
	Command string // the sudo command shown in full
	Tries   int
	e       *age.X25519Identity
	r       *age.X25519Identity
}

// StartRecovery places a request on host and returns the command the user
// runs there with sudo. The porter must match this computer's record.
func (s *Service) StartRecovery(ctx context.Context, host string) (*RecoveryRun, error) {
	lock, err := os.ReadFile(s.lockFile(host))
	if err != nil {
		return nil, valise.New("recovery_no_lock", "这台服务器没有备用锁", "换一台“已做”备用锁的服务器")
	}
	d, ok := s.LastDeploy(host)
	if !ok {
		return nil, valise.New("recovery_no_record", "这台电脑没有记下这台服务器 porter 的 SHA-256", "先在服务器设置里更新一次 porter，再回来找回")
	}
	e, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	run := &RecoveryRun{Host: host, ID: hex.EncodeToString(b), e: e}
	var res core.RequestResult
	err = s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, core.MethodRequest, core.RequestParams{ID: run.ID, Lock: lock, Recipient: e.Recipient().String()}, &res)
	})
	if err != nil {
		return nil, err
	}
	run.Command = recoveryCommand(res.Porter, d.SHA256, res.User)
	return run, nil
}

// recoveryCommand copies porter to a root-only file, checks its SHA-256
// and runs only that copy (docs/ARCHITECTURE.md 3.2 step 4).
func recoveryCommand(porterPath, sha, user string) string {
	return "sudo sh -c 'f=$(mktemp) && cat " + ssh.Quote(porterPath) + " > \"$f\" && echo \"" + sha + "  $f\" | sha256sum -c --quiet && chmod 700 \"$f\" && \"$f\" recovery-code --for " + user + "; r=$?; rm \"$f\"; exit $r'"
}

// VerifyRecovery fetches the answer and checks the code the user typed.
// After MaxTries wrong codes the request is void.
func (s *Service) VerifyRecovery(ctx context.Context, run *RecoveryRun, code string) error {
	code = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
	var res core.ResultResult
	err := s.withSession(ctx, run.Host, func(c caller) error {
		return c.Call(ctx, core.MethodResult, core.IDParams{ID: run.ID}, &res)
	})
	if err != nil {
		return err
	}
	if core.Code(run.ID, res.Ciphertext) != code {
		run.Tries++
		if run.Tries >= core.MaxTries {
			s.CancelRecovery(ctx, run)
			return valise.New("recovery_void", "验证码错了 3 次，这次找回作废", "重新开始找回")
		}
		return valise.New("recovery_code_wrong", "验证码不对", "核对服务器上命令最后显示的 8 位数字")
	}
	rtext, err := openWith(run.e, res.Ciphertext)
	if err != nil {
		return valise.New("recovery_answer_bad", "服务器上的找回结果打不开", "重新开始找回").WithDetail(err.Error())
	}
	if run.r, err = age.ParseX25519Identity(strings.TrimSpace(rtext)); err != nil {
		return valise.New("recovery_answer_bad", "服务器上的找回结果打不开", "重新开始找回")
	}
	return nil
}

// RecoveryDone is the result of setting the new passphrase.
type RecoveryDone struct {
	Locks []ServerLock `json:"locks"`
}

// FinishRecovery opens the key with R, seals it under the new passphrase,
// makes a new backup lock and clears the request on the server.
func (s *Service) FinishRecovery(ctx context.Context, run *RecoveryRun, newPassphrase string) (*RecoveryDone, error) {
	if run.r == nil {
		return nil, valise.New("recovery_unverified", "还没有核对验证码", "先输入验证码")
	}
	if err := keystore.CheckPassphrase(newPassphrase); err != nil {
		return nil, keyErr(err)
	}
	data, err := os.ReadFile(filepath.Join(s.recoveryDir(), "key.age"))
	if err != nil {
		return nil, err
	}
	idText, err := openWith(run.r, data)
	if err != nil {
		return nil, valise.New("recovery_key_bad", "备用锁打不开这台电脑上的钥匙", "用恢复套件导入钥匙").WithDetail(err.Error())
	}
	k, err := keystore.Import([]byte(idText))
	if err != nil {
		return nil, keyErr(err)
	}
	defer k.Lock()
	info, err := s.KeyShow()
	if err != nil {
		return nil, err
	}
	if k.Recipient() != info.Recipient {
		return nil, valise.New("recovery_key_bad", "备用锁里的钥匙与这台电脑上的不一致", "用恢复套件导入钥匙")
	}
	sealed, err := keystore.Seal(k, newPassphrase)
	if err != nil {
		return nil, keyErr(err)
	}
	if err := keystore.SaveFile(s.keystorePath(), sealed); err != nil {
		return nil, err
	}
	locks, err := s.makeLock(k, newPassphrase)
	if err != nil {
		return nil, err
	}
	s.CancelRecovery(ctx, run)
	return &RecoveryDone{Locks: locks}, nil
}

// CancelRecovery removes the request and its answer from the server.
func (s *Service) CancelRecovery(ctx context.Context, run *RecoveryRun) {
	if run == nil {
		return
	}
	_ = s.withSession(ctx, run.Host, func(c caller) error {
		return c.Call(ctx, core.MethodClear, core.IDParams{ID: run.ID}, nil)
	})
}
