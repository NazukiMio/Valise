package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"valise/client/config"
	"valise/core/valise"
	"valise/core/vault"
)

// Repositories are local folders escape pods are copied to by the client
// (docs/ARCHITECTURE.md section 7, "本机仓库"): the same append-only copy
// as a pull, into <repo>/<server name>/<instance>/<period>/.

var repoNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const repoStateFile = "repo-state.json"

// Repos lists the repositories.
func (s *Service) Repos() []config.Repo { return s.Config.Repos }

// RepoAdd records a repository; the folder is created when missing.
func (s *Service) RepoAdd(name, path string) (*config.Repo, error) {
	if !repoNameRe.MatchString(name) || len(name) > 32 {
		return nil, valise.New("repo_name_invalid", "仓库名只能包含小写字母、数字和连字符", "例如 usb 或 drive")
	}
	path = expandHome(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return nil, valise.New("repo_path_invalid", "请填写文件夹的完整路径", "例如 /Volumes/USB/valise 或 ~/Valise")
	}
	path = filepath.Clean(path)
	if slices.ContainsFunc(s.Config.Repos, func(r config.Repo) bool { return r.Name == name }) {
		return nil, valise.New("repo_exists", "已经有一个叫 "+name+" 的仓库", "换一个名字")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, valise.New("repo_unwritable", "无法使用文件夹 "+path, "确认路径存在且可写（外接硬盘已连接）").WithDetail(err.Error())
	}
	probe, err := os.CreateTemp(path, ".valise-probe-")
	if err != nil {
		return nil, valise.New("repo_unwritable", "文件夹 "+path+" 不可写", "换一个文件夹，或检查权限").WithDetail(err.Error())
	}
	probe.Close()
	os.Remove(probe.Name())
	r := config.Repo{Name: name, Path: path}
	repos := append(slices.Clone(s.Config.Repos), r)
	if err := config.SetRepos(s.Dirs.Data, repos); err != nil {
		return nil, err
	}
	s.Config.Repos = repos
	return &r, nil
}

// RepoRemove drops a repository from the list; its files stay.
func (s *Service) RepoRemove(name string) error {
	i := slices.IndexFunc(s.Config.Repos, func(r config.Repo) bool { return r.Name == name })
	if i < 0 {
		return valise.New("repo_unknown", "没有叫 "+name+" 的仓库", "用 valise repo list 查看")
	}
	repos := slices.Delete(slices.Clone(s.Config.Repos), i, i+1)
	if err := config.SetRepos(s.Dirs.Data, repos); err != nil {
		return err
	}
	s.Config.Repos = repos
	return nil
}

func (s *Service) repo(name string) (*config.Repo, error) {
	for _, r := range s.Config.Repos {
		if r.Name == name {
			return &r, nil
		}
	}
	return nil, valise.New("repo_unknown", "没有叫 "+name+" 的仓库", "用 valise repo list 查看")
}

// RepoSyncResult is one repository sync of a server.
type RepoSyncResult struct {
	Repo       string          `json:"repo"`
	Dir        string          `json:"dir"`
	Leaves     []string        `json:"leaves"`
	Snapshots  []string        `json:"snapshots"`
	Downloaded int             `json:"downloaded"`
	Bytes      int64           `json:"bytes"`
	Problems   []vault.Problem `json:"problems"`
}

// RepoSync copies host's escape pods the repository lacks, then verifies
// each period without a key.
func (s *Service) RepoSync(ctx context.Context, host, name string) (res *RepoSyncResult, err error) {
	r, err := s.repo(name)
	if err != nil {
		return nil, err
	}
	defer func() { s.recordRepo(name, host, res, err) }()
	if _, err := os.Stat(r.Path); err != nil {
		return nil, valise.New("repo_unavailable", "仓库 "+name+" 的文件夹现在不可用："+r.Path, "接上外接硬盘或检查路径后重试").WithDetail(err.Error())
	}
	st, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	if st.ServerName == "" {
		return nil, valise.New("server_name_missing", host+" 还没有名字，仓库不知道放进哪个文件夹", "先给服务器起名（服务器设置里按 n）")
	}
	base := filepath.Join(r.Path, st.ServerName)
	if err := writeTree(base, st.ServerName); err != nil {
		return nil, valise.New("repo_unwritable", "无法写入仓库 "+name, "检查文件夹权限与空间").WithDetail(err.Error())
	}
	res = &RepoSyncResult{Repo: name, Dir: base, Leaves: []string{}, Snapshots: []string{}, Problems: []vault.Problem{}}
	for _, l := range st.Leaves {
		if l.Deleting || l.VaultID == "" {
			continue
		}
		pr, err := s.Pull(ctx, host, l.Leaf, filepath.Join(base, leafDirName(l.Leaf)))
		if pr != nil {
			res.Downloaded += pr.Downloaded
			res.Bytes += pr.Bytes
			res.Problems = append(res.Problems, pr.Problems...)
		}
		if err != nil {
			return res, err
		}
		res.Leaves = append(res.Leaves, l.Leaf)
		res.Snapshots = append(res.Snapshots, l.Snapshots...)
	}
	if len(res.Problems) > 0 {
		return res, valise.New("repo_verify_failed", "仓库 "+name+" 里的副本没有通过校验", "检查硬盘后重新同步；有问题的文件不会被覆盖，需要时删掉那个周期的文件夹再同步")
	}
	return res, nil
}

