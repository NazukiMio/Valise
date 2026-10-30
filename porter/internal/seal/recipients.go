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

// RemoveRecipient deletes a recipient (and the label comment right above
// it). The last recipient cannot be removed. Snapshots already sealed stay
// decryptable by the removed key; the next seal starts a new epoch.
func RemoveRecipient(p Paths, recipient string) (removed bool, all []string, err error) {
	cur, err := ReadRecipients(p)
	if err != nil {
		return false, nil, err
	}
	found := false
	for _, r := range cur {
		found = found || r == recipient
	}
	if !found {
		return false, cur, nil
	}
	if len(cur) == 1 {
		return false, cur, valise.New("last_recipient", "不能移除最后一把收件人公钥，否则无法再封存", "先登记新的公钥，再移除这一把")
	}
	b, _ := os.ReadFile(p.Recipients())
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == recipient {
			if n := len(out); n > 0 && strings.HasPrefix(out[n-1], "# ") && !strings.HasPrefix(out[n-1], "# age recipients") {
				out = out[:n-1]
			}
			continue
		}
		out = append(out, lines[i])
	}
	if err := writeAtomic(p.Recipients(), []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		return false, nil, err
	}
	all, err = ReadRecipients(p)
	return true, all, err
}
