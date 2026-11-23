package service

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"valise/client/porter"
	"valise/client/profiles"
	"valise/core/install"
	"valise/core/profile"
	"valise/core/valise"
)

// Profiles loads the installed profile pack and local profiles.
func (s *Service) Profiles() (*profiles.Set, error) {
	set, err := profiles.Load(s.Dirs.Data, s.Release.PublicKey)
	if err != nil {
		return nil, valise.New("profiles_broken", "harness 资料包中的资料损坏", "重新安装资料包：valise profiles install").WithDetail(err.Error())
	}
	return set, nil
}

// needProfiles refuses to go on when no profile at all is usable.
func needProfiles(set *profiles.Set) error {
	if len(set.ByID) > 0 {
		return nil
	}
	if set.PackProblem != nil {
		ue, _ := valise.As(set.PackProblem)
		reason := "已安装的 harness 资料包没有通过校验"
		if ue != nil {
			reason += "：" + ue.Reason
		}
		return valise.New("pack_unusable", reason, "重新安装：valise profiles install")
	}
	return valise.New("no_profiles", "还没有安装 harness 资料包，Valise 无法识别任何 harness", "安装资料包：valise profiles install（在线拉取）或 valise profiles install --file <文件>")
}

// ProfilesStatus describes the installed pack and local profiles.
type ProfilesStatus struct {
	Pack        *profiles.Manifest `json:"pack,omitempty"`
	PackProblem string             `json:"pack_problem,omitempty"`
	Clouds      []string           `json:"clouds,omitempty"`
	Local       []string           `json:"local,omitempty"`
	Overrides   []string           `json:"overrides,omitempty"`
	Loaded      []ProfileSummary   `json:"loaded,omitempty"`
	Rejected    []string           `json:"rejected,omitempty"`
}

// ProfilesStatus reports what is installed.
func (s *Service) ProfilesStatus() (*ProfilesStatus, error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	st := &ProfilesStatus{Pack: set.Pack, Rejected: set.Rejected}
	if set.PackProblem != nil {
		st.PackProblem = set.PackProblem.Error()
		if ue, ok := valise.As(set.PackProblem); ok {
			st.PackProblem = ue.Reason
		}
	}
	if set.Kit != nil {
		for _, t := range set.Kit.Tools {
			for _, cl := range t.Clouds {
				st.Clouds = append(st.Clouds, cl.Name)
			}
		}
	}
	for id, l := range set.ByID {
		if l.Local {
			st.Local = append(st.Local, id)
		}
		if l.OverridesOfficial {
			st.Overrides = append(st.Overrides, id)
		}
		if l.Effective != nil {
			sum := ProfileSummary{
				ID:                id,
				DisplayName:       l.Effective.DisplayName,
				Version:           l.Effective.ProfileVersion,
				Status:            l.Effective.Status,
				Local:             l.Local,
				OverridesOfficial: l.OverridesOfficial,
			}
			if l.Official != nil {
				sum.OfficialVersion = l.Official.ProfileVersion
			}
			st.Loaded = append(st.Loaded, sum)
		}
	}
	sort.Strings(st.Local)
	sort.Strings(st.Overrides)
	sort.Slice(st.Loaded, func(i, j int) bool { return st.Loaded[i].ID < st.Loaded[j].ID })
	return st, nil
}

// PackFile is a pack read from disk or the network, not yet installed.
type PackFile struct {
	Data []byte
	// Sig is nil when no signature file was found.
	Sig []byte
	// From describes the source for messages.
	From string
	// Pack is the parsed content; SignatureErr is nil when the signature
	// is valid for this client's release key.
	Pack         *profiles.Pack
	SignatureErr error
}

// ReadPackFile reads a local pack and the .minisig next to it, if any.
func (s *Service) ReadPackFile(path string) (*PackFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, valise.New("pack_read", "无法读取资料包文件", "检查路径："+path).WithDetail(err.Error())
	}
	sig, err := os.ReadFile(path + ".minisig")
	if err != nil {
		sig = nil
	}
	return s.examine(data, sig, path)
}

