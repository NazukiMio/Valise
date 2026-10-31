package seal

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Status describes the vault tree without any key.
func Status(p Paths) (*vaultrpc.StatusResult, error) {
	res := &vaultrpc.StatusResult{Snapshots: []vault.Meta{}, Leaves: []vaultrpc.LeafState{}, Targets: []vaultrpc.TargetState{}, Recipients: []string{}}
	rec, err := ReadRecipients(p)
	if err != nil {
		return nil, err
	}
	for _, r := range rec {
		res.Recipients = append(res.Recipients, vault.Fingerprint(r))
	}
	if res.ServerName, err = ReadServerName(p); err != nil {
		return nil, err
	}
	targets, err := readTargets(p)
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		st := vaultrpc.TargetState{Target: t}
		if b, err := os.ReadFile(p.SyncState(t.Name)); err == nil {
			json.Unmarshal(b, &st)
			st.Target = t
		}
		res.Targets = append(res.Targets, st)
	}
	leaves, err := Leaves(p)
	if err != nil {
		return nil, err
	}
	t := now()
	for _, l := range leaves {
		ls := vaultrpc.LeafState{Leaf: l.Leaf, Period: l.Period, Deleting: l.Deleting, Snapshots: []string{}, Bytes: dirBytes(l.Dir)}
		if l.Period != nil && l.Period.TZ != "" && l.Period.Contains(t) {
			ls.Current = true
		}
		if !l.Deleting {
			if _, err := os.Stat(filepath.Join(l.Dir, vault.DescriptorFile)); err == nil {
				v, err := vault.Open(l.Dir)
				if err != nil {
					return nil, err
				}
				ls.VaultID, ls.Epochs = v.Desc.VaultID, len(v.Epochs)
				for _, m := range v.Metas {
					ls.Snapshots = append(ls.Snapshots, m.SnapshotID)
					res.Snapshots = append(res.Snapshots, m)
				}
			}
		}
		res.Leaves = append(res.Leaves, ls)
	}
	sort.SliceStable(res.Snapshots, func(i, j int) bool { return res.Snapshots[i].SnapshotID < res.Snapshots[j].SnapshotID })
	res.Exists = len(res.Snapshots) > 0
	return res, nil
}

func dirBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// Verify runs the no-key verification on every leaf.
func Verify(p Paths) (*vaultrpc.VerifyResult, error) {
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	leaves, err := Leaves(p)
	if err != nil {
		return nil, err
	}
	res := &vaultrpc.VerifyResult{Problems: []vault.Problem{}}
	checked := 0
	for _, l := range leaves {
		if l.Deleting {
			res.Problems = append(res.Problems, vault.Problem{Where: l.Leaf, What: "删除未完成"})
			continue
		}
		if _, err := os.Stat(filepath.Join(l.Dir, vault.DescriptorFile)); err != nil {
			continue
		}
		probs, err := vault.VerifyNoKey(l.Dir)
		if err != nil {
			return nil, err
		}
		for _, pr := range probs {
			pr.Where = l.Leaf + " " + pr.Where
			res.Problems = append(res.Problems, pr)
		}
		checked++
	}
	if checked == 0 && len(res.Problems) == 0 {
		return nil, valise.New("vault_missing", "服务器上还没有金库", "先建立一个逃生舱：valise seal <服务器> <实例>")
	}
	return res, nil
}

// Files lists every valid file of one leaf (temporaries excluded).
func Files(p Paths, leaf string) (*vaultrpc.FilesResult, error) {
	dir, err := p.LeafDir(leaf)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, vault.DeletingFile)); err == nil {
		return nil, valise.New("leaf_deleting", "周期 "+leaf+" 正在删除", "先完成这次删除")
	}
	v, err := vault.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, valise.New("vault_missing", "服务器上没有周期 "+leaf, "用 valise vault status 列出现有周期")
	}
	if err != nil {
		return nil, err
	}
	res := &vaultrpc.FilesResult{Leaf: leaf, VaultID: v.Desc.VaultID, Files: []vaultrpc.VaultFile{}}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if !vault.ValidRel(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		res.Files = append(res.Files, vaultrpc.VaultFile{Path: rel, Size: info.Size()})
		return nil
	})
	return res, err
}

