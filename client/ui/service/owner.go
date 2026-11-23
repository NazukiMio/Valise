package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"filippo.io/age"

	"valise/client/keystore"
	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/valise"
)

// The owner proof (docs/ARCHITECTURE.md 2.2): owner methods run on the
// daily key once the session has opened porter's age-encrypted nonce with
// the identity in keystore.age. The identity stays in memory for the admin
// window setting and goes when Valise exits.

// OwnerAuth is set by the frontend to ask for the Valise passphrase.
type OwnerAuth struct {
	Passphrase func(host string) (string, error)
	PIN        func(host string) (string, error)
}

type ownerCache struct {
	mu      sync.Mutex
	epoch   uint64
	closed  bool
	opDepth int
	id      age.Identity
	pass    []byte
	until   time.Time
	timer   *time.Timer
}

const pinOperationCeiling = 10 * time.Minute

func (o *ownerCache) clear() {
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	clear(o.pass)
	o.pass, o.id, o.until = nil, nil, time.Time{}
}

// BeginOperation opens an operation-scoped credential lease so multi-step
// actions under 6-digit PIN mode prompt for the PIN at most once and clear
// the temporary unlock immediately when the outermost operation finishes.
func (s *Service) BeginOperation() func() {
	s.owner.mu.Lock()
	s.owner.opDepth++
	s.owner.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.owner.mu.Lock()
			if s.owner.opDepth > 0 {
				s.owner.opDepth--
			}
			if s.owner.opDepth == 0 && s.PINEnabled() {
				s.owner.clear()
			}
			s.owner.mu.Unlock()
		})
	}
}

// rememberUnlock holds the lock until linked keys are loaded so LockAdmin
// cannot clear the agent halfway through a linked unlock.
func (s *Service) rememberUnlock(k *keystore.UnlockedKey, passphrase string, epoch uint64) {
	id, err := k.Identity()
	if err != nil {
		return
	}
	pin := s.PINEnabled()
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if s.owner.closed || s.owner.epoch != epoch || (pin && s.owner.opDepth <= 0) {
		return
	}
	if pin {
		s.cacheOwnerWindow(id, passphrase, pinOperationCeiling)
		return
	}
	s.cacheOwner(id, passphrase)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, host := range s.AdminPassServers() {
		if ctx.Err() != nil {
			break
		}
		if s.HasAdminKey(host) && !s.AdminUnlocked(ctx, host) {
			_ = s.addToAgent(ctx, host, passphrase, time.Until(s.owner.until), false)
		}
	}
}

func (s *Service) cacheOwner(id age.Identity, passphrase string) {
	s.cacheOwnerWindow(id, passphrase, s.AdminWindow())
}

func (s *Service) cacheOwnerWindow(id age.Identity, passphrase string, window time.Duration) {
	o := &s.owner
	o.clear()
	until := time.Now().Add(window)
	o.id, o.pass, o.until = id, []byte(passphrase), until
	o.timer = time.AfterFunc(window, func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.until == until {
			o.clear()
		}
	})
}

// ForgetOwner zeros the cached passphrase and drops the unlocked identity.
func (s *Service) ForgetOwner() {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	s.owner.epoch++
	s.owner.clear()
}

func (s *Service) activeOwner() bool {
	if s.owner.id == nil {
		return false
	}
	if !time.Now().Before(s.owner.until) {
		s.owner.clear()
		return false
	}
	if s.PINEnabled() && s.owner.opDepth <= 0 {
		s.owner.clear()
		return false
	}
	return true
}

// OwnerRemaining is how long the kept identity has left.
func (s *Service) OwnerRemaining() (time.Duration, bool) {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if s.PINEnabled() || !s.activeOwner() {
		return 0, false
	}
	return time.Until(s.owner.until), true
}

func (s *Service) hasCachedPassphrase() bool {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	return s.activeOwner() && len(s.owner.pass) != 0
}

func (s *Service) cachedPassphraseExpiry(passphrase string) (time.Time, bool) {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	match := s.activeOwner() && len(s.owner.pass) != 0 && string(s.owner.pass) == passphrase
	return s.owner.until, match
}

func (s *Service) promptValise(host string) (string, error) {
	if s.PINEnabled() {
		if s.OwnerAuth.PIN == nil {
			return "", valise.New("pin_locked", "这一步需要本机 PIN 验证", "在 Valise 界面里完成验证")
		}
		pin, err := s.OwnerAuth.PIN(host)
		if err != nil {
			return "", err
		}
		return s.VerifyPIN(pin)
	}
	if s.OwnerAuth.Passphrase == nil {
		return "", valise.New("owner_locked", "这一步需要确认是你本人（"+host+"），但这里没法询问 Valise 口令", "在终端界面或命令行里执行这一步")
	}
	return s.OwnerAuth.Passphrase(host)
}

