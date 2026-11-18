package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"valise/core/valise"
	"valise/core/vault"
)

// Export decrypts one backup into plain files in a local folder without
// starting anything (docs/ARCHITECTURE.md 8.2.3). The backup is read from
// the server or from any copy; credentials stay out unless asked for.

// ExportOptions are the person's choices.
type ExportOptions struct {
	Host       string `json:"host"`
	SnapshotID string `json:"snapshot_id"`
	To         string `json:"to"`
	// Source reads from a copy (repo:, dir:, cloud:) instead of the server.
	Source         string `json:"source,omitempty"`
	IncludeSecrets bool   `json:"include_secrets"`
}

// ExportResult says what was written.
type ExportResult struct {
	To         string        `json:"to"`
	From       string        `json:"from"` // the server or the copy's label
	Snapshot   vault.Meta    `json:"snapshot"`
	Files      int           `json:"files"`
	Bytes      int64         `json:"bytes"`
	Secrets    []string      `json:"secrets"` // credential files left out, or written with IncludeSecrets
	Links      int           `json:"links"`
	LinksKept  []string      `json:"links_listed,omitempty"` // listed in the notes, not recreated
	Transfer   *SnapshotPull `json:"transfer,omitempty"`
	NotesFile  string        `json:"notes_file"`
	HasSecrets bool          `json:"includes_secrets"`
}

const exportNotes = "VALISE-EXPORT.txt"

var rootNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Export writes the backup under o.To/<root id>/<path>.
func (s *Service) Export(ctx context.Context, o ExportOptions) (*ExportResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	to := filepath.Clean(expandHome(o.To))
	if !filepath.IsAbs(to) {
		return nil, valise.New("export_path_invalid", "请写导出文件夹的完整路径", "例如 ~/Desktop/agent-export")
	}
	if ents, err := os.ReadDir(to); err == nil && len(ents) > 0 {
		return nil, valise.New("export_not_empty", to+" 不是空文件夹", "换一个新文件夹，Valise 不覆盖已有文件")
	}
	id, err := s.ownerIdentity(o.Host)
	if err != nil {
		return nil, err
	}
	res := &ExportResult{To: to, From: o.Host, Secrets: []string{}, HasSecrets: o.IncludeSecrets}
	var v *vault.Vault
	if o.Source != "" {
		src, err := s.ParseCopySource(o.Host, o.Source)
		if err != nil {
			return nil, err
		}
		res.From = src.Label()
		if v, res.Transfer, _, err = s.openFromSource(ctx, src, o.SnapshotID, id); err != nil {
			return nil, err
		}
	} else {
		st, err := s.VaultStatus(ctx, o.Host)
		if err != nil {
			if SourceUnreachable(err) {
				return nil, s.unreachableWithCopies(ctx, o.Host, o.SnapshotID, err)
			}
			return nil, err
		}
		leaf := st.LeafOf(o.SnapshotID)
		if leaf == "" {
			return nil, valise.New("snapshot_unknown", o.Host+" 上没有备份 "+o.SnapshotID, "用 valise vault status "+o.Host+" 列出现有的备份")
		}
		if v, res.Transfer, err = s.PullSnapshot(ctx, o.Host, leaf, o.SnapshotID, id); err != nil {
			return nil, err
		}
	}
	for _, m := range v.Metas {
		if m.SnapshotID == o.SnapshotID {
			res.Snapshot = m
		}
	}
	head, list, err := v.Snapshot(o.SnapshotID, id)
	if err != nil {
		return nil, valise.New("snapshot_unreadable", "无法解密备份 "+o.SnapshotID, "确认本机钥匙是这个备份的收件人").WithDetail(err.Error())
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return nil, valise.New("export_unwritable", "无法创建 "+to, "换一个文件夹").WithDetail(err.Error())
	}
	for _, f := range list.Files {
		where := f.Root + ":" + f.Path
		if f.Category == "secrets" {
			res.Secrets = append(res.Secrets, where)
			if !o.IncludeSecrets {
				continue
			}
		}
		dst, err := exportPath(to, f.Root, f.Path)
		if err != nil {
			return res, err
		}
		if err := writeExported(v, f, dst, id); err != nil {
			return res, valise.New("export_failed", "导出 "+where+" 失败，已写出的文件保留在 "+to, "检查空间后换一个新文件夹重试").WithDetail(err.Error())
		}
		res.Files++
		res.Bytes += f.Size
	}
	for _, l := range list.Symlinks {
		res.Links++
		dst, err := exportPath(to, l.Root, l.Path)
		if err == nil && linkInside(to, dst, l.Target) {
			os.MkdirAll(filepath.Dir(dst), 0o700)
			if os.Symlink(l.Target, dst) == nil {
				continue
			}
		}
		res.LinksKept = append(res.LinksKept, l.Root+":"+l.Path+" -> "+l.Target)
	}
	res.NotesFile = filepath.Join(to, exportNotes)
	if err := os.WriteFile(res.NotesFile, []byte(exportNotesText(res, head)), 0o600); err != nil {
		return res, err
	}
	return res, nil
}

