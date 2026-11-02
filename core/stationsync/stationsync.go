// Package stationsync copies the vault tree to a station, lists it and
// deletes from it (docs/ARCHITECTURE.md section 7): rsync, falling back to
// sftp when rsync is missing on either side. porter uses it for direct
// copies, the client for relays.
package stationsync

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
)

// Runner runs a program and reports its exit code and output; err is set
// when it could not be started.
type Runner interface {
	Run(ctx context.Context, timeout time.Duration, argv ...string) (code int, stdout, stderr []byte, err error)
}

// Station is where a copy goes and how to reach it.
type Station struct {
	// Name names it in messages ("nas").
	Name string
	// Kind is "rsync" (falls back to sftp), "sftp", or "rrsync": the
	// station forces rrsync for this key, so only rsync works and a missing
	// rsync means the station cannot be used from here.
	Kind string
	// Host is the SSH host: an alias of the caller's SSH configuration or
	// an address used with SSH below.
	Host string
	// SSH are extra ssh options (identity, port, user, known_hosts);
	// empty uses the caller's own SSH configuration.
	SSH []string
	// Tmp is a private directory for batch and marker files.
	Tmp string
}

// Remote paths are "host:/path"; Dest builds one.
func (st Station) Dest(p string) string { return st.Host + ":" + p }

// SyncOrder follows docs/ARCHITECTURE.md section 7: data before the files
// that reference it, so an interrupted sync never leaves a dangling meta.
var SyncOrder = []struct{ Dir, Include string }{
	{vault.PacksDir, ""},
	{vault.IndexDir, ""},
	{vault.SnapshotsDir, "*.age"},
	{vault.SnapshotsDir, "*.meta.json"},
	{vault.EpochsDir, ""},
	{vault.KeysDir, ""},
}

// Timeout bounds one transfer.
const Timeout = 6 * time.Hour

// Remote copies to a station, lists it and deletes from it. Paths are
// "host:/path".
type Remote interface {
	Method() string
	// SyncLeaf copies the files of a leaf the station lacks, data before
	// the files that reference it.
	SyncLeaf(ctx context.Context, dir, dest string) error
	// PutFile copies one file unless the station has it.
	PutFile(ctx context.Context, src, dst string) error
	// List lists what is under base; a missing base is empty.
	List(ctx context.Context, base string) ([]Entry, error)
	// DeleteDir deletes parent/name: the DELETING marker first, then
	// everything else, the marker last.
	DeleteDir(ctx context.Context, parent, name string) error
}

// Entry is a file or directory relative to the listed base.
type Entry struct {
	Path string
	Size int64
	Dir  bool
}

// Methods reported to the user.
const (
	MethodRsync  = "rsync"
	MethodSFTP   = "sftp"
	MethodRRsync = "rrsync"
)

// UnreachableCode marks a failure to reach or log in to a station; the
// client then relays (docs/ARCHITECTURE.md section 7).
const UnreachableCode = "station_unreachable"

// errNoRsync marks an rsync run that failed because rsync is missing here
// or on the station.
type errNoRsync struct {
	err error
	why string // in a few words, from the full output
}

func (e *errNoRsync) Error() string { return e.err.Error() }
func (e *errNoRsync) Unwrap() error { return e.err }

// With runs op on the station, falling back from rsync to sftp when rsync
// is missing. note explains a fallback.
func With(ctx context.Context, run Runner, st Station, op func(Remote) error) (method, note string, err error) {
	if st.Kind == MethodSFTP {
		return MethodSFTP, "", op(&sftpRemote{run: run, st: st})
	}
	err = op(&rsyncRemote{run: run, st: st})
	var missing *errNoRsync
	if !errors.As(err, &missing) {
		return MethodRsync, "", err
	}
	if st.Kind == MethodRRsync {
		// The station only takes rrsync from this key: no sftp fallback.
		return MethodRsync, "", valise.New(UnreachableCode, "这里没有可用的 rsync，而空间站只接受 rsync", "经这台电脑中转").WithDetail(missing.why)
	}
	note = "rsync 不可用（" + missing.why + "），改用 sftp，速度会慢一些"
	return MethodSFTP, note, op(&sftpRemote{run: run, st: st})
}

// whyNoRsync says in a few words why rsync could not be used.
func whyNoRsync(detail string) string {
	switch {
	case strings.Contains(detail, "unrecognized option"), strings.Contains(detail, "unknown option"), strings.Contains(detail, "usage: rsync"):
		return "这里的 rsync 版本太旧"
	case strings.Contains(detail, "executable file not found"):
		return "这里没有安装 rsync"
	case strings.Contains(detail, "not found"):
		return "对方没有安装 rsync"
	}
	return lastLines(detail, 1)
}

