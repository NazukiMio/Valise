package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"filippo.io/age"

	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Copies that stand in for a server that cannot be reached
// (docs/ARCHITECTURE.md 8.2.3): a local repository, a folder the person
// chose, or a cloud logged in on this computer. A source is read only;
// one restore never mixes two sources.

// CopySource is one parsed --source.
type CopySource struct {
	Spec   string `json:"spec"`
	Kind   string `json:"kind"` // repo, dir or cloud
	Name   string `json:"name,omitempty"`
	Server string `json:"server"` // the server's folder name in the copy
	Dir    string `json:"dir,omitempty"`
}

// Label names the source in plans and messages.
func (c *CopySource) Label() string {
	switch c.Kind {
	case "repo":
		return "本机仓库 " + c.Name + "（" + c.Dir + "）"
	case "dir":
		return "本机文件夹 " + c.Dir
	}
	return "云盘 " + c.Name + "（这台电脑登录）"
}

// ParseCopySource reads repo:<名>[/<服务器名>], dir:<路径> or
// cloud:<名>[/<服务器名>]; the server name defaults to host.
func (s *Service) ParseCopySource(host, spec string) (*CopySource, error) {
	kind, rest, ok := strings.Cut(spec, ":")
	if !ok || rest == "" {
		return nil, valise.New("source_invalid", "无法识别的来源："+spec, "写成 repo:<仓库名>、dir:<文件夹路径> 或 cloud:<云盘名>")
	}
	c := &CopySource{Spec: spec, Kind: kind, Server: host}
	switch kind {
	case "repo", "cloud":
		name, server, _ := strings.Cut(rest, "/")
		c.Name = name
		if server != "" {
			c.Server = server
		}
		if !repoNameRe.MatchString(c.Server) && !validServerFolder(c.Server) {
			return nil, valise.New("source_invalid", "服务器名无效："+c.Server, "")
		}
		if kind == "cloud" {
			if !s.IsCloudHere(name) {
				return nil, valise.New("cloud_unknown", "这台电脑没有登录云盘 "+name, "先登录：valise cloud add --here")
			}
			return c, nil
		}
		r, err := s.repo(name)
		if err != nil {
			return nil, err
		}
		c.Dir = filepath.Join(r.Path, c.Server)
		if !hasTree(c.Dir) {
			return nil, valise.New("source_server_missing", "仓库 "+name+" 里没有服务器 "+c.Server+" 的备份", s.serversIn(r.Path, "repo:"+name))
		}
	case "dir":
		c.Dir = filepath.Clean(expandHome(rest))
		if !filepath.IsAbs(c.Dir) {
			return nil, valise.New("source_invalid", "请写文件夹的完整路径", "例如 dir:/Volumes/USB/valise-vault/ubuntu-vm")
		}
		b, err := os.ReadFile(filepath.Join(c.Dir, vault.TreeFile))
		var tree vault.TreeDesc
		if err != nil || json.Unmarshal(b, &tree) != nil || tree.ServerName == "" {
			return nil, valise.New("source_not_tree", c.Dir+" 不是一台服务器的备份文件夹（缺少 TREE）", s.serversIn(c.Dir, "dir:"+c.Dir))
		}
		c.Server = tree.ServerName
	default:
		return nil, valise.New("source_invalid", "无法识别的来源："+spec, "写成 repo:<仓库名>、dir:<文件夹路径> 或 cloud:<云盘名>")
	}
	return c, nil
}

func validServerFolder(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

func hasTree(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, vault.TreeFile))
	return err == nil
}

// serversIn suggests the server folders found under dir.
func (s *Service) serversIn(dir, prefix string) string {
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		if e.IsDir() && hasTree(filepath.Join(dir, e.Name())) {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "选择含有 TREE 文件的服务器文件夹"
	}
	if strings.HasPrefix(prefix, "dir:") {
		return "这里有：" + strings.Join(names, "、") + "；写成 dir:" + filepath.Join(dir, names[0])
	}
	return "这里有：" + strings.Join(names, "、") + "；写成 " + prefix + "/" + names[0]
}

