package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"filippo.io/age"

	"valise/client/keystore"
	"valise/core/valise"
)

const pinAttempts = 5

// The PIN protects a local age identity. The matching public recipient is
// sealed to the full Valise passphrase so rotation can authenticate it
// before rewrapping the new passphrase without asking for the PIN.
type pinState struct {
	Recipient []byte `json:"recipient_age"`
	Password  []byte `json:"passphrase_age"`
	Failures  int    `json:"failures"`
}

func (s *Service) pinPath() string      { return filepath.Join(s.Dirs.Data, "pin-unlock.age") }
func (s *Service) pinStatePath() string { return filepath.Join(s.Dirs.Data, "pin-state.json") }

// PINEnabled reports whether this computer has a local PIN wrapper.
func (s *Service) PINEnabled() bool { return fileExists(s.pinPath()) }

// ValidPIN accepts exactly six ASCII digits.
func ValidPIN(pin string) bool {
	if len(pin) != 6 {
		return false
	}
	for _, b := range []byte(pin) {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

var pinMu sync.Mutex

func (s *Service) lockPIN() (func(), error) {
	pinMu.Lock()
	path := s.pinPath() + ".lock"
	if err := os.MkdirAll(s.Dirs.Data, 0o700); err != nil {
		pinMu.Unlock()
		return nil, err
	}
	unlock, held, err := tryFileLock(path)
	if err != nil || held {
		pinMu.Unlock()
		if held {
			err = valise.New("pin_busy", "另一处 Valise 正在更改本机快捷解锁", "等那一步完成后重试")
		}
		return nil, err
	}
	return func() { unlock(); pinMu.Unlock() }, nil
}

// age/scrypt supplies its own random salt for each encryption.
func sealPINMaterial(secret string, recipient age.Recipient) ([]byte, error) {
	var b bytes.Buffer
	w, err := age.Encrypt(&b, recipient)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, secret); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func openPINMaterial(data []byte, id age.Identity) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func (p *pinState) reseal(passphrase string, rec *age.X25519Recipient) ([]byte, error) {
	binding, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	p.Recipient, err = sealPINMaterial(rec.String(), binding)
	if err != nil {
		return nil, err
	}
	p.Password, err = sealPINMaterial(passphrase, rec)
	if err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

func (p *pinState) rewrap(old, next string) ([]byte, error) {
	id, err := age.NewScryptIdentity(old)
	if err != nil {
		return nil, err
	}
	recipient, err := openPINMaterial(p.Recipient, id)
	if err != nil {
		return nil, errPINUnreadable()
	}
	rec, err := age.ParseX25519Recipient(string(recipient))
	if err != nil {
		return nil, errPINUnreadable()
	}
	return p.reseal(next, rec)
}

func errPINUnreadable() error {
	return valise.New("pin_unreadable", "本机 PIN 快捷解锁无法读取", "在设置里关闭快捷解锁，再用完整 Valise 口令开启")
}

func (s *Service) readPIN() (*pinState, error) {
	b, err := os.ReadFile(s.pinStatePath())
	if err != nil {
		return nil, err
	}
	var p pinState
	if json.Unmarshal(b, &p) != nil || len(p.Recipient) == 0 || len(p.Password) == 0 || p.Failures < 0 {
		return nil, errPINUnreadable()
	}
	return &p, nil
}

// EnablePIN verifies the full Valise passphrase and sets or replaces the PIN.
func (s *Service) EnablePIN(valisePass, pin string) error {
	if !ValidPIN(pin) {
		return valise.New("pin_format", "请输入 6 位数字 PIN 码", "只用 0 到 9")
	}
	unlock, err := s.lockPIN()
	if err != nil {
		return err
	}
	defer unlock()
	k, err := s.unlockStored(valisePass)
	if err != nil {
		return err
	}
	k.Lock()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return err
	}
	rec, err := age.NewScryptRecipient(pin)
	if err != nil {
		return err
	}
	wrapped, err := sealPINMaterial(id.String(), rec)
	if err != nil {
		return err
	}
	p := pinState{}
	b, err := p.reseal(valisePass, id.Recipient())
	if err != nil {
		return err
	}
	oldState, err := os.ReadFile(s.pinStatePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := keystore.SaveFile(s.pinStatePath(), b); err != nil {
		return err
	}
	if err := keystore.SaveFile(s.pinPath(), wrapped); err != nil {
		if oldState == nil {
			return errors.Join(err, removePINFile(s.pinStatePath()))
		}
		return errors.Join(err, keystore.SaveFile(s.pinStatePath(), oldState))
	}
	s.LockAdmin()
	return nil
}

func removePINFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Service) removePIN() error {
	if err := removePINFile(s.pinPath()); err != nil {
		return err
	}
	return removePINFile(s.pinStatePath())
}

// DisablePIN removes the local PIN wrapper and locks the client.
func (s *Service) DisablePIN() error {
	unlock, err := s.lockPIN()
	if err != nil {
		return err
	}
	defer unlock()
	s.LockAdmin()
	return s.removePIN()
}

func (s *Service) destroyPIN() error {
	s.LockAdmin()
	if err := s.removePIN(); err != nil {
		return errPINUnreadable()
	}
	return valise.New("pin_destroyed", "已连续输错 5 次，本机 PIN 快捷解锁已自动关闭", "请使用完整 Valise 口令")
}

// VerifyPIN reads the full passphrase for the current operation. It reserves
// the attempt on disk before trying to decrypt so a restart cannot refund it.
func (s *Service) VerifyPIN(pin string) (string, error) {
	unlock, err := s.lockPIN()
	if err != nil {
		return "", err
	}
	defer unlock()
	wrapped, err := os.ReadFile(s.pinPath())
	if err != nil {
		return "", err
	}
	p, err := s.readPIN()
	if err != nil {
		return "", errPINUnreadable()
	}
	if p.Failures >= pinAttempts {
		return "", s.destroyPIN()
	}
	p.Failures++
	if err := s.savePIN(p); err != nil {
		return "", err
	}
	var secret []byte
	if ValidPIN(pin) {
		id, err := age.NewScryptIdentity(pin)
		if err != nil {
			return "", err
		}
		secret, err = openPINMaterial(wrapped, id)
		var noMatch *age.NoIdentityMatchError
		if err != nil && !errors.As(err, &noMatch) {
			return "", errPINUnreadable()
		}
	}
	if len(secret) == 0 {
		if p.Failures == pinAttempts {
			return "", s.destroyPIN()
		}
		return "", valise.New("pin_wrong", "PIN 码不对", fmt.Sprintf("还剩 %d 次机会", pinAttempts-p.Failures))
	}
	defer clear(secret)
	id, err := age.ParseX25519Identity(string(secret))
	if err != nil {
		return "", errPINUnreadable()
	}
	pass, err := openPINMaterial(p.Password, id)
	if err != nil {
		return "", errPINUnreadable()
	}
	defer clear(pass)
	p.Failures = 0
	if err := s.savePIN(p); err != nil {
		return "", err
	}
	return string(pass), nil
}

func (s *Service) savePIN(p *pinState) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return keystore.SaveFile(s.pinStatePath(), b)
}
