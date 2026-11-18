package vault

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"valise/core/valise"
)

// NormalizeNote validates a user-supplied plaintext savepoint comment.
func NormalizeNote(note string) (string, error) {
	trimmed := strings.TrimSpace(note)
	if !utf8.ValidString(note) || strings.ContainsFunc(note, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
	}) || utf8.RuneCountInString(trimmed) > 140 {
		return "", valise.New("snapshot_note_invalid", "存档点备注须为一行，最多 140 个字符", "删去换行或控制字符后重试")
	}
	return trimmed, nil
}
