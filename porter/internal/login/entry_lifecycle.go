package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"valise/core/enroll"
	"valise/core/login"
	"valise/core/profile"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
	porterrevive "valise/porter/internal/revive"
)

const (
	defaultEntryStartWait = 15 * time.Second
	entryPrepareTimeout   = 120 * time.Second
	entryCheckTimeout     = 15 * time.Second
	entryStopTimeout      = 15 * time.Second
	maxCaptureLogBytes    = 256 << 10
)

var (
	entryStartPoll       = 250 * time.Millisecond
	capturePostReadyWait = time.Second
	unitActivePoll       = 250 * time.Millisecond
	defaultRestartWait   = 20 * time.Second
	procRoot             = "/proc"
	entryIDRe            = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	unitSanitizeRe       = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	relativeAuthSuffixRe = regexp.MustCompile(`(?i)(?:^|[\s"'(\[])((?:/[^\s"'<>]*)?[?#][^\s"'<>]*(?:token|key|secret|auth|code)=[^\s"'<>]+)`)
	sensitiveParamRe     = regexp.MustCompile(`(?i)(?:^|[\s?&;#,"'(=])(?:token|key|secret|auth|code)\s*=`)
	suspiciousTailRe     = regexp.MustCompile(`(?i)(?:http|tok|ken|key|sec|ret|auth|code|[=?#:/])`)
)

