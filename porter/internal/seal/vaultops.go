package seal

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Status describes the vault without any key.
func Status(p Paths) (*vaultrpc.StatusResult, error) {
	res := &vaultrpc.StatusResult{Snapshots: []vault.Meta{}, Targets: []vaultrpc.TargetState{}, Recipients: []string{}}
	rec, err := ReadRecipients(p)
	if err != nil {
		return nil, err
	}
	for _, r := range rec {
		res.Recipients = append(res.Recipients, vault.Fingerprint(r))
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
	if _, err := os.Stat(filepath.Join(p.Vault(), vault.DescriptorFile)); errors.Is(err, os.ErrNotExist) {
		return res, nil
	}
	v, err := vault.Open(p.Vault())
	if err != nil {
		return nil, err
	}
	res.Exists, res.VaultID, res.Epochs = true, v.Desc.VaultID, len(v.Epochs)
	if v.Metas != nil {
		res.Snapshots = v.Metas
	}
	return res, nil
}

// Verify runs the no-key verification.
func Verify(p Paths) (*vaultrpc.VerifyResult, error) {
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	probs, err := vault.VerifyNoKey(p.Vault())
	if errors.Is(err, os.ErrNotExist) {
		return nil, valise.New("vault_missing", "服务器上还没有金库", "先封存一次：valise seal <服务器>")
	}
	if probs == nil {
		probs = []vault.Problem{}
	}
	return &vaultrpc.VerifyResult{Problems: probs}, err
}

// Files lists every valid vault file (temporaries excluded).
func Files(p Paths) (*vaultrpc.FilesResult, error) {
	v, err := vault.Open(p.Vault())
	if errors.Is(err, os.ErrNotExist) {
		return nil, valise.New("vault_missing", "服务器上还没有金库", "先封存一次：valise seal <服务器>")
	}
	if err != nil {
		return nil, err
	}
	res := &vaultrpc.FilesResult{VaultID: v.Desc.VaultID, Files: []vaultrpc.VaultFile{}}
	err = filepath.WalkDir(p.Vault(), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(p.Vault(), path)
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

// OpenVaultFile opens a vault file for the data plane. Only paths matching
// the vault layout are served.
func OpenVaultFile(p Paths, rel string) (*os.File, error) {
	if !vault.ValidRel(rel) {
		return nil, errors.New("not a vault file: " + rel)
	}
	return os.Open(filepath.Join(p.Vault(), filepath.FromSlash(rel)))
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

// Sync copies the vault to a target with rsync, skipping existing files.
// The destination is <dest>/<vault_id>/ so several servers can share one
// target. The server's own SSH configuration supplies credentials.
func Sync(ctx context.Context, e *porterenroll.Env, p Paths, name, initiator string) (res *vaultrpc.SyncResult, err error) {
	ts, err := readTargets(p)
	if err != nil {
		return nil, err
	}
	var t *vaultrpc.Target
	for i := range ts {
		if ts[i].Name == name {
			t = &ts[i]
		}
	}
	if t == nil {
		return nil, valise.New("target_unknown", "服务器上没有名为 "+name+" 的同步目标", "先添加：valise target add <服务器> "+name+" --rsync <主机:路径>")
	}
	st, err := Status(p)
	if err != nil {
		return nil, err
	}
	if !st.Exists {
		return nil, valise.New("vault_missing", "服务器上还没有金库", "先封存一次：valise seal <服务器>")
	}
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	dest := strings.TrimRight(t.Dest, "/") + "/" + st.VaultID
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
	ssh := "ssh -o BatchMode=yes"
	for _, step := range syncOrder {
		src := filepath.Join(p.Vault(), step.dir)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		args := []string{"rsync", "-rt", "--ignore-existing", "--mkpath", "--chmod=D700,F600", "-e", ssh,
			"--exclude", "tmp-*", "--exclude", ".tmp-*"}
		if step.include != "" {
			args = append(args, "--include", "*/", "--include", step.include, "--exclude", "*")
		}
		args = append(args, src+"/", dest+"/"+step.dir+"/")
		out, err := e.Run(ctx, syncTimeout, p.Home, args...)
		if err != nil {
			return nil, valise.New("sync_failed", "无法运行 rsync", "确认服务器上安装了 rsync").WithDetail(err.Error())
		}
		if out.Code != 0 {
			return nil, valise.New("sync_failed", "同步到 "+t.Name+" 失败",
				"确认服务器能用 SSH 登录 "+strings.SplitN(t.Dest, ":", 2)[0]+"（凭据由你在服务器上配置），然后重试；金库只追加，可以断点续传").
				WithDetail(lastLines(string(out.Stderr), 5))
		}
	}
	out, err := e.Run(ctx, syncTimeout, p.Home, "rsync", "-t", "--ignore-existing", "--chmod=F600", "-e", ssh,
		filepath.Join(p.Vault(), vault.DescriptorFile), dest+"/"+vault.DescriptorFile)
	if err != nil || out.Code != 0 {
		return nil, valise.New("sync_failed", "同步 VAULT 文件失败", "检查网络后重试")
	}
	res = &vaultrpc.SyncResult{Target: t.Name, Dest: dest, Snapshots: []string{}}
	for _, m := range st.Snapshots {
		res.Snapshots = append(res.Snapshots, m.SnapshotID)
	}
	return res, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
