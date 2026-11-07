// Package porter finds, verifies and deploys valise-porter on a server.
package porter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"valise/client/ssh"
	"valise/core/rpc"
	"valise/core/valise"
)

// Candidates is the probe chain from AGENT.md, HOME-relative paths written
// with $HOME so the remote shell expands them. The bare command comes last,
// for authorized_keys command="…" setups.
var Candidates = []string{
	"$HOME/.local/bin/valise-porter",
	"$HOME/.local/lib/valise/valise-porter",
	"/usr/local/bin/valise-porter",
}

// BareCommand is the last probe candidate.
const BareCommand = "valise-porter"

// exitNotFound is returned by the probe script when no candidate exists.
const exitNotFound = 3

// Found is a located porter. The result is cached for the session only.
type Found struct {
	Path string          `json:"path"`
	Info rpc.VersionInfo `json:"info"`
}

// Supports requires an explicit capability declaration; an absent list on an
// older porter means unsupported, regardless of its protocol minor version.
func (f *Found) Supports(capability string) bool {
	if f == nil {
		return false
	}
	for _, c := range f.Info.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// ErrNotFound reports that no porter is installed on the host.
func ErrNotFound(host string) *valise.UserError {
	return valise.New("porter_not_found",
		fmt.Sprintf("服务器 %s 上还没有部署 valise-porter", host),
		fmt.Sprintf("运行 valise porter deploy %s --release <版本> 从 Release 部署，或用 --local <文件> 上传本地构建产物", host))
}

func probeScript() string {
	var b strings.Builder
	b.WriteString("for p in")
	for _, c := range Candidates {
		b.WriteString(` "` + c + `"`)
	}
	fmt.Fprintf(&b, `; do if [ -x "$p" ] && [ -f "$p" ]; then printf '%%s\n' "$p"; exit 0; fi; done; exit %d`, exitNotFound)
	return b.String()
}

// Probe walks the probe chain and verifies the porter it finds by running
// `version --json`.
func Probe(ctx context.Context, t *ssh.Transport) (*Found, error) {
	res, err := t.Run(ctx, probeScript(), nil)
	if err != nil {
		return nil, err
	}
	path := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode == 0 && strings.HasPrefix(path, "/") && !strings.Contains(path, "\n") {
		return versionOf(ctx, t, path)
	}
	f, err := versionOf(ctx, t, BareCommand)
	if err != nil {
		if ue, ok := valise.As(err); ok && ue.Code == "porter_unusable" {
			return nil, ErrNotFound(t.Host())
		}
		return nil, err
	}
	return f, nil
}

func versionOf(ctx context.Context, t *ssh.Transport, path string) (*Found, error) {
	return VersionAt(ctx, t, path, ssh.Quote(path))
}

// VersionAt runs `<word> version --json`, where word is already a shell
// word (for example "$HOME/…" in double quotes, so the remote shell expands
// it).
func VersionAt(ctx context.Context, t *ssh.Transport, path, word string) (*Found, error) {
	res, err := t.Run(ctx, word+" version --json", nil)
	if err != nil {
		return nil, err
	}
	var info rpc.VersionInfo
	if res.ExitCode != 0 || json.Unmarshal(res.Stdout, &info) != nil || info.Name != "valise-porter" {
		return nil, valise.New("porter_unusable",
			fmt.Sprintf("服务器 %s 上的 %s 无法正常运行", t.Host(), path),
			fmt.Sprintf("重新部署：valise porter deploy %s", t.Host()),
		).WithDetail(fmt.Sprintf("exit %d\n%s", res.ExitCode, strings.TrimSpace(string(res.Stderr))))
	}
	return &Found{Path: path, Info: info}, nil
}
