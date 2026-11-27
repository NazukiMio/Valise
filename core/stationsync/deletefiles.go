package stationsync

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"valise/core/valise"
)

// ValidRels checks relative paths for DeleteFiles: slash-separated, clean,
// never absolute, never leaving the base, no control, quote, wildcard,
// leading/trailing whitespace, or filter comment characters.
func ValidRels(rels []string) error {
	for _, rel := range rels {
		if rel == "" || strings.TrimSpace(rel) != rel ||
			strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "#") || strings.HasPrefix(rel, ";") ||
			strings.ContainsAny(rel, "\\\"\x00\r\n*?[]") ||
			path.Clean(rel) != rel || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			return valise.New("path_invalid", "无效的相对路径："+rel, "只删除备份库内的文件")
		}
		for _, r := range rel {
			if r < 0x20 {
				return valise.New("path_invalid", "无效的相对路径："+rel, "只删除备份库内的文件")
			}
		}
	}
	return nil
}

// DeleteFiles removes base/<rel> files with rsync alone (no command on the
// station): the parent directories are mirrored empty in a local skeleton and
// only the listed files are included, so --delete removes exactly those. A
// file already gone is not an error.
func (r *rsyncRemote) DeleteFiles(ctx context.Context, base string, rels []string) error {
	if err := ValidRels(rels); err != nil || len(rels) == 0 {
		return err
	}
	tmp, err := tempDir(r.st)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	skel := filepath.Join(tmp, "skeleton")
	if err := os.Mkdir(skel, 0o700); err != nil {
		return err
	}
	dirs := map[string]bool{}
	var rules []string
	for _, rel := range rels {
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
		rules = append(rules, rel)
	}
	var ds []string
	for d := range dirs {
		ds = append(ds, d)
	}
	sort.Strings(ds)
	args := []string{"rsync", "-r", "--delete", "-e", r.shell()}
	for _, d := range ds {
		if err := os.MkdirAll(filepath.Join(skel, filepath.FromSlash(d)), 0o700); err != nil {
			return err
		}
		args = append(args, "--include", "/"+d+"/")
	}
	for _, rel := range rules {
		args = append(args, "--include", "/"+rel)
	}
	args = append(args, "--exclude", "*", skel+"/", strings.TrimRight(base, "/")+"/")
	return r.exec(ctx, args, "删除 "+r.st.Name+" 上的备份文件失败")
}

// DeleteFiles removes base/<rel> with one sftp batch; "-rm" ignores files that
// are already gone.
func (r *sftpRemote) DeleteFiles(ctx context.Context, base string, rels []string) error {
	if err := ValidRels(rels); err != nil || len(rels) == 0 {
		return err
	}
	bp := strings.TrimRight(destPath(base), "/")
	lines := make([]string, 0, len(rels))
	for _, rel := range rels {
		lines = append(lines, "-rm "+quote(bp+"/"+rel))
	}
	_, err := r.batch(ctx, lines, "删除 "+r.st.Name+" 上的备份文件失败")
	return err
}

// DeleteFiles removes base/<rel> with rclone delete --files-from, which skips
// names that do not exist.
func (r *rcloneRemote) DeleteFiles(ctx context.Context, base string, rels []string) error {
	if err := ValidRels(rels); err != nil || len(rels) == 0 {
		return err
	}
	list, done, err := r.filesFrom(rels)
	if err != nil {
		return err
	}
	defer done()
	_, _, err = r.exec(ctx, "删除 "+r.st.Name+" 上的备份文件失败", "delete", "--files-from", list, "--no-traverse", base)
	return err
}
