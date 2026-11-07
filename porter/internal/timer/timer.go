// Package timer manages the optional timed seal of an instance: a
// valise-seal@<instance>.timer and .service in the user's systemd instance
// (docs/ARCHITECTURE.md section 7; AGENT.md systemd rules: no capability
// settings in --user units, only NoNewPrivileges=yes).
package timer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	protectioncore "valise/core/protection"
	"valise/core/valise"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/protection"
	"valise/porter/internal/seal"
)

const runTimeout = 30 * time.Second

func unitDir(home string) string { return filepath.Join(home, ".config", "systemd", "user") }

// UnitNames are the two unit files of an instance.
func UnitNames(instance string) (service, timer string) {
	return "valise-seal@" + instance + ".service", "valise-seal@" + instance + ".timer"
}

var everyRe = regexp.MustCompile(`(?m)^OnUnitActiveSec=(\d+)s$`)

// Status reads the instance's units.
func Status(ctx context.Context, e *porterenroll.Env, home, instance string) (*vaultrpc.TimerStatus, error) {
	if err := enroll.ValidInstanceID(instance); err != nil {
		return nil, valise.New("instance_invalid", err.Error(), "检查实例名")
	}
	st := &vaultrpc.TimerStatus{Linger: Linger(ctx, e, home)}
	svc, tmr := UnitNames(instance)
	b, err := os.ReadFile(filepath.Join(unitDir(home), tmr))
	if err != nil {
		return st, nil
	}
	st.Enabled = true
	st.Units = []string{filepath.Join(unitDir(home), svc), filepath.Join(unitDir(home), tmr)}
	if m := everyRe.FindSubmatch(b); m != nil {
		var secs int64
		fmt.Sscan(string(m[1]), &secs)
		st.Every = formatEvery(time.Duration(secs) * time.Second)
	}
	return st, nil
}

func formatEvery(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return d.String()
}

// Linger reports whether the user's systemd instance runs without a login.
func Linger(ctx context.Context, e *porterenroll.Env, home string) bool {
	user := e.Get("USER")
	if user == "" {
		return false
	}
	out, err := e.Run(ctx, runTimeout, home, "loginctl", "show-user", user, "-p", "Linger", "--value")
	return err == nil && out.Code == 0 && strings.TrimSpace(string(out.Stdout)) == "yes"
}

// Set turns the timer on (writing both units and enabling the timer) or off
// (disabling it and deleting both units).
func Set(ctx context.Context, e *porterenroll.Env, home string, p vaultrpc.TimerSetParams) (result *vaultrpc.TimerStatus, err error) {
	err = (protection.Store{Home: home}).WithLegacyWrite(p.InstanceID, func() error { var callErr error; result, callErr = setLegacy(ctx, e, home, p); return callErr })
	return result, err
}

func setLegacy(ctx context.Context, e *porterenroll.Env, home string, p vaultrpc.TimerSetParams) (*vaultrpc.TimerStatus, error) {
	if err := enroll.ValidInstanceID(p.InstanceID); err != nil {
		return nil, valise.New("instance_invalid", err.Error(), "检查实例名")
	}
	if err := (protection.Store{Home: home}).CheckLegacyWrite(p.InstanceID); err != nil {
		return nil, err
	}
	if rec, err := porterenroll.ReadRecord(home, p.InstanceID); err != nil || rec == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+p.InstanceID, "先把这个 agent 接入 Valise")
	}
	svc, tmr := UnitNames(p.InstanceID)
	dir := unitDir(home)
	systemctl := func(args ...string) error {
		out, err := e.Run(ctx, runTimeout, home, append([]string{"systemctl", "--user"}, args...)...)
		if err != nil {
			return valise.New("timer_failed", "无法运行 systemctl --user", "确认服务器使用 systemd 并且用户实例可用").WithDetail(err.Error())
		}
		if out.Code != 0 {
			return valise.New("timer_failed", "systemctl --user "+args[0]+" 失败", "查看细节后重试").WithDetail(strings.TrimSpace(string(out.Stderr)))
		}
		return nil
	}
	if !p.Enabled {
		if _, err := os.Stat(filepath.Join(dir, tmr)); err == nil {
			if err := systemctl("disable", "--now", tmr); err != nil {
				return nil, err
			}
		}
		for _, f := range []string{tmr, svc} {
			if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
		if err := systemctl("daemon-reload"); err != nil {
			return nil, err
		}
		return Status(ctx, e, home, p.InstanceID)
	}
	every, err := seal.ParseEvery(p.Every)
	if err != nil || every < seal.MinAutoEvery {
		return nil, valise.New("timer_every_invalid", "定时间隔无效："+p.Every, "至少 1 小时，例如 6h、1d")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return nil, err
	}
	if strings.ContainsAny(exe, " \t\n\"'\\%$") {
		return nil, valise.New("timer_path", "porter 的路径含有特殊字符，无法写进 systemd 单元", "把 porter 部署到默认位置 ~/.local/bin/valise-porter").WithDetail(exe)
	}
	service := fmt.Sprintf(`# Written by Valise; turned off and deleted from the instance settings.
[Unit]
Description=Valise timed escape pod for %[1]s

[Service]
Type=oneshot
ExecStart=%[2]s seal-timer --instance %[1]s
NoNewPrivileges=yes
`, p.InstanceID, exe)
	secs := int64(every / time.Second)
	timerUnit := fmt.Sprintf(`# Written by Valise; turned off and deleted from the instance settings.
[Unit]
Description=Valise timed escape pod for %[1]s

[Timer]
OnActiveSec=%[2]ds
OnUnitActiveSec=%[2]ds

[Install]
WantedBy=timers.target
`, p.InstanceID, secs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for name, body := range map[string]string{svc: service, tmr: timerUnit} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return nil, err
		}
	}
	if err := systemctl("daemon-reload"); err != nil {
		return nil, err
	}
	if err := systemctl("enable", "--now", tmr); err != nil {
		return nil, err
	}
	return Status(ctx, e, home, p.InstanceID)
}

// SealAccepted is the new fixed timer entrypoint. The accepted plan is read at
// execution time and kept frozen until the backup has reached its safe boundary.
func SealAccepted(ctx context.Context, e *porterenroll.Env, home, instance string, validateAgent func(context.Context, identity.AgentRef) error, runners ...func(context.Context, *protectioncore.ServerPlan) error) error {
	store := protection.Store{Home: home, Env: e, ValidateAgent: validateAgent}
	plan, err := store.ReadAccepted(ctx, instance)
	if err != nil {
		return err
	}
	return store.WithAccepted(ctx, plan.AgentRef, func(p *protectioncore.ServerPlan) error {
		if !*p.Schedule.Enabled {
			return valise.New("schedule_disabled", "保护计划未启用定时备份", "读取保护计划并确认定时设置")
		}
		if len(runners) != 1 || runners[0] == nil {
			return valise.New("unsupported_capability", "按计划备份执行器不可用", "更新 porter 后重新核实保护计划")
		}
		return runners[0](ctx, p)
	})
}

// Seal is what the legacy timer runs: a soul seal recorded as the timer's.
func Seal(ctx context.Context, e *porterenroll.Env, home, instance string) error {
	return (protection.Store{Home: home}).WithLegacyWrite(instance, func() error {
		_, err := seal.Seal(ctx, e, seal.Paths{Home: home}, vaultrpc.SealParams{InstanceID: instance, Tier: "soul", Reason: "定时建立逃生舱"}, vaultrpc.InitiatorTimer)
		return err
	})
}
