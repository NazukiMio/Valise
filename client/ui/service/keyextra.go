package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"valise/client/keystore"
	"valise/core/valise"
)

// KeyBackup is whether the key was backed up (the recovery kit exported)
// or the person chose to do it later (docs/ARCHITECTURE.md 3).
type KeyBackup struct {
	ExportedAt *time.Time `json:"exported_at,omitempty"`
	ExportedTo string     `json:"exported_to,omitempty"`
	Later      bool       `json:"later,omitempty"` // “我知道风险，稍后备份”
}

func (s *Service) keyBackupPath() string { return filepath.Join(s.Dirs.Data, "key-backup.json") }

// KeyBackupState reads what this computer knows about backing up the key.
func (s *Service) KeyBackupState() KeyBackup {
	var b KeyBackup
	if data, err := os.ReadFile(s.keyBackupPath()); err == nil {
		_ = json.Unmarshal(data, &b)
	}
	return b
}

func (s *Service) saveKeyBackup(b KeyBackup) error {
	data, _ := json.MarshalIndent(b, "", "  ")
	return writeAtomicFile(s.keyBackupPath(), data)
}

// KeyBackupDecided reports whether the first backup may go on: the kit
// was exported, or the person chose to back the key up later.
func (s *Service) KeyBackupDecided() bool {
	b := s.KeyBackupState()
	return b.ExportedAt != nil || b.Later
}

// BackUpKeyLater records “我知道风险，稍后备份”.
func (s *Service) BackUpKeyLater() error {
	b := s.KeyBackupState()
	b.Later = true
	return s.saveKeyBackup(b)
}

func (s *Service) recordKitExport(dir string) {
	b := s.KeyBackupState()
	now := time.Now().UTC()
	b.ExportedAt, b.ExportedTo = &now, dir
	_ = s.saveKeyBackup(b)
}

// ChangePassphrase seals the same key under a new passphrase; backups and
// servers are unaffected. The backup lock's own copy (recovery/r.age) is
// re-sealed too. An exported kit keeps the old passphrase.
func (s *Service) ChangePassphrase(old, new string) error {
	if old == new {
		return valise.New("passphrase_same", "新口令与现在的一样", "换一个不同的口令")
	}
	k, err := s.Unlock(old)
	if err != nil {
		return err
	}
	defer k.Lock()
	data, err := keystore.Seal(k, new)
	if err != nil {
		return keyErr(err)
	}
	if err := s.resealLock(old, new); err != nil {
		return err
	}
	return keystore.SaveFile(s.keystorePath(), data)
}
