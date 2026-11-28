package login

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"valise/core/profile"

	"valise/core/login"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

// entryAuthTimeout bounds the harness's token command.
const entryAuthTimeout = 15 * time.Second

var (
	entryAuthLingeringGrace = time.Second
	entryAuthPoll           = 50 * time.Millisecond
	urlRe                   = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

// EntryAuth runs the entry's declared url_argv or token_argv with the login
// environment and returns the URL suffix holding the token. The output is
// never logged or returned in errors; failures report the exit code only.
func EntryAuth(ctx context.Context, e *porterenroll.Env, home string, p login.EntryAuthParams) (*login.EntryAuthResult, error) {
	return EntryAuthWithLock(ctx, e, home, p, nil)
}

// EntryAuthWithLock is called by the RPC handler holding the existing server
// mutation lock.
func EntryAuthWithLock(ctx context.Context, e *porterenroll.Env, home string, p login.EntryAuthParams, lock *os.File) (*login.EntryAuthResult, error) {
	_ = lock
	rec, err := porterenroll.ReadRecord(home, p.InstanceID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+p.InstanceID, "先把这个 agent 接入 Valise")
	}
	if prof, err := porterenroll.ReadProfile(home, p.InstanceID); err == nil {
		e = e.ForInstance(prof, rec)
	}
	a := p.Entry.Auth
	if a == nil || (len(a.URLArgv) == 0 && len(a.TokenArgv) == 0) {
		return nil, valise.New("entry_auth_none", "profile 没有声明如何取得管理页令牌", "按页面提示，在服务器终端中运行 harness 自己的命令获取")
	}
	argv := a.URLArgv
	if len(argv) == 0 {
		argv = a.TokenArgv
	}
	vars := map[string]string{"${HOME}": home}
	if st, ok := rec.Roots["state"]; ok {
		vars["${STATE_DIR}"] = st
	}
	exp := make([]string, len(argv))
	for i, s := range argv {
		for k, v := range vars {
			s = strings.ReplaceAll(s, k, v)
		}
		exp[i] = s
	}
	if len(a.URLArgv) > 0 && a.URLField == "" {
		raw, err := runEntryAuthURL(ctx, e, home, p.Entry.Port, exp)
		if err != nil {
			return nil, err
		}
		return entrySuffix(p, a, raw)
	}
	out, err := e.Run(ctx, entryAuthTimeout, home, exp...)
	if err != nil {
		return nil, valise.New("entry_auth_failed", "无法运行取得令牌的命令", "确认 harness 已安装；也可以在服务器终端中手动获取")
	}
	if out.Code != 0 {
		return nil, valise.New("entry_auth_failed", fmt.Sprintf("取得令牌的命令失败（退出码 %d）", out.Code), "在服务器终端中手动运行这条命令查看原因")
	}
	raw := out.Stdout
	if len(a.URLArgv) > 0 && len(bytes.TrimSpace(raw)) == 0 && len(out.Stderr) > 0 {
		raw = out.Stderr
	}
	return entrySuffix(p, a, raw)
}

