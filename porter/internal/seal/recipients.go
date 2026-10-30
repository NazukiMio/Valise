package seal

import (
	"os"
	"strings"
	"time"

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

// AddRecipient appends an age1… recipient unless present, preceded by a
// "# <label> (added <date>)" comment when a label is given.
func AddRecipient(p Paths, recipient, label string) (added bool, all []string, err error) {
	if strings.ContainsAny(label, "\r\n") || len(label) > 200 {
		return false, nil, valise.New("recipient_label_invalid", "标签必须是一行、不超过 200 个字符", "换一个简短的说明")
	}
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
	if label != "" {
		text += "# " + label + " (added " + time.Now().UTC().Format("2006-01-02") + ")\n"
	}
	text += recipient + "\n"
	if err := writeAtomic(p.Recipients(), []byte(text), 0o600); err != nil {
		return false, nil, err
	}
	return true, append(cur, recipient), nil
}
