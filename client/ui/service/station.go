package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
	return nil, valise.New("station_unknown", "没有叫 "+name+" 的远程备份位置", "用 valise station list 查看")
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
	return valise.New("station_invalid", "远程备份位置的"+what, "例如 名字 nas、地址 192.168.1.20、用户 valise、路径 /volume1/valise")
}

// StationAddResult reports what adding a station found.
type StationAddResult struct {
	Station  config.Station
	Warnings []string
}

// StationAdd records a station after its host keys were confirmed, logs
// in once (password or the user's key; OpenSSH asks) to install the
// client's station key and the line trusting Valise's certificates, then
// finds what the station offers to limit server keys.
func (s *Service) StationAdd(ctx context.Context, st config.Station, confirmed []HostKey) (*StationAddResult, error) {
	res, err := s.stationAdd(ctx, st, confirmed)
	return res, err
}

func (s *Service) stationAdd(ctx context.Context, st config.Station, confirmed []HostKey) (*StationAddResult, error) {
	if err := st.Validate(); err != nil {
		return nil, stationInvalid(err)
	}
	caPub, err := s.ensureCA(ctx)
	if err != nil {
		return nil, err
	}
	list := s.Stations()
	if slices.ContainsFunc(list, func(x config.Station) bool { return x.Name == st.Name }) {
		return nil, valise.New("station_exists", "已经有一个叫 "+st.Name+" 的远程备份位置", "换一个名字，或先移除旧的")
	}
	if err := s.TrustHostKeys(ctx, stationServer(st), confirmed); err != nil {
		return nil, err
	}
	res := &StationAddResult{}
	// A station anyone can log in to makes every limit moot: say so.
	if s.refuseNoAuth(ctx, stationServer(st)) != nil {
		res.Warnings = append(res.Warnings, st.Address+" 不需要任何密码或钥匙就能登录：Valise 对钥匙的限制在它上面不起作用。请先给它设密码、为 Valise 开专用用户。")
	}
	if err := config.SaveStations(s.Dirs.Data, append(slices.Clone(list), st)); err != nil {
		return nil, err
	}
	undo := func() { _ = config.SaveStations(s.Dirs.Data, list) }
	key := s.stationKey(st.Name)
	if !fileExists(key) {
		if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
			undo()
			return nil, err
		}
		if err := runTool(ctx, s.toolPath("ssh-keygen"), []string{"-q", "-t", "ed25519", "-N", "", "-C", "valise-station@" + localName(), "-f", key}, os.Environ()); err != nil {
			undo()
			return nil, valise.New("keygen_failed", "无法生成远程备份位置钥匙", "确认本机安装了 OpenSSH 的 ssh-keygen").WithDetail(err.Error())
		}
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		undo()
		return nil, err
	}
	t, err := s.bootstrapTransport(stationServer(st))
	if err != nil {
		undo()
		return nil, err
	}
	if err := s.installStationKey(ctx, t, string(pub), ""); err != nil {
		undo()
		return nil, err
	}
	if _, err := s.stationTransport(st).Run(ctx, "true", nil); err != nil {
		undo()
		return nil, valise.New("station_key_unusable", "远程备份位置钥匙装好了，但用它登录失败", "检查远程备份位置 SSH 是否允许公钥登录").WithDetail(err.Error())
	}
	// Server keys only ever log in with Valise's certificates.
	if err := s.installStationKey(ctx, s.stationTransport(st), caPub, `cert-authority,restrict,principals="`+st.User+`"`); err != nil {
		undo()
		return nil, err
	}
	st.RRsync, st.SFTPServer = s.stationTools(ctx, st)
	if !st.RRsync && st.SFTPServer == "" {
		res.Warnings = append(res.Warnings, "远程备份位置上没有 rrsync 也没有 sftp-server，Valise 不会给服务器凭据：同步一律经这台电脑中转。")
	} else if !st.RRsync {
		res.Warnings = append(res.Warnings, "远程备份位置上没有 rrsync，服务器的证书限定为禁止删除的 sftp：它仍可能覆盖已有文件，建议在 NAS 上开启定时快照。")
	}
	if err := config.SaveStations(s.Dirs.Data, append(slices.Clone(list), st)); err != nil {
		return nil, err
	}
	res.Station = st
	res.Warnings = append(res.Warnings, "建议在 NAS 上为 Valise 的目录开启定时快照：即使有人拿到这台电脑上的钥匙删了备份，也能找回。")
	return res, nil
}

