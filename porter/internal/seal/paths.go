// Package seal implements sealing, vault status, verification and sync on
// the server (docs/ARCHITECTURE.md sections 6 and 7).
package seal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"valise/core/valise"
	"valise/porter/internal/actionjournal"
	porterenroll "valise/porter/internal/enroll"
)

// Paths are the porter's files under HOME (docs/ARCHITECTURE.md section 1).
type Paths struct{ Home string }

func (p Paths) Recipients() string {
	return filepath.Join(p.Home, ".config", "valise", "recipients.txt")
}
func (p Paths) Targets() string { return filepath.Join(p.Home, ".config", "valise", "targets.json") }
func (p Paths) State() string   { return filepath.Join(p.Home, ".local", "state", "valise") }
func (p Paths) Tmp() string     { return filepath.Join(p.State(), "tmp") }

// RcloneConfig is Valise's own rclone config file (docs/ARCHITECTURE.md 7.3).
func (p Paths) RcloneConfig() string {
	return filepath.Join(p.Home, ".config", "valise", "rclone", "rclone.conf")
}

// Rclone is the Kit-installed rclone.
func (p Paths) Rclone() string { return filepath.Join(p.Home, ".local", "bin", "rclone") }
func (p Paths) Audit() string  { return filepath.Join(p.State(), "audit.jsonl") }
func (p Paths) SyncState(target string) string {
	return filepath.Join(p.State(), "sync", target+".json")
}

// DedupeKey is ~/.config/valise/dedupe/<vault_id>.key.
func (p Paths) DedupeKey(vaultID string) string {
	return filepath.Join(p.Home, ".config", "valise", "dedupe", vaultID+".key")
}

// Lock takes the vault lock (flock) so only one seal or sync runs at a time.
func (p Paths) Lock() (unlock func(), err error) { return p.LockFor("") }

// holderNames describe a lock holder to the user.
var holderNames = map[string]string{"autosync": "正在后台自动同步到备份位置"}

// LockFor takes the vault lock and records what holds it, so a refused
// caller can say what is running.
func (p Paths) LockFor(holder string) (unlock func(), err error) {
	_, unlock, err = p.acquire(holder, "")
	return unlock, err
}

// ActionLock uses the same mutation lock; only the named pending action is exempt.
// A child command inherits the descriptor so recovery cannot race a surviving child.
func (p Paths) ActionLock(op string) (*os.File, func(), error) {
	return p.acquire("action:"+op, op)
}

func (p Paths) acquire(holder, except string) (*os.File, func(), error) {
	if err := os.MkdirAll(p.State(), 0o700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(filepath.Join(p.State(), "vault.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			reason := "服务器上正在进行另一次封存或同步"
			if b, rerr := os.ReadFile(f.Name()); rerr == nil && holderNames[strings.TrimSpace(string(b))] != "" {
				reason = "服务器上" + holderNames[strings.TrimSpace(string(b))]
			}
			f.Close()
			return nil, nil, valise.New("vault_busy", reason, "稍等片刻再试")
		}
		f.Close()
		return nil, nil, err
	}
	if err := (actionjournal.Store{Home: p.Home}).Check(except); err != nil {
		f.Close()
		return nil, nil, err
	}
	f.Truncate(0)
	f.WriteAt([]byte(holder), 0)
	return f, func() {
		f.Truncate(0)
		// Close releases this reference; an inherited child keeps its lock.
		f.Close()
	}, nil
}

// AuditEntry is one line of audit.jsonl. It never holds file contents or
// credentials (docs/ARCHITECTURE.md section 10).
type AuditEntry struct {
	Time        time.Time `json:"time"`
	Op          string    `json:"op"`
	Instance    string    `json:"instance,omitempty"`
	Initiator   string    `json:"initiator"`
	Result      string    `json:"result"`
	SnapshotID  string    `json:"snapshot_id,omitempty"`
	Bytes       int64     `json:"bytes,omitempty"`
	Target      string    `json:"target,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Tier        string    `json:"tier,omitempty"`
	OperationID string    `json:"operation_id,omitempty"`
}

// Audit appends one entry.
func (p Paths) AppendAudit(e AuditEntry) {
	if err := os.MkdirAll(p.State(), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(p.Audit(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	fmt.Fprintf(f, "%s\n", b)
}

// ResolveRclone prefers the Kit-installed rclone and falls back to the
// server's login PATH.
func (p Paths) ResolveRclone(e *porterenroll.Env) (string, error) {
	if st, err := os.Stat(p.Rclone()); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
		return p.Rclone(), nil
	}
	if e != nil {
		if path, err := e.LookPath("rclone"); err == nil {
			return path, nil
		}
	}
	return "", valise.New("rclone_missing", "服务器上没有找到 rclone", "在服务器设置或云盘登录页安装 rclone，或在服务器命令行自行安装后再试")
}