// OpenVaultFile opens a file of a leaf for the data plane. Only paths
// matching the vault layout are served.
func OpenVaultFile(p Paths, leaf, rel string) (*os.File, error) {
	dir, err := p.LeafDir(leaf)
	if err != nil {
		return nil, err
	}
	if !vault.ValidRel(rel) {
		return nil, errors.New("not a vault file: " + rel)
	}
	return os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
}

var targetNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type targetsFile struct {
	Targets []vaultrpc.Target `json:"targets"`
}

func readTargets(p Paths) ([]vaultrpc.Target, error) {
	b, err := os.ReadFile(p.Targets())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tf targetsFile
	if err := json.Unmarshal(b, &tf); err != nil {
		return nil, valise.New("targets_invalid", "服务器上的 targets.json 无法解析", "检查 ~/.config/valise/targets.json").WithDetail(err.Error())
	}
	return tf.Targets, nil
}

// SetTarget adds or replaces a sync target.
func SetTarget(p Paths, t vaultrpc.Target) error {
	if !targetNameRe.MatchString(t.Name) {
		return valise.New("target_name_invalid", "同步目标名只能包含小写字母、数字和连字符", "例如 nas")
	}
	switch t.Kind {
	case "rsync":
	case "rclone", "sftp":
		return valise.New("target_kind_unsupported", "这个版本只支持 rsync 同步", "改用 --rsync <主机:路径>")
	default:
		return valise.New("target_kind_invalid", "未知的同步方式："+t.Kind, "可选 rsync")
	}
	host, path, ok := strings.Cut(t.Dest, ":")
	if !ok || host == "" || strings.HasPrefix(host, "-") || !strings.HasPrefix(path, "/") || strings.ContainsAny(t.Dest, " \t\n'\"`$\\") {
		return valise.New("target_dest_invalid", "同步目标地址无效："+t.Dest, "格式为 <SSH 主机别名>:<绝对路径>，例如 nas-test:/mnt/test/nas/valise-vault/")
	}
	if err := validateAuto(t.Auto); err != nil {
		return err
	}
	ts, err := readTargets(p)
	if err != nil {
		return err
	}
	out := []vaultrpc.Target{t}
	for _, old := range ts {
		if old.Name != t.Name {
			out = append(out, old)
		}
	}
	b, _ := json.MarshalIndent(targetsFile{Targets: out}, "", "  ")
	return writeAtomic(p.Targets(), append(b, '\n'), 0o600)
}

const syncTimeout = 6 * time.Hour

// syncOrder follows docs/ARCHITECTURE.md section 7: data before the files
// that reference it, so an interrupted sync never leaves a dangling meta.
var syncOrder = []struct{ dir, include string }{
	{vault.PacksDir, ""},
	{vault.IndexDir, ""},
	{vault.SnapshotsDir, "*.age"},
	{vault.SnapshotsDir, "*.meta.json"},
	{vault.EpochsDir, ""},
	{vault.KeysDir, ""},
}

// Sync copies the vault tree to a target with rsync, skipping existing
// files: leaves go to <dest>/<server_name>/<instance>/<period>/, the legacy
// vault to <dest>/<vault_id>/ as before. Leaves being deleted are skipped.
// The server's own SSH configuration supplies credentials.
func Sync(ctx context.Context, e *porterenroll.Env, p Paths, name, initiator string) (res *vaultrpc.SyncResult, err error) {
	t, err := findTarget(p, name)
	if err != nil {
		return nil, err
	}
	st, err := Status(p)
	if err != nil {
		return nil, err
	}
	if !st.Exists {
		return nil, valise.New("vault_missing", "服务器上还没有金库", "先建立一个逃生舱：valise seal <服务器> <实例>")
	}
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return syncLocked(ctx, e, p, t, st, initiator)
}

func findTarget(p Paths, name string) (*vaultrpc.Target, error) {
	ts, err := readTargets(p)
	if err != nil {
		return nil, err
	}
	for i := range ts {
		if ts[i].Name == name {
			return &ts[i], nil
		}
	}
	return nil, valise.New("target_unknown", "服务器上没有名为 "+name+" 的云备份位置", "先添加：valise target add <服务器> "+name+" --rsync <主机:路径>")
}

// treeDest is <dest>/<server_name>.
func treeDest(t *vaultrpc.Target, serverName string) string {
	return strings.TrimRight(t.Dest, "/") + "/" + serverName
}

