package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"valise/client/ssh"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Pulling only what one escape pod needs (review F03, 2026-11-03): the
// period's small files first (VAULT, epochs, keys, indexes, snapshot
// headers and meta), then, with the key, the packs holding the pod's file
// list, then the packs holding its files. Packs already here are reused
// after their SHA-256 is checked again; downloaded ones are checked as
// they land; every file is still checked against its SHA-256 when it is
// written. Such a copy is restore material for that pod, marked partial
// next to the vault directory, never a full period copy: repositories and
// standalone restore keep copying whole periods.

// Stages of pulling one pod: the period's small files, then its packs.
const (
	StagePullMeta  = "pull_meta"
	StagePullPacks = "pull_packs"
)

// SnapshotPull reports what pulling one pod took.
type SnapshotPull struct {
	Dir     string `json:"dir"`
	VaultID string `json:"vault_id"`
	// PeriodBytes is every file of the period on the server; PodBytes the
	// pod's plain size; PackBytes the packs it uses; Downloaded what came
	// over the network this time; Reused the packs already here.
	PeriodBytes int64 `json:"period_bytes"`
	PodBytes    int64 `json:"pod_bytes"`
	PackBytes   int64 `json:"pack_bytes"`
	Downloaded  int64 `json:"downloaded_bytes"`
	Reused      int64 `json:"reused_bytes"`
	Packs       int   `json:"packs"`
	// Partial: this cache has not been verified as a complete period.
	Partial bool `json:"partial"`
}

// partialFile marks a cache directory holding only some pods' material.
func partialFile(dir string) string { return dir + ".partial.json" }

// PullSnapshot pulls what snapshot needs from leaf on host into the
// period's cache directory and opens it.
func (s *Service) PullSnapshot(ctx context.Context, host, leaf, snapshot string, id age.Identity) (*vault.Vault, *SnapshotPull, error) {
	t, _, err := s.transport(host)
	if err != nil {
		return nil, nil, err
	}
	found, err := s.porterOn(ctx, t, s.Tiered(host))
	if err != nil {
		return nil, nil, err
	}
	c, _, err := ssh.Dial(ctx, t, found.Path, s.Version)
	if err != nil {
		return nil, nil, err
	}
	var list vaultrpc.FilesResult
	err = c.Call(ctx, vaultrpc.MethodFiles, vaultrpc.FilesParams{Leaf: leaf}, &list)
	c.Close()
	if err != nil {
		return nil, nil, err
	}
	return s.pullFrom(ctx, list, snapshot, id, func(f vaultrpc.VaultFile, local string, w io.Writer) error {
		return s.fetch(ctx, t, found.Path, leaf, f, local, w)
	})
}

