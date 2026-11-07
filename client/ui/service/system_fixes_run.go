package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"valise/client/config"
	"valise/client/porter"
	"valise/core/install"
	"valise/core/valise"
)

// Fix modes. Direct runs the command as the user on a fresh admin
// connection; advanced runs it with sudo in a terminal of a fresh admin
// connection, only while the user has advanced mode on for that server
// (docs/INSTALL.md 4.1); manual only shows the commands.
const (
	FixDirect   = "direct"
	FixAdvanced = "advanced"
	FixManual   = "manual"
)

// SystemFixStep is one fixed command and the system setting it changes.
type SystemFixStep struct {
	Command []string `json:"command"`
	Change  string   `json:"change"`
	Undo    string   `json:"undo"`
	// When is set for a step that runs only if the problem remains.
	When string `json:"when,omitempty"`
	// Destructive marks a step that deletes or overwrites data and cannot
	// simply be undone (docs/N2_IMPLEMENTATION_2026-11-05.md F.3); a plan
	// with one needs an escape pod kept elsewhere first (INSTALL.md 4.2).
	// Turning linger or time sync on and restarting time sync are not.
	Destructive bool `json:"destructive,omitempty"`
}

// SystemFix is the confirmation page's content; Digest binds a run to it.
type SystemFix struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Instance string `json:"instance"`
	Goal     string `json:"goal"`
	Impact   string `json:"impact"`
	Mode     string `json:"mode"`
	Ready    bool   `json:"ready"` // advanced: the mode is on now
	// NonInteractive: advanced steps use sudo -n (takeover mode in the
	// terminal UI); sudo asking for a password stops the fix there.
	NonInteractive bool            `json:"non_interactive,omitempty"`
	Access         string          `json:"access"`
	Steps          []SystemFixStep `json:"steps"`
	Manual         [][]string      `json:"manual"`
	Why            string          `json:"why,omitempty"`
	Digest         string          `json:"digest"`
}

// SystemFixOptions: Sudo asks for the advanced-mode plan even when a
// direct one exists (linger after the system asked for administrator).
// NonInteractive plans the advanced steps with sudo -n, for takeover mode.
type SystemFixOptions struct{ Sudo, NonInteractive bool }

// SystemStepResult is one command's outcome; Ran is false for a step that
// was not needed or not reached.
type SystemStepResult struct {
	Command []string `json:"command"`
	Ran     bool     `json:"ran"`
	Exit    int      `json:"exit"`
	Note    string   `json:"note,omitempty"`
}

// SystemFixResult keeps the exit codes and the re-check apart.
type SystemFixResult struct {
	Fix          string              `json:"fix"`
	Mode         string              `json:"mode"`
	Steps        []SystemStepResult  `json:"steps"`
	Verification *HealthVerification `json:"verification"`
	Next         string              `json:"next,omitempty"`
	Audited      bool                `json:"audited"`
	// SudoPassword: sudo -n said it needs a password; Terminal is the
	// command that runs the fix in a terminal instead.
	SudoPassword bool   `json:"sudo_password,omitempty"`
	Terminal     string `json:"terminal,omitempty"`
}

// systemSettle is how long time sync may take to catch up; systemPoll the
// re-read interval.
var (
	systemSettle = 30 * time.Second
	systemPoll   = 3 * time.Second
)

func (s *Service) advancedOn(host string) bool { return host != "" && s.AdvancedHost() == host }

// adminLoginAvailable mirrors adminTransport: can a fresh admin connection
// be opened from this frontend without a terminal password prompt it
// cannot show?
func (s *Service) adminLoginAvailable(host string) bool {
	srv := s.server(host)
	return !srv.Valise() || s.HasAdminKey(host) || srv.Login == config.LoginKey || !s.NoTerminalLogin
}

// Command prefixes: none (direct), sudo, or sudo -n (takeover mode).
var (
	asUser  []string
	asSudo  = []string{"sudo"}
	asSudoN = []string{"sudo", "-n"}
)

func sudo(pre []string, argv ...string) []string {
	return append(append([]string{}, pre...), argv...)
}

