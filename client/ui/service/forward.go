package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"valise/client/tunnel"
	"valise/core/login"
	"valise/core/valise"
)

// Opening a loopback page of the server on this machine
// (docs/LOGIN_RELAY.md section 6). The destination is always the server's
// loopback address; the query and fragment (often a token) are never shown,
// logged or put in an error, only handed to the browser.

// ForwardTarget is a parsed page address.
type ForwardTarget struct {
	Port int
	// Display holds only scheme, host and port.
	Display string
	open    string
	scheme  string
	path    string
}

// ParseForward accepts a loopback http(s) URL with an explicit port, or a
// bare port number.
func ParseForward(s string) (*ForwardTarget, error) {
	s = strings.TrimSpace(s)
	if p, err := strconv.Atoi(s); err == nil {
		if p < 1 || p > 65535 {
			return nil, valise.New("forward_port_invalid", "端口必须在 1 到 65535 之间", "")
		}
		u := "http://127.0.0.1:" + s + "/"
		return &ForwardTarget{Port: p, Display: "http://127.0.0.1:" + s, open: u, scheme: "http", path: "/"}, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		// The parse error would quote the address; it is not shown.
		return nil, valise.New("forward_url_invalid", "无法识别这个地址", "粘贴完整的地址（例如 http://127.0.0.1:8080/…），或只填端口")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, valise.New("forward_scheme", "只能打开 http 或 https 地址", "")
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, valise.New("forward_not_loopback", "只能打开服务器本机回环地址上的页面（localhost、127.0.0.1、[::1]）",
			"Valise 不转发到其他主机")
	}
	if u.Port() == "" {
		return nil, valise.New("forward_no_port", "地址里没有写明端口", "粘贴带端口的地址，或只填端口")
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil || p < 1 || p > 65535 {
		return nil, valise.New("forward_port_invalid", "端口必须在 1 到 65535 之间", "")
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return &ForwardTarget{Port: p, Display: u.Scheme + "://" + u.Host, open: s, scheme: u.Scheme, path: path}, nil
}

// ForwardPage forwards the page's port from host to the same local port
// and keeps it open until ctx ends or it has been idle for o.Idle. o.Entry
// is ignored.
func (s *Service) ForwardPage(ctx context.Context, host string, ft *ForwardTarget, o OpenOptions) error {
	status := func(st EntryStatus) {
		if o.Status != nil {
			o.Status(st)
		}
	}
	t, _, err := s.transport(host)
	if err != nil {
		return err
	}
	c, err := s.session(ctx, host)
	if err != nil {
		return err
	}
	var listen login.ListenResult
	err = c.Call(ctx, login.MethodListen, login.ListenParams{Port: ft.Port}, &listen)
	c.Close()
	if err != nil {
		return err
	}
	remote := "127.0.0.1"
	if len(listen.Addrs) > 0 && !slices.ContainsFunc(listen.Addrs, func(a string) bool { return net.ParseIP(a).To4() != nil }) {
		remote = "::1"
	}
	ln, err := tunnel.Bind(ft.Port)
	if err != nil {
		var busy *tunnel.BusyError
		if errors.As(err, &busy) {
			return valise.New("forward_port_busy", busy.Error()+"；本机端口必须与服务器相同，Valise 不会悄悄换端口",
				"关闭占用这个端口的程序后重试")
		}
		return err
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go ln.Serve(ctx, entryDialer(t, remote, ft.Port))
	status(EntryStatus{Step: EntryTunnel, OK: true, Detail: fmt.Sprintf("本机 %v → 服务器 %s", ln.Addrs, net.JoinHostPort(remote, strconv.Itoa(ft.Port)))})
	if len(listen.Addrs) > 0 {
		status(EntryStatus{Step: EntryRemote, OK: true, Detail: fmt.Sprintf("%v", listen.Addrs)})
	} else {
		status(EntryStatus{Step: EntryRemote, OK: false, Detail: fmt.Sprintf("服务器上没有程序监听端口 %d；页面可能已经关闭", ft.Port)})
	}
	switch {
	case ft.scheme == "http":
		// Path only: the query may hold a token.
		status(healthCheck(ctx, "http://127.0.0.1:"+strconv.Itoa(ft.Port)+ft.path, nil))
	default:
		status(EntryStatus{Step: EntryHealth, OK: true, Detail: "HTTPS 页面不做健康检查（证书通常是服务器自己签的）"})
	}
	if o.Ready != nil {
		o.Ready(ft.open, ft.Display)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if _, _, idle := ln.Stats(); o.Idle > 0 && idle >= o.Idle {
				return nil
			}
		}
	}
}

// LoopbackListeners lists the loopback TCP ports the server's user listens
// on, for choosing a page to open. It returns nil, nil when the porter does
// not advertise login.LoopbackListenersCapability.
func (s *Service) LoopbackListeners(ctx context.Context, host string) ([]int, error) {
	c, err := s.session(ctx, host)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if c.hello == nil || !slices.Contains(c.hello.Capabilities, login.LoopbackListenersCapability) {
		return nil, nil
	}
	var res login.LoopbackListenersResult
	if err := c.Call(ctx, login.MethodLoopbackListeners, struct{}{}, &res); err != nil {
		return nil, err
	}
	return res.Ports, nil
}
