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
	"valise/core/stationsync"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Status describes the vault tree without any key.
func Status(p Paths) (*vaultrpc.StatusResult, error) {
	res := &vaultrpc.StatusResult{Snapshots: []vault.Meta{}, Leaves: []vaultrpc.LeafState{}, Targets: []vaultrpc.TargetState{}, Recipients: []string{}, Now: time.Now().UTC()}
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
		return nil, valise.New("vault_missing", "服务器上还没有备份库", "先建立一个备份：valise seal <服务器> <实例>")
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

var (
	targetNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// rcloneDestRe: Valise's remote, a relative folder (docs/ARCHITECTURE.md 7.3).
	rcloneDestRe  = regexp.MustCompile(`^valise-[a-z0-9]+(-[a-z0-9]+)*:[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*/?$`)
	stationAddrRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$`)
	stationUserRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,31}$`)
)

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
	if err := validTarget(t); err != nil {
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

// RemoveTarget forgets a copy location: its line in targets.json and its
// sync state. The files saved there are not touched, and a cloud drive's
// login stays (CloudForget removes that). A location already gone is done.
// The caller holds the plans' lock (protection.Store.WithTargetRemove).
func RemoveTarget(p Paths, name, initiator string) error {
	if !targetNameRe.MatchString(name) {
		return valise.New("target_name_invalid", "备份位置名无效："+name, "")
	}
	ts, err := readTargets(p)
	if err != nil {
		return err
	}
	found := false
	for _, t := range ts {
		found = found || t.Name == name
	}
	if !found {
		return nil
	}
	if err := removeTargetEntry(p, name); err != nil {
		return err
	}
	p.AppendAudit(AuditEntry{Time: time.Now().UTC(), Op: "target_remove", Initiator: initiator, Target: name, Result: "ok"})
	return nil
}

// CheckTarget logs in to a copy location from this server and lists its
// folder without recursing or writing (contracts 16.G3). A failed login is a result,
// not an error. Valise-managed locations keep their own certificate probe.
func CheckTarget(ctx context.Context, e *porterenroll.Env, p Paths, params vaultrpc.TargetCheckParams) (*vaultrpc.TargetCheckResult, error) {
	var t *vaultrpc.Target
	switch {
	case params.Target != nil && params.Name == "":
		if err := validTarget(*params.Target); err != nil {
			return nil, err
		}
		t = params.Target
	case params.Target == nil && params.Name != "":
		var err error
		if t, err = findTarget(p, params.Name); err != nil {
			return nil, err
		}
	default:
		return nil, valise.New("request_invalid", "要给出一个已保存的位置名，或一份待保存的设置", "")
	}
	if t.Station != nil {
		return nil, valise.New("check_not_applicable", t.Name+" 由 Valise 管理，连接测试在复制时自动进行", "直接复制；连不上时会经这台电脑中转")
	}
	res := &vaultrpc.TargetCheckResult{}
	var err error
	res.Method, res.Note, err = withRemote(ctx, e, p, t, func(r stationsync.Remote) error {
		return r.Probe(ctx, strings.TrimRight(t.Dest, "/"))
	})
	if err != nil {
		res.FailureCause = stationsync.CauseOf(err)
		if ue, ok := valise.As(err); ok {
			res.Reason, res.Suggestion, res.Detail = ue.Reason, ue.Suggestion, ue.Detail
		} else {
			res.Reason = err.Error()
		}
		return res, nil
	}
	res.Reachable = true
	return res, nil
}

func validTarget(t vaultrpc.Target) error {
	if !targetNameRe.MatchString(t.Name) {
		return valise.New("target_name_invalid", "同步目标名只能包含小写字母、数字和连字符", "例如 nas")
	}
	switch t.Kind {
	case stationsync.MethodRsync, stationsync.MethodSFTP, stationsync.MethodRRsync:
	case stationsync.MethodRclone:
		// A cloud drive (docs/ARCHITECTURE.md 7.3): the remote lives in
		// Valise's own rclone config; the dest holds no authorization.
		_, folder, _ := strings.Cut(t.Dest, ":")
		folder = "/" + strings.TrimSuffix(folder, "/") + "/"
		if t.Station != nil || !rcloneDestRe.MatchString(t.Dest) || strings.Contains(folder, "/../") || strings.Contains(folder, "/./") {
			return valise.New("target_dest_invalid", "云盘位置无效："+t.Dest, "在 Valise 里重新添加这个云盘")
		}
		return validateAuto(t.Auto)
	default:
		return valise.New("target_kind_invalid", "未知的同步方式："+t.Kind, "可选 rsync、sftp")
	}
	if c := t.Station; c != nil {
		if !stationAddrRe.MatchString(c.Address) || c.Port < 1 || c.Port > 65535 || !stationUserRe.MatchString(c.User) || len(c.KnownHosts) == 0 ||
			!strings.HasPrefix(t.Dest, c.Address+":") {
			return valise.New("station_invalid", "远程备份位置的连接信息无效", "在 Valise 里重新添加这个远程备份位置")
		}
		for _, l := range c.KnownHosts {
			if strings.ContainsAny(l, "\n\r") || len(strings.Fields(l)) < 3 {
				return valise.New("station_invalid", "远程备份位置的指纹记录无效", "在 Valise 里重新核对指纹")
			}
		}
	}
	host, path, ok := strings.Cut(t.Dest, ":")
	if !ok || host == "" || strings.HasPrefix(host, "-") || !strings.HasPrefix(path, "/") || strings.ContainsAny(t.Dest, " \t\n'\"`$\\") {
		return valise.New("target_dest_invalid", "同步目标地址无效："+t.Dest, "格式为 <SSH 主机别名>:<绝对路径>，例如 nas-test:/mnt/test/nas/valise-vault/")
	}
	return validateAuto(t.Auto)
}

const syncTimeout = 6 * time.Hour

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
		return nil, valise.New("vault_missing", "服务器上还没有备份库", "先建立一个备份：valise seal <服务器> <实例>")
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
	return nil, valise.New("target_unknown", "服务器上没有名为 "+name+" 的备份位置", "先添加：valise target add <服务器> "+name+" --rsync <主机:路径>")
}

