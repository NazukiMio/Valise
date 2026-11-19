package service

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"valise/client/config"
	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Cloud drives logged in on this computer (docs/ARCHITECTURE.md 7.3,
// "这台电脑登录"): rclone and its authorization stay here; copies are
// relayed through a repository or a temporary folder, only while Valise is
// open.

// IsCloudHere reports whether name is a cloud drive logged in here.
func (s *Service) IsCloudHere(name string) bool { return s.cloudHere(name) != nil }

func (s *Service) cloudHere(name string) *config.Cloud {
	for i := range s.Config.Clouds {
		if s.Config.Clouds[i].Name == name {
			return &s.Config.Clouds[i]
		}
	}
	return nil
}

// CloudIDOf returns the configured cloud driver ID for a local cloud
// location, or "" when unknown.
func (s *Service) CloudIDOf(name string) string {
	if c := s.cloudHere(name); c != nil && c.Cloud != "" {
		return c.Cloud
	}
	return ""
}

func (s *Service) validCloudName(name string) bool {
	if !repoNameRe.MatchString(name) || s.IsStation(name) {
		return false
	}
	for _, r := range s.Repos() {
		if r.Name == name {
			return false
		}
	}
	return true
}

func (s *Service) cloudFolderOf(name string) string {
	if c := s.cloudHere(name); c != nil && c.Folder != "" {
		return c.Folder
	}
	return cloudFolder
}

// CloudBucketOf returns the bucket prefix configured for a local cloud
// location, or "" when none.
func (s *Service) CloudBucketOf(name string) string {
	b, ok := strings.CutSuffix(s.cloudFolderOf(name), "/"+cloudFolder)
	if ok {
		return b
	}
	return ""
}

func (s *Service) upsertCloudHere(name, cloudID, folder string) []config.Cloud {
	if folder == cloudFolder {
		folder = ""
	}
	clouds := append([]config.Cloud{}, s.Config.Clouds...)
	for i := range clouds {
		if clouds[i].Name == name {
			clouds[i].Cloud = cloudID
			clouds[i].Folder = folder
			return clouds
		}
	}
	return append(clouds, config.Cloud{Name: name, Cloud: cloudID, Folder: folder})
}

func (s *Service) hereStation(ctx context.Context, name string) (stationsync.Station, error) {
	local := s.FindLocalRclone(ctx)
	if local == nil {
		return stationsync.Station{}, valise.New("rclone_local_missing", "这台电脑没有 rclone", "运行 valise cloud rclone 准备 rclone")
	}
	return stationsync.Station{Name: name, Kind: stationsync.MethodRclone, Host: vaultrpc.CloudRemote(name),
		Tmp: filepath.Join(s.Dirs.Cache, "tmp"), Rclone: local.Path, RcloneConfig: s.LocalRcloneConfig()}, nil
}

func (s *Service) localRemotes(ctx context.Context, bin string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "listremotes", "--config", s.LocalRcloneConfig()).Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Fields(string(out)) {
		names = append(names, strings.TrimSuffix(l, ":"))
	}
	return names, nil
}

func (s *Service) forgetHere(bin, name string) {
	exec.Command(bin, "config", "delete", vaultrpc.CloudRemote(name), "--config", s.LocalRcloneConfig()).Run()
}

