package seal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// A remote copies the vault tree to a target, lists it and deletes from it
// (docs/ARCHITECTURE.md section 7). rsync is the default; a target without
// rsync on either side falls back to sftp for that operation.
type remote interface {
	method() string
	// syncLeaf copies the files of a leaf the target lacks, data before
	// the files that reference it (syncOrder).
	syncLeaf(ctx context.Context, dir, dest string) error
	// putFile copies one file unless the target has it.
	putFile(ctx context.Context, src, dst string) error
	// list lists the regular files under base; a missing base is empty.
	list(ctx context.Context, base string) ([]remoteEntry, error)
	// deleteDir deletes parent/name: the DELETING marker first, then
	// everything else, the marker last.
	deleteDir(ctx context.Context, parent, name string) error
}

type remoteEntry struct {
	path string
	size int64
	dir  bool
}

// Sync methods reported to the client.
const (
	methodRsync = "rsync"
	methodSFTP  = "sftp"
)

// errNoRsync marks an rsync run that failed because rsync is missing here
// or on the target.
type errNoRsync struct{ err error }

func (e *errNoRsync) Error() string { return e.err.Error() }
func (e *errNoRsync) Unwrap() error { return e.err }

// withRemote runs op on the target's remote, falling back from rsync to
// sftp when rsync is missing. note explains a fallback.
func withRemote(ctx context.Context, e *porterenroll.Env, p Paths, t *vaultrpc.Target, op func(remote) error) (method, note string, err error) {
	if t.Kind == methodSFTP {
		r := &sftpRemote{e: e, p: p, t: t}
		return r.method(), "", op(r)
	}
	err = op(&rsyncRemote{e: e, p: p, t: t})
	var missing *errNoRsync
	if !errors.As(err, &missing) {
		return methodRsync, "", err
	}
	note = "rsync 不可用（" + firstLine(missing.err) + "），改用 sftp，速度会慢一些"
	return methodSFTP, note, op(&sftpRemote{e: e, p: p, t: t})
}

