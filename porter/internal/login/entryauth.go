package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"valise/core/profile"
	"valise/porter/internal/activeexec"
	"valise/porter/internal/runtimeguard"

	"valise/core/login"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

// entryAuthTimeout bounds the harness's token command.
const entryAuthTimeout = 15 * time.Second

var urlRe = regexp.MustCompile(`https?://[^\s"'<>]+`)

// EntryAuth runs the entry's declared url_argv or token_argv with the login
// environment and returns the URL suffix holding the token. The output is
// never logged or returned in errors; failures report the exit code only.
func EntryAuth(ctx context.Context, e *porterenroll.Env, home string, p login.EntryAuthParams) (*login.EntryAuthResult, error) {
	return EntryAuthWithLock(ctx, e, home, p, nil)
}

// EntryAuthWithLock is called by the RPC handler holding the existing server
// mutation lock. Its FD outlives the private namespace if porter disconnects.
func EntryAuthWithLock(ctx context.Context, e *porterenroll.Env, home string, p login.EntryAuthParams, lock *os.File) (*login.EntryAuthResult, error) {
	ctx, cancel := context.WithTimeout(ctx, entryAuthTimeout)
	defer cancel()
	cmd, accepted, err := (activeexec.Controller{Home: home}).Entry(ctx, p.InstanceID, p.Entry, lock)
	if err == nil {
		var output tokenOutput
		cmd.Stdout, cmd.Stderr = &output, io.Discard
		err := cmd.Run()
		if err != nil {
			reason := "当前环境的取令牌命令未完成"
			if cmd.ProcessState != nil && ctx.Err() == nil {
				reason = fmt.Sprintf("当前环境的取令牌命令失败（退出码 %d）", cmd.ProcessState.ExitCode())
			}
			return nil, valise.New("entry_auth_failed", reason, "重新检查这个 Agent 后再打开管理页；也可以使用它的原生界面")
		}
		return entrySuffix(p, accepted, output.Bytes())
	}
	if !errors.Is(err, activeexec.ErrUnmanaged) {
		return nil, err
	}
	if err := runtimeguard.CheckInstance(ctx, home, p.InstanceID); err != nil {
		return nil, err
	}
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
	out, err := e.Run(ctx, entryAuthTimeout, home, exp...)
	if err != nil {
		return nil, valise.New("entry_auth_failed", "无法运行取得令牌的命令", "确认 harness 已安装；也可以在服务器终端中手动获取")
	}
	if out.Code != 0 {
		return nil, valise.New("entry_auth_failed", fmt.Sprintf("取得令牌的命令失败（退出码 %d）", out.Code), "在服务器终端中手动运行这条命令查看原因")
	}
	return entrySuffix(p, a, out.Stdout)
}

// Child output can carry a secret. Keep a small bounded in-memory buffer,
// never an output detail, file, audit entry or tracked terminal record.
type tokenOutput struct{ data bytes.Buffer }

func (b *tokenOutput) Bytes() []byte { return b.data.Bytes() }

func (b *tokenOutput) Write(p []byte) (int, error) {
	if len(p) > 256<<10-b.data.Len() {
		return 0, io.ErrShortBuffer
	}
	return b.data.Write(p)
}

func entrySuffix(p login.EntryAuthParams, a *profile.EntryAuth, output []byte) (*login.EntryAuthResult, error) {
	if len(a.URLArgv) > 0 {
		candidates := urlRe.FindAllString(string(output), -1)
		if a.URLField != "" {
			candidates = nil
			var doc any
			if json.Unmarshal(output, &doc) == nil {
				for _, k := range strings.Split(a.URLField, ".") {
					m, _ := doc.(map[string]any)
					doc = m[k]
				}
				if s, ok := doc.(string); ok {
					candidates = []string{s}
				}
			}
		}
		for _, m := range candidates {
			u, err := url.Parse(m)
			if err != nil || u.Port() != strconv.Itoa(p.Entry.Port) {
				continue
			}
			suffix := u.EscapedPath()
			if suffix == "" {
				suffix = "/"
			}
			if u.RawQuery != "" {
				suffix += "?" + u.RawQuery
			}
			if u.Fragment != "" {
				suffix += "#" + u.EscapedFragment()
			}
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