// entryState tracks a Valise-started detached entry process or transient unit
// under ~/.local/state/valise/entries/<inst>_<entry>.json.
type entryState struct {
	InstanceID string    `json:"instance_id"`
	EntryID    string    `json:"entry_id"`
	Port       int       `json:"port"`
	Unit       string    `json:"unit,omitempty"`
	PID        int       `json:"pid,omitempty"`
	PGID       int       `json:"pgid,omitempty"`
	ProcStart  string    `json:"proc_start,omitempty"`
	LogPath    string    `json:"log_path,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

func entriesDir(home string) string {
	return filepath.Join(home, ".local", "state", "valise", "entries")
}

func entryStatePath(home, inst, entryID string) string {
	return filepath.Join(entriesDir(home), inst+"_"+entryID+".json")
}

func entryLogPath(home, inst, entryID string) string {
	return filepath.Join(entriesDir(home), inst+"_"+entryID+".log")
}

func entryUnitName(inst, entryID string) string {
	sInst := unitSanitizeRe.ReplaceAllString(inst, "-")
	sEntry := unitSanitizeRe.ReplaceAllString(entryID, "-")
	return fmt.Sprintf("valise-entry-%s-%s", sInst, sEntry)
}

func validateEntryTarget(inst string, entry profile.Entry) error {
	if err := enroll.ValidInstanceID(inst); err != nil {
		return valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	if !entryIDRe.MatchString(entry.ID) {
		return valise.New("entry_id_invalid", "入口 id 无效："+entry.ID, "检查 profile 中的 entries[].id")
	}
	if entry.Port < 1 || entry.Port > 65535 {
		return valise.New("port_invalid", fmt.Sprintf("入口端口无效：%d", entry.Port), "检查 profile 中的 entries[].port")
	}
	return nil
}

func loadEnrolledEntryEnv(e *porterenroll.Env, home, inst string, entry profile.Entry) (*enroll.Record, *profile.Profile, *porterenroll.Env, int, error) {
	if err := validateEntryTarget(inst, entry); err != nil {
		return nil, nil, nil, 0, err
	}
	rec, err := porterenroll.ReadRecord(home, inst)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	if rec == nil {
		return nil, nil, nil, 0, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+inst, "先把这个 agent 接入 Valise")
	}
	prof, _ := porterenroll.ReadProfile(home, inst)
	if prof != nil {
		if err := verifyAgainstEnrolledProfile(prof, rec, entry); err != nil {
			return nil, nil, nil, 0, err
		}
		e = e.ForInstance(prof, rec)
	} else {
		e = e.With(rec.Env)
	}
	port := entry.Port
	if p, ok := rec.EntryPorts[entry.ID]; ok && p > 0 {
		port = p
	}
	return rec, prof, e, port, nil
}

func verifyAgainstEnrolledProfile(prof *profile.Profile, rec *enroll.Record, entry profile.Entry) error {
	if prof == nil {
		return nil
	}
	// ReadProfile already scoped prof for rec.Name and applied EntryPorts + LocalEntries.
	if len(prof.Entries) == 0 {
		return nil
	}
	var declared *profile.Entry
	for i := range prof.Entries {
		if prof.Entries[i].ID == entry.ID {
			declared = &prof.Entries[i]
			break
		}
	}
	if declared == nil {
		return valise.New("entry_not_declared", "已接入的 profile 未声明入口 "+entry.ID, "更新接入的 profile 后重试")
	}
	if declared.LocalSource != "" && (entry.Prepare != nil || entry.Start != nil) {
		return valise.New("entry_not_declared", "本地汇报入口 "+entry.ID+" 仅支持只读转发，不允许执行准备或启动命令", "在服务器终端手动管理该入口")
	}
	if declared.Prepare != nil && entry.Prepare != nil {
		if len(declared.Prepare.CheckArgv) > 0 && !slices.Equal(declared.Prepare.CheckArgv, entry.Prepare.CheckArgv) {
			return valise.New("entry_argv_mismatch", "入口检查命令与已接入的 profile 不一致", "更新接入的 profile 后重试")
		}
		if len(declared.Prepare.Steps) > 0 {
			if len(declared.Prepare.Steps) != len(entry.Prepare.Steps) {
				return valise.New("entry_argv_mismatch", "入口准备步骤与已接入的 profile 不一致", "更新接入的 profile 后重试")
			}
			for i := range declared.Prepare.Steps {
				if !slices.Equal(declared.Prepare.Steps[i], entry.Prepare.Steps[i]) {
					return valise.New("entry_argv_mismatch", "入口准备步骤与已接入的 profile 不一致", "更新接入的 profile 后重试")
				}
			}
		}
	}
	if declared.Start != nil && entry.Start != nil {
		if len(declared.Start.Argv) > 0 && !slices.Equal(declared.Start.Argv, entry.Start.Argv) {
			return valise.New("entry_argv_mismatch", "入口启动命令与已接入的 profile 不一致", "更新接入的 profile 后重试")
		}
		if len(declared.Start.StopArgv) > 0 && !slices.Equal(declared.Start.StopArgv, entry.Start.StopArgv) {
			return valise.New("entry_argv_mismatch", "入口停止命令与已接入的 profile 不一致", "更新接入的 profile 后重试")
		}
	}
	return nil
}

func expandEntryArgv(home string, rec *enroll.Record, argv profile.Argv) ([]string, error) {
	if err := profile.ValidateEntryArgv(argv); err != nil {
		return nil, valise.New("entry_argv_invalid", err.Error(), "检查 profile 中的入口配置")
	}
	stateDir := ""
	hasStateDir := false
	if rec != nil {
		stateDir, hasStateDir = rec.Roots["state"]
	}
	exp := make([]string, len(argv))
	for i, s := range argv {
		if strings.Contains(s, "${STATE_DIR}") && !hasStateDir {
			return nil, valise.New("entry_argv_unexpanded", "入口命令中的 ${STATE_DIR} 无法展开", "检查实例档案中的 state 目录")
		}
		s = strings.ReplaceAll(s, "${HOME}", home)
		if hasStateDir {
			s = strings.ReplaceAll(s, "${STATE_DIR}", stateDir)
		}
		exp[i] = s
	}
	return exp, nil
}

func readEntryState(home, inst, entryID string) (*entryState, error) {
	b, err := os.ReadFile(entryStatePath(home, inst, entryID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st entryState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, nil
	}
	return &st, nil
}

func writeEntryState(home string, st entryState) error {
	dir := entriesDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(entryStatePath(home, st.InstanceID, st.EntryID), append(b, '\n'), 0o600)
}

func isUnitActive(ctx context.Context, e *porterenroll.Env, unit string) bool {
	if unit == "" {
		return false
	}
	out, err := e.Run(ctx, 5*time.Second, "", "systemctl", "--user", "is-active", unit)
	if err != nil {
		return false
	}
	s := strings.TrimSpace(string(out.Stdout))
	return s == "active" || s == "activating"
}

func waitUnitActive(ctx context.Context, e *porterenroll.Env, unit string, timeout time.Duration) bool {
	if unit == "" {
		return false
	}
	if timeout <= 0 {
		timeout = defaultRestartWait
	}
	deadline := time.Now().Add(timeout)
	for {
		out, err := e.Run(ctx, 5*time.Second, "", "systemctl", "--user", "is-active", unit)
		if err == nil && out.Code == 0 {
			s := strings.TrimSpace(string(out.Stdout))
			if s == "active" || s == "" {
				return true
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		rem := time.Until(deadline)
		sleep := unitActivePoll
		if rem < sleep {
			sleep = rem
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(sleep):
		}
	}
}

func procStart(pid int) string {
	if pid <= 0 || procRoot == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return ""
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return ""
	}
	return f[19] // field 22 overall
}

func hasProcFS() bool {
	if procRoot == "" {
		return false
	}
	fi, err := os.Stat(procRoot)
	return err == nil && fi.IsDir()
}

func isManagedPIDAlive(st *entryState) bool {
	if st == nil || st.PID <= 0 {
		return false
	}
	if err := syscall.Kill(st.PID, 0); err != nil {
		return false
	}
	if hasProcFS() {
		if st.ProcStart == "" {
			return false
		}
		return procStart(st.PID) == st.ProcStart
	}
	return true
}

func isEntryStateAlive(ctx context.Context, e *porterenroll.Env, st *entryState) bool {
	if st == nil {
		return false
	}
	if st.Unit != "" {
		return isUnitActive(ctx, e, st.Unit)
	}
	return isManagedPIDAlive(st)
}

func checkRelativePathUnder(root, rel string) (string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "${") {
		return "", valise.New("entry_check_path_invalid", "入口检查路径必须是相对路径："+rel, "检查 profile 中的 check_paths")
	}
	for _, seg := range strings.Split(strings.TrimSuffix(rel, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", valise.New("entry_check_path_invalid", "入口检查路径包含非法段："+rel, "检查 profile 中的 check_paths")
		}
	}
	cleanRoot := filepath.Clean(root)
	joined := filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(rel)))
	if joined != cleanRoot && !strings.HasPrefix(joined, cleanRoot+string(filepath.Separator)) {
		return "", valise.New("entry_check_path_invalid", "入口检查路径越界："+rel, "检查 profile 中的 check_paths")
	}
	return joined, nil
}

func evaluatePrepare(ctx context.Context, e *porterenroll.Env, home string, rec *enroll.Record, pr *profile.EntryPrepare) (bool, string) {
	if pr == nil {
		return true, ""
	}
	stateDir := rec.Roots["state"]
	for _, rel := range pr.CheckPaths {
		full, err := checkRelativePathUnder(stateDir, rel)
		if err != nil {
			return false, err.Error()
		}
		if _, err := os.Stat(full); err != nil {
			return false, "缺少文件或目录：" + rel
		}
	}
	if len(pr.CheckArgv) > 0 {
		exp, err := expandEntryArgv(home, rec, pr.CheckArgv)
		if err != nil {
			return false, err.Error()
		}
		out, err := e.Run(ctx, entryCheckTimeout, home, exp...)
		if err != nil {
			return false, "无法执行检查命令"
		}
		if out.Code != 0 {
			return false, fmt.Sprintf("检查命令退出码 %d", out.Code)
		}
		if pr.ExpectOutput != "" {
			got := strings.TrimSpace(string(out.Stdout))
			want := strings.TrimSpace(pr.ExpectOutput)
			if got != want {
				return false, fmt.Sprintf("检查输出与预期不符（预期 %q）", want)
			}
		}
	}
	return true, ""
}

// ProbeEntry inspects an entry's listening state, prepare readiness, and
// whether a Valise-started process or transient systemd unit is still running.
func ProbeEntry(ctx context.Context, e *porterenroll.Env, home string, p login.EntryProbeParams) (*login.EntryProbeResult, error) {
	rec, _, e, port, err := loadEnrolledEntryEnv(e, home, p.InstanceID, p.Entry)
	if err != nil {
		return nil, err
	}
	listen, err := ListenAddrs(port)
	if err != nil {
		return nil, err
	}
	prepared, note := evaluatePrepare(ctx, e, home, rec, p.Entry.Prepare)
	res := &login.EntryProbeResult{
		EntryID:     p.Entry.ID,
		Port:        port,
		Listening:   len(listen.Addrs) > 0,
		ListenAddrs: listen.Addrs,
		HasPrepare:  p.Entry.Prepare != nil,
		Prepared:    prepared,
		PrepareNote: note,
		HasStart:    p.Entry.Start != nil && len(p.Entry.Start.Argv) > 0,
		AgentHint:   p.Entry.AgentHint,
		LocalSource: p.Entry.LocalSource,
	}
	if st, err := readEntryState(home, p.InstanceID, p.Entry.ID); err == nil && st != nil {
		if isEntryStateAlive(ctx, e, st) {
			res.ManagedByUs = true
			res.ManagedUnit = st.Unit
			res.ManagedPID = st.PID
			res.ManagedAlive = true
		} else {
			_ = os.Remove(entryStatePath(home, p.InstanceID, p.Entry.ID))
		}
	}
	return res, nil
}

// PrepareEntry executes an entry's prepare steps in order (without any shell
// wrapper) and optionally restarts the instance's systemd service.
func PrepareEntry(ctx context.Context, e *porterenroll.Env, home string, p login.EntryPrepareParams) (*login.EntryPrepareResult, error) {
	rec, prof, e, port, err := loadEnrolledEntryEnv(e, home, p.InstanceID, p.Entry)
	if err != nil {
		return nil, err
	}
	pr := p.Entry.Prepare
	if pr == nil || len(pr.Steps) == 0 {
		return nil, valise.New("entry_prepare_none", "该入口没有声明需要准备的步骤", "可以直接尝试启动或打开入口")
	}
	stepsRun := 0
	for i, step := range pr.Steps {
		exp, err := expandEntryArgv(home, rec, step)
		if err != nil {
			return nil, err
		}
		out, err := e.Run(ctx, entryPrepareTimeout, home, exp...)
		if err != nil {
			return nil, valise.New("entry_prepare_failed", fmt.Sprintf("准备步骤 %d 无法执行", i+1), "确认相关工具已安装并在登录 PATH 中")
		}
		if out.Code != 0 {
			return nil, valise.New("entry_prepare_failed", fmt.Sprintf("准备步骤 %d 失败（退出码 %d）", i+1, out.Code), "在服务器终端手动检查配置或依赖后重试")
		}
		stepsRun++
	}
	restarted := false
	if pr.RestartService {
		unit := instanceServiceUnit(prof, rec)
		if unit != "" {
			if err := porterrevive.RestartUnit(ctx, e, unit); err != nil {
				return nil, err
			}
			restarted = true
			wait := defaultRestartWait
			if prof != nil && prof.Health != nil && prof.Health.GraceSeconds > 0 {
				wait = time.Duration(prof.Health.GraceSeconds) * time.Second
			}
			waitUnitActive(ctx, e, unit, wait)
		}
	}
	prepared, note := evaluatePrepare(ctx, e, home, rec, pr)
	listen, _ := ListenAddrs(port)
	listening := listen != nil && len(listen.Addrs) > 0
	return &login.EntryPrepareResult{
		EntryID:          p.Entry.ID,
		StepsRun:         stepsRun,
		ServiceRestarted: restarted,
		Prepared:         prepared,
		Listening:        listening,
		Detail:           note,
	}, nil
}

func instanceServiceUnit(prof *profile.Profile, rec *enroll.Record) string {
	if prof != nil {
		eff := prof.ForAgent(rec.Name)
		if eff.Service != nil && len(eff.Service.Candidates) > 0 {
			return eff.Service.Candidates[0].Unit
		}
	}
	if rec != nil {
		for _, sv := range rec.Service {
			if sv.Exists() && sv.Unit != "" {
				return sv.Unit
			}
		}
		if len(rec.Service) > 0 {
			return rec.Service[0].Unit
		}
	}
	return ""
}

// StartEntry starts an entry's command detached in the background (via
// systemd-run --user when available, falling back to Setsid + log file), polls
// ListenAddrs(port) up to WaitSeconds (default 15s), and returns immediately.
// Porter never stays resident.
func StartEntry(ctx context.Context, e *porterenroll.Env, home string, p login.EntryStartParams) (*login.EntryStartResult, error) {
	rec, _, e, port, err := loadEnrolledEntryEnv(e, home, p.InstanceID, p.Entry)
	if err != nil {
		return nil, err
	}
	if listen, err := ListenAddrs(port); err == nil && len(listen.Addrs) > 0 {
		res := &login.EntryStartResult{
			EntryID:     p.Entry.ID,
			Port:        port,
			Listening:   true,
			ListenAddrs: listen.Addrs,
			AlreadyUp:   true,
		}
		if st, err := readEntryState(home, p.InstanceID, p.Entry.ID); err == nil && st != nil {
			if isEntryStateAlive(ctx, e, st) {
				res.ManagedByUs = true
				res.ManagedUnit = st.Unit
				res.ManagedPID = st.PID
				res.LogPath = st.LogPath
			} else {
				_ = os.Remove(entryStatePath(home, p.InstanceID, p.Entry.ID))
			}
		}
		return res, nil
	}
	if st, err := readEntryState(home, p.InstanceID, p.Entry.ID); err == nil && st != nil && !isEntryStateAlive(ctx, e, st) {
		_ = os.Remove(entryStatePath(home, p.InstanceID, p.Entry.ID))
	}
	stSpec := p.Entry.Start
	if stSpec == nil || len(stSpec.Argv) == 0 {
		return nil, valise.New("entry_start_none", "该入口没有声明后台启动命令", "在服务器终端手动启动该入口后重试")
	}
	exp, err := expandEntryArgv(home, rec, stSpec.Argv)
	if err != nil {
		return nil, err
	}
	bin := exp[0]
	if !filepath.IsAbs(bin) {
		resolved, err := e.LookPath(bin)
		if err != nil {
			return nil, valise.New("entry_start_binary_missing", "服务器的登录 PATH 中找不到 "+bin, "确认 harness 已安装且位于登录 shell PATH 中")
		}
		bin = resolved
	}

	state := entryState{
		InstanceID: p.InstanceID,
		EntryID:    p.Entry.ID,
		Port:       port,
		StartedAt:  time.Now().UTC(),
	}

	startedBySystemd := false
	var origLogFile *os.File
	if _, err := e.LookPath("systemd-run"); err == nil {
		unitBase := entryUnitName(p.InstanceID, p.Entry.ID)
		unitFull := unitBase + ".service"
		// Stop and reset any active/residual transient unit of the same name
		// before systemd-run so a name collision never falls through to Setsid.
		if isUnitActive(ctx, e, unitFull) {
			_, _ = e.Run(ctx, entryStopTimeout, home, "systemctl", "--user", "stop", unitFull)
		}
		_, _ = e.Run(ctx, 5*time.Second, home, "systemctl", "--user", "reset-failed", unitFull)
		args := []string{
			"systemd-run", "--user",
			"--unit=" + unitBase,
			"--property=CollectMode=inactive-or-failed",
			"--working-directory=" + home,
		}
		var captureLogPath string
		if stSpec.CaptureURL {
			if err := os.MkdirAll(entriesDir(home), 0o700); err != nil {
				return nil, err
			}
			captureLogPath = entryLogPath(home, p.InstanceID, p.Entry.ID)
			f, err := os.OpenFile(captureLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return nil, err
			}
			_ = os.Chmod(captureLogPath, 0o600)
			origLogFile = f
			args = append(args,
				"--setenv=PYTHONUNBUFFERED=1",
				"--property=StandardOutput=append:"+captureLogPath,
				"--property=StandardError=append:"+captureLogPath,
			)
		}
		if pathVal := e.Get("PATH"); pathVal != "" {
			args = append(args, "--setenv=PATH="+pathVal)
		}
		for k, v := range rec.Env {
			args = append(args, "--setenv="+k+"="+v)
		}
		args = append(args, "--", bin)
		args = append(args, exp[1:]...)
		out, runErr := e.Run(ctx, 15*time.Second, home, args...)
		if runErr == nil && out.Code == 0 {
			state.Unit = unitFull
			state.LogPath = captureLogPath
			startedBySystemd = true
		} else if isUnitActive(ctx, e, unitFull) {
			state.Unit = unitFull
			state.LogPath = captureLogPath
			startedBySystemd = true
		} else if origLogFile != nil {
			_ = origLogFile.Close()
			origLogFile = nil
		}
	}

	if !startedBySystemd {
		if err := os.MkdirAll(entriesDir(home), 0o700); err != nil {
			return nil, err
		}
		logPath := entryLogPath(home, p.InstanceID, p.Entry.ID)
		openFlags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if stSpec.CaptureURL {
			openFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		logFile, err := os.OpenFile(logPath, openFlags, 0o600)
		if err != nil {
			return nil, err
		}
		if stSpec.CaptureURL {
			_ = logFile.Truncate(0)
			_ = os.Chmod(logPath, 0o600)
		}
		cmd := exec.Command(bin, exp[1:]...)
		cmd.Dir = home
		cmdEnv := append([]string(nil), e.Vars...)
		if stSpec.CaptureURL {
			cmdEnv = append(cmdEnv, "PYTHONUNBUFFERED=1")
		}
		cmd.Env = cmdEnv
		cmd.Stdin = nil
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			logFile.Close()
			return nil, valise.New("entry_start_failed", "无法在后台启动入口进程", "检查命令与日志后重试").WithDetail(err.Error())
		}
		pid := cmd.Process.Pid
		if stSpec.CaptureURL {
			origLogFile = logFile
		} else {
			logFile.Close()
		}
		_ = cmd.Process.Release()
		state.PID = pid
		state.PGID = pid
		state.ProcStart = procStart(pid)
		state.LogPath = logPath
	}

	var authSuffix string
	decoupled := false
	decoupleAndSanitize := func() {
		if decoupled || !stSpec.CaptureURL || state.LogPath == "" {
			return
		}
		decoupled = true
		sanitizeAndDecoupleEntryLog(state.LogPath, origLogFile, authSuffix)
		origLogFile = nil
	}
	if stSpec.CaptureURL && state.LogPath != "" {
		defer decoupleAndSanitize()
	} else if origLogFile != nil {
		defer origLogFile.Close()
	}

	if err := writeEntryState(home, state); err != nil {
		return nil, err
	}

	hasTokenSuffix := func(s string) bool {
		return strings.ContainsAny(s, "?#")
	}
	tryCapture := func() {
		if !stSpec.CaptureURL || state.LogPath == "" || hasTokenSuffix(authSuffix) {
			return
		}
		data := readBoundedFile(state.LogPath, maxCaptureLogBytes)
		if len(data) == 0 {
			return
		}
		if suf := extractPortURLSuffix(data, port); suf != "" {
			if hasTokenSuffix(suf) || authSuffix == "" {
				authSuffix = suf
			}
			if hasTokenSuffix(authSuffix) {
				decoupleAndSanitize()
			}
		}
	}

	wait := time.Duration(stSpec.WaitSeconds) * time.Second
	if wait <= 0 {
		wait = defaultEntryStartWait
	}
	deadline := time.Now().Add(wait)
	var listen *login.ListenResult
	for {
		tryCapture()
		listen, _ = ListenAddrs(port)
		if listen != nil && len(listen.Addrs) > 0 {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		rem := time.Until(deadline)
		sleep := entryStartPoll
		if rem < sleep {
			sleep = rem
		}
		select {
		case <-ctx.Done():
		case <-time.After(sleep):
		}
	}

	var addrs []string
	if listen != nil {
		addrs = listen.Addrs
	}
	if len(addrs) > 0 {
		waitEntryHTTPReady(ctx, addrs, port, p.Entry.Path, deadline, tryCapture)
	}
	tryCapture()
	if stSpec.CaptureURL && !hasTokenSuffix(authSuffix) {
		extraDeadline := time.Now().Add(capturePostReadyWait)
		if deadline.Before(extraDeadline) {
			extraDeadline = deadline
		}
		for !hasTokenSuffix(authSuffix) && time.Now().Before(extraDeadline) && ctx.Err() == nil {
			rem := time.Until(extraDeadline)
			sleep := entryStartPoll
			if rem < sleep {
				sleep = rem
			}
			if sleep <= 0 {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(sleep):
			}
			tryCapture()
		}
	}

	return &login.EntryStartResult{
		EntryID:     p.Entry.ID,
		Port:        port,
		Listening:   len(addrs) > 0,
		ListenAddrs: addrs,
		ManagedByUs: true,
		ManagedUnit: state.Unit,
		ManagedPID:  state.PID,
		LogPath:     state.LogPath,
		AuthSuffix:  authSuffix,
	}, nil
}

func waitEntryHTTPReady(ctx context.Context, addrs []string, port int, entryPath string, deadline time.Time, onPoll func()) bool {
	probeHost := "127.0.0.1"
	if len(addrs) > 0 && !slices.ContainsFunc(addrs, func(a string) bool {
		ip := net.ParseIP(a)
		return ip != nil && ip.To4() != nil
	}) {
		probeHost = "[::1]"
	}
	path := entryPath
	if path == "" {
		path = "/"
	}
	probeURL := fmt.Sprintf("http://%s:%d%s", probeHost, port, path)
	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for {
		if onPoll != nil {
			onPoll()
		}
		rem := time.Until(deadline)
		if rem <= 0 || ctx.Err() != nil {
			return false
		}
		reqTimeout := 2 * time.Second
		if rem < reqTimeout {
			reqTimeout = rem
		}
		reqCtx, cancel := context.WithTimeout(ctx, reqTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, probeURL, nil)
		if err == nil {
			resp, doErr := client.Do(req)
			if doErr == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				cancel()
				return true
			}
		}
		cancel()
		rem = time.Until(deadline)
		if rem <= 0 || ctx.Err() != nil {
			return false
		}
		sleep := entryStartPoll
		if rem < sleep {
			sleep = rem
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(sleep):
		}
	}
}

func readBoundedFile(path string, maxBytes int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, maxBytes))
	return b
}

func extractPortURLSuffix(output []byte, port int) string {
	wantPort := strconv.Itoa(port)
	text := string(output)
	var fallback string
	for _, m := range urlRe.FindAllString(text, -1) {
		u, err := url.Parse(m)
		if err != nil || u.Port() != wantPort {
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
		if u.RawQuery != "" || u.Fragment != "" {
			return suffix
		}
		if fallback == "" {
			fallback = suffix
		}
	}
	if m := relativeAuthSuffixRe.FindStringSubmatch(text); len(m) > 1 {
		s := strings.TrimRight(m[1], `.,);]}>"'`)
		if strings.HasPrefix(s, "?") || strings.HasPrefix(s, "#") {
			s = "/" + s
		}
		return s
	}
	return fallback
}

