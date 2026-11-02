// Package action runs managed actions declared in an instance's stored
// profile (docs/INSTALL.md section 5).
package action

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"valise/core/action"
	"valise/core/profile"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

// Timeout bounds one non-terminal action.
const Timeout = 2 * time.Minute

// Run executes a (never a terminal action) and shapes its result by the
// action's output kind. A secret that cannot be picked out is an error that
// does not quote the output.
func Run(ctx context.Context, e *porterenroll.Env, a *profile.Action) (*action.RunResult, error) {
	if a.Terminal {
		return nil, valise.New("action_terminal", "动作「"+a.Name+"」需要交互终端", "在 Valise 打开的独立终端里运行它")
	}
	argv, ok := e.ExpandArgv(a.Argv, nil)
	if !ok {
		return nil, valise.New("action_env", "动作「"+a.Name+"」的命令依赖未设置的环境变量", "")
	}
	out, err := e.Run(ctx, Timeout, "", argv...)
	if err != nil {
		return nil, valise.New("action_run_failed", "动作「"+a.Name+"」没能运行："+err.Error(), "检查 harness 是否还装在服务器上")
	}
	res := &action.RunResult{Exit: out.Code}
	switch a.Output {
	case profile.OutputText:
		res.Output, res.Truncated = tail(string(out.Stdout)+string(out.Stderr), action.MaxOutputLines)
	case profile.OutputSecret:
		if out.Code != 0 {
			return res, nil
		}
		s, ok := pick(out.Stdout, a.Secret)
		if !ok {
			return nil, valise.New("action_secret_missing", "动作「"+a.Name+"」的输出里没有找到机密", "harness 的输出格式可能变了；等 profile 更新，或在 Valise 打开的终端里自己运行")
		}
		res.Secret = s
	}
	return res, nil
}

// tail keeps the last n lines.
func tail(s string, n int) (string, bool) {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n"), false
	}
	return strings.Join(lines[len(lines)-n:], "\n"), true
}

// pick takes the secret out of stdout: a dotted JSON field, then a URL
// parameter (query or fragment); without either, the first non-empty line.
func pick(stdout []byte, sp *profile.SecretPick) (string, bool) {
	if sp == nil || sp.JSONField == "" {
		for _, l := range strings.Split(string(stdout), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				return l, true
			}
		}
		return "", false
	}
	var doc any
	if json.Unmarshal(stdout, &doc) != nil {
		return "", false
	}
	for _, k := range strings.Split(sp.JSONField, ".") {
		m, ok := doc.(map[string]any)
		if !ok {
			return "", false
		}
		doc = m[k]
	}
	s, ok := doc.(string)
	if !ok || s == "" {
		return "", false
	}
	if sp.URLParam == "" {
		return s, true
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}
	if v := u.Query().Get(sp.URLParam); v != "" {
		return v, true
	}
	if q, err := url.ParseQuery(u.Fragment); err == nil && q.Get(sp.URLParam) != "" {
		return q.Get(sp.URLParam), true
	}
	return "", false
}

// Approval runs a profile's approval_check: a read-only command whose JSON
// output is matched by equality (profile.EvalApproval).
func Approval(ctx context.Context, e *porterenroll.Env, c *profile.ApprovalCheck) (*action.ApprovalResult, error) {
	if c == nil {
		return &action.ApprovalResult{}, nil
	}
	res := &action.ApprovalResult{Declared: true, Pending: c.Verify == "pending", Mode: profile.ApprovalUnknown}
	argv, ok := e.ExpandArgv(c.Argv, nil)
	if !ok {
		res.Detail = "命令依赖未设置的环境变量"
		return res, nil
	}
	out, err := e.Run(ctx, time.Minute, "", argv...)
	switch {
	case err != nil:
		res.Detail = "没能运行：" + err.Error()
	case out.Code != 0:
		res.Detail = "命令退出码 " + strconv.Itoa(out.Code)
	default:
		res.Mode, res.Detail = profile.EvalApproval(c, out.Stdout)
	}
	return res, nil
}
