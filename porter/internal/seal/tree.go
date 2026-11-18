package seal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"valise/core/enroll"
	"valise/core/valise"
	"valise/core/vault"
)

// Vault tree paths (docs/VAULT_FORMAT.md section 10).

// now is the clock for choosing leaves; tests move it.
var now = time.Now

// Vaults is the server's vault tree.
func (p Paths) Vaults() string { return filepath.Join(p.Home, ".local", "share", "valise", "vaults") }

// LegacyVault is the vault from before the tree.
func (p Paths) LegacyVault() string {
	return filepath.Join(p.Home, ".local", "share", "valise", "vault")
}

// ServerFile is ~/.config/valise/server.json.
func (p Paths) ServerFile() string { return filepath.Join(p.Home, ".config", "valise", "server.json") }

// PeriodSettingFile is instances/<id>.vault.json.
func (p Paths) PeriodSettingFile(instance string) string {
	return filepath.Join(p.Home, ".config", "valise", "instances", instance+".vault.json")
}

// LeafDir is the directory of a leaf name (validated).
func (p Paths) LeafDir(leaf string) (string, error) {
	inst, period, ok := vault.SplitLeaf(leaf)
	switch {
	case !ok:
		return "", valise.New("leaf_invalid", "无效的周期："+leaf, "使用 valise vault status 列出的名称")
	case leaf == vault.LegacyLeaf:
		return p.LegacyVault(), nil
	}
	return filepath.Join(p.Vaults(), inst, period), nil
}

// ServerInfo is server.json.
type ServerInfo struct {
	Name string `json:"name"`
	// RemovedAt is set when the user removed the server from Valise and
	// cleaned it; the backup library stays (docs/ARCHITECTURE.md 2.3).
	RemovedAt *time.Time `json:"removed_at,omitempty"`
}

func readServerInfo(p Paths) (ServerInfo, error) {
	var si ServerInfo
	b, err := os.ReadFile(p.ServerFile())
	if errors.Is(err, os.ErrNotExist) {
		return si, nil
	}
	if err != nil {
		return si, err
	}
	if err := json.Unmarshal(b, &si); err != nil {
		return si, valise.New("server_file_invalid", "服务器上的 server.json 无法解析", "检查 ~/.config/valise/server.json").WithDetail(err.Error())
	}
	return si, nil
}

func writeServerInfo(p Paths, si ServerInfo) error {
	b, _ := json.MarshalIndent(si, "", "  ")
	return writeAtomic(p.ServerFile(), append(b, '\n'), 0o600)
}

// MarkServerRemoved writes removed_at into server.json; the backup library
// and the name stay. A server without a name has nothing to mark.
func MarkServerRemoved(p Paths, at time.Time) error {
	si, err := readServerInfo(p)
	if err != nil || si.Name == "" {
		return err
	}
	at = at.UTC().Truncate(time.Second)
	si.RemovedAt = &at
	return writeServerInfo(p, si)
}

// ReadServerName returns the server name, "" when not set yet.
func ReadServerName(p Paths) (string, error) {
	b, err := os.ReadFile(p.ServerFile())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var si ServerInfo
	if err := json.Unmarshal(b, &si); err != nil {
		return "", valise.New("server_file_invalid", "服务器上的 server.json 无法解析", "检查 ~/.config/valise/server.json").WithDetail(err.Error())
	}
	return si.Name, nil
}

// SetServerName records the server name once; it never changes afterwards
// because it names the cloud backup folder.
func SetServerName(p Paths, name string) error {
	if err := enroll.ValidInstanceID(name); err != nil {
		return valise.New("server_name_invalid", "服务器名只能包含小写字母、数字和连字符（最长 64 个字符）", "例如 home-vps")
	}
	cur, err := ReadServerName(p)
	if err != nil {
		return err
	}
	if cur == name {
		// Adding the server again clears the removed mark.
		if si, err := readServerInfo(p); err == nil && si.RemovedAt != nil {
			si.RemovedAt = nil
			return writeServerInfo(p, si)
		}
		return nil
	}
	if cur != "" {
		return valise.New("server_name_set", "这台服务器已经叫 "+cur, "要改名请用 valise server rename（会一并改备份位置里的文件夹）")
	}
	b, _ := json.MarshalIndent(ServerInfo{Name: name}, "", "  ")
	if err := writeAtomic(p.ServerFile(), append(b, '\n'), 0o600); err != nil {
		return err
	}
	return ensureTree(p)
}

