package keystore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// Recovery kit file names besides KeystoreFile and RecipientFile.
const (
	FingerprintFile = "FINGERPRINT"
	RecoveryDocFile = "RECOVERY.md"
)

var (
	ErrKitExists        = errors.New("目标目录已存在且不为空")
	ErrKitFingerprint   = errors.New("恢复套件中的指纹与公钥不一致")
	ErrKitIdentityMatch = errors.New("恢复套件中的身份与公钥不一致")
)

const recoveryDoc = `# Valise 离线恢复套件

这个目录可以让你在没有 Valise 的情况下恢复备份库中的数据。请离线保存，并另外抄下下面的指纹。

- 公钥：%s
- 指纹：%s

## 文件

- keystore.age：用你的恢复口令加密的 age 身份（标准 age 口令加密格式）。
- recipient.txt：对应的 age 公钥，服务器备份库用它加密。
- FINGERPRINT：公钥指纹（公钥字符串 SHA-256 十六进制值的前 16 位）。

## 不依赖 Valise 的恢复步骤

1. 安装标准的 age 与 zstd 命令行工具。
2. 解出身份（会提示输入恢复口令）：

       age -d -o identity.txt keystore.age

3. 使用 Valise 仓库中的 tools/standalone-restore/（只需要 Python 3 标准库、age 与 zstd），
   按 docs/VAULT_FORMAT.md 第 9 节恢复任意快照。备份库按 <服务器>/<实例>/<周期>/ 分文件夹，
   每个周期文件夹就是一个完整的备份库：把 --vault 指向那个周期文件夹即可。
4. 恢复完成后，妥善删除 identity.txt。
`

// ExportKit writes a recovery kit directory from the stored keystore.age and
// recipient. No passphrase is needed: keystore.age is copied as is.
func ExportKit(dir string, keystoreData []byte, recipient string) error {
	if _, err := age.ParseX25519Recipient(recipient); err != nil {
		return fmt.Errorf("公钥格式无效：%w", err)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return ErrKitExists
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fp := Fingerprint(recipient)
	files := []struct {
		name string
		data []byte
	}{
		{KeystoreFile, keystoreData},
		{RecipientFile, []byte(recipient + "\n")},
		{FingerprintFile, []byte(fp + "\n")},
		{RecoveryDocFile, []byte(fmt.Sprintf(recoveryDoc, recipient, fp))},
	}
	for _, f := range files {
		if err := SaveFile(filepath.Join(dir, f.name), f.data); err != nil {
			return err
		}
	}
	return nil
}

// Kit is an imported recovery kit.
type Kit struct {
	Key          *UnlockedKey
	KeystoreData []byte
	Fingerprint  string
}

// ImportKit reads a recovery kit, checks FINGERPRINT against recipient.txt,
// unlocks keystore.age and checks the identity matches recipient.txt. The
// caller shows Fingerprint to the user for comparison with the paper copy.
func ImportKit(dir, passphrase string) (*Kit, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b)), err
	}
	recipient, err := read(RecipientFile)
	if err != nil {
		return nil, err
	}
	fp, err := read(FingerprintFile)
	if err != nil {
		return nil, err
	}
	if Fingerprint(recipient) != fp {
		return nil, ErrKitFingerprint
	}
	data, err := os.ReadFile(filepath.Join(dir, KeystoreFile))
	if err != nil {
		return nil, err
	}
	k, err := Unlock(data, passphrase)
	if err != nil {
		return nil, err
	}
	if k.Recipient() != recipient {
		k.Lock()
		return nil, ErrKitIdentityMatch
	}
	return &Kit{Key: k, KeystoreData: data, Fingerprint: fp}, nil
}