// undoPrefix is what the user would type to undo: plain sudo, never -n.
func undoPrefix(pre []string) []string {
	if len(pre) > 0 {
		return asSudo
	}
	return asUser
}

func lingerSteps(user string, pre []string) []SystemFixStep {
	return []SystemFixStep{{Command: sudo(pre, "loginctl", "enable-linger", user),
		Change: "让 systemd 在你登出后继续运行你的用户服务（系统设置：用户 " + user + " 的 linger）",
		Undo:   strings.Join(sudo(undoPrefix(pre), "loginctl", "disable-linger", user), " ")}}
}

// clockSteps: turn NTP on, then restart systemd-timesyncd only if it is
// still off time; with NTP already on, the restart alone. Valise never sets
// the clock from this computer's time.
func clockSteps(st *SystemStatus, pre []string) []SystemFixStep {
	if st == nil || st.CanNTP == "no" {
		return nil
	}
	restart := SystemFixStep{Command: sudo(pre, "systemctl", "restart", "systemd-timesyncd"),
		Change: "重启系统时间同步服务 systemd-timesyncd，让它立即重新对时", Undo: "不需要撤销（只是重启这个服务）"}
	var steps []SystemFixStep
	if st.NTP != "yes" {
		steps = append(steps, SystemFixStep{Command: sudo(pre, "timedatectl", "set-ntp", "true"),
			Change: "开启系统的自动时间同步（NTP）", Undo: strings.Join(sudo(undoPrefix(pre), "timedatectl", "set-ntp", "false"), " ")})
		restart.When = "仅当开启后仍没有对上时间时执行"
	}
	if st.Timesyncd == "loaded" {
		steps = append(steps, restart)
	}
	return steps
}

func clockManual(st *SystemStatus) []string {
	if st != nil && st.CanNTP == "no" {
		return []string{"服务器上没有可用的时间同步服务，请服务器管理员安装并启用 systemd-timesyncd 或 chrony"}
	}
	if st == nil {
		return []string{"sudo timedatectl set-ntp true", "仍不同步时 sudo systemctl restart systemd-timesyncd"}
	}
	var out []string
	for _, step := range clockSteps(st, asSudo) {
		out = append(out, strings.Join(step.Command, " "))
	}
	if len(out) == 0 {
		out = append(out, "时间同步已开启但不是由 systemd-timesyncd 提供，请按服务器所用的时间同步服务（如 chrony）检查它的状态")
	}
	return out
}

// clockProblem is the same rule as the health item.
func clockProblem(st *SystemStatus, local time.Time) bool {
	return st.NTPSynchronized == "no" || (!st.Now.IsZero() && absDuration(st.Now.Sub(local)) > clockSkewLimit)
}

func (s *Service) systemStatus(ctx context.Context, host string) (*SystemStatus, error) {
	var st SystemStatus
	if err := s.call(ctx, host, MethodSystemStatus, struct{}{}, &st); err != nil {
		if rpcMissing(err) {
			return nil, valise.New("system_fix_unsupported", "服务器 porter 版本较旧，读不到 linger 与时间同步状态", "更新服务器上的 porter 后重试")
		}
		return nil, err
	}
	return &st, nil
}