// cloudBase is where a cloud keeps the server's periods.
func (c *CopySource) cloudBase() string {
	return vaultrpc.CloudRemote(c.Name) + ":" + cloudFolder + c.Server
}

// SourceStatus lists a copy's periods and backups without a key, shaped
// like the server's status (Exists, ServerName, Leaves, Snapshots).
func (s *Service) SourceStatus(ctx context.Context, c *CopySource) (*vaultrpc.StatusResult, error) {
	if c.Kind == "cloud" {
		return s.cloudSourceStatus(ctx, c)
	}
	st := &vaultrpc.StatusResult{ServerName: c.Server}
	for _, leaf := range localLeaves(c.Dir) {
		v, err := vault.Open(filepath.Join(c.Dir, leafDirName(leaf)))
		if err != nil {
			continue
		}
		addLeaf(st, leaf, v.Desc.VaultID, v.Metas)
	}
	st.Exists = len(st.Leaves) > 0
	return st, nil
}

// localLeaves finds <inst>/<period> folders holding a VAULT, and legacy.
func localLeaves(dir string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(dir, "legacy", vault.DescriptorFile)); err == nil {
		out = append(out, vault.LegacyLeaf)
	}
	vaults, _ := filepath.Glob(filepath.Join(dir, "*", "*", vault.DescriptorFile))
	for _, v := range vaults {
		rel, _ := filepath.Rel(dir, filepath.Dir(v))
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

func addLeaf(st *vaultrpc.StatusResult, leaf, vaultID string, metas []vault.Meta) {
	ls := vaultrpc.LeafState{Leaf: leaf, VaultID: vaultID, Snapshots: []string{}}
	for _, m := range metas {
		ls.Snapshots = append(ls.Snapshots, m.SnapshotID)
		ls.Bytes += m.PlainBytes
		st.Snapshots = append(st.Snapshots, m)
	}
	st.Leaves = append(st.Leaves, ls)
}

// cloudSourceStatus lists the cloud and reads each period's VAULT and
// meta files (small) through a temporary folder removed afterwards.
func (s *Service) cloudSourceStatus(ctx context.Context, c *CopySource) (*vaultrpc.StatusResult, error) {
	st := &vaultrpc.StatusResult{ServerName: c.Server}
	station, err := s.hereStation(ctx, c.Name)
	if err != nil {
		return nil, err
	}
	tmp, err := s.sourceTemp()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	_, _, err = stationsync.With(ctx, localRunner{}, station, func(r stationsync.Remote) error {
		entries, err := r.List(ctx, c.cloudBase())
		if err != nil {
			return err
		}
		var want []string
		for _, e := range entries {
			if e.Dir {
				continue
			}
			if path.Base(e.Path) == vault.DescriptorFile || (strings.HasSuffix(e.Path, ".meta.json") && path.Base(path.Dir(e.Path)) == vault.SnapshotsDir) {
				want = append(want, e.Path)
			}
		}
		return r.Fetch(ctx, c.cloudBase(), want, tmp)
	})
	if err != nil {
		return nil, err
	}
	for _, leaf := range localLeaves(tmp) {
		dir := filepath.Join(tmp, leafDirName(leaf))
		var desc vault.Descriptor
		b, err := os.ReadFile(filepath.Join(dir, vault.DescriptorFile))
		if err != nil || json.Unmarshal(b, &desc) != nil {
			continue
		}
		metas, _ := filepath.Glob(filepath.Join(dir, vault.SnapshotsDir, "*.meta.json"))
		var ms []vault.Meta
		for _, p := range metas {
			var m vault.Meta
			if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &m) == nil {
				ms = append(ms, m)
			}
		}
		addLeaf(st, leaf, desc.VaultID, ms)
	}
	st.Exists = len(st.Leaves) > 0
	return st, nil
}