// RenameServer renames a named server: server.json, and TREE records the
// old name (docs/VAULT_FORMAT.md 10). Copies elsewhere are the client's.
func RenameServer(p Paths, name string) (string, error) {
	if err := enroll.ValidInstanceID(name); err != nil {
		return "", valise.New("server_name_invalid", "服务器名只能包含小写字母、数字和连字符（最长 64 个字符）", "例如 home-vps")
	}
	cur, err := ReadServerName(p)
	switch {
	case err != nil:
		return "", err
	case cur == "":
		return "", valise.New("server_name_unset", "这台服务器还没有名字", "先起名：valise server name <服务器> <名字>")
	case cur == name:
		return cur, nil
	}
	b, _ := json.MarshalIndent(ServerInfo{Name: name}, "", "  ")
	if err := writeAtomic(p.ServerFile(), append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	path := filepath.Join(p.Vaults(), vault.TreeFile)
	d := vault.TreeDesc{Format: vault.TreeFormat, CreatedAt: time.Now().UTC()}
	if old, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(old, &d)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	d.ServerName, d.RenamedFrom = name, append(d.RenamedFrom, cur)
	if err := os.MkdirAll(p.Vaults(), 0o700); err != nil {
		return "", err
	}
	tb, _ := json.MarshalIndent(d, "", "  ")
	return cur, writeAtomic(path, append(tb, '\n'), 0o600)
}

// ensureTree writes TREE once the server has a name.
func ensureTree(p Paths) error {
	name, err := ReadServerName(p)
	if err != nil || name == "" {
		return err
	}
	path := filepath.Join(p.Vaults(), vault.TreeFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(p.Vaults(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(vault.TreeDesc{Format: vault.TreeFormat, ServerName: name, CreatedAt: time.Now().UTC()}, "", "  ")
	return writeAtomic(path, append(b, '\n'), 0o600)
}

// ReadPeriodSetting returns the instance's period setting or the default.
func ReadPeriodSetting(p Paths, instance string) (vault.PeriodSetting, error) {
	b, err := os.ReadFile(p.PeriodSettingFile(instance))
	if errors.Is(err, os.ErrNotExist) {
		return vault.DefaultPeriodSetting, nil
	}
	if err != nil {
		return vault.PeriodSetting{}, err
	}
	var s vault.PeriodSetting
	if err := json.Unmarshal(b, &s); err != nil || s.Validate() != nil {
		return vault.PeriodSetting{}, valise.New("period_setting_invalid", "实例 "+instance+" 的周期设置无效", "在实例设置中重新选择周期")
	}
	return s, nil
}

// SetPeriodSetting stores a setting; it applies from the next leaf on.
func SetPeriodSetting(p Paths, instance string, s vault.PeriodSetting) error {
	if err := enroll.ValidInstanceID(instance); err != nil {
		return valise.New("instance_invalid", err.Error(), "检查实例名")
	}
	if err := s.Validate(); err != nil {
		return valise.New("period_setting_invalid", err.Error(), "周期可选 week、month、quarter")
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return writeAtomic(p.PeriodSettingFile(instance), append(b, '\n'), 0o600)
}

// LeafInfo is one leaf found on disk.
type LeafInfo struct {
	Leaf     string
	Dir      string
	Period   *vault.Period // nil for the legacy and permanent leaves
	Deleting bool
}

// Leaves lists every leaf: the legacy vault first (when present), then the
// tree's leaves sorted by instance and period.
func Leaves(p Paths) ([]LeafInfo, error) {
	var out []LeafInfo
	if _, err := os.Stat(filepath.Join(p.LegacyVault(), vault.DescriptorFile)); err == nil {
		out = append(out, leafInfo(vault.LegacyLeaf, p.LegacyVault(), nil))
	}
	insts, err := os.ReadDir(p.Vaults())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, in := range insts {
		if !in.IsDir() || enroll.ValidInstanceID(in.Name()) != nil {
			continue
		}
		periods, err := os.ReadDir(filepath.Join(p.Vaults(), in.Name()))
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, pe := range periods {
			if pe.IsDir() && (vault.ValidPeriodName(pe.Name()) || pe.Name() == vault.SavepointsPeriod) {
				names = append(names, pe.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			dir := filepath.Join(p.Vaults(), in.Name(), n)
			if n == vault.SavepointsPeriod {
				out = append(out, leafInfo(in.Name()+"/"+n, dir, nil))
				continue
			}
			var per *vault.Period
			if b, err := os.ReadFile(filepath.Join(dir, vault.PeriodFile)); err == nil {
				var pp vault.Period
				if json.Unmarshal(b, &pp) == nil && pp.Name() == n && pp.InstanceID == in.Name() {
					per = &pp
				}
			}
			if per == nil {
				// PERIOD is written first when a leaf is created; a leaf
				// without it is a crashed creation or a broken copy.
				per = &vault.Period{InstanceID: in.Name(), Start: n[:10], End: n[11:]}
			}
			out = append(out, leafInfo(in.Name()+"/"+n, dir, per))
		}
	}
	return out, nil
}

func leafInfo(leaf, dir string, per *vault.Period) LeafInfo {
	_, err := os.Stat(filepath.Join(dir, vault.DeletingFile))
	return LeafInfo{Leaf: leaf, Dir: dir, Period: per, Deleting: err == nil}
}

// leafForSeal picks (creating when needed) the leaf a seal at t writes to.
func leafForSeal(p Paths, instance string, t time.Time) (LeafInfo, error) {
	all, err := Leaves(p)
	if err != nil {
		return LeafInfo{}, err
	}
	var last *LeafInfo
	for i := range all {
		if all[i].Period != nil && all[i].Period.InstanceID == instance {
			last = &all[i]
		}
	}
	if last != nil {
		day := vault.PeriodSetting{TZ: last.Period.TZ}.LocalDay(t).Format("2006-01-02")
		if last.Period.TZ == "" {
			day = t.UTC().Format("2006-01-02")
		}
		if day <= last.Period.End {
			if last.Deleting {
				return LeafInfo{}, valise.New("leaf_deleting", "当前周期 "+last.Leaf+" 有一次未完成的删除", "先在空间清理中完成这次删除")
			}
			return *last, nil
		}
	}
	setting, err := ReadPeriodSetting(p, instance)
	if err != nil {
		return LeafInfo{}, err
	}
	var prev *vault.Period
	if last != nil {
		prev = last.Period
	}
	per := vault.NextPeriod(instance, prev, setting, t)
	dir := filepath.Join(p.Vaults(), instance, per.Name())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return LeafInfo{}, err
	}
	if err := ensureTree(p); err != nil {
		return LeafInfo{}, err
	}
	b, _ := json.MarshalIndent(per, "", "  ")
	if err := writeAtomic(filepath.Join(dir, vault.PeriodFile), append(b, '\n'), 0o600); err != nil {
		return LeafInfo{}, err
	}
	return LeafInfo{Leaf: per.Leaf(), Dir: dir, Period: &per}, nil
}

func savepointLeafForSeal(p Paths, instance string) (LeafInfo, error) {
	leaf := instance + "/" + vault.SavepointsPeriod
	dir, err := p.LeafDir(leaf)
	if err != nil {
		return LeafInfo{}, err
	}
	info := leafInfo(leaf, dir, nil)
	if info.Deleting {
		return LeafInfo{}, valise.New("leaf_deleting", "存档点目录带有删除标记", "核对服务器备份目录后重试")
	}
	if err := ensureTree(p); err != nil {
		return LeafInfo{}, err
	}
	return info, nil
}
