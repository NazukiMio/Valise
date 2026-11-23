package service

import (
	"bytes"
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"valise/client/porter"
	"valise/core/enroll"
	"valise/core/install"
	"valise/core/revive"
)

// KitInstallOptions are the optional one-click steps after installing skills.
type KitInstallOptions struct {
	WireMCP        bool `json:"wire_mcp"`
	RestartService bool `json:"restart_service"`
}

// KitUninstallOptions are the optional one-click steps around removing skills.
type KitUninstallOptions struct {
	Force          bool `json:"force"`
	UnwireMCP      bool `json:"unwire_mcp"`
	RestartService bool `json:"restart_service"`
}

// KitStepOutcome is one harness command Valise ran for the person.
type KitStepOutcome struct {
	Attempted bool     `json:"attempted"`
	OK        bool     `json:"ok"`
	Command   []string `json:"command,omitempty"`
	ExitCode  int      `json:"exit_code"`
	Output    string   `json:"output,omitempty"` // last lines of stdout+stderr
	Err       string   `json:"err,omitempty"`
}

// KitServiceRestartOutcome keeps the restart command result and the
// resulting health as two separate facts.
type KitServiceRestartOutcome struct {
	Attempted bool                  `json:"attempted"`
	OK        bool                  `json:"ok"`      // stop and start were accepted
	Healthy   bool                  `json:"healthy"` // the unit was active when checked afterwards
	Unit      string                `json:"unit,omitempty"`
	ExitCode  int                   `json:"exit_code"`
	Service   []enroll.ServiceState `json:"service,omitempty"`
	Err       string                `json:"err,omitempty"`
}

const kitOutputLines = 40

// kitServiceUnit is the first service unit of the instance's effective
// profile ("" when the harness has none Valise controls).
func kitServiceUnit(t *LoginTarget) string {
	if t == nil || t.Record == nil {
		return ""
	}
	p := t.Profile.ForAgent(t.Record.Name)
	if p.Service == nil || len(p.Service.Candidates) == 0 {
		return ""
	}
	return p.Service.Candidates[0].Unit
}

func lastLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(bytes.TrimSpace(b)), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// kitRunStep runs the harness's own argv on the server through an admin
// connection of its own. A dry run only reports the command. Refused or
// missing commands are reported in the outcome, never run.
func (s *Service) kitRunStep(ctx context.Context, host string, target *LoginTarget, argv []string, instance string, dryRun bool, auditPrefix string) *KitStepOutcome {
	out := &KitStepOutcome{Command: argv, ExitCode: -1}
	if len(argv) == 0 {
		out.Err = "这个 harness 资料没有声明命令；请按说明手动配置"
		return out
	}
	for _, f := range install.CheckCommand(argv, "") {
		if f.Refuse {
			out.Err = "命令被拒绝执行：" + f.Reason
			return out
		}
	}
	if dryRun {
		return out
	}
	out.Attempted = true
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		out.Err = errText1(err)
		return out
	}
	defer done()
	found, err := porter.Probe(ctx, t)
	if err != nil {
		out.Err = errText1(err)
		return out
	}
	var dirs []string
	prefix := []string(nil)
	if target != nil {
		if target.Profile.Detect != nil {
			dirs = target.Profile.Detect.BinDirs
		}
		if target.Record != nil && len(target.Record.Env) > 0 {
			keys := make([]string, 0, len(target.Record.Env))
			for k := range target.Record.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			prefix = []string{"env"}
			for _, k := range keys {
				prefix = append(prefix, k+"="+target.Record.Env[k])
			}
		}
	}
	res, err := t.Run(ctx, execLine(found.Path, dirs, append(prefix, argv...)), nil)
	audit := install.AuditParams{PlanID: auditPrefix + instance, Instance: instance, Result: "failed"}
	if target != nil && target.Record != nil {
		audit.ProfileID = target.Record.ProfileID
		audit.ProfileVersion = target.Record.ProfileVersion
	}
	if err != nil {
		out.Err = errText1(err)
	} else {
		out.ExitCode = res.ExitCode
		combined := bytes.TrimSpace(res.Stdout)
		if errOut := bytes.TrimSpace(res.Stderr); len(errOut) > 0 {
			if len(combined) > 0 {
				combined = append(combined, '\n')
			}
			combined = append(combined, errOut...)
		}
		out.Output = lastLines(combined, kitOutputLines)
		out.OK = res.ExitCode == 0
		if !out.OK {
			out.Err = "命令退出码 " + strconv.Itoa(res.ExitCode)
		}
		audit.Steps = []install.StepAudit{{Kind: "kit_mcp", Command: strings.Join(argv, " "), Exit: res.ExitCode}}
		if out.OK {
			audit.Result = "steps_done"
		}
	}
	_ = s.withSession(context.WithoutCancel(ctx), host, func(c caller) error { return c.Call(ctx, install.MethodAudit, audit, nil) })
	return out
}

// kitRestartService stops and starts the instance's service unit (owner
// session), then reads its live state.
func (s *Service) kitRestartService(ctx context.Context, host, instance string, target *LoginTarget, dryRun bool) *KitServiceRestartOutcome {
	unit := kitServiceUnit(target)
	out := &KitServiceRestartOutcome{Unit: unit, ExitCode: -1}
	if unit == "" {
		out.Err = "这个实例没有 Valise 控制的服务单元，请自己重启 agent"
		return out
	}
	if dryRun {
		return out
	}
	out.Attempted = true
	err := s.withSession(ctx, host, func(c caller) error {
		if err := c.Call(ctx, revive.MethodService, revive.ServiceParams{Unit: unit, Action: "stop"}, nil); err != nil {
			return err
		}
		return c.Call(ctx, revive.MethodService, revive.ServiceParams{Unit: unit, Action: "start"}, nil)
	})
	if err != nil {
		out.ExitCode, out.Err = 1, errText1(err)
		return out
	}
	out.OK, out.ExitCode = true, 0
	st := &NativeServiceRestoreResult{}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nativeServiceSettle+5*time.Second)
	defer cancel()
	s.settleNativeService(settleCtx, host, instance, st)
	out.Service = st.Service
	healthy := false
	for _, sv := range st.Service {
		if sv.Unit == unit {
			healthy = st.Checked && sv.Active == "active"
		}
	}
	out.Healthy = healthy
	return out
}