// PlanSystemFix re-reads the server and builds the confirmation for one
// fix. The problem must still be there.
func (s *Service) PlanSystemFix(ctx context.Context, host, instance, id string, o SystemFixOptions) (*SystemFix, error) {
	if id != FixLinger && id != FixClock {
		return nil, valise.New("system_fix_unknown", "没有这个修复："+id, "可用的是 linger 与 clock")
	}
	st, err := s.systemStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	f := &SystemFix{ID: id, Name: systemFixNames[id], Host: host, Instance: instance, Steps: []SystemFixStep{}}
	switch id {
	case FixLinger:
		switch {
		case st.User == "":
			return nil, valise.New("system_fix_user", "读不到服务器上的用户名，无法确定要为谁开启 linger", "在服务器终端里自己执行 sudo loginctl enable-linger $USER")
		case st.Linger == "yes":
			return nil, valise.New("system_fix_resolved", "linger 已经开启，不需要修复", "刷新健康页")
		case st.Linger != "no":
			return nil, valise.New("system_fix_unknown_state", "读不到 linger 是否开启，没有准备修复", "检查服务器连接后刷新健康页")
		}
		f.Goal = "让定时备份在你退出登录或服务器重启后照常运行"
		f.Impact = "不会中断 agent 或其他服务；改的是这台服务器的系统设置，不在 agent 的备份里"
		for _, step := range lingerSteps(st.User, asSudo) {
			f.Manual = append(f.Manual, step.Command)
		}
	case FixClock:
		if !clockProblem(st, time.Now()) {
			return nil, valise.New("system_fix_resolved", "服务器时钟已经正常，不需要修复", "刷新健康页")
		}
		f.Goal = "让服务器自动与时间服务器对时；Valise 不会用这台电脑的时间改写服务器时钟"
		f.Impact = "不会中断 agent；服务器时间可能跳到正确值，定时任务按新时间计算"
		for _, step := range clockSteps(st, asSudo) {
			f.Manual = append(f.Manual, step.Command)
		}
	}
	root := o.Sudo || id == FixClock || s.advancedOn(host)
	switch {
	case root && s.advancedOn(host):
		f.Mode, f.Ready = FixAdvanced, true
	case root:
		if err := s.AdvancedReady(host); err != nil {
			f.Mode = FixManual
			f.Why = "需要管理员权限，而 Valise 只在你开启的高级模式里用 sudo 执行；"
			if ue, ok := valise.As(err); ok {
				f.Why += ue.Reason
			}
		} else {
			f.Mode = FixAdvanced
		}
	case s.adminLoginAvailable(host):
		f.Mode = FixDirect
	default:
		f.Mode = FixManual
		f.Why = "这里不能新开管理登录（没有管理钥匙，终端界面里也不能输入服务器密码）"
	}
	if f.Mode != FixManual {
		pre := asSudo
		switch {
		case f.Mode == FixAdvanced && o.NonInteractive && s.Takeover(host):
			pre, f.NonInteractive = asSudoN, true
		case f.Mode == FixDirect && id == FixLinger:
			pre = asUser
		}
		if id == FixLinger {
			f.Steps = lingerSteps(st.User, pre)
		} else {
			f.Steps = clockSteps(st, pre)
		}
		if len(f.Steps) == 0 {
			f.Mode, f.Why = FixManual, clockManual(st)[0]
		}
	}
	f.Access = s.systemFixAccess(host, f.Mode)
	if f.NonInteractive {
		f.Access = takeoverAccess
	}
	f.Digest = systemFixDigest(f)
	return f, nil
}

func systemFixDigest(f *SystemFix) string {
	b, _ := json.Marshal(struct {
		ID, Host, Instance, Mode string
		Steps                    []SystemFixStep
		NonInteractive           bool `json:",omitempty"`
	}{f.ID, f.Host, f.Instance, f.Mode, f.Steps, f.NonInteractive})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Service) systemFixAccess(host, mode string) string {
	switch mode {
	case FixManual:
		return "Valise 不会执行；请自己登录服务器执行上面的命令"
	case FixAdvanced:
		return "高级模式：每条命令新开一次管理登录，在终端里用 sudo 执行；sudo 密码在终端里输入，Valise 不读取、不保存"
	}
	login := "用你自己的 SSH 登录"
	switch {
	case !s.Tiered(host):
	case s.HasAdminKey(host):
		login = "要解锁管理钥匙（管理口令）"
	default:
		login = "要输入服务器密码（临时管理登录）"
	}
	return "新开一次管理登录执行，不复用日常连接 · " + login
}

// SystemFixSteps is the confirmed plan as a checked batch, for the
// advanced-mode confirmation of the CLI.
func SystemFixSteps(f *SystemFix) *RequirementBatch {
	b := &RequirementBatch{}
	for _, st := range f.Steps {
		fs := install.CheckCommand(st.Command, "")
		b.Commands = append(b.Commands, CheckedCommand{Command: st.Command, Source: "valise", Findings: fs})
		b.Refused = b.Refused || install.Refused(fs)
		b.Sentence = b.Sentence || install.NeedsRiskSentence(fs)
	}
	return b
}

