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
	st, done, err := stationOf(p, t)
	if err != nil {
		return "", "", err
	}
	defer done()
	return stationsync.With(ctx, envRunner{e, p.Home}, st, op)
}

// stationOf is how porter reaches a target: a managed station with its
// recorded host keys and the server's station key (and certificate,
// <key>-cert.pub, which ssh loads by itself).
func stationOf(p Paths, t *vaultrpc.Target) (stationsync.Station, func(), error) {
	st := stationsync.Station{Name: t.Name, Kind: t.Kind, Host: strings.SplitN(t.Dest, ":", 2)[0], Tmp: p.Tmp()}
	c := t.Station
	if c == nil {
		return st, func() {}, nil
	}
	if err := os.MkdirAll(p.Tmp(), 0o700); err != nil {
		return st, nil, err
	}
	kh, err := os.CreateTemp(p.Tmp(), "known-hosts-")
	if err != nil {
		return st, nil, err
	}
	kh.WriteString(strings.Join(c.KnownHosts, "\n") + "\n")
	kh.Close()
	st.Host = c.Address
	st.SSH = []string{"-o", "UserKnownHostsFile=" + kh.Name(), "-o", "StrictHostKeyChecking=yes", "-o", "IdentitiesOnly=yes",
		"-o", "IdentityAgent=none", "-i", p.StationKey(t.Name), "-p", strconv.Itoa(c.Port), "-l", c.User}
	return st, func() { os.Remove(kh.Name()) }, nil
}

// SetStationCert installs the client-signed certificate of the server's
// station key.
func SetStationCert(p Paths, name, cert string) error {
	f := strings.Fields(cert)
	if !stationNameRe.MatchString(name) || len(f) < 2 || !strings.HasSuffix(f[0], "-cert-v01@openssh.com") || strings.ContainsAny(cert, "\r") {
		return valise.New("station_cert_invalid", "远程备份位置证书格式不对", "在 Valise 里重新同步一次")
	}
	return writeAtomic(p.StationKey(name)+"-cert.pub", []byte(strings.TrimSpace(cert)+"\n"), 0o600)
}

// StationProbe connects to a managed station with the current
// certificate and returns what its forced command printed (the address
// the station sees, during attach).
func StationProbe(ctx context.Context, e *porterenroll.Env, p Paths, name string) (string, error) {
	t, err := findTarget(p, name)
	if err != nil {
		return "", err
	}
	if t.Station == nil {
		return "", valise.New("station_unmanaged", name+" 不是 Valise 管理的远程备份位置", "")
	}
	st, done, err := stationOf(p, t)
	if err != nil {
		return "", err
	}
	defer done()
	argv := append(append([]string{"ssh", "-o", "BatchMode=yes"}, st.SSH...), st.Host, "true")
	out, err := e.Run(ctx, time.Minute, p.Home, argv...)
	if err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", valise.New("station_probe_failed", "服务器连不上远程备份位置 "+name, "经这台电脑中转").WithDetail(lastLine(string(out.Stderr)))
	}
	s := strings.TrimSpace(string(out.Stdout))
	if len(s) > 200 {
		s = s[:200]
	}
	return s, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

var stationNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// StationKeyFor makes the server's key for a managed station once (with
// ssh-keygen; the private key never leaves the server) and returns its
// public key.
func StationKeyFor(ctx context.Context, e *porterenroll.Env, p Paths, name, comment string) (string, error) {
	if !stationNameRe.MatchString(name) {
		return "", valise.New("station_name_invalid", "远程备份位置名只能包含小写字母、数字和连字符", "例如 nas")
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
			return "", valise.New("station_key_failed", "无法在服务器上生成远程备份位置钥匙", "确认服务器上有 ssh-keygen（OpenSSH 客户端）").WithDetail(detail)
		}
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(pub)), nil
}