// FetchPack downloads a pack release: tag "" picks the newest profiles-*.
func (s *Service) FetchPack(ctx context.Context, tag string) (*PackFile, error) {
	h := s.Release.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	if tag == "" {
		var err error
		if tag, err = s.latestPackTag(ctx, h); err != nil {
			return nil, err
		}
	}
	if !strings.HasPrefix(tag, profiles.PackTagPrefix) || strings.ContainsAny(tag, "/\\?# ") || strings.Contains(tag, "..") {
		return nil, valise.New("pack_bad_tag", "资料包版本号无效："+tag, "使用形如 "+profiles.PackTagPrefix+"2026.10.31 的版本号")
	}
	data, err := s.download(ctx, h, s.releaseBase()+"/"+tag+"/"+profiles.PackAsset, 4<<20)
	if err != nil {
		return nil, err
	}
	sig, err := s.download(ctx, h, s.releaseBase()+"/"+tag+"/"+profiles.PackSigAsset, 1<<20)
	if err != nil {
		return nil, err
	}
	return s.examine(data, sig, tag)
}

func (s *Service) examine(data, sig []byte, from string) (*PackFile, error) {
	p, err := profiles.Read(data)
	if err != nil {
		return nil, err
	}
	return &PackFile{Data: data, Sig: sig, From: from, Pack: p, SignatureErr: profiles.Verify(data, sig, s.Release.PublicKey)}, nil
}

// InstallPack installs a verified pack.
func (s *Service) InstallPack(f *PackFile) error {
	if f.SignatureErr != nil {
		return f.SignatureErr
	}
	_, err := profiles.Install(s.Dirs.Data, f.Data, f.Sig, s.Release.PublicKey)
	return err
}

// InstallPackAsLocal writes an unverified pack's profiles as local profiles.
func (s *Service) InstallPackAsLocal(f *PackFile) ([]string, error) {
	return profiles.InstallLocal(s.Dirs.Data, f.Pack)
}

func (s *Service) releaseBase() string {
	if s.Release.BaseURL != "" {
		return s.Release.BaseURL
	}
	return porter.DefaultReleaseBase
}

// ReleasesAPI lists the project's releases; overridable in tests.
var ReleasesAPI = "https://api.github.com/repos/NazukiMio/Valise/releases?per_page=50"

// ReleasesAtomURL is the public GitHub Releases Atom feed fallback when
// api.github.com is rate-limited or unavailable; overridable in tests.
var ReleasesAtomURL = "https://github.com/NazukiMio/Valise/releases.atom"

func (s *Service) latestPackTag(ctx context.Context, h *http.Client) (string, error) {
	b, apiErr := s.download(ctx, h, ReleasesAPI, 2<<20)
	if apiErr == nil {
		var rels []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err := json.Unmarshal(b, &rels); err == nil {
			best := ""
			for _, r := range rels {
				if !r.Draft && !r.Prerelease && strings.HasPrefix(r.Tag, profiles.PackTagPrefix) && r.Tag > best {
					best = r.Tag
				}
			}
			if best == "" {
				return "", valise.New("pack_none", "还没有发布任何 harness 资料包", "稍后再试，或用 --file 安装本地文件")
			}
			return best, nil
		} else {
			apiErr = valise.New("pack_list", "无法读取 Release 列表", "稍后重试，或用 --tag 指定版本").WithDetail(err.Error())
		}
	}
	if tag, atomErr := s.latestPackTagFromAtom(ctx, h); atomErr == nil {
		return tag, nil
	} else if ue, ok := valise.As(atomErr); ok && ue.Code == "pack_none" {
		return "", atomErr
	}
	return "", valise.New("pack_list", "读不到 Valise 的发布列表，无法在线获取资料包",
		"项目还没有公开发布或网络不通时，改用本地文件安装（valise profiles install --file <文件>，终端界面里选「本地文件」）").WithDetail(errText1(apiErr))
}