// stationTools finds rrsync and sftp-server on a station.
func (s *Service) stationTools(ctx context.Context, st config.Station) (rrsync bool, sftpServer string) {
	r, err := s.stationTransport(st).Run(ctx, `command -v rrsync >/dev/null 2>&1 && echo rrsync; for p in /usr/lib/openssh/sftp-server /usr/libexec/sftp-server /usr/libexec/openssh/sftp-server /usr/lib/ssh/sftp-server /usr/lib/sftp-server; do [ -x "$p" ] && { echo "$p"; break; }; done; true`, nil)
	if err != nil {
		return false, ""
	}
	for _, l := range strings.Split(string(r.Stdout), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "rrsync":
			rrsync = true
		case strings.HasPrefix(l, "/") && sftpServer == "":
			sftpServer = l
		}
	}
	return rrsync, sftpServer
}

// StationRemove forgets a station; nothing on it or on servers changes.
func (s *Service) StationRemove(name string) error {
	list := s.Stations()
	i := slices.IndexFunc(list, func(x config.Station) bool { return x.Name == name })
	if i < 0 {
		return valise.New("station_unknown", "没有叫 "+name+" 的远程备份位置", "用 valise station list 查看")
	}
	return config.SaveStations(s.Dirs.Data, slices.Delete(list, i, i+1))
}

// installStationKey appends a restricted authorized_keys line on the
// station (no terminal, no forwarding), replacing the same key.
func (s *Service) installStationKey(ctx context.Context, t *ssh.Transport, pub, options string) error {
	f := strings.Fields(pub)
	if len(f) < 2 {
		return valise.New("station_key_invalid", "远程备份位置钥匙的公钥格式不对", "重试；仍不行请报告")
	}
	comment := "valise-station"
	if len(f) > 2 {
		comment = f[2]
	}
	if options == "" {
		options = "restrict"
	}
	line := options + " " + f[0] + " " + f[1] + " " + comment
	res, err := t.Run(ctx, "sh -s", strings.NewReader(installKeysScript(map[string]string{"station": line})))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return valise.New("install_keys_failed", "没能把钥匙写进远程备份位置的 ~/.ssh/authorized_keys", "检查远程备份位置上 ~/.ssh 的权限与磁盘空间").
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 500 * time.Millisecond
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return 0, nil, nil, ctx.Err()
	}
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