func destPath(dest string) string {
	_, p, _ := strings.Cut(dest, ":")
	return p
}

const partPrefix = ".part-"

// skipName reports names never copied: the vault's temporaries and an
// sftp upload in progress.
func skipName(name string) bool {
	return strings.HasPrefix(name, "tmp-") || strings.HasPrefix(name, ".tmp-") || strings.HasPrefix(name, partPrefix)
}

// failure explains a failed copy, naming the usual SSH causes.
func failure(st Station, what, stderr string) *valise.UserError {
	host := st.Host
	reason, hint := what, "确认能用 SSH 登录 "+host+"，然后重试；金库只追加，可以断点续传"
	switch {
	case strings.Contains(stderr, "Could not resolve hostname"):
		reason, hint = what+"：不认识主机名 "+host, "检查空间站的地址；服务器自己配置的空间站要在它的 ~/.ssh/config 里写上 Host "+host
	case strings.Contains(stderr, "Permission denied"):
		reason, hint = what+"："+host+" 拒绝了登录", "在空间站设置里重新装一次钥匙（需要空间站的密码）"
	case strings.Contains(stderr, "Host key verification failed"):
		reason, hint = what+"：还没有确认过 "+host+" 的身份", "在空间站设置里重新核对指纹"
	case strings.Contains(stderr, "Connection refused"), strings.Contains(stderr, "timed out"), strings.Contains(stderr, "No route to host"), strings.Contains(stderr, "Network is unreachable"):
		reason, hint = what+"：连不上 "+host, "确认 "+host+" 已开机、网络可达、SSH 服务在运行"
	}
	code := "sync_failed"
	if unreachable(stderr) {
		code = UnreachableCode
	}
	return valise.New(code, reason, hint).WithDetail(lastLines(stderr, 5))
}

