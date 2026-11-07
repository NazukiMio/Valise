package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"valise/core/identity"
	"valise/core/install"
	"valise/core/protection"
	"valise/core/valise"
)

// System settings Valise itself depends on (docs/N2_IMPLEMENTATION_2026-11-05.md
// addendum E, docs/UI.md 9.5 health): linger, so planned backups run without
// a login, and the server clock's time synchronisation. Both fixes are fixed
// commands built into Valise (not harness knowledge, never in a profile).

// Wire contract of porter's system_status / system_fix_audit (mirrors
// porter/internal/sysfix until the types move to core).
const (
	MethodSystemStatus   = "system_status"
	MethodSystemFixAudit = "system_fix_audit"
	SystemFixCapability  = "system-fix/1"
)

// SystemStatus is the server's linger and time-sync state; each field is
// "yes", "no" or "unknown" (a failed read is never "no").
type SystemStatus struct {
	Now             time.Time `json:"now"`
	User            string    `json:"user,omitempty"`
	Linger          string    `json:"linger"`
	CanNTP          string    `json:"can_ntp"`
	NTP             string    `json:"ntp"`
	NTPSynchronized string    `json:"ntp_synchronized"`
	Timesyncd       string    `json:"timesyncd"`
}

// SystemFixAudit is one fix's audit entry: commands and exit codes only.
type SystemFixAudit struct {
	Fix      string              `json:"fix"`
	Instance string              `json:"instance,omitempty"`
	Mode     string              `json:"mode"`
	Steps    []install.StepAudit `json:"steps"`
	Result   string              `json:"result"`
}

// System fix ids (HealthItem.SystemFix and the health item ids).
const (
	FixLinger = "linger"
	FixClock  = "clock"
)

// systemRead is what the health page reads for both items.
type systemRead struct {
	status    *SystemStatus
	statusErr error // nil, errSystemUnsupported, or a read failure
	scheduled bool  // an accepted protection plan with its schedule on
}

var errSystemUnsupported = errors.New("system_status unsupported")

// readSystem reads system_status and whether the accepted plan is
// scheduled, on one daily session. Old porters lack one or both; that is
// reported as unsupported, never as a problem.
func (s *Service) readSystem(ctx context.Context, host, instance string) (*systemRead, error) {
	out := &systemRead{}
	err := s.withSession(ctx, host, func(c caller) error {
		var st SystemStatus
		switch err := c.Call(ctx, MethodSystemStatus, struct{}{}, &st); {
		case err == nil:
			out.status = &st
		case rpcMissing(err):
			out.statusErr = errSystemUnsupported
		default:
			out.statusErr = err
		}
		var b identity.Binding
		if err := c.Call(ctx, identity.MethodGet, identity.GetParams{InstanceID: instance}, &b); err != nil {
			return unsupportedOK(err)
		}
		if b.IdentityState != identity.Bound || b.Agent == nil {
			return nil
		}
		var v protection.View
		if err := c.Call(ctx, protection.MethodGet, protection.GetParams{AgentRef: b.Agent.Ref}, &v); err != nil {
			return unsupportedOK(err)
		}
		if v.Mode == "accepted" && v.ServerPlan != nil && v.ServerPlan.Schedule.Enabled != nil && *v.ServerPlan.Schedule.Enabled {
			out.scheduled = true
			if out.status != nil && out.status.Linger == "unknown" && v.Timer.Linger {
				out.status.Linger = "yes"
			}
		}
		return nil
	})
	return out, err
}

func unsupportedOK(err error) error {
	if ue, ok := valise.As(err); (ok && ue.Code == "unsupported_capability") || rpcMissing(err) {
		return nil
	}
	return err
}