// treeDest is <dest>/<server_name>.
func treeDest(t *vaultrpc.Target, serverName string) string {
	if t.Station != nil && t.Station.Rooted {
		// The station already anchors this server at its own folder.
		return strings.TrimRight(t.Dest, "/")
	}
	return strings.TrimRight(t.Dest, "/") + "/" + serverName
}

// syncLocked runs with the vault lock held.
func syncLocked(ctx context.Context, e *porterenroll.Env, p Paths, t *vaultrpc.Target, st *vaultrpc.StatusResult, initiator string) (res *vaultrpc.SyncResult, err error) {
	hasTree := false
	for _, l := range st.Leaves {
		hasTree = hasTree || l.Leaf != vault.LegacyLeaf
	}
	if hasTree && st.ServerName == "" {
		return nil, valise.New("server_name_missing", "这台服务器还没有名字，备份位置不知道放进哪个文件夹", "先给服务器起名：valise server name <服务器> <名字>")
	}
	defer func() {
		state := vaultrpc.TargetState{Target: *t}
		if prev, rerr := os.ReadFile(p.SyncState(t.Name)); rerr == nil {
			json.Unmarshal(prev, &state)
			state.Target = *t
		}
		ent := AuditEntry{Time: time.Now().UTC(), Op: "sync", Initiator: initiator, Target: t.Name, Result: "ok"}
		if res != nil {
			state.Checks = mergeChecks(state.Checks, res.Checks)
		}
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
	}
	res.Method, res.Note, err = withRemote(ctx, e, p, t, func(r stationsync.Remote) error {
		res.Leaves, res.Snapshots = []string{}, []string{}
		if hasTree {
			if err := r.PutFile(ctx, filepath.Join(p.Vaults(), vault.TreeFile), res.Dest+"/"+vault.TreeFile); err != nil {
				return err
			}
		}
		for _, l := range st.Leaves {
			if l.Deleting || l.VaultID == "" {
				continue
			}
			dir, err := p.LeafDir(l.Leaf)
			if err != nil {
				return err
			}
			dest := res.Dest + "/" + l.Leaf
			if l.Leaf == vault.LegacyLeaf {
				dest = strings.TrimRight(t.Dest, "/") + "/" + l.VaultID
			}
			if err := r.SyncLeaf(ctx, dir, dest); err != nil {
				return err
			}
			res.Leaves = append(res.Leaves, l.Leaf)
			res.Snapshots = append(res.Snapshots, l.Snapshots...)
		}
		if t.Station == nil {
			res.Checks = checkCopied(ctx, r, p, t, res)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