// RunSystemFix runs the confirmed plan; a changed plan is refused. The
// exit codes and the re-check are reported separately, and an audit entry
// is written whatever the outcome.
func (s *Service) RunSystemFix(ctx context.Context, host, instance, id, digest string, o SystemFixOptions, tio InstallIO) (*SystemFixResult, error) {
	f, err := s.PlanSystemFix(ctx, host, instance, id, o)
	if err != nil {
		return nil, err
	}
	if digest == "" || f.Digest != digest {
		return nil, valise.New("system_fix_plan_changed", "服务器状态或执行方式已变化，修复没有执行", "重新打开确认页，核对要执行的命令")
	}
	switch {
	case f.Mode == FixManual:
		return nil, valise.New("system_fix_manual", "Valise 不能代为执行："+f.Why, "自己在服务器上执行："+joinCommands(f.Manual))
	case f.Mode == FixAdvanced && !s.advancedOn(host):
		return nil, valise.New("advanced_off", "高级模式没有为 "+host+" 开启，修复没有执行", "开启高级模式后重试，或自己在服务器上执行："+joinCommands(f.Manual))
	case SystemFixSteps(f).Refused:
		return nil, valise.New("advanced_refused", "修复命令没有通过检查，没有执行", "")
	}
	if err := s.destructiveGate(ctx, host, f.Steps); err != nil {
		return nil, err
	}
	res := &SystemFixResult{Fix: id, Mode: f.Mode}
	if f.NonInteractive {
		ok, err := s.sudoWithoutPassword(ctx, host)
		if err != nil {
			return nil, err
		}
		if !ok {
			for _, step := range f.Steps {
				res.Steps = append(res.Steps, SystemStepResult{Command: step.Command, Note: "这台服务器的 sudo 需要密码，没有在这里执行"})
			}
			s.sudoNeedsPassword(f, res)
			res.Verification = s.verifySystem(ctx, host, id, 0)
			s.auditSystemFix(ctx, host, f, res, nil)
			return res, nil
		}
	}
	var runErr error
	stopped := false
	for i, step := range f.Steps {
		sr := SystemStepResult{Command: step.Command}
		switch {
		case stopped:
			sr.Note = "前一步没有成功，没有执行"
		case step.When != "" && s.verifySystem(ctx, host, id, systemSettle).State == "resolved":
			sr.Note = "已经对上时间，不需要执行"
		default:
			if tio.Step != nil {
				tio.Step(i, PlanStep{Kind: "system", Summary: strings.Join(step.Command, " "), Command: step.Command})
			}
			sr.Ran = true
			if f.NonInteractive {
				var askedPassword bool
				sr.Exit, askedPassword, runErr = s.runSudoN(ctx, host, step.Command)
				if askedPassword {
					s.sudoNeedsPassword(f, res)
				}
			} else {
				sr.Exit, runErr = s.runSystemStep(ctx, host, f.Mode, step.Command, tio)
			}
			stopped = runErr != nil || sr.Exit != 0
		}
		res.Steps = append(res.Steps, sr)
	}
	settle := time.Duration(0)
	if id == FixClock && !stopped {
		settle = systemSettle
	}
	res.Verification = s.verifySystem(ctx, host, id, settle)
	if !res.SudoPassword {
		res.Next = systemNext(f, res)
	}
	s.auditSystemFix(ctx, host, f, res, runErr)
	return res, runErr
}

// auditSystemFix writes the fix's audit entry: commands and exit codes.
func (s *Service) auditSystemFix(ctx context.Context, host string, f *SystemFix, res *SystemFixResult, runErr error) {
	result := res.Verification.State
	if runErr != nil {
		result = "failed"
	}
	var steps []install.StepAudit
	for _, sr := range res.Steps {
		if sr.Ran {
			steps = append(steps, install.StepAudit{Kind: "system", Command: strings.Join(sr.Command, " "), Exit: sr.Exit})
		}
	}
	audit := SystemFixAudit{Fix: f.ID, Instance: f.Instance, Mode: f.Mode, Steps: steps, Result: result}
	res.Audited = s.call(context.WithoutCancel(ctx), host, MethodSystemFixAudit, audit, nil) == nil
}

