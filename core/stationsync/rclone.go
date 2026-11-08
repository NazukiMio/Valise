package stationsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"valise/core/valise"
	"valise/core/vault"
)

// rcloneRemote reaches a cloud drive through rclone with Valise's own
// config file (docs/ARCHITECTURE.md 7.3). Copies only add files
// (--ignore-existing --immutable); "sync" is never used. Paths are
// "<remote>:<path>".
type rcloneRemote struct {
	run Runner
	st  Station
}

func (r *rcloneRemote) Method() string { return MethodRclone }

// rcloneNotFound is rclone's exit code for a missing directory.
const rcloneNotFound = 3

func (r *rcloneRemote) argv(args ...string) []string {
	return append([]string{r.st.Rclone, "--config", r.st.RcloneConfig, "--retries", "3"}, args...)
}

// rcloneFailure explains a failed rclone run, naming login and network
// causes.
func rcloneFailure(st Station, what, stderr string) *valise.UserError {
	reason, hint := what, "检查网络后重试；金库只追加，可以断点续传"
	code := "sync_failed"
	switch Cause(stderr) {
	case CauseAuthDenied:
		reason, hint, code = what+"：网盘的登录已失效", "在备份位置里重新登录这个云盘", UnreachableCode
	case CauseConnectTimeout, CauseConnectRefused, CauseHostUnknown:
		reason, hint, code = what+"：连不上网盘", "确认这台机器能访问互联网", UnreachableCode
	}
	return valise.New(code, reason, hint).WithDetail(lastLines(stderr, 5))
}

func (r *rcloneRemote) exec(ctx context.Context, what string, args ...string) (int, []byte, error) {
	code, stdout, stderr, err := r.run.Run(ctx, Timeout, r.argv(args...)...)
	if err != nil {
		return 0, nil, valise.New("sync_failed", "无法运行 rclone", "在服务器设置里安装 rclone，或在这台电脑上重新准备 rclone").WithDetail(err.Error())
	}
	if code != 0 && code != rcloneNotFound {
		return code, nil, rcloneFailure(r.st, what, string(stderr))
	}
	return code, stdout, nil
}

type lsjsonEntry struct {
	Path    string `json:"Path"`
	Size    int64  `json:"Size"`
	ModTime string `json:"ModTime"`
	IsDir   bool   `json:"IsDir"`
}

func (r *rcloneRemote) List(ctx context.Context, base string) ([]Entry, error) {
	code, out, err := r.exec(ctx, "无法列出 "+r.st.Name+" 上的内容", "lsjson", "-R", base)
	if err != nil {
		return nil, err
	}
	if code == rcloneNotFound {
		return nil, nil
	}
	var list []lsjsonEntry
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, valise.New("target_list_failed", "读不懂 rclone 的列表", "确认 rclone 版本与 Valise 锁定的一致").WithDetail(err.Error())
	}
	var res []Entry
	for _, e := range list {
		if skipName(path.Base(e.Path)) {
			continue
		}
		res = append(res, Entry{Path: e.Path, Size: e.Size, Dir: e.IsDir, Stamp: e.ModTime})
	}
	return res, nil
}

// filesFrom writes a --files-from list into a private temporary directory.
func (r *rcloneRemote) filesFrom(rels []string) (string, func(), error) {
	tmp, err := tempDir(r.st)
	if err != nil {
		return "", nil, err
	}
	f := filepath.Join(tmp, "files")
	if err := os.WriteFile(f, []byte(strings.Join(rels, "\n")+"\n"), 0o600); err != nil {
		os.RemoveAll(tmp)
		return "", nil, err
	}
	return f, func() { os.RemoveAll(tmp) }, nil
}

func (r *rcloneRemote) copyFiles(ctx context.Context, src, dst string, rels []string, what string) error {
	if len(rels) == 0 {
		return nil
	}
	list, done, err := r.filesFrom(rels)
	if err != nil {
		return err
	}
	defer done()
	_, _, err = r.exec(ctx, what, "copy", "--files-from", list, "--ignore-existing", "--immutable", "--no-traverse", src, dst)
	return err
}

func (r *rcloneRemote) SyncLeaf(ctx context.Context, dir, dest string) error {
	entries, err := r.List(ctx, dest)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Path] = true
	}
	what := "同步到 " + r.st.Name + " 失败"
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
			if rel = filepath.ToSlash(rel); !have[rel] {
				rels = append(rels, rel)
			}
			return nil
		})
		sort.Strings(rels)
		if err := r.copyFiles(ctx, dir, dest, rels, what); err != nil {
			return err
		}
	}
	var last []string
	for _, f := range []string{vault.PeriodFile, vault.DescriptorFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil && !have[f] {
			last = append(last, f)
		}
	}
	return r.copyFiles(ctx, dir, dest, last, what)
}

func (r *rcloneRemote) PutFile(ctx context.Context, src, dst string) error {
	_, _, err := r.exec(ctx, "同步到 "+r.st.Name+" 失败", "copyto", "--ignore-existing", "--immutable", src, dst)
	return err
}

func (r *rcloneRemote) Fetch(ctx context.Context, base string, rels []string, dir string) error {
	return r.copyFiles(ctx, base, dir, rels, "从 "+r.st.Name+" 读取失败")
}

// DeleteDir writes the DELETING marker first, deletes everything else,
// then the marker and the empty folders.
func (r *rcloneRemote) DeleteDir(ctx context.Context, parent, name string) error {
	target := strings.TrimRight(parent, "/") + "/" + name
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
	if _, _, err := r.exec(ctx, what, "copyto", markFile, target+"/"+vault.DeletingFile); err != nil {
		return err
	}
	if _, _, err := r.exec(ctx, what, "delete", "--exclude", "/"+vault.DeletingFile, target); err != nil {
		return err
	}
	if _, _, err := r.exec(ctx, what, "deletefile", target+"/"+vault.DeletingFile); err != nil {
		return err
	}
	_, _, err = r.exec(ctx, what, "rmdirs", target)
	return err
}

// probeFlags make rclone give up within ProbeTimeout and say why: by
// default it retries a throttled or failing drive quietly for minutes, and
// the check then ends with nothing to show (seen with Google Drive's shared
// app, 2026-11-08).
var probeFlags = []string{"--retries", "1", "--low-level-retries", "2", "--contimeout", "8s", "--timeout", "12s"}

func (r *rcloneRemote) Probe(ctx context.Context, base string) error {
	args := append([]string{r.st.Rclone, "--config", r.st.RcloneConfig}, probeFlags...)
	code, _, stderr, err := r.run.Run(ctx, ProbeTimeout, append(args, "lsjson", "--max-depth", "1", base)...)
	if errors.Is(err, context.DeadlineExceeded) {
		return valise.New(UnreachableCode, fmt.Sprintf("无法连接 %s：rclone 在 %d 秒内没有回应", r.st.Name, int(ProbeTimeout.Seconds())),
			"稍后重试；网盘可能在限流，Google Drive 请改用自己的应用（client_id）")
	}
	if err != nil {
		return valise.New("target_list_failed", "无法运行 rclone", "在服务器设置里安装 rclone").WithDetail(err.Error())
	}
	if code != 0 && code != rcloneNotFound {
		return rcloneFailure(r.st, "无法连接 "+r.st.Name, string(stderr))
	}
	return nil
}
