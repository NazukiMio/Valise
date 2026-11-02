package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"valise/client/config"
	"valise/client/ssh"
	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Stations Valise manages (docs/ARCHITECTURE.md section 7): the client
// keeps their address and its own key; each server gets its own key for
// direct copies. When a server cannot reach a station the client relays,
// from a repository or through a temporary folder.

// Stations lists the stations Valise manages.
func (s *Service) Stations() []config.Station {
	list, _ := config.LoadStations(s.Dirs.Data)
	return list
}

func (s *Service) station(name string) (*config.Station, error) {
	for _, st := range s.Stations() {
		if st.Name == name {
			return &st, nil
		}
	}
	return nil, valise.New("station_unknown", "没有叫 "+name+" 的空间站", "用 valise station list 查看")
}

// IsStation reports whether name is a station Valise manages.
func (s *Service) IsStation(name string) bool {
	_, err := s.station(name)
	return err == nil
}

// stationServer lets a station use the server add flow (host keys, the
// one bootstrap login).
func stationServer(st config.Station) config.Server {
	return config.Server{Name: config.StationAlias(st.Name), Source: config.SourceValise, HostName: st.Address,
		Port: st.PortOr22(), User: st.User, Login: st.Login, LoginKey: st.LoginKey}
}

func (s *Service) stationKey(name string) string {
	return filepath.Join(config.StationKeyDir(s.Dirs.Data, name), "client")
}

// StationScan reads a new station's host keys for the user to confirm.
func (s *Service) StationScan(ctx context.Context, st config.Station) ([]HostKey, error) {
	if err := st.Validate(); err != nil {
		return nil, stationInvalid(err)
	}
	return s.ScanHostKeys(ctx, stationServer(st))
}

func stationInvalid(err error) error {
	what := map[string]string{"name": "名字只能包含小写字母、数字和连字符", "address": "地址无效", "port": "端口无效",
		"user": "用户名无效", "path": "路径必须是绝对路径，只含字母、数字和 . _ - /"}[err.Error()]
	return valise.New("station_invalid", "空间站的"+what, "例如 名字 nas、地址 192.168.1.20、用户 valise、路径 /volume1/valise")
}

// StationAdd records a station after its host keys were confirmed, logs
// in once (password or the user's key; OpenSSH asks) to install the
// client's station key, and checks the key works.
func (s *Service) StationAdd(ctx context.Context, st config.Station, confirmed []HostKey) error {
	if err := st.Validate(); err != nil {
		return stationInvalid(err)
	}
	list := s.Stations()
	if slices.ContainsFunc(list, func(x config.Station) bool { return x.Name == st.Name }) {
		return valise.New("station_exists", "已经有一个叫 "+st.Name+" 的空间站", "换一个名字，或先移除旧的")
	}
	if err := s.TrustHostKeys(ctx, stationServer(st), confirmed); err != nil {
		return err
	}
	if err := config.SaveStations(s.Dirs.Data, append(slices.Clone(list), st)); err != nil {
		return err
	}
	undo := func() { _ = config.SaveStations(s.Dirs.Data, list) }
	key := s.stationKey(st.Name)
	if !fileExists(key) {
		if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
			undo()
			return err
		}
		if err := runTool(ctx, s.toolPath("ssh-keygen"), []string{"-q", "-t", "ed25519", "-N", "", "-C", "valise-station@" + localName(), "-f", key}, os.Environ()); err != nil {
			undo()
			return valise.New("keygen_failed", "无法生成空间站钥匙", "确认本机安装了 OpenSSH 的 ssh-keygen").WithDetail(err.Error())
		}
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		undo()
		return err
	}
	t, err := s.bootstrapTransport(stationServer(st))
	if err != nil {
		undo()
		return err
	}
	if err := s.installStationKey(ctx, t, string(pub)); err != nil {
		undo()
		return err
	}
	if _, err := s.stationTransport(st).Run(ctx, "true", nil); err != nil {
		undo()
		return valise.New("station_key_unusable", "空间站钥匙装好了，但用它登录失败", "检查空间站 SSH 是否允许公钥登录").WithDetail(err.Error())
	}
	return nil
}