// systemItems adds the linger and clock items. legacyTimer and
// legacyLinger come from the old soul timer; vaultNow is the server clock
// from vault_status (used when system_status is unavailable).
func (s *Service) systemItems(r *HealthReport, host, instance string, read func() (*systemRead, error), legacyTimer, legacyLinger bool, vaultNow time.Time) {
	var sr *systemRead
	var err error
	if read != nil {
		sr, err = read()
	}
	if sr == nil {
		sr = &systemRead{statusErr: errSystemUnsupported}
	}
	if err == nil {
		err = sr.statusErr
	}
	if errors.Is(err, errSystemUnsupported) {
		r.read("system", "linger 与时间同步", nil)
		r.Checks[len(r.Checks)-1].State = "unsupported"
		r.Checks[len(r.Checks)-1].Reason = "服务器 porter 版本较旧，读不到时间同步状态，也不能一键修复；更新 porter 后可用"
		err = nil
	} else {
		r.read("system", "linger 与时间同步", err)
	}
	st := sr.status
	if err != nil {
		st = nil
	}
	now := time.Now()
	// linger: only when something scheduled depends on it.
	linger := "unknown"
	switch {
	case st != nil && st.Linger != "unknown":
		linger = st.Linger
	case legacyTimer && !legacyLinger:
		linger = "no"
	}
	if (sr.scheduled || legacyTimer) && linger == "no" {
		user := "<用户>"
		if st != nil && st.User != "" {
			user = st.User
		}
		it := HealthItem{ID: FixLinger, Group: HealthAgent, Level: HealthWarn, Title: "linger",
			Text:   "没有开启：退出登录或服务器重启后，定时备份不会运行",
			Detail: "linger 让 systemd 在你登出后继续运行你的用户服务（包括定时备份）。自己在服务器上执行：sudo loginctl enable-linger " + user + "；撤销：sudo loginctl disable-linger " + user + "。"}
		s.bindSystemFix(&it, host, st)
		r.Items = append(r.Items, it)
	}
	// clock: the skew against this computer, and NTP state.
	serverNow := vaultNow
	if st != nil && !st.Now.IsZero() {
		serverNow = st.Now
	}
	var parts []string
	if !serverNow.IsZero() {
		if skew := absDuration(serverNow.Sub(now)); skew > clockSkewLimit {
			parts = append(parts, "和这台电脑差 "+agoPlain(skew))
		}
	}
	if st != nil && st.NTPSynchronized == "no" {
		if st.NTP == "no" {
			parts = append(parts, "没有开启时间同步（NTP）")
		} else {
			parts = append(parts, "时间同步（NTP）还没有对上时间")
		}
	}
	if len(parts) > 0 {
		it := HealthItem{ID: FixClock, Group: HealthAgent, Level: HealthWarn, Title: "服务器时钟", Text: strings.Join(parts, "；"),
			Detail: "时钟偏差会让空间站的短期证书失效，也会让备份时间与定时备份不准。Valise 不会用这台电脑的时间改写服务器时钟。自己在服务器上执行：" +
				strings.Join(clockManual(st), "；") + "。"}
		s.bindSystemFix(&it, host, st)
		r.Items = append(r.Items, it)
	}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// bindSystemFix offers the one-key fix when Valise can run it in some way:
// a management login (linger without sudo), or advanced mode the user can
// turn on for this server. Without system_status there is nothing to plan.
func (s *Service) bindSystemFix(it *HealthItem, host string, st *SystemStatus) {
	if st == nil || st.User == "" {
		return
	}
	if it.ID == FixClock && len(clockSteps(st, false)) == 0 {
		return
	}
	if (it.ID == FixLinger && s.adminLoginAvailable(host)) || s.advancedOn(host) || s.AdvancedReady(host) == nil {
		it.SystemFix, it.ActionName, it.Key = it.ID, systemFixNames[it.ID], "f"
	}
}

var systemFixNames = map[string]string{FixLinger: "开启 linger", FixClock: "开启时间同步"}

// lingerNote keeps the timer line consistent with the linger item, which
// carries the warning and the fix.
func lingerNote(r *HealthReport) {
	has := false
	for _, it := range r.Items {
		has = has || it.ID == FixLinger
	}
	for i := range r.Items {
		if it := &r.Items[i]; has && it.ID == "timer" && it.Level == HealthOK {
			it.Level = HealthInfo
			it.Text = strings.TrimSuffix(it.Text, "，登出后也会运行") + "；linger 没有开启，登出或重启后会停下（见 linger 一项）"
		}
	}
}