func (s *Service) latestPackTagFromAtom(ctx context.Context, h *http.Client) (string, error) {
	if ReleasesAtomURL == "" {
		return "", valise.New("pack_list", "未配置 Atom 备选地址", "")
	}
	b, err := s.download(ctx, h, ReleasesAtomURL, 2<<20)
	if err != nil {
		return "", err
	}
	var feed struct {
		Entries []struct {
			Links []struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(b, &feed); err != nil {
		return "", valise.New("pack_list", "无法解析 Release Atom 列表", "稍后重试，或用 --tag 指定版本").WithDetail(err.Error())
	}
	best := ""
	const marker = "/releases/tag/"
	for _, entry := range feed.Entries {
		for _, link := range entry.Links {
			idx := strings.Index(link.Href, marker)
			if idx < 0 {
				continue
			}
			tag := link.Href[idx+len(marker):]
			if cut := strings.IndexAny(tag, "?#"); cut >= 0 {
				tag = tag[:cut]
			}
			decoded, err := url.PathUnescape(tag)
			if err != nil {
				continue
			}
			tag = strings.TrimSpace(decoded)
			if !strings.HasPrefix(tag, profiles.PackTagPrefix) || strings.ContainsAny(tag, "/\\?# ") || strings.Contains(tag, "..") {
				continue
			}
			if tag > best {
				best = tag
			}
		}
	}
	if best == "" {
		return "", valise.New("pack_none", "还没有发布任何 harness 资料包", "稍后再试，或用 --file 安装本地文件")
	}
	return best, nil
}

func (s *Service) download(ctx context.Context, h *http.Client, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, valise.New("download_failed", "下载失败", "检查网络后重试").WithDetail(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, valise.New("download_failed", fmt.Sprintf("下载失败（HTTP %d）", resp.StatusCode), "确认这个地址存在后重试；资料包也可以用本地文件安装").WithDetail(url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, valise.New("download_failed", "下载中断", "重试").WithDetail(err.Error())
	}
	if int64(len(b)) > max {
		return nil, valise.New("download_too_large", "下载的文件超出大小限制", "确认来源正确").WithDetail(url)
	}
	return b, nil
}

// LocalProfileFile is one local profile YAML read from disk, previewed but
// not yet imported.
type LocalProfileFile struct {
	From              string          `json:"from"`
	ID                string          `json:"id"`
	DisplayName       string          `json:"display_name"`
	Version           int             `json:"profile_version"`
	Status            string          `json:"status"`
	RestoreCap        string          `json:"restore_capability"`
	Pending           int             `json:"pending"`
	Warnings          []profile.Issue `json:"warnings,omitempty"`
	Commands          []string        `json:"commands,omitempty"`
	RefusedCmds       []string        `json:"refused_commands,omitempty"`
	ReplacesLocal     bool            `json:"replaces_local,omitempty"`
	OverridesOfficial bool            `json:"overrides_official,omitempty"`
	OfficialVersion   int             `json:"official_version,omitempty"`
	Diff              *ProfileDiff    `json:"diff,omitempty"`
	Data              []byte          `json:"-"`
}

const maxLocalProfileBytes = 1 << 20

// ReadLocalProfileFile parses and lints one local profile YAML together with
// the installed official pack and the other local profiles.
func (s *Service) ReadLocalProfileFile(path string) (*LocalProfileFile, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, valise.New("local_profile_read", "无法读取本地资料文件", "检查路径："+path).WithDetail(err.Error())
	}
	if st.Size() > maxLocalProfileBytes {
		return nil, valise.New("local_profile_too_big", "本地资料文件超过 1 MiB", "确认选的是单份 harness 资料 .yaml 文件")
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, valise.New("local_profile_read", "无法读取本地资料文件", "检查路径："+path).WithDetail(err.Error())
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, maxLocalProfileBytes+1))
	if err != nil {
		return nil, valise.New("local_profile_read", "无法读取本地资料文件", "检查路径："+path).WithDetail(err.Error())
	}
	if len(data) > maxLocalProfileBytes {
		return nil, valise.New("local_profile_too_big", "本地资料文件超过 1 MiB", "确认选的是单份 harness 资料 .yaml 文件")
	}
	f, err := s.checkLocalProfile(data, filepath.Base(path))
	if err != nil {
		return nil, err
	}
	f.From = path
	return f, nil
}

// checkLocalProfile is the shared parse, conflict, lint and command audit.
func (s *Service) checkLocalProfile(data []byte, name string) (*LocalProfileFile, error) {
	newDoc, err := profile.Parse(data)
	if err != nil {
		return nil, valise.New("local_profile_parse", "本地资料文件无法解析", "对照 docs/PROFILE_AUTHORING.md 修正后重试").WithDetail(err.Error())
	}
	newDoc.Path = name
	id := newDoc.Profile.ID
	replaces := false
	if _, err := os.Stat(filepath.Join(profiles.LocalDir(s.Dirs.Data), id+".yaml")); err == nil {
		replaces = true
	}
	matches, _ := filepath.Glob(filepath.Join(profiles.LocalDir(s.Dirs.Data), "*.yaml"))
	sort.Strings(matches)
	var otherLocal []*profile.Document
	localIDs := map[string]bool{id: true}
	for _, m := range matches {
		d, err := profile.LoadFile(m)
		if err != nil {
			continue
		}
		if d.Profile.ID == id {
			replaces = true
			continue
		}
		if d.Profile.ID != "" {
			if localIDs[d.Profile.ID] {
				continue
			}
			localIDs[d.Profile.ID] = true
		}
		otherLocal = append(otherLocal, d)
	}
	var docs []*profile.Document
	overridesOfficial := false
	officialVersion := 0
	pack, packErr := profiles.Installed(s.Dirs.Data, s.Release.PublicKey)
	if packErr == nil && pack != nil {
		for _, d := range pack.Docs {
			if d.Profile.ID == id {
				overridesOfficial = true
				officialVersion = d.Profile.ProfileVersion
				continue
			}
			if localIDs[d.Profile.ID] {
				continue
			}
			docs = append(docs, d)
		}
	}
	docs = append(docs, otherLocal...)
	docs = append(docs, newDoc)
	results := profile.Lint(docs)
	var res *profile.Result
	var childIssues []string
	for _, r := range results {
		if r.Doc == newDoc {
			r := r
			res = &r
		} else if r.Doc.Profile.Extends == id && len(r.Issues) > 0 {
			for _, i := range r.Issues {
				childIssues = append(childIssues, i.String())
			}
		}
	}
	if res == nil {
		return nil, valise.New("local_profile_lint_failed", "本地资料文件没有通过校验", "对照 docs/PROFILE_AUTHORING.md 修正后重试")
	}
	if len(res.Issues) > 0 || len(childIssues) > 0 {
		var lines []string
		for _, i := range res.Issues {
			lines = append(lines, i.String())
		}
		lines = append(lines, childIssues...)
		return nil, valise.New("local_profile_lint_failed", "本地资料文件没有通过校验", "按详情里的字段提示修正后重试").WithDetail(strings.Join(lines, "\n"))
	}
	eff := res.Effective
	if eff == nil {
		return nil, valise.New("local_profile_lint_failed", "本地资料文件无法解析其父资料", "确认 extends 的父资料已安装")
	}
	f := &LocalProfileFile{
		ID: id, DisplayName: eff.DisplayName, Version: eff.ProfileVersion, Status: eff.Status,
		RestoreCap: eff.CapabilityStatus("restore"), Pending: len(newDoc.Pending),
		Warnings: res.Warnings, Commands: profiles.Commands(eff),
		ReplacesLocal: replaces, OverridesOfficial: overridesOfficial, OfficialVersion: officialVersion,
		Data: data,
	}
	if set, err := s.Profiles(); err == nil && set != nil {
		if existing := set.ByID[id]; existing != nil && existing.Effective != nil {
			f.Diff = diffProfiles(existing.Effective, eff)
		}
	}
	for _, cmd := range profile.Commands(eff) {
		if len(cmd.Argv) == 0 {
			continue
		}
		for _, fd := range install.CheckCommand(cmd.Argv, "") {
			if fd.Refuse {
				f.RefusedCmds = append(f.RefusedCmds, cmd.Label+": "+strings.Join(cmd.Argv, " ")+"（"+fd.Reason+"）")
			}
		}
	}
	return f, nil
}

// InstallLocalProfile writes a previewed local profile after checking it again.
func (s *Service) InstallLocalProfile(f *LocalProfileFile) (string, error) {
	if f == nil || len(f.Data) == 0 {
		return "", valise.New("local_profile_empty", "没有可导入的本地资料", "重新选择文件")
	}
	if len(f.RefusedCmds) > 0 {
		return "", valise.New("local_profile_refused", "资料里含有 Valise 在任何模式下都不会执行的命令", "删除这些命令后重新选择文件").WithDetail(strings.Join(f.RefusedCmds, "\n"))
	}
	again, err := s.checkLocalProfile(f.Data, f.ID+".yaml")
	if err != nil {
		return "", err
	}
	if len(again.RefusedCmds) > 0 {
		return "", valise.New("local_profile_refused", "资料里含有 Valise 在任何模式下都不会执行的命令", "删除这些命令后重新选择文件").WithDetail(strings.Join(again.RefusedCmds, "\n"))
	}
	path, err := profiles.WriteLocalFile(s.Dirs.Data, again.ID, f.Data)
	if err != nil {
		return "", valise.New("local_profile_write", "无法写入本地资料", "检查数据目录权限").WithDetail(err.Error())
	}
	return path, nil
}