// StationRemove forgets a station; nothing on it or on servers changes.
func (s *Service) StationRemove(name string) error {
	list := s.Stations()
	i := slices.IndexFunc(list, func(x config.Station) bool { return x.Name == name })
	if i < 0 {
		return valise.New("station_unknown", "没有叫 "+name+" 的空间站", "用 valise station list 查看")
	}
	return config.SaveStations(s.Dirs.Data, slices.Delete(list, i, i+1))
}

// installStationKey appends a restricted authorized_keys line on the
// station (no terminal, no forwarding), replacing the same key.
func (s *Service) installStationKey(ctx context.Context, t *ssh.Transport, pub string) error {
	f := strings.Fields(pub)
	if len(f) < 2 {
		return valise.New("station_key_invalid", "空间站钥匙的公钥格式不对", "重试；仍不行请报告")
	}
	comment := "valise-station"
	if len(f) > 2 {
		comment = f[2]
	}
	line := "restrict " + f[0] + " " + f[1] + " " + comment
	res, err := t.Run(ctx, "sh -s", strings.NewReader(installKeysScript(map[string]string{"station": line})))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return valise.New("install_keys_failed", "没能把钥匙写进空间站的 ~/.ssh/authorized_keys", "检查空间站上 ~/.ssh 的权限与磁盘空间").
			WithDetail(strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// stationTransport logs in to a station with the client's station key.
func (s *Service) stationTransport(st config.Station) *ssh.Transport {
	o := s.valiseOptions(stationServer(st))
	o.Identity, o.IdentityAgent, o.BatchMode, o.NoControl = s.stationKey(st.Name), "none", true, true
	t, _ := ssh.New(config.StationAlias(st.Name), o)
	return t
}

// relayStation is the client's way to a station for relays.
func (s *Service) relayStation(st config.Station) stationsync.Station {
	return stationsync.Station{Name: st.Name, Kind: stationsync.MethodRsync, Host: config.StationAlias(st.Name),
		SSH: []string{"-F", filepath.Join(s.Dirs.Data, config.SSHConfigFile), "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-i", s.stationKey(st.Name)},
		Tmp: filepath.Join(s.Dirs.Cache, "tmp")}
}

type localRunner struct{}

func (localRunner) Run(ctx context.Context, timeout time.Duration, argv ...string) (int, []byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !asExit(err, &exit) {
		return 0, nil, nil, err
	}
	return cmd.ProcessState.ExitCode(), []byte(out.String()), []byte(errb.String()), nil
}

func asExit(err error, exit **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	*exit = e
	return ok
}

// attachStation gives host its own key for st and records st in host's
// targets.json, so the server can copy directly (and sync by itself).
func (s *Service) attachStation(ctx context.Context, host string, st config.Station) error {
	var key vaultrpc.StationKeyResult
	if err := s.call(ctx, host, vaultrpc.MethodStationKey, vaultrpc.StationKeyParams{Name: st.Name}, &key); err != nil {
		return err
	}
	if err := s.installStationKey(ctx, s.stationTransport(st), key.PublicKey); err != nil {
		return err
	}
	data, _ := os.ReadFile(filepath.Join(s.Dirs.Data, config.KnownHostsFile))
	_, lines := parseHostKeys(data, knownHostsField(st.Address, st.PortOr22()))
	t := vaultrpc.Target{Name: st.Name, Kind: stationsync.MethodRsync, Dest: st.Address + ":" + st.Path,
		Station: &vaultrpc.StationConn{Address: st.Address, Port: st.PortOr22(), User: st.User, KnownHosts: lines}}
	return s.TargetSet(ctx, host, t)
}

// attached reports whether host's targets.json already has st as recorded.
func attached(status *vaultrpc.StatusResult, st config.Station) bool {
	for _, t := range status.Targets {
		if t.Name == st.Name && t.Station != nil && t.Station.Address == st.Address && t.Station.Port == st.PortOr22() && t.Station.User == st.User &&
			t.Dest == st.Address+":"+st.Path {
			return true
		}
	}
	return false
}

// Relay paths.
const (
	PathDirect = "direct"
	PathRepo   = "repo"
	PathTemp   = "temp"
)

// StationSyncResult is one copy of a server to a station.
type StationSyncResult struct {
	Station   string   `json:"station"`
	Path      string   `json:"path"` // direct, repo or temp
	Repo      string   `json:"repo,omitempty"`
	Method    string   `json:"method"`
	Note      string   `json:"note,omitempty"`
	Dest      string   `json:"dest"`
	Leaves    []string `json:"leaves"`
	Snapshots []string `json:"snapshots"`
	// Direct says why the direct copy was not used.
	Direct string `json:"direct,omitempty"`
}

// SyncStation copies host's escape pods to a station: directly from the
// server, else relayed through a repository or a temporary folder.
func (s *Service) SyncStation(ctx context.Context, host, name string) (*StationSyncResult, error) {
	st, err := s.station(name)
	if err != nil {
		return nil, err
	}
	status, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	if status.ServerName == "" {
		return nil, valise.New("server_name_missing", host+" 还没有名字，空间站不知道放进哪个文件夹", "先给服务器起名（服务器设置里按 n）")
	}
	res := &StationSyncResult{Station: name}
	var directErr error
	if !attached(status, *st) {
		directErr = s.attachStation(ctx, host, *st)
	}
	if directErr == nil {
		r, err := s.Sync(ctx, host, name)
		if err == nil {
			res.Path, res.Method, res.Note, res.Dest, res.Leaves, res.Snapshots = PathDirect, r.Method, r.Note, r.Dest, r.Leaves, r.Snapshots
			return res, nil
		}
		ue, ok := valise.As(err)
		if !ok || ue.Code != stationsync.UnreachableCode {
			return nil, err
		}
		directErr = err
	}
	res.Direct = errText1(directErr)
	return s.relay(ctx, host, *st, status, res)
}

func errText1(err error) string {
	if ue, ok := valise.As(err); ok {
		return ue.Reason
	}
	return err.Error()
}

// relay copies through this computer: from an available repository, else
// through a temporary folder deleted afterwards.
func (s *Service) relay(ctx context.Context, host string, st config.Station, status *vaultrpc.StatusResult, res *StationSyncResult) (*StationSyncResult, error) {
	var base string
	for _, r := range s.Config.Repos {
		if _, err := os.Stat(r.Path); err == nil {
			if _, err := s.RepoSync(ctx, host, r.Name); err != nil {
				return nil, err
			}
			base, res.Path, res.Repo = filepath.Join(r.Path, status.ServerName), PathRepo, r.Name
			break
		}
	}
	if base == "" {
		if err := os.MkdirAll(filepath.Join(s.Dirs.Cache, "tmp"), 0o700); err != nil {
			return nil, err
		}
		tmp, err := os.MkdirTemp(filepath.Join(s.Dirs.Cache, "tmp"), "relay-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		base, res.Path = filepath.Join(tmp, status.ServerName), PathTemp
		if err := writeTree(base, status.ServerName); err != nil {
			return nil, err
		}
		for _, l := range status.Leaves {
			if l.Deleting || l.VaultID == "" {
				continue
			}
			pr, err := s.Pull(ctx, host, l.Leaf, filepath.Join(base, leafDirName(l.Leaf)))
			if err != nil {
				return nil, err
			}
			if len(pr.Problems) > 0 {
				return nil, valise.New("relay_verify_failed", "取回的逃生舱没有通过校验，没有上传", "检查网络后重试")
			}
		}
	}
	rs := s.relayStation(st)
	dest := strings.TrimRight(st.Path, "/") + "/" + status.ServerName
	res.Dest = st.Address + ":" + dest
	method, note, err := stationsync.With(ctx, localRunner{}, rs, func(r stationsync.Remote) error {
		res.Leaves, res.Snapshots = []string{}, []string{}
		if err := r.PutFile(ctx, filepath.Join(base, vault.TreeFile), rs.Dest(dest+"/"+vault.TreeFile)); err != nil {
			return err
		}
		for _, l := range status.Leaves {
			if l.Deleting || l.VaultID == "" {
				continue
			}
			to := dest + "/" + l.Leaf
			if l.Leaf == vault.LegacyLeaf {
				to = strings.TrimRight(st.Path, "/") + "/" + l.VaultID
			}
			if err := r.SyncLeaf(ctx, filepath.Join(base, leafDirName(l.Leaf)), rs.Dest(to)); err != nil {
				return err
			}
			res.Leaves = append(res.Leaves, l.Leaf)
			res.Snapshots = append(res.Snapshots, l.Snapshots...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Method, res.Note = method, note
	// The server's sync record learns what the station now holds.
	if attached(status, st) || s.IsStation(st.Name) {
		_ = s.call(ctx, host, vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: st.Name, Snapshots: res.Snapshots}, nil)
	}
	return res, nil
}

// PathText says how a copy went, for people.
func (r *StationSyncResult) PathText() string {
	switch r.Path {
	case PathRepo:
		return "经本机仓库 " + r.Repo + " 中转（服务器连不上空间站：" + r.Direct + "）"
	case PathTemp:
		return "经这台电脑临时中转，临时文件已删除（服务器连不上空间站：" + r.Direct + "）"
	}
	return "服务器直传"
}

// StationList lists what a station holds for host: through the server
// when it can reach the station, else from this computer.
func (s *Service) StationList(ctx context.Context, host, name string) (*vaultrpc.TargetListResult, error) {
	st, err := s.station(name)
	if err != nil {
		return nil, err
	}
	status, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	if attached(status, *st) {
		l, err := s.TargetList(ctx, host, name)
		if ue, ok := valise.As(err); !ok || ue.Code != stationsync.UnreachableCode {
			return l, err
		}
	}
	if status.ServerName == "" {
		return &vaultrpc.TargetListResult{Target: name, Leaves: []vaultrpc.RemoteLeaf{}}, nil
	}
	base := strings.TrimRight(st.Path, "/") + "/" + status.ServerName
	rs := s.relayStation(*st)
	var entries []stationsync.Entry
	_, _, err = stationsync.With(ctx, localRunner{}, rs, func(r stationsync.Remote) error {
		var err error
		entries, err = r.List(ctx, rs.Dest(base))
		return err
	})
	if err != nil {
		return nil, err
	}
	res := &vaultrpc.TargetListResult{Target: name, Dest: st.Address + ":" + base, Leaves: []vaultrpc.RemoteLeaf{}}
	byLeaf := map[string]int{}
	for _, e := range entries {
		parts := strings.SplitN(e.Path, "/", 3)
		if e.Dir || len(parts) < 3 {
			continue
		}
		leaf := parts[0] + "/" + parts[1]
		if _, _, ok := vault.SplitLeaf(leaf); !ok {
			continue
		}
		i, ok := byLeaf[leaf]
		if !ok {
			i = len(res.Leaves)
			byLeaf[leaf] = i
			res.Leaves = append(res.Leaves, vaultrpc.RemoteLeaf{Leaf: leaf, Snapshots: []string{}, Packs: []string{}})
		}
		l := &res.Leaves[i]
		l.Bytes += e.Size
		switch rel := parts[2]; {
		case rel == vault.DeletingFile:
			l.Deleting = true
		case strings.HasPrefix(rel, vault.SnapshotsDir+"/") && strings.HasSuffix(rel, ".meta.json"):
			l.Snapshots = append(l.Snapshots, strings.TrimSuffix(strings.TrimPrefix(rel, vault.SnapshotsDir+"/"), ".meta.json"))
		}
	}
	return res, nil
}