// syncLocked runs with the vault lock held.
func syncLocked(ctx context.Context, e *porterenroll.Env, p Paths, t *vaultrpc.Target, st *vaultrpc.StatusResult, initiator string) (res *vaultrpc.SyncResult, err error) {
	hasTree := false
	for _, l := range st.Leaves {
		hasTree = hasTree || l.Leaf != vault.LegacyLeaf
	}
	if hasTree && st.ServerName == "" {
		return nil, valise.New("server_name_missing", "这台服务器还没有名字，云备份不知道放进哪个文件夹", "先给服务器起名：valise server name <服务器> <名字>")
	}
	defer func() {
		state := vaultrpc.TargetState{Target: *t}
		if prev, rerr := os.ReadFile(p.SyncState(t.Name)); rerr == nil {
			json.Unmarshal(prev, &state)
			state.Target = *t
		}
		ent := AuditEntry{Time: time.Now().UTC(), Op: "sync", Initiator: initiator, Target: t.Name, Result: "ok"}
		if err != nil {
			ent.Result, state.LastError = "failed", err.Error()
		} else {
			now := time.Now().UTC()
			state.LastOK, state.LastError, state.LastSynced = &now, "", res.Snapshots
		}
		b, _ := json.MarshalIndent(state, "", "  ")
		writeAtomic(p.SyncState(t.Name), b, 0o600)
		p.AppendAudit(ent)
	}()
	res = &vaultrpc.SyncResult{Target: t.Name, Dest: strings.TrimRight(t.Dest, "/"), Leaves: []string{}, Snapshots: []string{}}
	if hasTree {
		res.Dest = treeDest(t, st.ServerName)
		if err := rsyncFile(ctx, e, p, t, filepath.Join(p.Vaults(), vault.TreeFile), res.Dest+"/"+vault.TreeFile); err != nil {
			return nil, err
		}
	}
	for _, l := range st.Leaves {
		if l.Deleting || l.VaultID == "" {
			continue
		}
		dir, err := p.LeafDir(l.Leaf)
		if err != nil {
			return nil, err
		}
		dest := res.Dest + "/" + l.Leaf
		if l.Leaf == vault.LegacyLeaf {
			dest = strings.TrimRight(t.Dest, "/") + "/" + l.VaultID
		}
		if err := syncLeaf(ctx, e, p, t, dir, dest); err != nil {
			return nil, err
		}
		res.Leaves = append(res.Leaves, l.Leaf)
		res.Snapshots = append(res.Snapshots, l.Snapshots...)
	}
	return res, nil
}

const rsyncSSH = "ssh -o BatchMode=yes"

func syncLeaf(ctx context.Context, e *porterenroll.Env, p Paths, t *vaultrpc.Target, dir, dest string) error {
	for _, step := range syncOrder {
		src := filepath.Join(dir, step.dir)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		args := []string{"rsync", "-rt", "--ignore-existing", "--mkpath", "--chmod=D700,F600", "-e", rsyncSSH,
			"--exclude", "tmp-*", "--exclude", ".tmp-*"}
		if step.include != "" {
			args = append(args, "--include", "*/", "--include", step.include, "--exclude", "*")
		}
		args = append(args, src+"/", dest+"/"+step.dir+"/")
		if err := runRsync(ctx, e, p, t, args); err != nil {
			return err
		}
	}
	for _, f := range []string{vault.PeriodFile, vault.DescriptorFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			if err := rsyncFile(ctx, e, p, t, filepath.Join(dir, f), dest+"/"+f); err != nil {
				return err
			}
		}
	}
	return nil
}

func rsyncFile(ctx context.Context, e *porterenroll.Env, p Paths, t *vaultrpc.Target, src, dst string) error {
	return runRsync(ctx, e, p, t, []string{"rsync", "-t", "--ignore-existing", "--mkpath", "--chmod=F600", "-e", rsyncSSH, src, dst})
}

func runRsync(ctx context.Context, e *porterenroll.Env, p Paths, t *vaultrpc.Target, args []string) error {
	out, err := e.Run(ctx, syncTimeout, p.Home, args...)
	if err != nil {
		return valise.New("sync_failed", "无法运行 rsync", "确认服务器上安装了 rsync").WithDetail(err.Error())
	}
	if out.Code != 0 {
		return valise.New("sync_failed", "同步到 "+t.Name+" 失败",
			"确认服务器能用 SSH 登录 "+strings.SplitN(t.Dest, ":", 2)[0]+"（凭据由你在服务器上配置），然后重试；金库只追加，可以断点续传").
			WithDetail(lastLines(string(out.Stderr), 5))
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
