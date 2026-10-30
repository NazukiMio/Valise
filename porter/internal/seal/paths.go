// Package seal implements sealing, vault status, verification and sync on
// the server (docs/ARCHITECTURE.md sections 6 and 7).
package seal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"valise/core/valise"
)

// Paths are the porter's files under HOME (docs/ARCHITECTURE.md section 1).
type Paths struct{ Home string }

func (p Paths) Vault() string { return filepath.Join(p.Home, ".local", "share", "valise", "vault") }
func (p Paths) Recipients() string {
	return filepath.Join(p.Home, ".config", "valise", "recipients.txt")
}
func (p Paths) Targets() string { return filepath.Join(p.Home, ".config", "valise", "targets.json") }
func (p Paths) State() string   { return filepath.Join(p.Home, ".local", "state", "valise") }
func (p Paths) Tmp() string     { return filepath.Join(p.State(), "tmp") }
func (p Paths) Audit() string   { return filepath.Join(p.State(), "audit.jsonl") }
func (p Paths) SyncState(target string) string {
	return filepath.Join(p.State(), "sync", target+".json")
}

// DedupeKey is ~/.config/valise/dedupe/<vault_id>.key.
func (p Paths) DedupeKey(vaultID string) string {
	return filepath.Join(p.Home, ".config", "valise", "dedupe", vaultID+".key")
}

// Lock takes the vault lock (flock) so only one seal or sync runs at a time.
func (p Paths) Lock() (unlock func(), err error) {
	if err := os.MkdirAll(p.State(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(p.State(), "vault.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, valise.New("vault_busy", "服务器上正在进行另一次封存或同步", "稍等片刻再试")
		}
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// AuditEntry is one line of audit.jsonl. It never holds file contents or
// credentials (docs/ARCHITECTURE.md section 10).
type AuditEntry struct {
	Time       time.Time `json:"time"`
	Op         string    `json:"op"`
	Instance   string    `json:"instance,omitempty"`
	Initiator  string    `json:"initiator"`
	Result     string    `json:"result"`
	SnapshotID string    `json:"snapshot_id,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
	Target     string    `json:"target,omitempty"`
	Reason     string    `json:"reason,omitempty"`
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
