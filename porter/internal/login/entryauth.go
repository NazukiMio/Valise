package login

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	if len(a.URLArgv) > 0 {
		candidates := urlRe.FindAllString(string(out.Stdout), -1)
		if a.URLField != "" {
			candidates = nil
			var doc any
			if json.Unmarshal(out.Stdout, &doc) == nil {
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
	for _, line := range strings.Split(string(out.Stdout), "\n") {
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