func unreachable(stderr string) bool {
	for _, s := range []string{"Could not resolve hostname", "Permission denied", "Host key verification failed",
		"Connection refused", "timed out", "No route to host", "Network is unreachable", "Connection closed", "connection unexpectedly closed"} {
		if strings.Contains(stderr, s) {
			return true
		}
	}
	return false
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// --- rsync ---

type rsyncRemote struct {
	run Runner
	st  Station
}

func (r *rsyncRemote) Method() string { return MethodRsync }

// shell is rsync's -e: ssh with the station's options, quoted for rsync's
// own word splitting.
func (r *rsyncRemote) shell() string {
	words := []string{"ssh", "-o", "BatchMode=yes"}
	for _, o := range r.st.SSH {
		if strings.ContainsAny(o, " '") {
			o = "'" + strings.ReplaceAll(o, "'", "") + "'"
		}
		words = append(words, o)
	}
	return strings.Join(words, " ")
}

func (r *rsyncRemote) SyncLeaf(ctx context.Context, dir, dest string) error {
	for _, step := range SyncOrder {
		src := filepath.Join(dir, step.Dir)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		args := []string{"rsync", "-rt", "--ignore-existing", "--mkpath", "--chmod=D700,F600", "-e", r.shell(),
			"--exclude", "tmp-*", "--exclude", ".tmp-*", "--exclude", partPrefix + "*"}
		if step.Include != "" {
			args = append(args, "--include", "*/", "--include", step.Include, "--exclude", "*")
		}
		args = append(args, src+"/", dest+"/"+step.Dir+"/")
		if err := r.exec(ctx, args, "同步到 "+r.st.Name+" 失败"); err != nil {
			return err
		}
	}
	for _, f := range []string{vault.PeriodFile, vault.DescriptorFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			if err := r.PutFile(ctx, filepath.Join(dir, f), dest+"/"+f); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *rsyncRemote) PutFile(ctx context.Context, src, dst string) error {
	return r.exec(ctx, []string{"rsync", "-t", "--ignore-existing", "--mkpath", "--chmod=F600", "-e", r.shell(), src, dst}, "同步到 "+r.st.Name+" 失败")
}

func (r *rsyncRemote) exec(ctx context.Context, args []string, what string) error {
	code, _, stderr, err := r.run.Run(ctx, Timeout, args...)
	if err != nil {
		return &errNoRsync{valise.New("sync_failed", "无法运行 rsync", "安装 rsync，或改用 sftp").WithDetail(err.Error()), whyNoRsync(err.Error())}
	}
	if code != 0 {
		ue := failure(r.st, what, string(stderr))
		if rsyncMissing(code, string(stderr)) {
			return &errNoRsync{ue, whyNoRsync(string(stderr))}
		}
		return ue
	}
	return nil
}

// rsyncMissing recognises a usable rsync missing on either side: the
// station's shell cannot find it (exit 127 or "not found"), or the local
// one is too old for --mkpath (macOS's openrsync).
func rsyncMissing(code int, stderr string) bool {
	return code == 127 || (strings.Contains(stderr, "rsync") && strings.Contains(stderr, "not found")) ||
		strings.Contains(stderr, "unrecognized option") || strings.Contains(stderr, "unknown option")
}

func (r *rsyncRemote) List(ctx context.Context, base string) ([]Entry, error) {
	code, stdout, stderr, err := r.run.Run(ctx, 10*time.Minute, "rsync", "--list-only", "-r", "-e", r.shell(), base+"/")
	if err != nil {
		return nil, &errNoRsync{valise.New("target_list_failed", "无法运行 rsync", "安装 rsync，或改用 sftp").WithDetail(err.Error()), whyNoRsync(err.Error())}
	}
	if code != 0 {
		if strings.Contains(string(stderr), "No such file or directory") {
			return nil, nil
		}
		ue := failure(r.st, "无法列出 "+r.st.Name+" 上的内容", string(stderr))
		if rsyncMissing(code, string(stderr)) {
			return nil, &errNoRsync{ue, whyNoRsync(string(stderr))}
		}
		return nil, ue
	}
	var res []Entry
	for _, line := range strings.Split(string(stdout), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || (f[0][0] != '-' && f[0][0] != 'd') {
			continue
		}
		name := strings.Join(f[4:], " ")
		if name == "." {
			continue
		}
		size, err := strconv.ParseInt(strings.ReplaceAll(strings.ReplaceAll(f[1], ",", ""), ".", ""), 10, 64)
		if err != nil {
			continue
		}
		res = append(res, Entry{Path: name, Size: size, Dir: f[0][0] == 'd'})
	}
	return res, nil
}

// DeleteDir deletes with rsync alone: the marker, then two --delete passes
// from an empty directory, the first keeping the marker. No command runs
// on the station.
func (r *rsyncRemote) DeleteDir(ctx context.Context, parent, name string) error {
	tmp, err := tempDir(r.st)
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
	what := "删除 " + r.st.Name + " 上的周期失败"
	if err := r.exec(ctx, []string{"rsync", "-t", "--chmod=F600", "-e", r.shell(), markFile, parent + "/" + name + "/" + vault.DeletingFile}, what); err != nil {
		return err
	}
	for _, protect := range []bool{true, false} {
		args := []string{"rsync", "-r", "--delete", "-e", r.shell()}
		if protect {
			args = append(args, "--filter", "P /"+name+"/"+vault.DeletingFile)
		}
		args = append(args, "--include", "/"+name+"/***", "--exclude", "*", empty+"/", parent+"/")
		if err := r.exec(ctx, args, what); err != nil {
			return err
		}
	}
	return nil
}

func tempDir(st Station) (string, error) {
	if err := os.MkdirAll(st.Tmp, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(st.Tmp, "station-")
}

// writeMark writes the DELETING marker into dir for upload.
func writeMark(dir string) (string, error) {
	mark, _ := json.Marshal(map[string]any{"started": time.Now().UTC(), "initiator": "client"})
	f := filepath.Join(dir, vault.DeletingFile)
	return f, os.WriteFile(f, append(mark, '\n'), 0o600)
}

// --- sftp ---

// sftpRemote uses OpenSSH's sftp in batch mode; only sftp-server is needed
// on the station. Uploads go to a ".part-" name in the same directory and
// are renamed when complete.
type sftpRemote struct {
	run Runner
	st  Station
}

func (r *sftpRemote) Method() string { return MethodSFTP }

// quote quotes a path for an sftp batch line; vault paths and validated
// dests hold no quotes.
func quote(s string) string { return `"` + s + `"` }

// batch runs batch lines; a line starting with "-" may fail.
func (r *sftpRemote) batch(ctx context.Context, lines []string, what string) (string, error) {
	tmp, err := tempDir(r.st)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "batch")
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	argv := append([]string{"sftp", "-b", file, "-o", "BatchMode=yes"}, sftpOptions(r.st.SSH)...)
	code, stdout, stderr, err := r.run.Run(ctx, Timeout, append(argv, r.st.Host)...)
	if err != nil {
		return "", valise.New("sync_failed", "无法运行 sftp", "确认安装了 OpenSSH 客户端").WithDetail(err.Error())
	}
	if code != 0 {
		return "", failure(r.st, what+"（sftp）", string(stderr))
	}
	return string(stdout), nil
}

// sftpOptions turns ssh options into sftp's: sftp spells the port -P.
func sftpOptions(ssh []string) []string {
	out := make([]string, 0, len(ssh))
	for i := 0; i < len(ssh); i++ {
		if ssh[i] == "-p" && i+1 < len(ssh) {
			out = append(out, "-P", ssh[i+1])
			i++
			continue
		}
		if ssh[i] == "-l" && i+1 < len(ssh) {
			out = append(out, "-o", "User="+ssh[i+1])
			i++
			continue
		}
		out = append(out, ssh[i])
	}
	return out
}

// List walks base level by level: one sftp session per depth.
func (r *sftpRemote) List(ctx context.Context, base string) ([]Entry, error) {
	root := strings.TrimRight(destPath(base), "/")
	var res []Entry
	dirs := []string{root}
	for len(dirs) > 0 {
		var lines []string
		for _, d := range dirs {
			lines = append(lines, "-ls -ln "+quote(d))
		}
		out, err := r.batch(ctx, lines, "列出 "+r.st.Name+" 上的内容")
		if err != nil {
			return nil, err
		}
		dirs = nil
		for _, e := range parseLongList(out) {
			rel, ok := strings.CutPrefix(e.Path, root+"/")
			if !ok || skipName(path.Base(rel)) {
				continue
			}
			if e.Dir {
				dirs = append(dirs, e.Path)
			}
			res = append(res, Entry{Path: rel, Size: e.Size, Dir: e.Dir})
		}
	}
	return res, nil
}

// parseLongList reads `ls -ln <dir>` output: OpenSSH prints each entry
// with its full path as the last field; echoed commands start "sftp>".
func parseLongList(out string) []Entry {
	var res []Entry
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
		res = append(res, Entry{Path: name, Size: size, Dir: f[0][0] == 'd'})
	}
	return res
}

// mkdirs returns batch lines creating dir and its parents (errors for
// existing ones are ignored).
func mkdirs(dir string, made map[string]bool) []string {
	var lines, chain []string
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

func (r *sftpRemote) SyncLeaf(ctx context.Context, dir, dest string) error {
	have := map[string]bool{}
	entries, err := r.List(ctx, dest)
	if err != nil {
		return err
	}
	for _, e := range entries {
		have[e.Path] = true
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
	for _, step := range SyncOrder {
		var rels []string
		filepath.WalkDir(filepath.Join(dir, step.Dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || skipName(d.Name()) {
				return nil
			}
			if step.Include != "" {
				if ok, _ := path.Match(step.Include, d.Name()); !ok {
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
	_, err = r.batch(ctx, lines, "同步到 "+r.st.Name+" 失败")
	return err
}

func (r *sftpRemote) PutFile(ctx context.Context, src, dst string) error {
	dp := destPath(dst)
	out, err := r.batch(ctx, []string{"-ls -ln " + quote(path.Dir(dp))}, "列出 "+r.st.Name+" 上的内容")
	if err != nil {
		return err
	}
	for _, e := range parseLongList(out) {
		if e.Path == dp {
			return nil
		}
	}
	_, err = r.batch(ctx, append(mkdirs(path.Dir(dp), map[string]bool{}), putLines(src, dp)...), "同步到 "+r.st.Name+" 失败")
	return err
}

func (r *sftpRemote) DeleteDir(ctx context.Context, parent, name string) error {
	tp := destPath(parent) + "/" + name
	tmp, err := tempDir(r.st)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	markFile, err := writeMark(tmp)
	if err != nil {
		return err
	}
	what := "删除 " + r.st.Name + " 上的周期失败"
	mark := path.Join(tp, vault.DeletingFile)
	if _, err := r.batch(ctx, append(mkdirs(tp, map[string]bool{}), putLines(markFile, mark)...), what); err != nil {
		return err
	}
	entries, err := r.List(ctx, r.st.Dest(tp))
	if err != nil {
		return err
	}
	var lines, dirs []string
	for _, e := range entries {
		switch {
		case e.Dir:
			dirs = append(dirs, path.Join(tp, e.Path))
		case e.Path != vault.DeletingFile:
			lines = append(lines, "rm "+quote(path.Join(tp, e.Path)))
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/") })
	for _, d := range dirs {
		lines = append(lines, "rmdir "+quote(d))
	}
	lines = append(lines, "rm "+quote(mark), "rmdir "+quote(tp))
	_, err = r.batch(ctx, lines, what)
	return err
}
