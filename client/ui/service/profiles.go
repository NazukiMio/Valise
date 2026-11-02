package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"valise/client/porter"
	"valise/client/profiles"
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
	Local       []string           `json:"local,omitempty"`
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
	for id, l := range set.ByID {
		if l.Local {
			st.Local = append(st.Local, id)
		}
	}
	sort.Strings(st.Local)
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
	if !strings.HasPrefix(tag, profiles.PackTagPrefix) || strings.ContainsAny(tag, "/?# ") {
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

func (s *Service) latestPackTag(ctx context.Context, h *http.Client) (string, error) {
	b, err := s.download(ctx, h, ReleasesAPI, 2<<20)
	if err != nil {
		return "", err
	}
	var rels []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(b, &rels); err != nil {
		return "", valise.New("pack_list", "无法读取 Release 列表", "稍后重试，或用 --tag 指定版本").WithDetail(err.Error())
	}
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
		return nil, valise.New("download_failed", fmt.Sprintf("下载失败（HTTP %d）", resp.StatusCode), "确认版本号存在后重试").WithDetail(url)
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