// writeTree writes <base>/TREE once.
func writeTree(base, server string) error {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(base, vault.TreeFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(vault.TreeDesc{Format: vault.TreeFormat, ServerName: server, CreatedAt: time.Now().UTC()}, "", "  ")
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// RepoState is the last sync of a server to a repository.
type RepoState struct {
	LastOK    *time.Time `json:"last_ok,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

type repoStates map[string]map[string]RepoState // repo → server → state

func (s *Service) loadRepoStates() repoStates {
	out := repoStates{}
	if b, err := os.ReadFile(filepath.Join(s.Dirs.Data, repoStateFile)); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func (s *Service) recordRepo(repo, host string, res *RepoSyncResult, err error) {
	all := s.loadRepoStates()
	if all[repo] == nil {
		all[repo] = map[string]RepoState{}
	}
	st := all[repo][host]
	if err != nil {
		st.LastError = err.Error()
		if ue, ok := valise.As(err); ok {
			st.LastError = ue.Reason
		}
	} else {
		now := time.Now().UTC()
		st.LastOK, st.LastError = &now, ""
	}
	all[repo][host] = st
	b, _ := json.MarshalIndent(all, "", "  ")
	_ = os.WriteFile(filepath.Join(s.Dirs.Data, repoStateFile), append(b, '\n'), 0o600)
}

// RepoStatus is what a repository holds for a server.
type RepoStatus struct {
	config.Repo
	Available bool      `json:"available"`
	State     RepoState `json:"state"`
	// Snapshots present, by period ("<instance>/<period>").
	Leaves map[string][]string `json:"leaves"`
}

// RepoStatuses reads every repository for host's server name: only local
// files are looked at.
func (s *Service) RepoStatuses(host, serverName string) []RepoStatus {
	states := s.loadRepoStates()
	var out []RepoStatus
	for _, r := range s.Config.Repos {
		rs := RepoStatus{Repo: r, State: states[r.Name][host], Leaves: map[string][]string{}}
		if _, err := os.Stat(r.Path); err == nil {
			rs.Available = true
		}
		if serverName != "" {
			metas, _ := filepath.Glob(filepath.Join(r.Path, serverName, "*", "*", vault.SnapshotsDir, "*.meta.json"))
			legacy, _ := filepath.Glob(filepath.Join(r.Path, serverName, "legacy", vault.SnapshotsDir, "*.meta.json"))
			for _, m := range append(metas, legacy...) {
				rel, _ := filepath.Rel(filepath.Join(r.Path, serverName), m)
				parts := strings.Split(filepath.ToSlash(rel), "/")
				leaf := parts[0] + "/" + parts[1]
				if parts[0] == "legacy" {
					leaf = vault.LegacyLeaf
				}
				rs.Leaves[leaf] = append(rs.Leaves[leaf], strings.TrimSuffix(parts[len(parts)-1], ".meta.json"))
			}
		}
		out = append(out, rs)
	}
	return out
}

// leafDirName is a leaf's folder under a server: the legacy vault ("-")
// goes to "legacy".
func leafDirName(leaf string) string {
	if leaf == vault.LegacyLeaf {
		return "legacy"
	}
	return filepath.FromSlash(leaf)
}

// Has reports whether a repository status holds a snapshot.
func (r RepoStatus) Has(snapshotID string) bool {
	for _, ids := range r.Leaves {
		if slices.Contains(ids, snapshotID) {
			return true
		}
	}
	return false
}

// expandHome turns a leading "~/" into the user's home.
func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}
