package seal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/kittools"
	"valise/core/login"
	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	porterlogin "valise/porter/internal/login"
)

// Cloud drive logins on the server (docs/ARCHITECTURE.md 7.3,
// docs/LOGIN_RELAY.md section 7): porter only builds the fixed rclone
// command from the signed cloud entry and prepares a login-relay session.
// The authorization stays in rclone's own config; porter never reads it.

func (p Paths) ensureRcloneConfig() error {
	if err := os.MkdirAll(filepath.Dir(p.RcloneConfig()), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p.RcloneConfig(), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// rcloneRemotes lists the remote names of Valise's rclone config (names
// only, through rclone itself).
func rcloneRemotes(ctx context.Context, e *porterenroll.Env, p Paths) ([]string, error) {
	rc, rerr := p.ResolveRclone(e)
	if rerr != nil {
		return nil, rerr
	}
	code, out, stderr, err := envRunner{e, p.Home}.Run(ctx, 30*time.Second, rc, "listremotes", "--config", p.RcloneConfig())
	if err != nil || code != 0 {
		return nil, valise.New("rclone_failed", "无法运行服务器上的 rclone", "在服务器设置的工具里重新安装 rclone").WithDetail(lastLine(string(stderr)))
	}
	var names []string
	for _, l := range strings.Fields(string(out)) {
		names = append(names, strings.TrimSuffix(l, ":"))
	}
	return names, nil
}

func isSameCloudTarget(t *vaultrpc.Target, name string) bool {
	return t != nil && t.Kind == stationsync.MethodRclone &&
		strings.HasPrefix(t.Dest, vaultrpc.CloudRemote(name)+":")
}

// cloudFresh checks a new cloud login or explicit cloud re-login and clears a
// login left over (or expired) before creating a fresh remote.
func cloudFresh(ctx context.Context, e *porterenroll.Env, p Paths, name string, cloud kittools.Cloud) error {
	if !targetNameRe.MatchString(name) {
		return valise.New("target_name_invalid", "备份位置名只能包含小写字母、数字和连字符", "例如 gdrive")
	}
	if probs := cloud.Problems(); len(probs) > 0 {
		return valise.New("cloud_invalid", "云盘条目无效："+strings.Join(probs, "；"), "更新 harness 资料包后重试")
	}
	rc, rerr := p.ResolveRclone(e)
	if rerr != nil {
		return rerr
	}
	if t, _ := findTarget(p, name); t != nil && !isSameCloudTarget(t, name) {
		return valise.New("target_exists", "服务器上已经有名为 "+name+" 的备份位置", "换一个名字，或先移除它")
	}
	if err := p.ensureRcloneConfig(); err != nil {
		return err
	}
	remote := vaultrpc.CloudRemote(name)
	names, err := rcloneRemotes(ctx, e, p)
	if err != nil {
		return err
	}
	// Either no non-cloud target uses this name or we are refreshing an existing
	// rclone cloud target's credentials: drop the old remote so rclone can create
	// a fresh login while keeping targets.json and sync state intact.
	for _, n := range names {
		if n != remote {
			continue
		}
		code, _, stderr, err := envRunner{e, p.Home}.Run(ctx, 30*time.Second, rc, "config", "delete", remote, "--config", p.RcloneConfig())
		if err != nil || code != 0 {
			return valise.New("rclone_failed", "没能清除上次没有完成的 "+name+" 云盘登录", "稍后重试，或运行 valise cloud remove <服务器> "+name).WithDetail(lastLine(string(stderr)))
		}
		p.AppendAudit(AuditEntry{Time: time.Now().UTC(), Op: "cloud_forget", Initiator: vaultrpc.InitiatorClient, Target: name, Result: "ok", Reason: "unfinished or relogin"})
	}
	return nil
}

// cloudStarted checks that a form login being continued exists and is
// either unsaved or a same-name cloud target being re-authenticated.
func cloudStarted(ctx context.Context, e *porterenroll.Env, p Paths, name string) error {
	if !targetNameRe.MatchString(name) {
		return valise.New("target_name_invalid", "备份位置名无效："+name, "")
	}
	if t, _ := findTarget(p, name); t != nil && !isSameCloudTarget(t, name) {
		return valise.New("target_exists", "服务器上已经有名为 "+name+" 的备份位置", "换一个名字，或先移除它")
	}
	names, err := rcloneRemotes(ctx, e, p)
	if err != nil {
		return err
	}
	for _, n := range names {
		if n == vaultrpc.CloudRemote(name) {
			return nil
		}
	}
	return valise.New("cloud_login_gone", "这次云盘登录已经不在了", "重新开始登录")
}

// CloudPrepare prepares the server login of a cloud drive. Nothing runs
// yet: the client runs login-run under a terminal and relays the browser.
// With State it continues a form login at its browser step.
func CloudPrepare(ctx context.Context, e *porterenroll.Env, p Paths, q vaultrpc.CloudPrepareParams) (*login.PrepareResult, error) {
	remote := vaultrpc.CloudRemote(q.Name)
	var args []string
	if q.State != "" {
		if err := kittools.CheckAnswer(q.State, "true"); err != nil {
			return nil, valise.New("request_invalid", "云盘登录的步骤无效", "重新开始登录")
		}
		if err := cloudStarted(ctx, e, p, q.Name); err != nil {
			return nil, err
		}
		args = kittools.ContinueArgs(remote, q.State, "true")
	} else {
		if err := cloudFresh(ctx, e, p, q.Name, q.Cloud); err != nil {
			return nil, err
		}
		args = q.Cloud.CreateArgs(remote, !q.Fallback)
	}
	rc, err := p.ResolveRclone(e)
	if err != nil {
		return nil, err
	}
	argv := append(append([]string{rc}, args...), "--config", p.RcloneConfig())
	return porterlogin.PrepareArgv(e, "cloud:"+q.Cloud.ID, argv)
}

// CloudStep runs one step of a form login and returns only rclone's
// question (docs/ARCHITECTURE.md 7.3): nothing rclone holds is read or
// returned, and answers are not logged.
func CloudStep(ctx context.Context, e *porterenroll.Env, p Paths, q vaultrpc.CloudStepParams) (*kittools.Question, error) {
	remote := vaultrpc.CloudRemote(q.Name)
	var args []string
	if q.State == "" {
		if err := kittools.CheckApp(q.App); err != nil {
			return nil, valise.New("request_invalid", "自己的应用填写有误", "client_id 与 client_secret 各填一行")
		}
		if err := cloudFresh(ctx, e, p, q.Name, q.Cloud); err != nil {
			return nil, err
		}
		args = q.Cloud.FormArgs(remote, q.App)
	} else {
		if err := kittools.CheckAnswer(q.State, q.Result); err != nil {
			return nil, valise.New("request_invalid", "云盘登录的回答无效", "重新开始登录")
		}
		if err := cloudStarted(ctx, e, p, q.Name); err != nil {
			return nil, err
		}
		args = kittools.ContinueArgs(remote, q.State, q.Result)
	}
	args = append(args, "--config", p.RcloneConfig())
	rc, err := p.ResolveRclone(e)
	if err != nil {
		return nil, err
	}
	code, out, stderr, err := envRunner{e, p.Home}.Run(ctx, time.Minute, append([]string{rc}, args...)...)
	if err != nil || code != 0 {
		return nil, valise.New("rclone_failed", "服务器上的 rclone 没能继续登录", "重新开始登录").WithDetail(lastLine(string(stderr)))
	}
	qn, err := kittools.ParseQuestion(out)
	if err != nil {
		return nil, valise.New("rclone_failed", "服务器上的 rclone 没有给出下一步", "重新开始登录")
	}
	return qn, nil
}

// CloudForget deletes a cloud drive's login from Valise's rclone config;
// a saved location using it must be removed first.
func CloudForget(ctx context.Context, e *porterenroll.Env, p Paths, name string, removeTarget bool) error {
	if !targetNameRe.MatchString(name) {
		return valise.New("target_name_invalid", "备份位置名无效："+name, "")
	}
	if t, _ := findTarget(p, name); t != nil {
		if !removeTarget || t.Kind != stationsync.MethodRclone || !strings.HasPrefix(t.Dest, vaultrpc.CloudRemote(name)+":") {
			return valise.New("target_exists", "备份位置 "+name+" 还在使用这个登录", "先移除这个备份位置")
		}
		if err := removeTargetEntry(p, name); err != nil {
			return err
		}
	}
	rc, err := p.ResolveRclone(e)
	if err != nil {
		return err
	}
	code, _, stderr, err := envRunner{e, p.Home}.Run(ctx, 30*time.Second, rc, "config", "delete", vaultrpc.CloudRemote(name), "--config", p.RcloneConfig())
	if err != nil || code != 0 {
		return valise.New("rclone_failed", "没能删除服务器上的云盘登录", "稍后重试").WithDetail(lastLine(string(stderr)))
	}
	p.AppendAudit(AuditEntry{Time: time.Now().UTC(), Op: "cloud_forget", Initiator: vaultrpc.InitiatorClient, Target: name, Result: "ok"})
	return nil
}

// removeTargetEntry drops a location from targets.json and its sync state.
func removeTargetEntry(p Paths, name string) error {
	ts, err := readTargets(p)
	if err != nil {
		return err
	}
	var out []vaultrpc.Target
	for _, t := range ts {
		if t.Name != name {
			out = append(out, t)
		}
	}
	b, _ := json.MarshalIndent(targetsFile{Targets: out}, "", "  ")
	if err := writeAtomic(p.Targets(), append(b, '\n'), 0o600); err != nil {
		return err
	}
	os.Remove(p.SyncState(name))
	return nil
}