func runEntryAuthURL(ctx context.Context, e *porterenroll.Env, home string, port int, exp []string) ([]byte, error) {
	if len(exp) == 0 || exp[0] == "" {
		return nil, valise.New("entry_auth_failed", "无法运行取得令牌的命令", "确认 harness 已安装；也可以在服务器终端中手动获取")
	}
	bin := exp[0]
	if !filepath.IsAbs(bin) {
		resolved, err := e.LookPath(bin)
		if err != nil {
			return nil, valise.New("entry_auth_failed", "无法运行取得令牌的命令", "确认 harness 已安装；也可以在服务器终端中手动获取")
		}
		bin = resolved
	}
	runCtx, cancel := context.WithTimeout(ctx, entryAuthTimeout)
	defer cancel()

	cmd := exec.Command(bin, exp[1:]...)
	cmd.Dir = home
	cmdEnv := append([]string(nil), e.Vars...)
	cmdEnv = append(cmdEnv, "PYTHONUNBUFFERED=1")
	cmd.Env = cmdEnv
	cmd.Stdin = nil
	buf := &tokenOutput{dropOverflow: true}
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, valise.New("entry_auth_failed", "无法运行取得令牌的命令", "确认 harness 已安装；也可以在服务器终端中手动获取")
	}
	pid := cmd.Process.Pid
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	killGroup := func() {
		if pid <= 0 {
			return
		}
		select {
		case <-waitCh:
			return
		default:
		}
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case <-waitCh:
			return
		case <-time.After(300 * time.Millisecond):
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-waitCh
	}

	tick := time.NewTicker(entryAuthPoll)
	defer tick.Stop()
	var graceTimer *time.Timer
	var graceCh <-chan time.Time
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()

	for {
		select {
		case waitErr := <-waitCh:
			if waitErr != nil || (cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 0) {
				code := -1
				if cmd.ProcessState != nil {
					code = cmd.ProcessState.ExitCode()
				}
				return nil, valise.New("entry_auth_failed", fmt.Sprintf("取得令牌的命令失败（退出码 %d）", code), "在服务器终端中手动运行这条命令查看原因")
			}
			return buf.Bytes(), nil
		case <-graceCh:
			killGroup()
			return buf.Bytes(), nil
		case <-runCtx.Done():
			killGroup()
			return nil, valise.New("entry_auth_failed", "无法运行取得令牌的命令", "确认 harness 已安装；也可以在服务器终端中手动获取")
		case <-tick.C:
			if graceTimer != nil {
				continue
			}
			snap := buf.Bytes()
			if lastNL := bytes.LastIndexByte(snap, '\n'); lastNL >= 0 {
				if suf := extractPortURLSuffix(snap[:lastNL+1], port); strings.ContainsAny(suf, "?#") {
					graceTimer = time.NewTimer(entryAuthLingeringGrace)
					graceCh = graceTimer.C
				}
			}
		}
	}
}

// Child output can carry a secret. Keep a small bounded in-memory buffer,
// never an output detail, file, audit entry or tracked terminal record.
type tokenOutput struct {
	mu           sync.Mutex
	dropOverflow bool
	data         bytes.Buffer
}

func (b *tokenOutput) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data.Bytes()...)
}

func (b *tokenOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rem := (256 << 10) - b.data.Len()
	if rem <= 0 {
		if b.dropOverflow {
			return len(p), nil
		}
		return 0, io.ErrShortBuffer
	}
	if len(p) > rem {
		if b.dropOverflow {
			_, _ = b.data.Write(p[:rem])
			return len(p), nil
		}
		return 0, io.ErrShortBuffer
	}
	return b.data.Write(p)
}

func entrySuffix(p login.EntryAuthParams, a *profile.EntryAuth, output []byte) (*login.EntryAuthResult, error) {
	if len(a.URLArgv) > 0 {
		if a.URLField != "" {
			var doc any
			if json.Unmarshal(output, &doc) == nil {
				for _, k := range strings.Split(a.URLField, ".") {
					m, _ := doc.(map[string]any)
					doc = m[k]
				}
				if s, ok := doc.(string); ok {
					if u, err := url.Parse(s); err == nil && u.Port() == strconv.Itoa(p.Entry.Port) {
						return &login.EntryAuthResult{Suffix: portURLSuffix(u)}, nil
					}
				}
			}
			return nil, valise.New("entry_auth_failed", "命令的输出中没有指向这个管理页端口的地址", "在服务器终端中手动运行这条命令查看")
		}
		if suffix := extractPortURLSuffix(output, p.Entry.Port); suffix != "" {
			return &login.EntryAuthResult{Suffix: suffix}, nil
		}
		return nil, valise.New("entry_auth_failed", "命令的输出中没有指向这个管理页端口的地址", "在服务器终端中手动运行这条命令查看")
	}
	token := ""
	for _, line := range strings.Split(string(output), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			token = t
			break
		}
	}
	if token == "" {
		return nil, valise.New("entry_auth_failed", "取得令牌的命令没有输出令牌", "在服务器终端中手动运行这条命令查看")
	}
	return &login.EntryAuthResult{Suffix: p.Entry.Path + strings.ReplaceAll(a.URLSuffix, "${TOKEN}", url.QueryEscape(token))}, nil
}