func extractAuthSecrets(authSuffix string) []string {
	if authSuffix == "" {
		return nil
	}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) >= 3 && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if u, err := url.Parse("http://localhost" + authSuffix); err == nil {
		if u.RawQuery != "" {
			add(u.RawQuery)
			for _, vals := range u.Query() {
				for _, v := range vals {
					add(v)
				}
			}
		}
		if u.Fragment != "" {
			add(u.Fragment)
			if q, err := url.ParseQuery(u.Fragment); err == nil {
				for _, vals := range q {
					for _, v := range vals {
						add(v)
					}
				}
			}
		}
	}
	return out
}

func sanitizeLogContent(raw, authSuffix string) string {
	if raw == "" {
		return ""
	}
	secrets := extractAuthSecrets(authSuffix)
	hasTrailingNL := strings.HasSuffix(raw, "\n")
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		redact := strings.Contains(lower, "http://") ||
			strings.Contains(lower, "https://") ||
			strings.Contains(lower, "token") ||
			sensitiveParamRe.MatchString(line)
		if !redact {
			for _, sec := range secrets {
				if strings.Contains(line, sec) {
					redact = true
					break
				}
			}
		}
		if !redact && i == len(lines)-1 && !hasTrailingNL && suspiciousTailRe.MatchString(line) {
			redact = true
		}
		if redact {
			lines[i] = "[REDACTED]"
		}
	}
	return strings.Join(lines, "\n")
}

