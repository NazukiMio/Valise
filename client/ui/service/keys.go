package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"valise/client/keystore"
	"valise/core/valise"
)

// KeyInfo describes the stored identity without unlocking it.
type KeyInfo struct {
	Recipient   string `json:"recipient"`
	Fingerprint string `json:"fingerprint"`
	Path        string `json:"path"`
}

func (s *Service) keystorePath() string  { return filepath.Join(s.Dirs.Data, keystore.KeystoreFile) }
func (s *Service) recipientPath() string { return filepath.Join(s.Dirs.Data, keystore.RecipientFile) }

// keyErr maps keystore errors to user errors.
func keyErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keystore.ErrPassphraseTooShort):
		return valise.New("passphrase_too_short", err.Error(), "换一个更长的口令；它是你恢复备份库的唯一钥匙，请离线抄写保存")
	case errors.Is(err, keystore.ErrWrongPassphrase):
		return valise.New("passphrase_wrong", "口令错误", "重新输入；如果忘记口令，Valise 无法帮你找回")
	case errors.Is(err, keystore.ErrCorrupt):
		return valise.New("keystore_corrupt", "keystore.age 已损坏或被篡改", "从离线恢复套件重新导入：valise key import-kit <目录>").WithDetail(err.Error())
	case errors.Is(err, keystore.ErrNotIdentity):
		return valise.New("identity_invalid", "导入的内容不是一个 age X25519 身份", "导入 age-keygen 生成的身份文件（AGE-SECRET-KEY-1…）")
	case errors.Is(err, keystore.ErrKitFingerprint), errors.Is(err, keystore.ErrKitIdentityMatch):
		return valise.New("kit_mismatch", err.Error(), "这个恢复套件不完整或被改动过，换一份备份")
	case errors.Is(err, keystore.ErrKitExists):
		return valise.New("kit_target_exists", "导出目录已存在且不为空", "选择一个新的空目录")
	}
	return err
}

func (s *Service) ensureNoKey() error {
	if _, err := os.Stat(s.keystorePath()); err == nil {
		return valise.New("keystore_exists", "这台电脑上已经有 Valise 密钥", "如需更换，请先自行备份并移走 "+s.keystorePath())
	}
	return nil
}

func (s *Service) store(k *keystore.UnlockedKey, data []byte) (*KeyInfo, error) {
	if err := keystore.SaveFile(s.recipientPath(), []byte(k.Recipient()+"\n")); err != nil {
		return nil, err
	}
	if err := keystore.SaveFile(s.keystorePath(), data); err != nil {
		return nil, err
	}
	return &KeyInfo{Recipient: k.Recipient(), Fingerprint: k.Fingerprint(), Path: s.keystorePath()}, nil
}

// KeyInit generates a new identity protected by passphrase.
func (s *Service) KeyInit(passphrase string) (*KeyInfo, error) {
	k, err := keystore.Generate()
	if err != nil {
		return nil, err
	}
	return s.sealAndStore(k, passphrase)
}

// KeyImport imports an existing age identity.
func (s *Service) KeyImport(identity []byte, passphrase string) (*KeyInfo, error) {
	k, err := keystore.Import(identity)
	if err != nil {
		return nil, keyErr(err)
	}
	return s.sealAndStore(k, passphrase)
}

func (s *Service) sealAndStore(k *keystore.UnlockedKey, passphrase string) (*KeyInfo, error) {
	defer k.Lock()
	if err := s.ensureNoKey(); err != nil {
		return nil, err
	}
	data, err := keystore.Seal(k, passphrase)
	if err != nil {
		return nil, keyErr(err)
	}
	return s.store(k, data)
}

// KeyShow reads the recipient without unlocking.
func (s *Service) KeyShow() (*KeyInfo, error) {
	b, err := os.ReadFile(s.recipientPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, valise.New("keystore_missing", "这台电脑上还没有 Valise 密钥", "运行 valise key init 生成，或 valise key import-kit <目录> 从恢复套件导入")
	}
	if err != nil {
		return nil, err
	}
	r := strings.TrimSpace(string(b))
	return &KeyInfo{Recipient: r, Fingerprint: keystore.Fingerprint(r), Path: s.keystorePath()}, nil
}

// Unlock decrypts the stored identity. The caller must Lock it.
func (s *Service) Unlock(passphrase string) (*keystore.UnlockedKey, error) {
	info, err := s.KeyShow()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.keystorePath())
	if err != nil {
		return nil, err
	}
	k, err := keystore.Unlock(data, passphrase)
	if err != nil {
		return nil, keyErr(err)
	}
	if k.Recipient() != info.Recipient {
		k.Lock()
		return nil, valise.New("keystore_mismatch", "keystore.age 与 recipient.txt 不一致", "从离线恢复套件重新导入")
	}
	return k, nil
}

// ExportKit writes the offline recovery kit into dir.
func (s *Service) ExportKit(dir string) (*KeyInfo, error) {
	info, err := s.KeyShow()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.keystorePath())
	if err != nil {
		return nil, err
	}
	if err := keystore.ExportKit(dir, data, info.Recipient); err != nil {
		return nil, keyErr(err)
	}
	return info, nil
}

// ImportKit restores the identity from a recovery kit. The returned
// fingerprint must be compared by the user with the paper copy.
func (s *Service) ImportKit(dir, passphrase string) (*KeyInfo, error) {
	if err := s.ensureNoKey(); err != nil {
		return nil, err
	}
	kit, err := keystore.ImportKit(dir, passphrase)
	if err != nil {
		return nil, keyErr(err)
	}
	defer kit.Key.Lock()
	return s.store(kit.Key, kit.KeystoreData)
}