func firstLine(err error) string {
	s := err.Error()
	if ue, ok := valise.As(err); ok && ue.Detail != "" {
		s = ue.Detail
	}
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// targetHost is the SSH host of a target's dest ("host:/path").
func targetHost(t *vaultrpc.Target) string { return strings.SplitN(t.Dest, ":", 2)[0] }

// splitDest splits "host:/path" into its path.
func destPath(dest string) string {
	_, p, _ := strings.Cut(dest, ":")
	return p
}

// skipName reports names never copied: the vault's own temporaries and an
// sftp upload in progress.
func skipName(name string) bool {
	return strings.HasPrefix(name, "tmp-") || strings.HasPrefix(name, ".tmp-") || strings.HasPrefix(name, partPrefix)
}

// sshFailure explains a failed copy, naming the usual SSH causes: the
// server reaching the target is the user's own SSH setup on the server.
func sshFailure(t *vaultrpc.Target, what, stderr string) *valise.UserError {
	host := targetHost(t)
	reason, hint := what, "确认服务器能用 SSH 登录 "+host+"（凭据由你在服务器上配置），然后重试；金库只追加，可以断点续传"
	switch {
	case strings.Contains(stderr, "Could not resolve hostname"):
		reason = what + "：服务器不认识主机名 " + host
		hint = "在服务器的 ~/.ssh/config 里加上 Host " + host + "（HostName、User、IdentityFile），或把位置改成 <地址>:<路径>"
	case strings.Contains(stderr, "Permission denied"):
		reason = what + "：" + host + " 拒绝了服务器的登录"
		hint = "把服务器上用于登录 " + host + " 的公钥加到 " + host + " 的 ~/.ssh/authorized_keys，或在服务器的 ~/.ssh/config 里为它指定 IdentityFile"
	case strings.Contains(stderr, "Host key verification failed"):
		reason = what + "：服务器还没有确认过 " + host + " 的身份"
		hint = "在服务器上手动执行一次 ssh " + host + "，核对并接受指纹后重试"
	case strings.Contains(stderr, "Connection refused"), strings.Contains(stderr, "timed out"), strings.Contains(stderr, "No route to host"):
		reason = what + "：服务器连不上 " + host
		hint = "确认 " + host + " 已开机、网络可达、SSH 服务在运行，然后重试"
	}
	return valise.New("sync_failed", reason, hint).WithDetail(lastLines(stderr, 5))
}

// --- rsync ---

type rsyncRemote struct {
	e *porterenroll.Env
	p Paths
	t *vaultrpc.Target
}

const rsyncSSH = "ssh -o BatchMode=yes"

func (r *rsyncRemote) method() string { return methodRsync }

func (r *rsyncRemote) syncLeaf(ctx context.Context, dir, dest string) error {
	for _, step := range syncOrder {
		src := filepath.Join(dir, step.dir)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		args := []string{"rsync", "-rt", "--ignore-existing", "--mkpath", "--chmod=D700,F600", "-e", rsyncSSH,
			"--exclude", "tmp-*", "--exclude", ".tmp-*", "--exclude", partPrefix + "*"}
		if step.include != "" {
			args = append(args, "--include", "*/", "--include", step.include, "--exclude", "*")
		}
		args = append(args, src+"/", dest+"/"+step.dir+"/")
		if err := r.run(ctx, args); err != nil {
			return err
		}
	}
	for _, f := range []string{vault.PeriodFile, vault.DescriptorFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			if err := r.putFile(ctx, filepath.Join(dir, f), dest+"/"+f); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *rsyncRemote) putFile(ctx context.Context, src, dst string) error {
	return r.run(ctx, []string{"rsync", "-t", "--ignore-existing", "--mkpath", "--chmod=F600", "-e", rsyncSSH, src, dst})
}

func (r *rsyncRemote) run(ctx context.Context, args []string) error {
	out, err := r.e.Run(ctx, syncTimeout, r.p.Home, args...)
	if err != nil {
		return &errNoRsync{valise.New("sync_failed", "无法运行 rsync", "确认服务器上安装了 rsync").WithDetail(err.Error())}
	}
	if out.Code != 0 {
		ue := sshFailure(r.t, "同步到 "+r.t.Name+" 失败", string(out.Stderr))
		if rsyncMissing(out.Code, string(out.Stderr)) {
			return &errNoRsync{ue}
		}
		return ue
	}
	return nil
}

// rsyncMissing recognises a target without rsync: the remote shell cannot
// find it (exit 127 or "not found"), which rsync reports as a closed
// connection.
func rsyncMissing(code int, stderr string) bool {
	return code == 127 || (strings.Contains(stderr, "rsync") && strings.Contains(stderr, "not found"))
}

func (r *rsyncRemote) list(ctx context.Context, base string) ([]remoteEntry, error) {
	out, err := r.e.Run(ctx, 10*time.Minute, r.p.Home, "rsync", "--list-only", "-r", "-e", rsyncSSH, base+"/")
	if err != nil {
		return nil, &errNoRsync{valise.New("target_list_failed", "无法运行 rsync", "确认服务器上安装了 rsync").WithDetail(err.Error())}
	}
	if out.Code != 0 {
		if strings.Contains(string(out.Stderr), "No such file or directory") {
			return nil, nil
		}
		ue := sshFailure(r.t, "无法列出 "+r.t.Name+" 上的内容", string(out.Stderr))
		if rsyncMissing(out.Code, string(out.Stderr)) {
			return nil, &errNoRsync{ue}
		}
		return nil, ue
	}
	var res []remoteEntry
	for _, line := range strings.Split(string(out.Stdout), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.HasPrefix(f[0], "-") {
			continue
		}
		size, err := strconv.ParseInt(strings.ReplaceAll(strings.ReplaceAll(f[1], ",", ""), ".", ""), 10, 64)
		if err != nil {
			continue
		}
		res = append(res, remoteEntry{path: strings.Join(f[4:], " "), size: size})
	}
	return res, nil
}

// deleteDir deletes with rsync alone: the marker, then two --delete passes
// from an empty directory, the first keeping the marker. No command runs
// on the target host.
func (r *rsyncRemote) deleteDir(ctx context.Context, parent, name string) error {
	if err := os.MkdirAll(r.p.Tmp(), 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(r.p.Tmp(), "target-delete-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	empty := filepath.Join(tmp, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		return err
	}
	markFile, err := writeMark(tmp)
	if err != nil {
		return err
	}
	if err := r.run(ctx, []string{"rsync", "-t", "--chmod=F600", "-e", rsyncSSH, markFile, parent + "/" + name + "/" + vault.DeletingFile}); err != nil {
		return err
	}
	for _, protect := range []bool{true, false} {
		args := []string{"rsync", "-r", "--delete", "-e", rsyncSSH}
		if protect {
			args = append(args, "--filter", "P /"+name+"/"+vault.DeletingFile)
		}
		args = append(args, "--include", "/"+name+"/***", "--exclude", "*", empty+"/", parent+"/")
		if err := r.run(ctx, args); err != nil {
			return err
		}
	}
	return nil
}

// writeMark writes the DELETING marker into dir for upload.
func writeMark(dir string) (string, error) {
	mark, _ := json.Marshal(deletingMark{Started: time.Now().UTC(), Initiator: vaultrpc.InitiatorClient})
	f := filepath.Join(dir, vault.DeletingFile)
	return f, os.WriteFile(f, append(mark, '\n'), 0o600)
}

// --- sftp ---

// sftpRemote uses OpenSSH's sftp in batch mode with the server's own SSH
// configuration; only sftp-server is needed on the target. Uploads go to
// a ".part-" name in the same directory and are renamed when complete.
type sftpRemote struct {
	e *porterenroll.Env
	p Paths
	t *vaultrpc.Target
}

const partPrefix = ".part-"

func (r *sftpRemote) method() string { return methodSFTP }

// quote quotes a path for an sftp batch line; vault paths and validated
// dests hold no quotes or spaces.
func quote(s string) string { return `"` + s + `"` }

// run runs batch lines; a line starting with "-" may fail.
func (r *sftpRemote) run(ctx context.Context, lines []string, what string) (string, error) {
	if err := os.MkdirAll(r.p.Tmp(), 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(r.p.Tmp(), "sftp-batch-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	f.WriteString(strings.Join(lines, "\n") + "\n")
	f.Close()
	out, err := r.e.Run(ctx, syncTimeout, r.p.Home, "sftp", "-b", f.Name(), "-o", "BatchMode=yes", targetHost(r.t))
	if err != nil {
		return "", valise.New("sync_failed", "无法运行 sftp", "确认服务器上安装了 OpenSSH 客户端").WithDetail(err.Error())
	}
	if out.Code != 0 {
		return "", sshFailure(r.t, what+"失败（sftp，"+r.t.Name+"）", string(out.Stderr))
	}
	return string(out.Stdout), nil
}

// list walks base level by level: one sftp session per depth.
func (r *sftpRemote) list(ctx context.Context, base string) ([]remoteEntry, error) {
	root := destPath(base)
	var res []remoteEntry
	dirs := []string{root}
	for len(dirs) > 0 {
		var lines []string
		for _, d := range dirs {
			lines = append(lines, "-ls -ln "+quote(d))
		}
		out, err := r.run(ctx, lines, "列出 "+r.t.Name+" 上的内容")
		if err != nil {
			return nil, err
		}
		dirs = nil
		for _, e := range parseLongList(out) {
			rel, ok := strings.CutPrefix(e.path, strings.TrimRight(root, "/")+"/")
			if !ok || skipName(path.Base(rel)) {
				continue
			}
			if e.dir {
				dirs = append(dirs, e.path)
			}
			res = append(res, remoteEntry{path: rel, size: e.size, dir: e.dir})
		}
	}
	return res, nil
}

// parseLongList reads `ls -ln <dir>` output: OpenSSH prints each entry
// with its full path as the last field; its echoed commands start with
// "sftp>".
func parseLongList(out string) []remoteEntry {
	var res []remoteEntry
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 || strings.HasPrefix(line, "sftp>") || (f[0][0] != '-' && f[0][0] != 'd') {
			continue
		}
		name := strings.Join(f[8:], " ")
		if b := path.Base(name); b == "." || b == ".." {
			continue
		}
		size, _ := strconv.ParseInt(f[4], 10, 64)
		res = append(res, remoteEntry{path: name, size: size, dir: f[0][0] == 'd'})
	}
	return res
}

// mkdirs returns batch lines creating dir and its parents (errors for
// existing ones are ignored).
func mkdirs(dir string, made map[string]bool) []string {
	var lines []string
	var chain []string
	for d := dir; d != "/" && d != "." && d != ""; d = path.Dir(d) {
		chain = append(chain, d)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if !made[chain[i]] {
			made[chain[i]] = true
			lines = append(lines, "-mkdir "+quote(chain[i]))
		}
	}
	return lines
}

// putLines uploads src to dst through a part file.
func putLines(src, dst string) []string {
	part := path.Join(path.Dir(dst), partPrefix+path.Base(dst))
	return []string{"-rm " + quote(part), "put " + quote(src) + " " + quote(part), "rename " + quote(part) + " " + quote(dst), "-chmod 600 " + quote(dst)}
}

func (r *sftpRemote) syncLeaf(ctx context.Context, dir, dest string) error {
	have := map[string]bool{}
	entries, err := r.list(ctx, dest)
	if err != nil {
		return err
	}
	for _, e := range entries {
		have[e.path] = true
	}
	root := destPath(dest)
	var lines []string
	made := map[string]bool{}
	add := func(rel string) {
		if have[rel] {
			return
		}
		dst := path.Join(root, rel)
		lines = append(lines, mkdirs(path.Dir(dst), made)...)
		lines = append(lines, putLines(filepath.Join(dir, filepath.FromSlash(rel)), dst)...)
	}
	for _, step := range syncOrder {
		var rels []string
		filepath.WalkDir(filepath.Join(dir, step.dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || skipName(d.Name()) {
				return nil
			}
			if step.include != "" {
				if ok, _ := path.Match(step.include, d.Name()); !ok {
					return nil
				}
			}
			rel, _ := filepath.Rel(dir, p)
			rels = append(rels, filepath.ToSlash(rel))
			return nil
		})
		sort.Strings(rels)
		for _, rel := range rels {
			add(rel)
		}
	}
	for _, f := range []string{vault.PeriodFile, vault.DescriptorFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			add(f)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	_, err = r.run(ctx, lines, "同步到 "+r.t.Name)
	return err
}

func (r *sftpRemote) putFile(ctx context.Context, src, dst string) error {
	dp := destPath(dst)
	out, err := r.run(ctx, []string{"-ls -ln " + quote(path.Dir(dp))}, "列出 "+r.t.Name+" 上的内容")
	if err != nil {
		return err
	}
	for _, e := range parseLongList(out) {
		if e.path == dp {
			return nil
		}
	}
	lines := append(mkdirs(path.Dir(dp), map[string]bool{}), putLines(src, dp)...)
	_, err = r.run(ctx, lines, "同步到 "+r.t.Name)
	return err
}

func (r *sftpRemote) deleteDir(ctx context.Context, parent, name string) error {
	target := parent + "/" + name
	tp := destPath(target)
	if err := os.MkdirAll(r.p.Tmp(), 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(r.p.Tmp(), "target-delete-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	markFile, err := writeMark(tmp)
	if err != nil {
		return err
	}
	mark := path.Join(tp, vault.DeletingFile)
	if _, err := r.run(ctx, append(mkdirs(tp, map[string]bool{}), putLines(markFile, mark)...), "删除 "+r.t.Name+" 上的周期"); err != nil {
		return err
	}
	entries, err := r.list(ctx, target)
	if err != nil {
		return err
	}
	var lines, dirs []string
	for _, e := range entries {
		switch {
		case e.dir:
			dirs = append(dirs, path.Join(tp, e.path))
		case e.path != vault.DeletingFile:
			lines = append(lines, "rm "+quote(path.Join(tp, e.path)))
		}
	}
	// Deepest first.
	sort.Slice(dirs, func(i, j int) bool { return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/") })
	for _, d := range dirs {
		lines = append(lines, "rmdir "+quote(d))
	}
	lines = append(lines, "rm "+quote(mark), "rmdir "+quote(tp))
	_, err = r.run(ctx, lines, "删除 "+r.t.Name+" 上的周期")
	return err
}