// ValisePassphrase supplies the verified passphrase to a trusted frontend
// creating an administrator key. In PIN mode it is only used for this call.
func (s *Service) ValisePassphrase(host string) (string, error) {
	s.owner.mu.Lock()
	if s.activeOwner() && len(s.owner.pass) != 0 {
		pass := string(s.owner.pass)
		s.owner.mu.Unlock()
		return pass, nil
	}
	s.owner.mu.Unlock()
	pass, err := s.promptValise(host)
	if err != nil {
		return "", err
	}
	k, err := s.Unlock(pass)
	if err != nil {
		return "", err
	}
	k.Lock()
	return pass, nil
}

// verifyOwnerFresh prompts once for the 6-digit PIN (when enabled) or Valise
// passphrase and unlocks the keystore into any active operation lease.
func (s *Service) verifyOwnerFresh(host string) error {
	pass, err := s.promptValise(host)
	if err != nil {
		return err
	}
	k, err := s.Unlock(pass)
	if err != nil {
		return err
	}
	k.Lock()
	return nil
}

func (s *Service) ownerIdentity(host string) (age.Identity, error) {
	s.owner.mu.Lock()
	if s.activeOwner() {
		id := s.owner.id
		s.owner.mu.Unlock()
		return id, nil
	}
	s.owner.mu.Unlock()
	pass, err := s.promptValise(host)
	if err != nil {
		return nil, err
	}
	k, err := s.Unlock(pass)
	if err != nil {
		return nil, err
	}
	defer k.Lock()
	return k.Identity()
}

// proveOwner runs the owner proof on a daily porter session.
func (s *Service) proveOwner(ctx context.Context, c *ssh.RPCClient, host string) error {
	var ch rpc.OwnerChallengeResult
	start := time.Now()
	err := c.Call(ctx, rpc.MethodOwnerChallenge, struct{}{}, &ch)
	s.logRPC(rpc.MethodOwnerChallenge, host, time.Since(start).Milliseconds(), err)
	if err != nil {
		var re *rpc.Error
		if errors.As(err, &re) && re.Code == rpc.CodeMethodNotFound {
			return valise.New("porter_outdated", host+" 上的 porter 版本较旧，还不支持用 Valise 口令确认本人",
				"在服务器设置里按 u 更新 porter（需要开发者模式）")
		}
		return err
	}
	id, err := s.ownerIdentity(host)
	if err != nil {
		return err
	}
	r, err := age.Decrypt(bytes.NewReader(ch.Ciphertext), id)
	if err != nil {
		return valise.New("owner_key_mismatch", "你的加密钥匙解不开 "+host+" 的确认问题：这台服务器上登记的不是你的钥匙",
			"在服务器设置里按 k 登记你的钥匙").WithDetail(err.Error())
	}
	nonce, err := io.ReadAll(io.LimitReader(r, 64))
	if err != nil {
		return err
	}
	startProve := time.Now()
	err = c.Call(ctx, rpc.MethodOwnerProve, rpc.OwnerProveParams{Nonce: nonce}, nil)
	s.logRPC(rpc.MethodOwnerProve, host, time.Since(startProve).Milliseconds(), err)
	return err
}

// ownerPorter is where owner operations run on host: the daily key and its
// fixed porter on servers with tiers (each session proves the owner), the
// user's own login elsewhere.
type ownerPorter struct {
	host   string
	t      *ssh.Transport
	path   string
	tiered bool
	found  *porter.Found
}

func (s *Service) ownerPorter(ctx context.Context, host string) (*ownerPorter, error) {
	t, _, err := s.transport(host)
	if err != nil {
		return nil, err
	}
	tiered := s.Tiered(host)
	found, err := s.porterOn(ctx, t, tiered)
	if err != nil {
		return nil, err
	}
	return &ownerPorter{host: host, t: t, path: found.Path, tiered: tiered, found: found}, nil
}

// call runs one method in its own porter session, proving the owner first
// on the daily key.
func (s *Service) callOwner(ctx context.Context, o *ownerPorter, env []string, method string, params, result any) error {
	c, hello, err := ssh.DialEnv(ctx, o.t, o.path, s.Version, env)
	if err != nil {
		return err
	}
	defer c.Close()
	if method == revive.MethodStageBegin {
		if err := n2Compatibility(hello, method, params); err != nil {
			return err
		}
	}
	if o.tiered {
		if err := s.proveOwner(ctx, c, o.host); err != nil {
			return err
		}
	}
	start := time.Now()
	err = c.Call(ctx, method, params, result)
	s.logRPC(method, o.host, time.Since(start).Milliseconds(), err)
	return err
}
