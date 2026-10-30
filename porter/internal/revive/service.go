package revive

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

var unitRe = regexp.MustCompile(`^[A-Za-z0-9@._:-]+\.service$`)

const systemctlTimeout = 90 * time.Second

func systemctl(ctx context.Context, e *porterenroll.Env, args ...string) (string, int, error) {
	out, err := e.Run(ctx, systemctlTimeout, "", append([]string{"systemctl", "--user"}, args...)...)
	if err != nil {
		return "", -1, err
	}
	return strings.TrimSpace(string(out.Stdout)), out.Code, nil
}

// unitState returns "active", "inactive", "failed", … or "" when unknown.
func unitState(ctx context.Context, e *porterenroll.Env, unit string) string {
	s, _, err := systemctl(ctx, e, "is-active", unit)
	if err != nil {
		return ""
	}
	return s
}

func unitExists(ctx context.Context, e *porterenroll.Env, unit string) bool {
	s, _, err := systemctl(ctx, e, "show", unit, "-p", "LoadState", "--value")
	return err == nil && s == "loaded"
}

// StopUnit stops a unit and waits until it is no longer active.
func StopUnit(ctx context.Context, e *porterenroll.Env, unit string) error {
	if !unitRe.MatchString(unit) {
		return valise.New("unit_invalid", "服务单元名无效："+unit, "检查 profile")
	}
	if _, code, err := systemctl(ctx, e, "stop", unit); err != nil || code != 0 {
		return valise.New("service_stop_failed", "无法停止服务 "+unit, "在服务器上运行 systemctl --user status "+unit+" 查看原因")
	}
	for i := 0; i < 30; i++ {
		if s := unitState(ctx, e, unit); s != "active" && s != "deactivating" {
			return nil
		}
		time.Sleep(time.Second)
	}
	return valise.New("service_stop_timeout", "服务 "+unit+" 在 30 秒内没有停止", "查看服务日志后重试")
}

// StartUnit starts a unit.
func StartUnit(ctx context.Context, e *porterenroll.Env, unit string) error {
	if !unitRe.MatchString(unit) {
		return valise.New("unit_invalid", "服务单元名无效："+unit, "检查 profile")
	}
	if _, code, err := systemctl(ctx, e, "start", unit); err != nil || code != 0 {
		return valise.New("service_start_failed", "无法启动服务 "+unit, "在服务器上运行 systemctl --user status "+unit+" 查看原因")
	}
	return nil
}

// recentLogs returns the unit's last journal lines, shown to the user only.
func recentLogs(ctx context.Context, e *porterenroll.Env, unit string) string {
	out, err := e.Run(ctx, 20*time.Second, "", "journalctl", "--user", "-u", unit, "-n", "40", "--no-pager")
	if err != nil {
		return fmt.Sprintf("（无法读取服务日志：%v）", err)
	}
	return string(out.Stdout)
}