// pullFrom fills the period's cache with what snapshot needs, fetching
// each missing file with fetch (from a server or a copy, docs/ARCHITECTURE.md
// 8.2.3); list is the period's files with their sizes.
func (s *Service) pullFrom(ctx context.Context, list vaultrpc.FilesResult, snapshot string, id age.Identity,
	fetch func(f vaultrpc.VaultFile, local string, w io.Writer) error) (*vault.Vault, *SnapshotPull, error) {
	dir := s.DefaultPullDir(list.VaultID)
	if b, err := os.ReadFile(filepath.Join(dir, vault.DescriptorFile)); err == nil && !strings.Contains(string(b), `"`+list.VaultID+`"`) {
		return nil, nil, valise.New("pull_wrong_vault", "本机缓存目录里已经是另一个备份库", "删除 "+dir+" 后重试")
	}
	res := &SnapshotPull{Dir: dir, VaultID: list.VaultID, Partial: true}
	// Persist the conservative state before changing any cache files. A
	// failed or interrupted pull must never leave an unmarked partial copy.
	if err := markPartial(dir, "", true); err != nil {
		return nil, res, err
	}
	byPath := map[string]vaultrpc.VaultFile{}
	var meta []vaultrpc.VaultFile
	for _, f := range list.Files {
		if !vault.ValidRel(f.Path) {
			continue
		}
		byPath[f.Path] = f
		res.PeriodBytes += f.Size
		if !strings.HasPrefix(f.Path, vault.PacksDir+"/") {
			meta = append(meta, f)
		}
	}
	sort.SliceStable(meta, func(i, j int) bool { return pullRank(meta[i].Path) < pullRank(meta[j].Path) })
	var total, done int64
	for _, f := range meta {
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil || st.Size() != f.Size {
			total += f.Size
		}
	}
	report(ctx, StagePullMeta, 0, total)
	for _, f := range meta {
		local := filepath.Join(dir, filepath.FromSlash(f.Path))
		if st, err := os.Stat(local); err == nil && st.Size() == f.Size {
			continue
		}
		if err := fetch(f, local, &countingWriter{ctx: ctx, stage: StagePullMeta, done: &done, total: total, w: io.Discard}); err != nil {
			return nil, res, err
		}
		res.Downloaded += f.Size
	}
	v, err := vault.Open(dir)
	if err != nil {
		return nil, res, err
	}
	for _, m := range v.Metas {
		if m.SnapshotID == snapshot {
			res.PodBytes = m.PlainBytes
		}
	}
	report(ctx, StageOpen, 0, 0)
	head, err := v.SnapshotHeader(snapshot, id)
	if err != nil {
		return nil, res, valise.New("snapshot_unreadable", "无法解密备份 "+snapshot, "确认本机钥匙是这个备份的收件人").WithDetail(err.Error())
	}
	used := map[string]bool{}
	ensure := func(blobs []string) error {
		packs, unknown := v.PacksOf(blobs)
		if len(unknown) > 0 {
			return valise.New("pull_blob_unknown", "备份用到的数据块不在这个周期的索引里", "在服务器上运行 valise vault verify；必要时取回整个周期").
				WithDetail(strings.Join(unknown[:min(3, len(unknown))], " "))
		}
		var missing []vaultrpc.VaultFile
		var need int64
		for _, p := range packs {
			if used[p] {
				continue
			}
			used[p] = true
			rel := vault.PackPath(p)
			f, ok := byPath[rel]
			if !ok {
				return valise.New("pull_pack_missing", "来源缺少备份用到的数据包 "+p[:12], "换一个来源，或在服务器上运行 valise vault verify")
			}
			res.Packs++
			res.PackBytes += f.Size
			local := filepath.Join(dir, filepath.FromSlash(rel))
			if packIntact(local, p) {
				res.Reused += f.Size
				continue
			}
			missing = append(missing, f)
			need += f.Size
		}
		var got int64
		report(ctx, StagePullPacks, 0, need)
		for _, f := range missing {
			local := filepath.Join(dir, filepath.FromSlash(f.Path))
			_ = os.Remove(local) // a damaged copy is fetched again
			if err := fetch(f, local, &countingWriter{ctx: ctx, stage: StagePullPacks, done: &got, total: need, w: io.Discard}); err != nil {
				return err
			}
			res.Downloaded += f.Size
		}
		return nil
	}
	if err := ensure(head.FilesBlobs); err != nil {
		return nil, res, err
	}
	_, files, err := v.Snapshot(snapshot, id)
	if err != nil {
		return nil, res, valise.New("snapshot_unreadable", "无法读出备份 "+snapshot+" 的文件清单", "取回整个周期后再试").WithDetail(err.Error())
	}
	var blobs []string
	for _, f := range files.Files {
		blobs = append(blobs, f.Blobs...)
	}
	if err := ensure(blobs); err != nil {
		return nil, res, err
	}
	if err := markPartial(dir, snapshot, true); err != nil {
		return nil, res, err
	}
	return v, res, nil
}

// packIntact reports a local pack whose SHA-256 is its id.
func packIntact(path, id string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == id
}

type partialMark struct {
	Note      string    `json:"note"`
	Snapshots []string  `json:"snapshots"`
	Updated   time.Time `json:"updated"`
}

// markPartial records which pods a partial cache can restore, or removes
// the mark once the period is complete.
func markPartial(dir, snapshot string, partial bool) (err error) {
	defer func() {
		if err != nil {
			err = valise.New("pull_cache_state", "无法保存本机缓存的完整性状态", "检查缓存目录的权限和剩余空间后重试").WithDetail(err.Error())
		}
	}()
	path := partialFile(dir)
	if !partial {
		err = os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var m partialMark
	if b, readErr := os.ReadFile(path); readErr == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	m.Note = "这是尚未通过完整周期校验的本机缓存；下面仅列出已成功取回恢复材料的备份，清单为空时可能仍在取回或已中断"
	if snapshot != "" && !containsStr(m.Snapshots, snapshot) {
		m.Snapshots = append(m.Snapshots, snapshot)
	}
	m.Updated = time.Now().UTC()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".partial-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Summary says what pulling the pod took, "" for none.
func (t *SnapshotPull) Summary() string {
	if t == nil {
		return ""
	}
	line := fmt.Sprintf("取回：周期共 %s，这个备份 %s，用到 %d 个数据包 %s；这次下载 %s，复用本机已有 %s",
		sizeText(t.PeriodBytes), sizeText(t.PodBytes), t.Packs, sizeText(t.PackBytes), sizeText(t.Downloaded), sizeText(t.Reused))
	if t.Partial {
		line += "（本机缓存是这个备份的恢复材料，尚未通过完整周期校验）"
	}
	return line
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
