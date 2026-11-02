package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/valise"
)

// DeploySource is chosen explicitly by the user: exactly one field is set.
type DeploySource struct {
	LocalPath  string `json:"local_path,omitempty"`
	ReleaseTag string `json:"release_tag,omitempty"`
}

// DeployReport is the outcome of a porter deploy.
type DeployReport struct {
	Host     string          `json:"host"`
	Source   DeploySource    `json:"source"`
	Platform porter.Platform `json:"platform"`
	Porter   porter.Found    `json:"porter"`
}

const maxLocalPorterBytes = 64 << 20

// DeployPorter installs the porter as ~/.local/bin/valise-porter on host.
func (s *Service) DeployPorter(ctx context.Context, host string, src DeploySource) (*DeployReport, error) {
	if (src.LocalPath == "") == (src.ReleaseTag == "") {
		return nil, valise.New("deploy_source_required", "需要明确选择 porter 的来源",
			"二选一：--release <版本> 从 GitHub Release 下载并校验，或 --local <文件> 上传本地构建产物")
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return nil, err
	}
	defer done()
	return s.deployOn(ctx, t, src)
}

// deployOn installs the porter through a full login t.
func (s *Service) deployOn(ctx context.Context, t *ssh.Transport, src DeploySource) (*DeployReport, error) {
	if (src.LocalPath == "") == (src.ReleaseTag == "") {
		return nil, valise.New("deploy_source_required", "需要明确选择 porter 的来源",
			"二选一：--release <版本> 从 GitHub Release 下载并校验，或 --local <文件> 上传本地构建产物")
	}
	host := t.Host()
	p, err := porter.RemotePlatform(ctx, t)
	if err != nil {
		return nil, err
	}
	var bin []byte
	if src.LocalPath != "" {
		bin, err = readLocal(src.LocalPath)
	} else {
		bin, err = s.Release.Fetch(ctx, src.ReleaseTag, p)
	}
	if err != nil {
		return nil, err
	}
	found, err := porter.Upload(ctx, t, bin, p)
	if err != nil {
		return nil, err
	}
	return &DeployReport{Host: host, Source: src, Platform: p, Porter: *found}, nil
}

func readLocal(path string) ([]byte, error) {
	// A path typed into the TUI or quoted in a shell keeps its "~".
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, rest)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, valise.New("local_porter_unreadable", "无法读取本地 porter 文件 "+path, "确认路径正确，并且是 valise-porter 的构建产物").WithDetail(err.Error())
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxLocalPorterBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxLocalPorterBytes {
		return nil, valise.New("local_porter_too_large", fmt.Sprintf("本地文件超过 %d MiB，不像是 porter", maxLocalPorterBytes>>20), "确认选择的是 valise-porter 的构建产物")
	}
	return b, nil
}