// exportPath is <to>/<root>/<path>, refused when it would leave to.
func exportPath(to, root, rel string) (string, error) {
	if !rootNameRe.MatchString(root) || !safeRel(rel) {
		return "", valise.New("export_path_unsafe", "备份里有不安全的路径 "+root+":"+rel, "这个备份不能直接导出；用 tools/standalone-restore 检查")
	}
	dst := filepath.Join(to, root, filepath.FromSlash(rel))
	if r, err := filepath.Rel(to, dst); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", valise.New("export_path_unsafe", "备份里有不安全的路径 "+root+":"+rel, "这个备份不能直接导出")
	}
	return dst, nil
}

// safeRel accepts a relative slash path with no empty, "." or ".."
// segment.
func safeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\x00") {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// linkInside reports a relative link target that stays inside to.
func linkInside(to, link, target string) bool {
	if target == "" || filepath.IsAbs(target) {
		return false
	}
	r, err := filepath.Rel(to, filepath.Join(filepath.Dir(link), target))
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// writeExported writes one file through a temporary name; WriteFile
// checks its SHA-256, and only a match is renamed into place.
func writeExported(v *vault.Vault, f vault.File, dst string, id age.Identity) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-export-")
	if err != nil {
		return err
	}
	werr := v.WriteFile(f, tmp, id)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("%v %v", werr, cerr)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if !f.MTime.IsZero() {
		os.Chtimes(dst, time.Now(), f.MTime)
	}
	return nil
}

func exportNotesText(r *ExportResult, head *vault.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Valise 导出：备份 %s\n", r.Snapshot.SnapshotID)
	fmt.Fprintf(&b, "实例：%s　harness 资料：%s v%d　档位：%s\n", head.InstanceID, head.Profile.ID, head.Profile.Version, head.Tier)
	fmt.Fprintf(&b, "备份时间：%s\n来源：%s\n导出时间：%s\n\n", head.CreatedAt.Local().Format("2006-01-02 15:04"), r.From, time.Now().Format("2006-01-02 15:04"))
	b.WriteString("这里只是备份里的文件，没有安装或启动任何程序。目录按 <根>/<相对路径> 排列（例如 state/ 是 Agent 的状态目录）。\n")
	fmt.Fprintf(&b, "共 %d 个文件，每个都已按备份记录的 SHA-256 核对。\n\n", r.Files)
	sort.Strings(r.Secrets)
	switch {
	case len(r.Secrets) == 0:
	case r.HasSecrets:
		b.WriteString("包含凭据文件（按要求导出，请妥善保管）：\n")
	default:
		b.WriteString("未导出的凭据文件（需要时加 --include-secrets 重新导出到新文件夹）：\n")
	}
	for _, s := range r.Secrets {
		b.WriteString("  " + s + "\n")
	}
	if len(r.LinksKept) > 0 {
		b.WriteString("\n没有重建的符号链接（目标在导出文件夹之外或是绝对路径）：\n")
		for _, l := range r.LinksKept {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}
