package seal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	code, out, stderr, err := envRunner{e, p.Home}.Run(ctx, 30*time.Second, p.Rclone(), "listremotes", "--config", p.RcloneConfig())
	if err != nil || code != 0 {
		return nil, valise.New("rclone_failed", "无法运行服务器上的 rclone", "在服务器设置的工具里重新安装 rclone").WithDetail(lastLine(string(stderr)))
	}
	var names []string
	for _, l := range strings.Fields(string(out)) {
		names = append(names, strings.TrimSuffix(l, ":"))
	}
	return names, nil
}

// CloudPrepare prepares the server login of a cloud drive. Nothing runs
// yet: the client runs login-run under a terminal and relays the browser.
func CloudPrepare(ctx context.Context, e *porterenroll.Env, p Paths, q vaultrpc.CloudPrepareParams) (*login.PrepareResult, error) {
	if !targetNameRe.MatchString(q.Name) {
		return nil, valise.New("target_name_invalid", "备份位置名只能包含小写字母、数字和连字符", "例如 gdrive")
	}
	if probs := q.Cloud.Problems(); len(probs) > 0 {
		return nil, valise.New("cloud_invalid", "云盘条目无效："+strings.Join(probs, "；"), "更新 harness 资料包后重试")
	}
	if _, err := os.Stat(p.Rclone()); err != nil {
		return nil, valise.New("rclone_missing", "服务器上没有 Valise 安装的 rclone", "先在服务器设置的工具里安装 rclone，再登录云盘")
	}
	if t, _ := findTarget(p, q.Name); t != nil {
		return nil, valise.New("target_exists", "服务器上已经有名为 "+q.Name+" 的备份位置", "换一个名字，或先移除它")
	}
	if err := p.ensureRcloneConfig(); err != nil {
		return nil, err
	}
	remote := vaultrpc.CloudRemote(q.Name)
	names, err := rcloneRemotes(ctx, e, p)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		if n == remote {
			return nil, valise.New("cloud_exists", "服务器上已经有 "+q.Name+" 的云盘登录", "先移除它：valise cloud remove <服务器> "+q.Name)
		}
	}
	argv := append([]string{p.Rclone()}, q.Cloud.CreateArgs(remote, !q.Fallback)...)
	argv = append(argv, "--config", p.RcloneConfig())
	return porterlogin.PrepareArgv(e, "cloud:"+q.Cloud.ID, argv)
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
	if _, err := os.Stat(p.Rclone()); err != nil {
		return valise.New("rclone_missing", "服务器上没有 Valise 安装的 rclone", "安装 rclone 后再移除登录")
	}
	code, _, stderr, err := envRunner{e, p.Home}.Run(ctx, 30*time.Second, p.Rclone(), "config", "delete", vaultrpc.CloudRemote(name), "--config", p.RcloneConfig())
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
