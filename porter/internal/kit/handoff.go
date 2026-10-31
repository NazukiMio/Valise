package kit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

// HandoffTargets resolves the profile's handoff targets to files: every
// existing match of a glob, or the literal path when nothing matches yet.
func HandoffTargets(rec *enroll.Record, p *profile.Profile) ([]TargetBefore, error) {
	if p.Handoff == nil || len(p.Handoff.Targets) == 0 {
		return nil, valise.New("handoff_no_target", "profile "+p.ID+" 没有声明交接目标", "这个 harness 暂不支持交接")
	}
	var out []TargetBefore
	for _, t := range p.Handoff.Targets {
		root := rec.Roots[t.Root]
		if root == "" {
			continue
		}
		rels := globFiles(root, t.Glob)
		if len(rels) == 0 && profile.IsLiteral(t.Glob) {
			rels = []string{t.Glob}
		}
		for _, rel := range rels {
			tb := TargetBefore{Root: t.Root, Path: rel, MaxLines: t.MaxLines}
			if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				sum := sha256.Sum256(b)
				tb.Exists, tb.SHA256, tb.Size = true, hex.EncodeToString(sum[:]), int64(len(b))
			}
			out = append(out, tb)
		}
	}
	if len(out) == 0 {
		return nil, valise.New("handoff_no_target", "交接目标文件不存在", "检查实例的记忆目录")
	}
	return out, nil
}

func globFiles(root, glob string) []string {
	paths, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
	var out []string
	for _, p := range paths {
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

var datedHeading = regexp.MustCompile(`^#{1,6}\s.*\d{4}-\d{2}-\d{2}`)

// TargetCheck is the outcome for one handoff target.
type TargetCheck struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Added   int    `json:"added_lines"`
	Problem string `json:"problem,omitempty"`
}

// CheckHandoff verifies append_section edits against the state at begin:
// the old content is an unchanged prefix, the appended part is at most
// max_lines lines and starts with a dated Markdown heading. At least one
// target must have changed.
func CheckHandoff(rec *enroll.Record, st *HandoffState) ([]TargetCheck, error) {
	var checks []TargetCheck
	changed := 0
	for _, t := range st.Targets {
		c := TargetCheck{Root: t.Root, Path: t.Path}
		b, err := os.ReadFile(filepath.Join(rec.Roots[t.Root], filepath.FromSlash(t.Path)))
		if err != nil {
			if t.Exists {
				c.Problem = "文件在交接期间被删除"
			}
			checks = append(checks, c)
			continue
		}
		sum := sha256.Sum256(b)
		if t.Exists && hex.EncodeToString(sum[:]) == t.SHA256 {
			checks = append(checks, c)
			continue
		}
		c.Changed = true
		var added []byte
		if t.Exists {
			if int64(len(b)) < t.Size {
				c.Problem = "原有内容被删改（文件变短了）"
				checks = append(checks, c)
				continue
			}
			prefix := sha256.Sum256(b[:t.Size])
			if hex.EncodeToString(prefix[:]) != t.SHA256 {
				c.Problem = "原有内容被修改；交接只能在文件末尾追加一个区段"
				checks = append(checks, c)
				continue
			}
			added = b[t.Size:]
		} else {
			added = b
		}
		text := strings.TrimRight(string(added), "\n")
		lines := strings.Split(strings.TrimLeft(text, "\n"), "\n")
		c.Added = len(lines)
		switch {
		case strings.TrimSpace(text) == "":
			c.Problem = "只追加了空白"
		case len(lines) > t.MaxLines:
			c.Problem = fmt.Sprintf("追加了 %d 行，超过上限 %d 行", len(lines), t.MaxLines)
		case !datedHeading.MatchString(lines[0]):
			c.Problem = "追加的区段必须以带日期（YYYY-MM-DD）的 Markdown 标题开头，例如 \"## Handoff 2026-10-31\""
		case t.Exists && t.Size > 0 && !bytes.HasSuffix(b[:t.Size], []byte("\n")) && !strings.HasPrefix(string(added), "\n"):
			c.Problem = "区段标题必须另起一行"
		default:
			changed++
		}
		checks = append(checks, c)
	}
	for _, c := range checks {
		if c.Problem != "" {
			return checks, valise.New("handoff_check_failed", c.Path+"："+c.Problem,
				"按要求修正后再次调用 valise_handoff_finish；原文件可以从快照 "+st.SnapshotID+" 恢复")
		}
	}
	if changed == 0 {
		return checks, valise.New("handoff_not_written", "交接目标文件都没有被修改", "先把交接内容追加到目标文件，再调用 valise_handoff_finish")
	}
	return checks, nil
}

// Stale reports a handoff begun long ago (abandoned).
func (h *HandoffState) Stale(now time.Time) bool { return now.Sub(h.Started) > 24*time.Hour }
