package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"valise/client/porter"
	"valise/client/profiles"
	"valise/client/relay"
	"valise/client/ssh"
	"valise/client/tunnel"
	"valise/core/enroll"
	"valise/core/login"
	"valise/core/profile"
	"valise/core/valise"
)

// Login relay states, in order (docs/LOGIN_RELAY.md section 1).
const (
	LoginWaitingLink      = "waiting_link"
	LoginRelayReady       = "relay_ready" // callback tunnel listening, before the browser opens
	LoginBrowserOpened    = "browser_opened"
	LoginLinkSkipped      = "link_skipped"
	LoginCallbackReceived = "callback_received"
	LoginExited           = "exited"
)

// LoginTarget is an instance and the login methods its profile declares.
type LoginTarget struct {
	Record  *enroll.Record  `json:"record"`
	Profile profile.Profile `json:"profile"`
	Local   bool            `json:"profile_local"`
}

// LoginStatus is one state change of a running login.
type LoginStatus struct {
	State    string   `json:"state"`
	Link     string   `json:"link,omitempty"`
	Domain   string   `json:"domain,omitempty"`
	Port     int      `json:"port,omitempty"`
	Local    []string `json:"local_addrs,omitempty"`
	Remote   string   `json:"remote_addr,omitempty"`
	ExitCode int      `json:"exit_code"`
}

// LoginIO connects a running login to the user. Stdin and Stdout carry the
// remote PTY; they are never logged.
type LoginIO struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Cols, Rows  int
	Status      func(LoginStatus)
	ConfirmOpen func(link, domain string) bool
	OpenBrowser func(link string) error
}

// LoginMethods reads the instance record and the client's profile for it.
func (s *Service) LoginMethods(ctx context.Context, host, instance string) (*LoginTarget, error) {
	var res enroll.InstanceGetResult
	if err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, enroll.MethodInstanceGet, enroll.InstanceGetParams{InstanceID: instance}, &res)
	}); err != nil {
		return nil, err
	}
	if res.Record == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有登记实例 "+instance, "先运行 valise enroll <服务器> --accept")
	}
	set, err := profiles.Load(profiles.LocalDir(s.Dirs.Data))
	if err != nil {
		return nil, err
	}
	lp := set.ByID[res.Record.ProfileID]
	if lp == nil {
		return nil, valise.New("profile_missing", "客户端中没有 profile "+res.Record.ProfileID, "升级客户端")
	}
	return &LoginTarget{Record: res.Record, Profile: *lp.Effective, Local: lp.Local}, nil
}

// OpenURL opens a link in the system browser of this machine.
func OpenURL(link string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, link).Run()
}

// callbackWait bounds how long the callback listener may take to appear.
var callbackWait = 10 * time.Second