// attachStation gives host its own key for st with a fresh certificate,
// and records st in host's targets.json, so the server can copy directly
// (and sync by itself while the certificate lasts).
func (s *Service) attachStation(ctx context.Context, host string, st config.Station, status *vaultrpc.StatusResult) error {
	_, kind, rooted, ok := certLimit(st, status.ServerName)
	if !ok {
		return valise.New(stationsync.UnreachableCode, "备份位置 "+st.Name+" 无法限制服务器的钥匙，Valise 不给服务器凭据", "经这台电脑中转")
	}
	var key vaultrpc.StationKeyResult
	if err := s.call(ctx, host, vaultrpc.MethodStationKey, vaultrpc.StationKeyParams{Name: st.Name}, &key); err != nil {
		return err
	}
	dest := st.Address + ":" + st.Path
	if rooted {
		// rrsync anchors this server at its own folder, which must exist.
		folder := strings.TrimRight(st.Path, "/") + "/" + status.ServerName
		r, err := s.stationTransport(st).Run(ctx, "mkdir -p "+ssh.Quote(folder), nil)
		if err == nil && r.ExitCode != 0 {
			err = valise.New("station_mkdir_failed", "无法在远程备份位置上创建 "+folder, "检查远程备份位置上这个目录的权限").WithDetail(strings.TrimSpace(string(r.Stderr)))
		}
		if err != nil {
			return err
		}
		dest = st.Address + ":/"
	}
	data, _ := os.ReadFile(filepath.Join(s.Dirs.Data, config.KnownHostsFile))
	_, lines := parseHostKeys(data, knownHostsField(st.Address, st.PortOr22()))
	t := vaultrpc.Target{Name: st.Name, Kind: kind, Dest: dest,
		Station: &vaultrpc.StationConn{Address: st.Address, Port: st.PortOr22(), User: st.User, KnownHosts: lines, Rooted: rooted}}
	for _, old := range status.Targets {
		if old.Name == st.Name && old.Auto != nil {
			t.Auto = old.Auto
		}
	}
	if err := s.TargetSet(ctx, host, t); err != nil {
		return err
	}
	return s.issueCert(ctx, host, status.ServerName, key.PublicKey, st)
}