// CloudLoginHere logs this computer in to a cloud drive: rclone's own
// configuration runs in the user's terminal and opens this computer's
// browser. A failed login leaves nothing behind.
func (s *Service) CloudLoginHere(ctx context.Context, name, cloudID string, o CloudOptions, in io.Reader, out io.Writer) error {
	cloud, err := s.cloudEntry(cloudID)
	if err != nil {
		return err
	}
	if cloud.Pending() && !o.AllowPending {
		return valise.New("cloud_pending", cloud.Name+" 的登录与复制还没有在这个版本上测试", "确实要试用时加 --allow-pending；出现问题请报告")
	}
	folder, err := resolveCloudFolder(cloud, o.Bucket)
	if err != nil {
		return err
	}
	if !s.validCloudName(name) {
		return valise.New("cloud_name_invalid", "名字无效或已被使用："+name, "换一个只含小写字母、数字和连字符的名字")
	}
	local := s.FindLocalRclone(ctx)
	if local == nil {
		return valise.New("rclone_local_missing", "这台电脑没有 rclone", "运行 valise cloud rclone 准备 rclone")
	}
	if err := s.ensureLocalRcloneConfig(); err != nil {
		return err
	}
	remotes, err := s.localRemotes(ctx, local.Path)
	if err != nil {
		return valise.New("rclone_failed", "这台电脑上的 rclone 无法运行", "运行 valise cloud rclone 检查").WithDetail(err.Error())
	}
	// The name is not a saved cloud (checked above), so an existing login
	// is left over from one that never finished; the new login replaces it.
	for _, r := range remotes {
		if r == vaultrpc.CloudRemote(name) {
			s.forgetHere(local.Path, name)
		}
	}
	args := append(cloud.CreateArgs(vaultrpc.CloudRemote(name), !o.Headless), "--config", s.LocalRcloneConfig())
	cmd := exec.CommandContext(ctx, local.Path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, out
	if err := cmd.Run(); err != nil {
		s.forgetHere(local.Path, name)
		return valise.New("cloud_login_failed", "云盘登录没有完成", "重新登录；浏览器里的授权完成后 rclone 才会退出").WithDetail(err.Error())
	}
	st, err := s.hereStation(ctx, name)
	if err == nil {
		_, _, err = stationsync.With(ctx, localRunner{}, st, func(r stationsync.Remote) error {
			return r.Probe(ctx, st.Dest(folder))
		})
	}
	if err != nil {
		s.forgetHere(local.Path, name)
		return err
	}
	clouds := s.upsertCloudHere(name, cloudID, folder)
	if err := config.SetClouds(s.Dirs.Data, clouds); err != nil {
		s.forgetHere(local.Path, name)
		return err
	}
	s.Config.Clouds = clouds
	return nil
}

// CloudRemoveHere removes this computer's login of a cloud drive.
func (s *Service) CloudRemoveHere(ctx context.Context, name string) error {
	if s.cloudHere(name) == nil {
		return valise.New("cloud_unknown", "这台电脑上没有云盘 "+name, "运行 valise cloud list --here 查看")
	}
	if local := s.FindLocalRclone(ctx); local != nil {
		s.forgetHere(local.Path, name)
	}
	var keep []config.Cloud
	for _, c := range s.Config.Clouds {
		if c.Name != name {
			keep = append(keep, c)
		}
	}
	if err := config.SetClouds(s.Dirs.Data, keep); err != nil {
		return err
	}
	s.Config.Clouds = keep
	return nil
}

// SyncCloud copies host's backups to a cloud drive: directly from the
// server when it is logged in there, else (or when that fails to reach
// the drive) relayed through this computer when logged in here.
func (s *Service) SyncCloud(ctx context.Context, host, name string) (*StationSyncResult, error) {
	return s.syncCloud(ctx, host, name, true)
}

// syncCloud without wait returns *LocationBusyError when another Valise
// process is copying to the cloud (automatic copies skip it).
func (s *Service) syncCloud(ctx context.Context, host, name string, wait bool) (*StationSyncResult, error) {
	unlock, err := s.lockLocation(ctx, "cloud", name, host, wait)
	if err != nil {
		return nil, err
	}
	defer unlock()
	status, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	if status.ServerName == "" {
		return nil, valise.New("server_name_missing", host+" 还没有名字，云盘不知道放进哪个文件夹", "先给服务器起名（服务器设置里按 n）")
	}
	res := &StationSyncResult{Station: name}
	onServer := false
	for _, t := range status.Targets {
		onServer = onServer || (t.Name == name && t.Kind == stationsync.MethodRclone)
	}
	if onServer {
		r, err := s.syncOnly(ctx, host, name)
		if err == nil {
			res.Path, res.Method, res.Leaves, res.Snapshots, res.Dest = PathDirect, r.Method, r.Leaves, r.Snapshots, r.Dest
			res.Checks = ServerChecks(name, r.Checks)
			return res, nil
		}
		ue, ok := valise.As(err)
		if !ok || ue.Code != stationsync.UnreachableCode || !s.IsCloudHere(name) {
			return nil, err
		}
		res.Direct = errText1(err)
	} else if !s.IsCloudHere(name) {
		return nil, valise.New("cloud_unknown", "服务器和这台电脑都没有登录云盘 "+name, "先登录：valise cloud add")
	}
	st, err := s.hereStation(ctx, name)
	if err != nil {
		return nil, err
	}
	base, path, repo, cleanup, err := s.relayBase(ctx, host, status)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	res.Path, res.Repo = path, repo
	if res.Direct == "" {
		res.Direct = "只在这台电脑登录了"
	}
	dest := s.cloudFolderOf(name) + status.ServerName
	res.Dest = st.Dest(dest)
	res.Method, _, err = stationsync.With(ctx, localRunner{}, st, func(r stationsync.Remote) error {
		res.Leaves, res.Snapshots = []string{}, []string{}
		if err := r.PutFile(ctx, filepath.Join(base, vault.TreeFile), st.Dest(dest+"/"+vault.TreeFile)); err != nil {
			return err
		}
		for _, l := range status.Leaves {
			if l.Deleting || l.VaultID == "" || l.Leaf == vault.LegacyLeaf {
				continue
			}
			if err := r.SyncLeaf(ctx, filepath.Join(base, leafDirName(l.Leaf)), st.Dest(dest+"/"+l.Leaf)); err != nil {
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
	if onServer {
		_ = s.call(ctx, host, vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: name, Snapshots: res.Snapshots}, nil)
	}
	res.Checks = s.CheckAfterCopy(ctx, host, name, res.Leaves)
	return res, nil
}
