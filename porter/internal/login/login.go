// Package login runs the server side of the login relay: sessions, the
// login command under the ssh PTY, the BROWSER shim and listening-address
// lookups (docs/LOGIN_RELAY.md section 5).
package login

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"valise/core/login"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"

	"golang.org/x/sys/unix"
)

// Session is what login-run needs; it is written by login_prepare.
type Session struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instance_id"`
	LoginID    string    `json:"login_id"`
	Argv       []string  `json:"argv"` // argv[0] resolved on the login PATH
	Env        []string  `json:"env"`
	Created    time.Time `json:"created"`
}

// Dir is ~/.local/state/valise/login.
func Dir(home string) string { return filepath.Join(home, ".local", "state", "valise", "login") }

func sessionPath(home, id string) string { return filepath.Join(Dir(home), id+".json") }
func urlPath(home, id string) string     { return filepath.Join(Dir(home), id+".url") }

func badSession(id string) error {
	return valise.New("login_session_invalid", "登录会话 "+id+" 无效或已结束", "重新运行 valise login")
}

// Prepare expands the login argv for an enrolled instance and writes the
// session. Nothing runs yet.
func Prepare(e *porterenroll.Env, p login.PrepareParams) (*login.PrepareResult, error) {
	rec, err := porterenroll.ReadRecord(e.Home, p.InstanceID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+p.InstanceID, "先运行 valise enroll <服务器> --accept")
	}
	if prof, err := porterenroll.ReadProfile(e.Home, p.InstanceID); err == nil {
		e = e.ForInstance(prof, rec)
	}
	if len(p.Login.Argv) == 0 {
		return nil, valise.New("login_argv_missing", "登录方式 "+p.Login.ID+" 没有声明命令", "检查 profile 的 login 条目")
	}
	argv, ok := e.ExpandArgv(p.Login.Argv, map[string]string{"STATE_DIR": rec.Roots["state"]})
	if !ok {
		return nil, valise.New("login_argv_unexpanded", "登录命令中的变量无法展开", "检查 profile 的 login 条目")
	}
	if !filepath.IsAbs(argv[0]) {
		bin, err := e.LookPath(argv[0])
		if err != nil {
			return nil, valise.New("login_binary_missing", "服务器的登录 PATH 中找不到 "+argv[0], "确认 harness 已安装，且在登录 shell 的 PATH 中")
		}
		argv[0] = bin
	}
	return write(e, p.InstanceID, p.Login.ID, argv)
}

// PrepareArgv writes a session for a fixed argv that is not a harness
// login (a cloud drive's rclone login, docs/LOGIN_RELAY.md section 7).
func PrepareArgv(e *porterenroll.Env, loginID string, argv []string) (*login.PrepareResult, error) {
	return write(e, "", loginID, argv)
}

func write(e *porterenroll.Env, instance, loginID string, argv []string) (*login.PrepareResult, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	s := Session{ID: "ls_" + hex.EncodeToString(b[:]), InstanceID: instance, LoginID: loginID,
		Argv: argv, Env: e.Vars, Created: time.Now().UTC()}
	if err := os.MkdirAll(Dir(e.Home), 0o700); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(sessionPath(e.Home, s.ID), data, 0o600); err != nil {
		return nil, err
	}
	return &login.PrepareResult{Session: s.ID, Argv: argv}, nil
}

func load(home, id string) (*Session, error) {
	if !login.ValidSession(id) {
		return nil, badSession(id)
	}
	b, err := os.ReadFile(sessionPath(home, id))
	if err != nil {
		return nil, badSession(id)
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, badSession(id)
	}
	return &s, nil
}

// Exec replaces this process with the session's login command, inheriting
// the ssh PTY. BROWSER points at this porter's browser-shim. cols and rows,
// when set, size the PTY like the user's terminal.
func Exec(home, id, porterPath string, cols, rows int) error {
	s, err := load(home, id)
	if err != nil {
		return err
	}
	env := make([]string, 0, len(s.Env)+2)
	for _, kv := range s.Env {
		if !strings.HasPrefix(kv, "BROWSER=") && !strings.HasPrefix(kv, login.SessionEnv+"=") {
			env = append(env, kv)
		}
	}
	env = append(env, "BROWSER="+porterPath+" browser-shim", login.SessionEnv+"="+id)
	if err := os.Chdir(home); err != nil {
		return err
	}
	saneTerminal(0)
	if cols > 0 && rows > 0 && cols < 10000 && rows < 10000 {
		unix.IoctlSetWinsize(0, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	}
	return syscall.Exec(s.Argv[0], s.Argv, env)
}

// Shim records links a login command tried to open. Without a session it
// only prints them, so a stray call never fails the caller.
func Shim(home, id string, args []string) error {
	var links []string
	for _, a := range args {
		if a = strings.TrimSpace(a); a != "" {
			links = append(links, a)
		}
	}
	if !login.ValidSession(id) {
		for _, l := range links {
			fmt.Fprintln(os.Stderr, l)
		}
		return nil
	}
	if _, err := os.Stat(sessionPath(home, id)); err != nil {
		return nil
	}
	f, err := os.OpenFile(urlPath(home, id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, l := range links {
		fmt.Fprintln(f, l)
	}
	return nil
}

// URLs returns the links browser-shim recorded for a session.
func URLs(home, id string) (*login.URLsResult, error) {
	if _, err := load(home, id); err != nil {
		return nil, err
	}
	res := &login.URLsResult{URLs: []string{}}
	b, err := os.ReadFile(urlPath(home, id))
	if errors.Is(err, os.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			res.URLs = append(res.URLs, l)
		}
	}
	return res, nil
}

// End removes a session's files and returns it for the audit log.
func End(home, id string) (*Session, error) {
	s, err := load(home, id)
	if err != nil {
		return nil, err
	}
	os.Remove(urlPath(home, id))
	return s, os.Remove(sessionPath(home, id))
}

// ProcNet is where listening sockets are read; tests replace it.
var ProcNet = "/proc/net"

// ListenAddrs lists the addresses listening on TCP port, read from
// /proc/net/tcp and /proc/net/tcp6.
func ListenAddrs(port int) (*login.ListenResult, error) {
	if port <= 0 || port > 65535 {
		return nil, valise.New("port_invalid", "端口无效："+strconv.Itoa(port), "")
	}
	res := &login.ListenResult{Addrs: []string{}}
	seen := map[string]bool{}
	for _, name := range []string{"tcp", "tcp6"} {
		f, err := os.Open(filepath.Join(ProcNet, name))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // header
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 4 || fields[3] != "0A" { // 0A = LISTEN
				continue
			}
			ip, p, ok := parseHexAddr(fields[1])
			if ok && p == port && !seen[ip] {
				seen[ip] = true
				res.Addrs = append(res.Addrs, ip)
			}
		}
		f.Close()
	}
	return res, nil
}

// parseHexAddr decodes "0100007F:05AF": the address is stored as 32-bit
// words in host (little-endian) order.
func parseHexAddr(s string) (string, int, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return "", 0, false
	}
	raw, err := hex.DecodeString(a)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, false
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		ip[i], ip[i+1], ip[i+2], ip[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	return ip.String(), int(port), true
}