// sanitizeAndDecoupleEntryLog reads the current capture log, redacts sensitive
// lines in memory, atomically replaces logPath with a brand-new 0600 inode via
// os.Rename so any detached child holding the old FD can never write plaintext
// tokens to logPath, and truncates + closes origFile when provided.
func sanitizeAndDecoupleEntryLog(logPath string, origFile *os.File, authSuffix string) {
	defer func() {
		if origFile != nil {
			_ = origFile.Truncate(0)
			_ = origFile.Close()
		}
	}()
	if logPath == "" {
		return
	}
	raw := string(readBoundedFile(logPath, maxCaptureLogBytes))
	clean := sanitizeLogContent(raw, authSuffix)
	dir := filepath.Dir(logPath)
	tmp, err := os.CreateTemp(dir, ".entry-log-*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return
	}
	if _, err := tmp.WriteString(clean); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	if err := os.Rename(tmpPath, logPath); err != nil {
		_ = os.Remove(tmpPath)
	}
}

// StopEntry stops a Valise-started entry (trying declared StopArgv first, then
// stopping the recorded transient systemd unit or process group), removes its
// state file, and returns the updated port listening state.
func StopEntry(ctx context.Context, e *porterenroll.Env, home string, p login.EntryStopParams) (*login.EntryStopResult, error) {
	rec, _, e, port, err := loadEnrolledEntryEnv(e, home, p.InstanceID, p.Entry)
	if err != nil {
		return nil, err
	}
	st, _ := readEntryState(home, p.InstanceID, p.Entry.ID)
	var details []string
	var logPath string

	if st != nil {
		logPath = st.LogPath
		if isEntryStateAlive(ctx, e, st) {
			if p.Entry.Start != nil && len(p.Entry.Start.StopArgv) > 0 {
				if exp, err := expandEntryArgv(home, rec, p.Entry.Start.StopArgv); err == nil {
					if out, err := e.Run(ctx, entryStopTimeout, home, exp...); err == nil && out.Code == 0 {
						details = append(details, "stop_argv ok")
					}
				}
			}
			if st.Unit != "" {
				_, _ = e.Run(ctx, entryStopTimeout, home, "systemctl", "--user", "stop", st.Unit)
				_, _ = e.Run(ctx, 5*time.Second, home, "systemctl", "--user", "reset-failed", st.Unit)
				details = append(details, "stopped unit "+st.Unit)
			} else if isManagedPIDAlive(st) {
				pgid := st.PGID
				if pgid <= 0 {
					pgid = st.PID
				}
				_ = syscall.Kill(-pgid, syscall.SIGTERM)
				deadline := time.Now().Add(3 * time.Second)
				for isManagedPIDAlive(st) && time.Now().Before(deadline) {
					time.Sleep(100 * time.Millisecond)
				}
				if isManagedPIDAlive(st) {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				}
				details = append(details, fmt.Sprintf("stopped pid %d", st.PID))
			}
		}
		_ = os.Remove(entryStatePath(home, p.InstanceID, p.Entry.ID))
	}
	if logPath == "" && p.Entry.Start != nil && p.Entry.Start.CaptureURL {
		logPath = entryLogPath(home, p.InstanceID, p.Entry.ID)
	}
	if logPath != "" {
		if _, err := os.Stat(logPath); err == nil {
			sanitizeAndDecoupleEntryLog(logPath, nil, "")
		}
	}

	listen, _ := ListenAddrs(port)
	var addrs []string
	if listen != nil {
		addrs = listen.Addrs
	}
	return &login.EntryStopResult{
		EntryID:     p.Entry.ID,
		Port:        port,
		Stopped:     len(addrs) == 0,
		Listening:   len(addrs) > 0,
		ListenAddrs: addrs,
		Detail:      strings.Join(details, "; "),
	}, nil
}