func (s *Service) sourceTemp() (string, error) {
	if err := os.MkdirAll(filepath.Join(s.Dirs.Cache, "tmp"), 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(filepath.Join(s.Dirs.Cache, "tmp"), "source-")
}

// openFromSource opens snapshot's period from the copy. A repository or
// folder is read in place, every pack the backup uses checked by hash; a
// cloud fills the restore cache with only what the backup needs.
func (s *Service) openFromSource(ctx context.Context, c *CopySource, snapshot string, id age.Identity) (*vault.Vault, *SnapshotPull, *vaultrpc.StatusResult, error) {
	st, err := s.SourceStatus(ctx, c)
	if err != nil {
		return nil, nil, nil, err
	}
	leaf := st.LeafOf(snapshot)
	if leaf == "" {
		return nil, nil, st, valise.New("snapshot_unknown", c.Label()+" 里没有服务器 "+c.Server+" 的备份 "+snapshot,
			"用 valise vault status <服务器> --source "+c.Spec+" 列出这个来源里的备份")
	}
	if c.Kind == "cloud" {
		v, pull, err := s.pullFromCloud(ctx, c, leaf, snapshot, id)
		return v, pull, st, err
	}
	dir := filepath.Join(c.Dir, leafDirName(leaf))
	v, err := vault.Open(dir)
	if err != nil {
		return nil, nil, st, valise.New("source_unreadable", "无法读取 "+c.Label()+" 里的周期 "+leaf, "换一个来源").WithDetail(err.Error())
	}
	pull := &SnapshotPull{Dir: dir, VaultID: v.Desc.VaultID, PeriodBytes: dirBytes(dir)}
	for _, m := range v.Metas {
		if m.SnapshotID == snapshot {
			pull.PodBytes = m.PlainBytes
		}
	}
	// Packs are checked before anything is decrypted from them: the file
	// list itself lives in a pack.
	checked := map[string]bool{}
	check := func(blobs []string) error {
		packs, unknown := v.PacksOf(blobs)
		if len(unknown) > 0 {
			return valise.New("pull_blob_unknown", "备份用到的数据块不在这个副本的索引里", "换一个来源").WithDetail(strings.Join(unknown[:min(3, len(unknown))], " "))
		}
		var bad []string
		for _, p := range packs {
			if checked[p] {
				continue
			}
			checked[p] = true
			local := filepath.Join(dir, filepath.FromSlash(vault.PackPath(p)))
			fi, err := os.Stat(local)
			if err != nil || !packIntact(local, p) {
				bad = append(bad, p[:12])
				continue
			}
			pull.Packs++
			pull.PackBytes += fi.Size()
		}
		if len(bad) > 0 {
			return valise.New("source_packs_bad", fmt.Sprintf("%s 里这个备份用到的 %d 个数据包缺失或损坏", c.Label(), len(bad)),
				"换一个来源；这个副本需要重新同步").WithDetail(strings.Join(bad, " "))
		}
		return nil
	}
	head, err := v.SnapshotHeader(snapshot, id)
	if err != nil {
		return nil, pull, st, valise.New("snapshot_unreadable", "无法解密备份 "+snapshot, "确认本机钥匙是这个备份的收件人").WithDetail(err.Error())
	}
	if err := check(head.FilesBlobs); err != nil {
		return nil, pull, st, err
	}
	_, files, err := v.Snapshot(snapshot, id)
	if err != nil {
		return nil, pull, st, valise.New("snapshot_unreadable", "无法读出备份 "+snapshot+" 的文件清单", "换一个来源").WithDetail(err.Error())
	}
	var blobs []string
	for _, f := range files.Files {
		blobs = append(blobs, f.Blobs...)
	}
	if err := check(blobs); err != nil {
		return nil, pull, st, err
	}
	pull.Reused = pull.PackBytes
	return v, pull, st, nil
}

// pullFromCloud fetches the period's file list and fills the restore cache
// through this computer's rclone.
func (s *Service) pullFromCloud(ctx context.Context, c *CopySource, leaf, snapshot string, id age.Identity) (*vault.Vault, *SnapshotPull, error) {
	station, err := s.hereStation(ctx, c.Name)
	if err != nil {
		return nil, nil, err
	}
	base := c.cloudBase() + "/" + leaf
	var v *vault.Vault
	var pull *SnapshotPull
	_, _, err = stationsync.With(ctx, localRunner{}, station, func(r stationsync.Remote) error {
		entries, err := r.List(ctx, base)
		if err != nil {
			return err
		}
		list := vaultrpc.FilesResult{Leaf: leaf}
		for _, e := range entries {
			if !e.Dir && vault.ValidRel(e.Path) {
				list.Files = append(list.Files, vaultrpc.VaultFile{Path: e.Path, Size: e.Size})
			}
		}
		tmp, err := s.sourceTemp()
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err := r.Fetch(ctx, base, []string{vault.DescriptorFile}, tmp); err != nil {
			return err
		}
		var desc vault.Descriptor
		if b, err := os.ReadFile(filepath.Join(tmp, vault.DescriptorFile)); err != nil || json.Unmarshal(b, &desc) != nil || desc.VaultID == "" {
			return valise.New("source_unreadable", "云盘 "+c.Name+" 上的周期 "+leaf+" 缺少 VAULT", "换一个来源")
		}
		list.VaultID = desc.VaultID
		v, pull, err = s.pullFrom(ctx, list, snapshot, id, func(f vaultrpc.VaultFile, local string, _ io.Writer) error {
			root := strings.TrimSuffix(local, filepath.FromSlash(f.Path))
			return r.Fetch(ctx, base, []string{f.Path}, root)
		})
		return err
	})
	return v, pull, err
}

// SourceUnreachable reports an error that means the server cannot be read
// at all: no connection, a refused login, or a changed host key (perhaps a
// reinstalled server). Reading a copy never needs that server.
func SourceUnreachable(err error) bool {
	ue, ok := valise.As(err)
	return ok && (ue.Code == "ssh_connect_failed" || ue.Code == "ssh_auth_failed" || ue.Code == "host_key_mismatch")
}

// CopyCandidate is a copy this computer knows that may hold a backup.
type CopyCandidate struct {
	Spec  string `json:"spec"`
	Label string `json:"label"`
	Has   bool   `json:"has"`
	Note  string `json:"note,omitempty"`
}

// CopiesHolding checks the repositories (local files only) and the
// clouds logged in here (a network listing) for snapshot of host.
func (s *Service) CopiesHolding(ctx context.Context, host, snapshot string) []CopyCandidate {
	var out []CopyCandidate
	check := func(spec string) {
		c, err := s.ParseCopySource(host, spec)
		if err != nil {
			out = append(out, CopyCandidate{Spec: spec, Label: spec, Note: errText1(err)})
			return
		}
		cand := CopyCandidate{Spec: spec, Label: c.Label()}
		st, err := s.SourceStatus(ctx, c)
		switch {
		case err != nil:
			cand.Note = "读不到：" + errText1(err)
		case snapshot == "":
			cand.Has = st.Exists
		default:
			cand.Has = st.LeafOf(snapshot) != ""
		}
		out = append(out, cand)
	}
	for _, r := range s.Config.Repos {
		check("repo:" + r.Name)
	}
	for _, c := range s.Config.Clouds {
		check("cloud:" + c.Name)
	}
	return out
}

// unreachableWithCopies stops a restore whose server cannot be reached and
// lists the copies that hold the backup; nothing switches by itself.
func (s *Service) unreachableWithCopies(ctx context.Context, host, snapshot string, cause error) error {
	var have []string
	for _, c := range s.CopiesHolding(ctx, host, snapshot) {
		if c.Has {
			have = append(have, c.Spec+"（"+c.Label+"）")
		}
	}
	reason := "连不上 " + host + "：" + errText1(cause)
	if len(have) == 0 {
		return valise.New("source_unreachable", reason+"；这台电脑知道的副本里也没有这个备份",
			"有从 NAS 或网盘下载下来的副本时，用 --source dir:<服务器文件夹> 指定")
	}
	return valise.New("source_unreachable", reason+"；这些副本有这个备份："+strings.Join(have, "、"),
		"选一个加上 --source 重新运行，例如 --source "+strings.SplitN(have[0], "（", 2)[0])
}

// dirBytes is the size of the regular files under dir (a period copy).
func dirBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
