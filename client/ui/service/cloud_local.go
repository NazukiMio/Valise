package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"valise/core/kittools"
	"valise/core/valise"
)

// rclone on this computer (docs/ARCHITECTURE.md 7.3, "这台电脑登录"): the
// one on PATH, else the signed list's pinned build downloaded into
// Valise's own folder after the user confirms. Valise's own config file
// keeps the user's rclone configuration untouched.

// LocalRclone is the rclone this computer uses.
type LocalRclone struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	// Ours: downloaded by Valise into its own folder.
	Ours bool `json:"ours"`
}

func (s *Service) ownRclone() string { return filepath.Join(s.Dirs.Data, "tools", "rclone") }

// LocalRcloneConfig is Valise's rclone config on this computer.
func (s *Service) LocalRcloneConfig() string {
	return filepath.Join(s.Dirs.Data, "rclone", "rclone.conf")
}

func rcloneVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line), nil
}

// lookRclone finds rclone on PATH; tests replace it so a real rclone is
// never run by a test.
var lookRclone = func() (string, error) { return exec.LookPath("rclone") }

// FindLocalRclone returns the rclone to use here, nil when there is none.
func (s *Service) FindLocalRclone(ctx context.Context) *LocalRclone {
	if p, err := lookRclone(); err == nil {
		if v, err := rcloneVersion(ctx, p); err == nil {
			return &LocalRclone{Path: p, Version: v}
		}
	}
	if v, err := rcloneVersion(ctx, s.ownRclone()); err == nil {
		return &LocalRclone{Path: s.ownRclone(), Version: v, Ours: true}
	}
	return nil
}

// RcloneDownload is what downloading rclone here means, to show before
// asking: the address, the expected SHA-256 and, when known, the size.
type RcloneDownload struct {
	kittools.ClientArchive
	Version string `json:"version"`
	Bytes   int64  `json:"bytes,omitempty"`
}

// PlanLocalRclone finds this computer's download in the signed list.
func (s *Service) PlanLocalRclone(ctx context.Context) (*RcloneDownload, error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	var t *kittools.Tool
	if set.Kit != nil {
		t = set.Kit.ByID("rclone")
	}
	if t == nil || t.Binary == nil {
		return nil, valise.New("kit_tools_none", "已安装的资料包里没有 rclone", "安装或更新资料包：valise profiles install")
	}
	a := t.ClientArchive(runtime.GOOS, runtime.GOARCH)
	if a == nil {
		return nil, valise.New("rclone_no_build", "清单里没有这台电脑（"+runtime.GOOS+"/"+runtime.GOARCH+"）的 rclone", "自己安装 rclone 并放进 PATH 后重试")
	}
	d := &RcloneDownload{ClientArchive: *a, Version: t.Binary.Version}
	h := s.Release.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	if req, err := http.NewRequestWithContext(ctx, http.MethodHead, a.URL, nil); err == nil {
		if resp, err := h.Do(req); err == nil {
			resp.Body.Close()
			d.Bytes = resp.ContentLength
		}
	}
	return d, nil
}

// InstallLocalRclone downloads the planned build, checks its SHA-256 and
// puts the program into Valise's own folder. Call only after the user
// confirmed the plan.
func (s *Service) InstallLocalRclone(ctx context.Context, d *RcloneDownload) (*LocalRclone, error) {
	h := s.Release.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	data, err := s.download(ctx, h, d.URL, 256<<20)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != d.SHA256 {
		return nil, valise.New("rclone_sha_mismatch", "下载的 rclone 与清单记录的 SHA-256 不一致，没有使用", "重试；仍不一致请报告").WithDetail(d.URL)
	}
	bin, err := extractMember(d.URL, data, d.Member)
	if err != nil {
		return nil, valise.New("rclone_extract", "无法从下载的压缩包里取出 rclone", "请报告").WithDetail(err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(s.ownRclone()), 0o700); err != nil {
		return nil, err
	}
	tmp := s.ownRclone() + ".new"
	if err := os.WriteFile(tmp, bin, 0o700); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, s.ownRclone()); err != nil {
		return nil, err
	}
	v, err := rcloneVersion(ctx, s.ownRclone())
	if err != nil {
		return nil, valise.New("rclone_broken", "下载的 rclone 无法运行", "自己安装 rclone 并放进 PATH 后重试").WithDetail(err.Error())
	}
	return &LocalRclone{Path: s.ownRclone(), Version: v, Ours: true}, nil
}

// ensureLocalRcloneConfig makes Valise's rclone config here (0600).
func (s *Service) ensureLocalRcloneConfig() error {
	if err := os.MkdirAll(filepath.Dir(s.LocalRcloneConfig()), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.LocalRcloneConfig(), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}
