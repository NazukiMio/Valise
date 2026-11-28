package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"valise/client/tunnel"
	"valise/core/login"
	"valise/core/profile"
	"valise/core/valise"
)

// Web entry steps, shown separately (docs/LOGIN_RELAY.md section 4).
const (
	EntryTunnel = "tunnel"
	EntryRemote = "remote_listening"
	EntryHealth = "health"
	// EntryAuth is the token step (docs/LOGIN_RELAY.md 4.1): OK when the
	// harness's own command supplied it; otherwise Detail is the hint.
	EntryAuth = "auth"
)

// GenericEntryHint is shown when a profile declares no hint.
const GenericEntryHint = "如果页面要求令牌或密码，请打开服务器终端（valise shell <服务器>），运行 harness 自己的命令获取，再粘贴到页面中"

// EntryStatus is the result of one step.
type EntryStatus struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// OpenOptions configure a web entry tunnel.
type OpenOptions struct {
	Entry string        // entry id; the profile's first entry when empty
	Idle  time.Duration // close after this long without connections; 0 never
	// InitialSuffix is an optional URL suffix (path + query + fragment, e.g.
	// captured from entry startup output) appended to the browser URL when
	// EntryAuth does not supply one. It may contain a token and must never be
	// logged or placed in display.
	InitialSuffix string
	// Status receives each step; Ready receives the URL for the browser
	// (it may hold the entry's token, so it is never shown) and a
	// token-free URL to display, once the tunnel is up.
	Status func(EntryStatus)
	Ready  func(open, display string)
}

// DefaultEntryIdle closes an unused web entry tunnel.
const DefaultEntryIdle = 30 * time.Minute

// entryDialer forwards one connection; tests replace it.
var entryDialer = tunnel.SSHDialer

// EntryPortBusyCode is returned when the entry's port is taken on this
// machine. The local port always equals the server's (docs/LOGIN_RELAY.md
// section 1); it never moves silently.
const EntryPortBusyCode = "entry_port_busy"

func entryBusy(err error, host, instance string, entry *profile.Entry) error {
	var busy *tunnel.BusyError
	if !errors.As(err, &busy) {
		return err
	}
	hint := "关闭占用这个端口的程序后重试"
	if entry.PortChange != nil {
		hint += fmt.Sprintf("；也可以让服务器和本机一起改用另一个端口：valise open %s %s --change-port", host, instance)
	}
	return valise.New(EntryPortBusyCode, busy.Error()+"；管理页的本机端口必须与服务器相同，Valise 不会悄悄换端口", hint)
}