// runSystemStep opens a fresh admin connection for one command.
func (s *Service) runSystemStep(ctx context.Context, host, mode string, argv []string, tio InstallIO) (int, error) {
	if mode == FixAdvanced {
		if s.HasAdminKey(host) {
			if _, err := s.unlockAdmin(ctx, host); err != nil {
				return -1, err
			}
		}
		if !s.advancedOn(host) {
			return -1, valise.New("advanced_off", "高级模式已经关闭，后面的命令没有执行", "")
		}
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return -1, err
	}
	defer done()
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return -1, err
	}
	if mode == FixAdvanced {
		return runTTY(ctx, t, found.Path, nil, argv, tio)
	}
	r, err := t.Run(ctx, execLine(found.Path, nil, argv), nil)
	if err != nil {
		return -1, err
	}
	return r.ExitCode, nil
}

// verifySystem re-reads the server (polling up to settle) independently
// of exit codes. Unknown never means resolved.
func (s *Service) verifySystem(ctx context.Context, host, id string, settle time.Duration) *HealthVerification {
	deadline := time.Now().Add(settle)
	for {
		v := &HealthVerification{ItemID: id, State: "unknown", Text: "无法复核，请检查服务器连接后刷新健康页"}
		st, err := s.systemStatus(ctx, host)
		if err == nil {
			switch {
			case id == FixLinger && st.Linger == "yes":
				v.State, v.Text = "resolved", "本次检查通过：linger 已开启"
			case id == FixLinger && st.Linger == "no":
				v.State, v.Text = "remaining", "问题仍在：linger 仍没有开启"
			case id == FixClock && st.NTPSynchronized != "unknown" && !clockProblem(st, time.Now()):
				v.State, v.Text = "resolved", "本次检查通过：时间同步已对上，和这台电脑的差在 5 分钟以内"
			case id == FixClock && clockProblem(st, time.Now()):
				v.State, v.Text = "remaining", "问题仍在："+clockText(st)
			}
		}
		if v.State == "resolved" || !time.Now().Before(deadline) {
			return v
		}
		select {
		case <-ctx.Done():
			return v
		case <-time.After(systemPoll):
		}
	}
}

func clockText(st *SystemStatus) string {
	var parts []string
	if skew := absDuration(st.Now.Sub(time.Now())); !st.Now.IsZero() && skew > clockSkewLimit {
		parts = append(parts, "和这台电脑差 "+agoPlain(skew))
	}
	if st.NTPSynchronized == "no" {
		parts = append(parts, "时间同步还没有对上时间")
	}
	return strings.Join(parts, "，")
}

// systemNext is the next step when the fix did not verify.
func systemNext(f *SystemFix, r *SystemFixResult) string {
	if r.Verification.State == "resolved" {
		return ""
	}
	failed := false
	for _, st := range r.Steps {
		failed = failed || (st.Ran && st.Exit != 0)
	}
	manual := "自己在服务器上执行：" + joinCommands(f.Manual)
	switch {
	case r.Verification.State == "unknown":
		return "稍后刷新健康页复核；若问题仍在，" + manual
	case f.ID == FixLinger && failed && f.Mode == FixDirect:
		return "系统可能要求管理员权限：开启高级模式后再修复一次（会用 sudo），或" + manual
	case f.ID == FixClock && !failed:
		return "命令已执行但时间还没对上：可能是服务器连不上时间服务器（例如防火墙挡住了 UDP 123 端口）。几分钟后刷新健康页；仍不行请联系服务器管理员或云厂商"
	}
	return "看上面的退出码与终端输出找原因；或" + manual
}

func joinCommands(cmds [][]string) string {
	var out []string
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return strings.Join(out, "；")
}
