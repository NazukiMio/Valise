package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"filippo.io/age"

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
}

type ownerCache struct {
	mu    sync.Mutex
	id    age.Identity
	until time.Time
}

// rememberOwner keeps an unlocked identity for the window.
func (s *Service) rememberOwner(id age.Identity) {
	s.owner.mu.Lock()
	s.owner.id, s.owner.until = id, time.Now().Add(s.AdminWindow())
	s.owner.mu.Unlock()
}

// ForgetOwner drops the kept identity (lock now).
func (s *Service) ForgetOwner() {
	s.owner.mu.Lock()
	s.owner.id = nil
	s.owner.mu.Unlock()
}

// OwnerRemaining is how long the kept identity has left; false when none.
func (s *Service) OwnerRemaining() (time.Duration, bool) {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if s.owner.id == nil || time.Now().After(s.owner.until) {
		return 0, false
	}
	return time.Until(s.owner.until), true
}

// ownerIdentity returns the kept identity, asking for the Valise
// passphrase when there is none.
func (s *Service) ownerIdentity(host string) (age.Identity, error) {
	s.owner.mu.Lock()
	id := s.owner.id
	if id != nil && time.Now().After(s.owner.until) {
		id, s.owner.id = nil, nil
	}
	s.owner.mu.Unlock()
	if id != nil {
		return id, nil
	}
	if s.OwnerAuth.Passphrase == nil {
		return nil, valise.New("owner_locked", "这一步需要确认是你本人（"+host+"），但这里没法询问 Valise 口令", "在终端界面或命令行里执行这一步")
	}
	pass, err := s.OwnerAuth.Passphrase(host)
	if err != nil {
		return nil, err
	}
	k, err := s.Unlock(pass)
	if err != nil {
		return nil, err
	}
	defer k.Lock()
	xid, err := k.Identity()
	if err != nil {
		return nil, err
	}
	s.rememberOwner(xid)
	return xid, nil
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
	return &ownerPorter{host: host, t: t, path: found.Path, tiered: tiered}, nil
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