// OpenEntry tunnels a web entry of an instance to this machine and keeps it
// open until ctx ends or it has been idle for o.Idle.
func (s *Service) OpenEntry(ctx context.Context, host, instance string, o OpenOptions) error {
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return err
	}
	var entry *profile.Entry
	for i := range target.Profile.Entries {
		if o.Entry == "" || target.Profile.Entries[i].ID == o.Entry {
			entry = &target.Profile.Entries[i]
			break
		}
	}
	if entry == nil {
		if o.Entry == "" {
			return valise.New("entry_none", "harness 资料 "+target.Profile.ID+" 没有声明入口", "这个 harness 没有本机 Web 页面")
		}
		return valise.New("entry_unknown", "harness 资料 "+target.Profile.ID+" 中没有入口 "+o.Entry, "不带 --entry 使用默认入口")
	}
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
	defer c.Close()
	var listen login.ListenResult
	if err = c.Call(ctx, login.MethodListen, login.ListenParams{Port: entry.Port}, &listen); err != nil {
		return err
	}
	remote := "127.0.0.1"
	if len(listen.Addrs) > 0 && !slices.ContainsFunc(listen.Addrs, func(a string) bool { return net.ParseIP(a).To4() != nil }) {
		remote = "::1"
	}

	ln, err := tunnel.Bind(entry.Port)
	if err != nil {
		return entryBusy(err, host, instance, entry)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go ln.Serve(ctx, entryDialer(t, remote, entry.Port))
	local := "http://127.0.0.1:" + strconv.Itoa(ln.Port) + entry.Path
	display := local
	detail := fmt.Sprintf("本机 %v → 服务器 %s", ln.Addrs, net.JoinHostPort(remote, strconv.Itoa(entry.Port)))
	status(EntryStatus{Step: EntryTunnel, OK: true, Detail: detail})
	if len(listen.Addrs) > 0 {
		status(EntryStatus{Step: EntryRemote, OK: true, Detail: fmt.Sprintf("%v", listen.Addrs)})
	} else {
		status(EntryStatus{Step: EntryRemote, OK: false, Detail: fmt.Sprintf("服务器上没有程序监听端口 %d；服务可能没有运行", entry.Port)})
	}
	status(healthCheck(ctx, local, expectStatus(&target.Profile, entry.ID)))
	hint := GenericEntryHint
	if entry.Auth != nil && entry.Auth.Hint != "" {
		hint = entry.Auth.Hint
	}
	authApplied := false
	hasInitialToken := strings.ContainsAny(o.InitialSuffix, "?#")
	if entry.Auth != nil && (len(entry.Auth.URLArgv) > 0 || len(entry.Auth.TokenArgv) > 0) && !hasInitialToken {
		suffix, err := entryAuth(ctx, c, login.EntryAuthParams{InstanceID: instance, Entry: *entry})
		if err == nil {
			local = "http://127.0.0.1:" + strconv.Itoa(ln.Port) + suffix
			authApplied = true
			d := "已自动附带访问令牌打开浏览器"
			status(EntryStatus{Step: EntryAuth, OK: true, Detail: d})
		} else {
			reason := err.Error()
			if ue, ok := valise.As(err); ok {
				reason = ue.Reason
			}
			status(EntryStatus{Step: EntryAuth, OK: false, Detail: reason + "。" + hint})
		}
	} else if o.InitialSuffix == "" {
		status(EntryStatus{Step: EntryAuth, OK: false, Detail: hint})
	}
	if !authApplied && o.InitialSuffix != "" {
		suffix := o.InitialSuffix
		if strings.HasPrefix(suffix, "?") || strings.HasPrefix(suffix, "#") {
			local = "http://127.0.0.1:" + strconv.Itoa(ln.Port) + entry.Path + suffix
		} else {
			if !strings.HasPrefix(suffix, "/") {
				suffix = "/" + suffix
			}
			local = "http://127.0.0.1:" + strconv.Itoa(ln.Port) + suffix
		}
		status(EntryStatus{Step: EntryAuth, OK: true, Detail: "已自动附带访问令牌打开浏览器"})
	}
	c.Close()
	if o.Ready != nil {
		o.Ready(local, display)
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

func expectStatus(p *profile.Profile, entry string) []int {
	if p.Health == nil {
		return nil
	}
	for _, ch := range p.Health.Checks {
		if ch.Kind == "http" && ch.Entry == entry {
			return ch.ExpectStatus
		}
	}
	return nil
}

// healthCheck fetches the entry through the tunnel. Without expected codes
// any HTTP response passes.
func healthCheck(ctx context.Context, url string, expect []int) EntryStatus {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	// No keep-alive: an idle pooled connection would hold the tunnel open.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return EntryStatus{Step: EntryHealth, OK: false, Detail: "无法通过隧道访问入口：" + err.Error()}
	}
	resp.Body.Close()
	if len(expect) > 0 && !slices.Contains(expect, resp.StatusCode) {
		return EntryStatus{Step: EntryHealth, OK: false, Detail: fmt.Sprintf("HTTP %d，预期 %v", resp.StatusCode, expect)}
	}
	return EntryStatus{Step: EntryHealth, OK: true, Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
}
