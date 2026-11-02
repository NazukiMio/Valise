package seal

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Copies to a target go through core/stationsync (docs/ARCHITECTURE.md
// section 7). A Valise-managed station is reached with its recorded
// address, known_hosts lines and the server's own station key; an older
// target through the server's SSH configuration.

type envRunner struct {
	e    *porterenroll.Env
	home string
}

func (r envRunner) Run(ctx context.Context, timeout time.Duration, argv ...string) (int, []byte, []byte, error) {
	out, err := r.e.Run(ctx, timeout, r.home, argv...)
	if err != nil {
		return 0, nil, nil, err
	}
	return out.Code, out.Stdout, out.Stderr, nil
}

// StationKey is the private key porter uses for a managed station.
func (p Paths) StationKey(name string) string {
	return filepath.Join(p.Home, ".config", "valise", "station-keys", name)
}

// withRemote runs op on the target, falling back from rsync to sftp.
func withRemote(ctx context.Context, e *porterenroll.Env, p Paths, t *vaultrpc.Target, op func(stationsync.Remote) error) (method, note string, err error) {
	st := stationsync.Station{Name: t.Name, Kind: t.Kind, Host: strings.SplitN(t.Dest, ":", 2)[0], Tmp: p.Tmp()}
	if c := t.Station; c != nil {
		if err := os.MkdirAll(p.Tmp(), 0o700); err != nil {
			return "", "", err
		}
		kh, err := os.CreateTemp(p.Tmp(), "known-hosts-")
		if err != nil {
			return "", "", err
		}
		defer os.Remove(kh.Name())
		kh.WriteString(strings.Join(c.KnownHosts, "\n") + "\n")
		kh.Close()
		st.Host = c.Address
		st.SSH = []string{"-o", "UserKnownHostsFile=" + kh.Name(), "-o", "StrictHostKeyChecking=yes", "-o", "IdentitiesOnly=yes",
			"-o", "IdentityAgent=none", "-i", p.StationKey(t.Name), "-p", strconv.Itoa(c.Port), "-l", c.User}
	}
	return stationsync.With(ctx, envRunner{e, p.Home}, st, op)
}

var stationNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// StationKeyFor makes the server's key for a managed station once (with
// ssh-keygen; the private key never leaves the server) and returns its
// public key.
func StationKeyFor(ctx context.Context, e *porterenroll.Env, p Paths, name, comment string) (string, error) {
	if !stationNameRe.MatchString(name) {
		return "", valise.New("station_name_invalid", "空间站名只能包含小写字母、数字和连字符", "例如 nas")
	}
	key := p.StationKey(name)
	if _, err := os.Stat(key); err != nil {
		if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
			return "", err
		}
		out, err := e.Run(ctx, time.Minute, p.Home, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", comment, "-f", key)
		if err != nil || out.Code != 0 {
			detail := ""
			if out != nil {
				detail = string(out.Stderr)
			}
			if err != nil {
				detail = err.Error()
			}
			return "", valise.New("station_key_failed", "无法在服务器上生成空间站钥匙", "确认服务器上有 ssh-keygen（OpenSSH 客户端）").WithDetail(detail)
		}
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(pub)), nil
}
