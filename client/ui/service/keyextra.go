package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// ChangePassphrase re-seals the same identity, the backup lock and keys
// that use the old Valise passphrase. Exported kits retain their passphrase.
func (s *Service) ChangePassphrase(old, new string) error {
	if old == new {
		return valise.New("passphrase_same", "新口令与现在的一样", "换一个不同的口令")
	}
	unlock, err := s.lockPIN()
	if err != nil {
		return err
	}
	defer unlock()
	k, err := s.unlockStored(old)
	if err != nil {
		return err
	}
	defer k.Lock()
	data, err := keystore.Seal(k, new)
	if err != nil {
		return keyErr(err)
	}
	path := s.keystorePath()
	before, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updates := []credentialUpdate{{path, before, data}}
	if s.RecoveryEnabled() {
		path := filepath.Join(s.recoveryDir(), "r.age")
		before, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		r, err := keystore.Unlock(before, old)
		if err != nil {
			return valise.New("recovery_lock_unreadable", "备用锁打不开", "在设置里关闭再重新开启备用锁")
		}
		defer r.Lock()
		data, err := keystore.Seal(r, new)
		if err != nil {
			return keyErr(err)
		}
		updates = append(updates, credentialUpdate{path, before, data})
	}
	if s.PINEnabled() {
		p, err := s.readPIN()
		if err != nil {
			return err
		}
		before, err := os.ReadFile(s.pinStatePath())
		if err != nil {
			return err
		}
		data, err := p.rewrap(old, new)
		if err != nil {
			return err
		}
		updates = append(updates, credentialUpdate{s.pinStatePath(), before, data})
	}
	admin, err := s.prepareAdminRotation(old, new)
	if err != nil {
		return err
	}
	s.LockAdmin()
	return commitCredentials(append(updates, admin...))
}

type credentialUpdate struct {
	path          string
	before, after []byte
}

func commitCredentials(updates []credentialUpdate) error {
	for i, u := range updates {
		if err := keystore.SaveFile(u.path, u.after); err != nil {
			for _, previous := range updates[:i] {
				err = errors.Join(err, keystore.SaveFile(previous.path, previous.before))
			}
			return valise.New("passphrase_change_failed", "未能保存修改后的口令资料", "检查本机存储权限与可用空间后重试").WithDetail(err.Error())
		}
	}
	return nil
}

func (s *Service) prepareAdminRotation(old, new string) ([]credentialUpdate, error) {
	keys, err := filepath.Glob(filepath.Join(s.Dirs.Data, "keys", "*", adminKeyName))
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	env, err := askpassEnviron(map[string]string{askpassSecret: old, askpassOld: old, askpassNew: new})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var updates []credentialUpdate
	for _, path := range keys {
		update, err := s.rotateAdminCopy(ctx, path, env)
		if err != nil {
			return nil, err
		}
		if update != nil {
			updates = append(updates, *update)
		}
	}
	return updates, nil
}

func (s *Service) rotateAdminCopy(ctx context.Context, path string, env []string) (*credentialUpdate, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	staged, err := os.MkdirTemp(filepath.Dir(path), ".admin-rotate-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(staged)
	copyPath := filepath.Join(staged, adminKeyName)
	if err := keystore.SaveFile(copyPath, before); err != nil {
		return nil, err
	}
	defer os.Remove(copyPath)
	if err := runTool(ctx, s.toolPath("ssh-keygen"), []string{"-q", "-p", "-f", copyPath}, env); err != nil {
		if strings.Contains(err.Error(), "incorrect passphrase supplied to decrypt private key") {
			return nil, nil
		}
		return nil, valise.New("admin_passphrase_change_failed", "无法重加密 "+filepath.Base(filepath.Dir(path))+" 的管理钥匙", "检查这把管理钥匙能否正常使用，再重试")
	}
	after, err := os.ReadFile(copyPath)
	if err != nil {
		return nil, err
	}
	return &credentialUpdate{path, before, after}, nil
}

// KitImportedFrom records that the key came from a recovery kit, which is
// itself a backup of the key.
func (s *Service) KitImportedFrom(dir string) { s.recordKitExport(dir) }
