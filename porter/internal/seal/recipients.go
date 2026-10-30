package seal

import (
	"os"
	"strings"

	"filippo.io/age"

	"valise/core/valise"
	"valise/core/vault"
)

// ReadRecipients returns the server's recipients (missing file: none).
func ReadRecipients(p Paths) ([]string, error) {
	b, err := os.ReadFile(p.Recipients())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := vault.Recipients(string(b))
	if err != nil {
		return nil, valise.New("recipients_invalid", "服务器上的 recipients.txt 无法解析", "检查 ~/.config/valise/recipients.txt").WithDetail(err.Error())
	}
	return r, nil
}

// AddRecipient appends an age1… recipient unless present.
func AddRecipient(p Paths, recipient string) (added bool, all []string, err error) {
	if _, err := age.ParseX25519Recipient(recipient); err != nil {
		return false, nil, valise.New("recipient_invalid", "公钥格式无效", "使用 valise key show 显示的 age1… 公钥")
	}
	cur, err := ReadRecipients(p)
	if err != nil {
		return false, nil, err
	}
	for _, r := range cur {
		if r == recipient {
			return false, cur, nil
		}
	}
	b, _ := os.ReadFile(p.Recipients())
	text := string(b)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text == "" {
		text = "# age recipients for Valise; one age1… key per line\n"
	}
	text += recipient + "\n"
	if err := writeAtomic(p.Recipients(), []byte(text), 0o600); err != nil {
		return false, nil, err
	}
	return true, append(cur, recipient), nil
}
