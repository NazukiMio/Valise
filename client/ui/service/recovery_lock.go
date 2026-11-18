package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"

	"valise/client/config"
	"valise/client/keystore"
)

// The backup lock (docs/ARCHITECTURE.md 3.2): a second age identity R
// that can open the key. R is sealed with the Valise passphrase (r.age)
// and to each server's ssh-ed25519 host key (<server>.age); the key's own
// identity is sealed to R (key.age). All of it stays on this computer.

func (s *Service) recoveryDir() string { return filepath.Join(s.Dirs.Data, "recovery") }

// Backup lock states of one server.
const (
	LockDone        = "done"        // sealed to the host key we know now
	LockChanged     = "changed"     // the host key changed since
	LockPending     = "pending"     // made next time the passphrase is typed
	LockUnsupported = "unsupported" // no ssh-ed25519 host key known
)

// ServerLock is one server's backup lock.
type ServerLock struct {
	Server string     `json:"server"`
	State  string     `json:"state"`
	At     *time.Time `json:"at,omitempty"`
}

type lockRecord struct {
	HostKey string    `json:"host_key"`
	At      time.Time `json:"at"`
}

func (s *Service) lockState() map[string]lockRecord {
	m := map[string]lockRecord{}
	if b, err := os.ReadFile(filepath.Join(s.recoveryDir(), "state.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *Service) saveLockState(m map[string]lockRecord) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return writeAtomicFile(filepath.Join(s.recoveryDir(), "state.json"), b)
}

// RecoveryEnabled reports whether the backup lock is on.
func (s *Service) RecoveryEnabled() bool {
	return fileExists(filepath.Join(s.recoveryDir(), "r.age"))
}

// hostKey returns host's ssh-ed25519 host key line (“ssh-ed25519 AAAA…”)
// from Valise's known_hosts, or for a server from ~/.ssh/config from the
// user's known_hosts; "" when none is known.
func (s *Service) hostKey(host string) string {
	srv := s.server(host)
	field, file := "", ""
	if srv.Valise() {
		field, file = knownHostsField(srv.HostName, srv.Port), filepath.Join(s.Dirs.Data, config.KnownHostsFile)
	} else {
		c, err := s.SSHConfig()
		if err != nil {
			return ""
		}
		hc := c.Lookup(host)
		name := hc.HostName
		if name == "" {
			name = host
		}
		field, file = knownHostsField(name, hc.Port), filepath.Join(s.Home, ".ssh", "known_hosts")
	}
	out, err := exec.Command(s.toolPath("ssh-keygen"), "-F", field, "-f", file).Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && !strings.HasPrefix(f[0], "#") && f[1] == "ssh-ed25519" {
			return f[1] + " " + f[2]
		}
	}
	return ""
}

// ServerLocks lists every recorded server's backup lock.
func (s *Service) ServerLocks() []ServerLock {
	list, _ := s.ServerList()
	st := s.lockState()
	var out []ServerLock
	for _, srv := range list {
		l := ServerLock{Server: srv.Name, State: LockPending}
		key := s.hostKey(srv.Name)
		rec, ok := st[srv.Name]
		switch {
		case key == "":
			l.State = LockUnsupported
		case ok && rec.HostKey == key && fileExists(s.lockFile(srv.Name)):
			l.State, l.At = LockDone, &rec.At
		case ok:
			l.State = LockChanged
		}
		out = append(out, l)
	}
	return out
}

func (s *Service) lockFile(server string) string {
	return filepath.Join(s.recoveryDir(), server+".age")
}

func sealTo(r age.Recipient, plain string) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func openWith(id age.Identity, data []byte) (string, error) {
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(r, 1<<16))
	return string(b), err
}

// EnableRecovery turns the backup lock on (or makes it anew): a new R,
// the key sealed to it, R sealed with the passphrase and to every server
// with an ssh-ed25519 host key.
func (s *Service) EnableRecovery(passphrase string) ([]ServerLock, error) {
	k, err := s.Unlock(passphrase)
	if err != nil {
		return nil, err
	}
	defer k.Lock()
	return s.makeLock(k, passphrase)
}

func (s *Service) makeLock(k *keystore.UnlockedKey, passphrase string) ([]ServerLock, error) {
	r, err := keystore.Generate()
	if err != nil {
		return nil, err
	}
	defer r.Lock()
	rid, _ := r.Identity()
	kid, err := k.Identity()
	if err != nil {
		return nil, err
	}
	keyAge, err := sealTo(rid.Recipient(), kid.String())
	if err != nil {
		return nil, err
	}
	rAge, err := keystore.Seal(r, passphrase)
	if err != nil {
		return nil, keyErr(err)
	}
	if err := os.MkdirAll(s.recoveryDir(), 0o700); err != nil {
		return nil, err
	}
	for _, f := range []struct {
		name string
		b    []byte
	}{{"key.age", keyAge}, {"r.age", rAge}} {
		if err := keystore.SaveFile(filepath.Join(s.recoveryDir(), f.name), f.b); err != nil {
			return nil, err
		}
	}
	_ = s.saveLockState(map[string]lockRecord{})
	s.wrapLocks(rid, true)
	return s.ServerLocks(), nil
}

// wrapLocks seals R to the servers that need it (all of them when all).
func (s *Service) wrapLocks(rid *age.X25519Identity, all bool) {
	st := s.lockState()
	for _, l := range s.ServerLocks() {
		if l.State == LockUnsupported || (!all && l.State == LockDone) {
			continue
		}
		key := s.hostKey(l.Server)
		rcpt, err := agessh.ParseRecipient(key)
		if err != nil {
			continue
		}
		b, err := sealTo(rcpt, rid.String())
		if err != nil || keystore.SaveFile(s.lockFile(l.Server), b) != nil {
			continue
		}
		st[l.Server] = lockRecord{HostKey: key, At: time.Now().UTC()}
	}
	_ = s.saveLockState(st)
}

// refreshLocks makes the missing or outdated server locks once the
// passphrase was typed correctly; failures wait for the next time.
func (s *Service) refreshLocks(passphrase string) {
	if !s.RecoveryEnabled() || s.locking.Swap(true) {
		return
	}
	defer s.locking.Store(false)
	need := false
	for _, l := range s.ServerLocks() {
		need = need || l.State == LockPending || l.State == LockChanged
	}
	if !need {
		return
	}
	data, err := os.ReadFile(filepath.Join(s.recoveryDir(), "r.age"))
	if err != nil {
		return
	}
	r, err := keystore.Unlock(data, passphrase)
	if err != nil {
		return
	}
	defer r.Lock()
	rid, err := r.Identity()
	if err == nil {
		s.wrapLocks(rid, false)
	}
}

// DisableRecovery turns the backup lock off and deletes its files.
func (s *Service) DisableRecovery() error {
	err := os.RemoveAll(s.recoveryDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