// RunLogin runs one login method of an instance on host and relays it.
func (s *Service) RunLogin(ctx context.Context, host, instance, method string, lio LoginIO) (*LoginStatus, error) {
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	var m *profile.Login
	for i := range target.Profile.Login {
		if target.Profile.Login[i].ID == method {
			m = &target.Profile.Login[i]
		}
	}
	if m == nil {
		return nil, valise.New("login_method_unknown", "profile "+target.Profile.ID+" 中没有登录方式 "+method, "不带 --method 运行 valise login 查看可用的登录方式")
	}
	if m.Kind == login.KindRemoteBrowser {
		return nil, valise.New("login_kind_unsupported", "远端浏览器登录还没有实现", "改用 profile 中的其他登录方式")
	}
	timeout := time.Duration(m.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = login.DefaultTimeoutSeconds * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	t, _, err := s.transport(host)
	if err != nil {
		return nil, err
	}
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return nil, err
	}
	c, _, err := ssh.Dial(ctx, t, found.Path, s.Version)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var prep login.PrepareResult
	if err := c.Call(ctx, login.MethodPrepare, login.PrepareParams{InstanceID: instance, Login: *m}, &prep); err != nil {
		return nil, err
	}
	exit := -1
	defer func() {
		// A fresh context: the session file goes even after a timeout.
		endCtx, endCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer endCancel()
		c.Call(endCtx, login.MethodEnd, login.EndParams{Session: prep.Session, ExitCode: exit}, nil)
	}()

	status := func(st LoginStatus) {
		if lio.Status != nil {
			lio.Status(st)
		}
	}
	scan, err := relay.NewScanner(m.URLPattern)
	if err != nil {
		return nil, err
	}
	remote := ssh.Quote(found.Path) + " login-run " + prep.Session
	if lio.Cols > 0 && lio.Rows > 0 {
		remote += " " + strconv.Itoa(lio.Cols) + " " + strconv.Itoa(lio.Rows)
	}
	cmd := exec.CommandContext(ctx, t.Binary(), t.TTYArgs(remote)...)
	out := io.MultiWriter(lio.Stdout, scan)
	cmd.Stdout, cmd.Stderr = out, out
	// After ssh is killed, do not wait long for its output pipes.
	cmd.WaitDelay = 3 * time.Second
	// Keystrokes are copied by our own goroutine: a terminal never reaches
	// EOF, and Wait must not wait for it once the login command has exited.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	status(LoginStatus{State: LoginWaitingLink})
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if lio.Stdin != nil {
		go func() { io.Copy(stdin, lio.Stdin); stdin.Close() }()
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	relayErr := make(chan error, 1)
	var once sync.Once
	var lnMu sync.Mutex
	var ln *tunnel.Listener
	defer func() {
		lnMu.Lock()
		defer lnMu.Unlock()
		if ln != nil {
			ln.Close()
		}
	}()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	handled := false
	for {
		select {
		case err := <-waited:
			scan.Flush()
			exit = exitCode(cmd, err)
			st := LoginStatus{State: LoginExited, ExitCode: exit}
			status(st)
			if ctx.Err() == context.DeadlineExceeded {
				return &st, valise.New("login_timeout", fmt.Sprintf("登录在 %d 秒内没有完成，已结束登录命令", int(timeout.Seconds())), "重新运行 valise login")
			}
			return &st, nil
		case err := <-relayErr:
			cmd.Process.Kill()
			<-waited
			exit = exitCode(cmd, nil)
			status(LoginStatus{State: LoginExited, ExitCode: exit})
			return nil, err
		case <-tick.C:
			if handled {
				continue
			}
			links := scan.Links()
			var shim login.URLsResult
			if c.Call(ctx, login.MethodURLs, login.SessionParams{Session: prep.Session}, &shim) == nil {
				links = append(shim.URLs, links...)
			}
			if len(links) == 0 {
				continue
			}
			handled = true
			link := links[0]
			go func() {
				l, err := s.relayLink(ctx, c, t, m.Kind, link, lio, status, &once)
				lnMu.Lock()
				ln = l
				lnMu.Unlock()
				if err != nil {
					relayErr <- err
				}
			}()
		}
	}
}

// relayLink sets up the callback tunnel when the link redirects to a
// loopback address, then opens the link after the user confirms.
func (s *Service) relayLink(ctx context.Context, c *ssh.RPCClient, t *ssh.Transport, kind, link string, lio LoginIO,
	status func(LoginStatus), once *sync.Once) (*tunnel.Listener, error) {
	domain := relay.Domain(link)
	var ln *tunnel.Listener
	st := LoginStatus{Link: link, Domain: domain}
	if cb, ok := relay.LoopbackCallback(link); ok && kind == login.KindLoopbackOAuth {
		addr, err := listeningAddr(ctx, c, cb.Port)
		if err != nil {
			return nil, err
		}
		ln, err = tunnel.Bind(cb.Port)
		if err != nil {
			var busy *tunnel.BusyError
			if errors.As(err, &busy) {
				return nil, valise.New("login_port_busy", busy.Error()+"；登录回调必须使用同一端口，Valise 不会换端口",
					"关闭占用该端口的程序后重新运行 valise login")
			}
			return nil, err
		}
		ln.OnConn = func() {
			once.Do(func() {
				status(LoginStatus{State: LoginCallbackReceived, Port: cb.Port, Local: ln.Addrs, Remote: addr})
			})
		}
		go ln.Serve(ctx, tunnel.SSHDialer(t, addr, cb.Port))
		st.Port, st.Local, st.Remote = cb.Port, ln.Addrs, addr
		ready := st
		ready.State = LoginRelayReady
		status(ready)
	}
	if lio.ConfirmOpen != nil && !lio.ConfirmOpen(link, domain) {
		st.State = LoginLinkSkipped
		status(st)
		return ln, nil
	}
	open := lio.OpenBrowser
	if open == nil {
		open = OpenURL
	}
	if err := open(link); err != nil {
		st.State = LoginLinkSkipped
	} else {
		st.State = LoginBrowserOpened
	}
	status(st)
	return ln, nil
}

// listeningAddr asks porter which address listens on port and picks the
// one to forward to: [::1] only when nothing listens on IPv4.
func listeningAddr(ctx context.Context, c *ssh.RPCClient, port int) (string, error) {
	deadline := time.Now().Add(callbackWait)
	for {
		var res login.ListenResult
		if err := c.Call(ctx, login.MethodListen, login.ListenParams{Port: port}, &res); err != nil {
			return "", err
		}
		v4, v6 := false, false
		for _, a := range res.Addrs {
			if strings.Contains(a, ":") {
				v6 = true
			} else {
				v4 = true
			}
		}
		switch {
		case v4:
			return "127.0.0.1", nil
		case v6:
			return "::1", nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return "", valise.New("login_callback_missing", fmt.Sprintf("服务器上没有程序监听回调端口 %d", port),
				"登录命令可能不在服务器上接收回调；按它的提示把浏览器最后跳转到的地址粘贴回终端")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func exitCode(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}