// attached reports whether host's targets.json has st as it should be.
func attached(status *vaultrpc.StatusResult, st config.Station) bool {
	_, kind, rooted, ok := certLimit(st, status.ServerName)
	for _, t := range status.Targets {
		if ok && t.Name == st.Name && t.Station != nil && t.Station.Address == st.Address && t.Station.Port == st.PortOr22() &&
			t.Station.User == st.User && t.Kind == kind && t.Station.Rooted == rooted {
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
	// Checks compare the copied periods with the server's lists.
	Checks []CopyCheck `json:"checks,omitempty"`
}

// SyncStation copies host's escape pods to a station: directly from the
// server, else relayed through a repository or a temporary folder.
func (s *Service) SyncStation(ctx context.Context, host, name string) (*StationSyncResult, error) {
	if err := s.finishRename(ctx, host, name); err != nil {
		return nil, err
	}
	st, err := s.station(name)
	if err != nil {
		return nil, err
	}
	status, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	if status.ServerName == "" {
		return nil, valise.New("server_name_missing", host+" 还没有名字，远程备份位置不知道放进哪个文件夹", "先给服务器起名（服务器设置里按 n）")
	}
	res := &StationSyncResult{Station: name}
	var directErr error
	if !attached(status, *st) || !s.certFresh(host, name) {
		directErr = s.attachStation(ctx, host, *st, status)
	}
	if directErr == nil {
		r, err := s.syncOnly(ctx, host, name)
		if err == nil {
			res.Path, res.Method, res.Note, res.Leaves, res.Snapshots = PathDirect, r.Method, strings.ReplaceAll(r.Note, "这里", "服务器上"), r.Leaves, r.Snapshots
			res.Dest = st.Address + ":" + strings.TrimRight(st.Path, "/") + "/" + status.ServerName
			res.Checks = s.CheckAfterCopy(ctx, host, name, res.Leaves)
			return res, nil
		}
		ue, ok := valise.As(err)
		if !ok || ue.Code != stationsync.UnreachableCode {
			return nil, err
		}
		directErr = err
	}
	res.Direct = errText1(directErr)
	out, err := s.relay(ctx, host, *st, status, res)
	if err == nil && out != nil {
		out.Checks = s.CheckAfterCopy(ctx, host, name, out.Leaves)
	}
	return out, err
}

func errText1(err error) string {
	if ue, ok := valise.As(err); ok {
		return ue.Reason
	}
	return err.Error()
}

// relay copies through this computer: from an available repository, else
// through a temporary folder deleted afterwards.
// relayBase brings host's backups to this computer for a relay: into the
// first available repository, else a temporary folder (removed by cleanup).
func (s *Service) relayBase(ctx context.Context, host string, status *vaultrpc.StatusResult) (base, path, repo string, cleanup func(), err error) {
	cleanup = func() {}
	for _, r := range s.Config.Repos {
		if _, err := os.Stat(r.Path); err == nil {
			if _, err := s.RepoSync(ctx, host, r.Name); err != nil {
				return "", "", "", cleanup, err
			}
			return filepath.Join(r.Path, status.ServerName), PathRepo, r.Name, cleanup, nil
		}
	}
	if err := os.MkdirAll(filepath.Join(s.Dirs.Cache, "tmp"), 0o700); err != nil {
		return "", "", "", cleanup, err
	}
	tmp, err := os.MkdirTemp(filepath.Join(s.Dirs.Cache, "tmp"), "relay-")
	if err != nil {
		return "", "", "", cleanup, err
	}
	cleanup = func() { os.RemoveAll(tmp) }
	base = filepath.Join(tmp, status.ServerName)
	if err := writeTree(base, status.ServerName); err != nil {
		return "", "", "", cleanup, err
	}
	for _, l := range status.Leaves {
		if l.Deleting || l.VaultID == "" {
			continue
		}
		pr, err := s.Pull(ctx, host, l.Leaf, filepath.Join(base, leafDirName(l.Leaf)))
		if err != nil {
			return "", "", "", cleanup, err
		}
		if len(pr.Problems) > 0 {
			return "", "", "", cleanup, valise.New("relay_verify_failed", "取回的备份没有通过校验，没有上传", "检查网络后重试")
		}
	}
	return base, PathTemp, "", cleanup, nil
}

func (s *Service) relay(ctx context.Context, host string, st config.Station, status *vaultrpc.StatusResult, res *StationSyncResult) (*StationSyncResult, error) {
	base, path, repo, cleanup, err := s.relayBase(ctx, host, status)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	res.Path, res.Repo = path, repo
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
	res.Method, res.Note = method, strings.ReplaceAll(note, "这里", "本机")
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
		return "经本机仓库 " + r.Repo + " 中转（" + r.Direct + "）"
	case PathTemp:
		return "经这台电脑临时中转，临时文件已删除（" + r.Direct + "）"
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
	// Server keys may only write (rrsync -wo): the client lists.
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

// stationLeafDelete deletes a period on a managed station from this
// computer (server keys cannot delete): an owner operation while the
// server still holds the period, developer mode when it is the only copy
// (docs/ARCHITECTURE.md 2.2).
func (s *Service) stationLeafDelete(ctx context.Context, host, name, leaf string) error {
	st, err := s.station(name)
	if err != nil {
		return err
	}
	inst, period, ok := vault.SplitLeaf(leaf)
	if !ok {
		return valise.New("leaf_invalid", "无效的周期："+leaf, "使用列表中的名称")
	}
	status, err := s.VaultStatus(ctx, host)
	if err != nil {
		return err
	}
	onServer := false
	for _, l := range status.Leaves {
		if l.Leaf == leaf {
			if l.Current {
				return valise.New("leaf_current", "这是正在写入的周期，不能删除", "等它结束后再删")
			}
			onServer = !l.Deleting && l.VaultID != ""
		}
	}
	if onServer {
		if _, err := s.ownerIdentity(host); err != nil {
			return err
		}
	} else {
		t, done, err := s.adminTransport(ctx, host)
		if err != nil {
			return err
		}
		_ = t
		done()
	}
	rs := s.relayStation(*st)
	parent := strings.TrimRight(st.Path, "/") + "/" + status.ServerName + "/" + inst
	if _, _, err := stationsync.With(ctx, localRunner{}, rs, func(r stationsync.Remote) error {
		return r.DeleteDir(ctx, rs.Dest(parent), period)
	}); err != nil {
		return err
	}
	if attached(status, *st) {
		_ = s.call(ctx, host, vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: name, Forget: leaf}, nil)
	}
	return nil
}
