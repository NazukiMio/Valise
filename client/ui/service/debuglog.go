package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"valise/client/config"
	"valise/client/ssh"
	"valise/core/rpc"
	"valise/core/valise"
)

const (
	// debugEnvVar overrides the debug log switch when set to "1" or "0".
	debugEnvVar = "VALISE_DEBUG"
	// defaultDebugLogMaxBytes is the 5 MiB rotation threshold for valise-debug.log.
	defaultDebugLogMaxBytes int64 = 5 * 1024 * 1024
	defaultRecentDebugLines       = 200
	maxDebugFieldRunes            = 256
)

type debugLogger struct {
	mu       sync.Mutex
	maxBytes int64 // 0 uses defaultDebugLogMaxBytes; overridden in tests
	closed   bool
}

// DebugEvent is one structured JSONL line recorded in valise-debug.log.
type DebugEvent struct {
	TS         string `json:"ts"`
	Kind       string `json:"kind"`
	Action     string `json:"action,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Method     string `json:"method,omitempty"`
	Host       string `json:"host,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Phase      string `json:"phase,omitempty"`
	SSHKind    string `json:"ssh_kind,omitempty"`
	Op         string `json:"op,omitempty"`
	Stage      string `json:"stage,omitempty"`
	Target     string `json:"target,omitempty"`
	Status     string `json:"status,omitempty"`
	PromptKind string `json:"prompt_kind,omitempty"`
	Template   string `json:"template,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	ErrCode    string `json:"err_code,omitempty"`
}

// DebugLogDir returns the directory holding valise-debug.log.
func (s *Service) DebugLogDir() string {
	if s == nil || s.Dirs.Data == "" {
		return ""
	}
	return filepath.Join(s.Dirs.Data, "logs")
}

// DebugLogPath returns the full path to valise-debug.log.
func (s *Service) DebugLogPath() string {
	dir := s.DebugLogDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "valise-debug.log")
}

// DebugLogEnvOverride returns "1" or "0" when VALISE_DEBUG explicitly overrides
// the configured/default debug log state, or "" otherwise.
func DebugLogEnvOverride() string {
	switch os.Getenv(debugEnvVar) {
	case "1":
		return "1"
	case "0":
		return "0"
	default:
		return ""
	}
}

// DebugLogEnabled reports whether client debug diagnostic logging is active.
// Precedence: VALISE_DEBUG ("1"/"0") > Config.DebugLog > Version containing
// "dev" or "test" (case-insensitive); release builds default to false.
func (s *Service) DebugLogEnabled() bool {
	if s == nil {
		return false
	}
	switch DebugLogEnvOverride() {
	case "1":
		return true
	case "0":
		return false
	}
	s.debug.mu.Lock()
	cfg := s.Config.DebugLog
	s.debug.mu.Unlock()
	if cfg != nil {
		return *cfg
	}
	v := strings.ToLower(strings.TrimSpace(s.Version))
	return strings.Contains(v, "dev") || strings.Contains(v, "test")
}

// SetDebugLog persists the debug_log setting to config.json and updates the
// in-memory configuration.
func (s *Service) SetDebugLog(on bool) error {
	if s == nil || s.Dirs.Data == "" {
		return valise.New("request_invalid", "缺少客户端数据目录，无法保存调试日志开关", "")
	}
	s.debug.mu.Lock()
	defer s.debug.mu.Unlock()
	if err := config.SetDebugLog(s.Dirs.Data, on); err != nil {
		return err
	}
	v := on
	s.Config.DebugLog = &v
	return nil
}

var openDirCmd = func(dir string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, dir).Run()
}

// OpenDebugLogDir ensures the debug log directory exists and opens it in the
// system file manager.
func (s *Service) OpenDebugLogDir() error {
	dir := s.DebugLogDir()
	if dir == "" {
		return valise.New("request_invalid", "缺少客户端数据目录，无法打开日志目录", "")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return openDirCmd(dir)
}

// RecentDebugLogs returns up to maxLines most recent lines across
// valise-debug.log.1 and valise-debug.log. Missing files yield an empty string.
func (s *Service) RecentDebugLogs(maxLines int) (string, error) {
	path := s.DebugLogPath()
	if path == "" {
		return "", nil
	}
	if maxLines <= 0 {
		maxLines = defaultRecentDebugLines
	}
	s.debug.mu.Lock()
	defer s.debug.mu.Unlock()
	var lines []string
	for _, p := range []string{path + ".1", path} {
		b, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n"), nil
}

// LogUIEvent records a sanitized UI route transition or button action.
func (s *Service) LogUIEvent(action, detail string) {
	s.writeDebugEvent(DebugEvent{
		Kind:   "ui",
		Action: sanitizeDebugField(action),
		Detail: sanitizeDebugField(detail),
	})
}

// LogStage records a stage transition for backup, restore, restore_test, or
// system fix flows.
func (s *Service) LogStage(op, stage, target, status, errCode string) {
	s.writeDebugEvent(DebugEvent{
		Kind:    "stage",
		Op:      sanitizeDebugField(op),
		Stage:   sanitizeDebugField(stage),
		Target:  sanitizeDebugField(target),
		Status:  sanitizeDebugField(status),
		ErrCode: sanitizeErrCodeText(errCode),
	})
}

func (s *Service) logRPC(method, host string, durationMS int64, err error) {
	d := durationMS
	s.writeDebugEvent(DebugEvent{
		Kind:       "rpc",
		Method:     sanitizeDebugField(method),
		Host:       sanitizeDebugField(host),
		DurationMS: &d,
		ErrCode:    safeDebugErrCode(err),
	})
}

func (s *Service) sshProcessHook() func(ssh.ProcessEvent) {
	if s == nil {
		return nil
	}
	return s.logSSH
}

func (s *Service) logSSH(ev ssh.ProcessEvent) {
	out := DebugEvent{
		Kind:    "ssh",
		Phase:   sanitizeDebugField(ev.Phase),
		SSHKind: sanitizeDebugField(ev.Kind),
		Host:    sanitizeDebugField(ev.Host),
		Mode:    sanitizeDebugField(ev.Mode),
		ErrCode: sanitizeErrCodeText(ev.ErrCode),
	}
	if ev.Phase == "end" {
		d := ev.DurationMS
		ex := ev.ExitCode
		out.DurationMS = &d
		out.ExitCode = &ex
	}
	s.writeDebugEvent(out)
}

func (s *Service) logAskpass(prompt string) {
	kind, tmpl := ClassifyAskpassPrompt(prompt)
	s.writeDebugEvent(DebugEvent{
		Kind:       "askpass",
		PromptKind: kind,
		Template:   tmpl,
	})
}

// ClassifyAskpassPrompt maps an OpenSSH or sudo askpass prompt to a fixed,
// redacted category and template string. It never copies raw paths, usernames,
// hosts, or secrets into the return values.
func ClassifyAskpassPrompt(prompt string) (kind, template string) {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	switch {
	case strings.Contains(lower, "passphrase"):
		return "key_passphrase", "Enter passphrase for key <redacted>"
	case strings.Contains(lower, "sudo"):
		return "sudo_password", "[sudo] password prompt"
	case strings.Contains(lower, "'s password") || strings.Contains(lower, "password"):
		return "server_password", "<user>@<host>'s password"
	default:
		return "generic", "ssh askpass prompt"
	}
}

func (s *Service) closeDebugLog() {
	if s == nil {
		return
	}
	s.debug.mu.Lock()
	s.debug.closed = true
	s.debug.mu.Unlock()
}

func (s *Service) writeDebugEvent(ev DebugEvent) {
	if s == nil || s.Dirs.Data == "" || !s.DebugLogEnabled() {
		return
	}
	ev.TS = time.Now().UTC().Format(time.RFC3339Nano)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		return
	}
	line := buf.Bytes()

	dir := s.DebugLogDir()
	path := s.DebugLogPath()
	s.debug.mu.Lock()
	defer s.debug.mu.Unlock()
	if s.debug.closed {
		return
	}
	limit := s.debug.maxBytes
	if limit <= 0 {
		limit = defaultDebugLogMaxBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if st, err := os.Stat(path); err == nil && st.Size() > limit {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(line)
	_ = f.Close()
}

// safeDebugErrCode extracts only a bounded error code from err, never
// including err.Error(), UserError.Reason, or UserError.Detail.
func safeDebugErrCode(err error) string {
	if err == nil {
		return ""
	}
	if ue, ok := valise.As(err); ok && ue.Code != "" {
		return sanitizeErrCodeText(ue.Code)
	}
	var re *rpc.Error
	if errors.As(err, &re) {
		return fmt.Sprintf("rpc_%d", re.Code)
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline_exceeded"
	}
	return "unknown_error"
}

var (
	pemBlockRe  = regexp.MustCompile(`(?i)-----BEGIN [A-Z0-9 ]+-----`)
	tokenRe     = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{6,}|ghp_[A-Za-z0-9]{6,}|xox[baprs]-[A-Za-z0-9-]{6,})\b`)
	bearerRe    = regexp.MustCompile(`(?i)\bbearer\s+\S+`)
	kvSecretRe  = regexp.MustCompile(`(?i)\b(password|passphrase|token|secret|api_key)\s*[:=]\s*\S+`)
	sshKeyRe    = regexp.MustCompile(`(?:[A-Za-z]:)?[^\s"']*(?:/\.ssh/|\\\.ssh\\|id_ed25519|id_rsa|id_ecdsa|id_dsa|secret_ed25519|keystore\.age)[^\s"']*`)
	errCodeSafe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

func sanitizeDebugField(v string) string {
	v = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if pemBlockRe.MatchString(v) {
		return "<redacted>"
	}
	v = kvSecretRe.ReplaceAllString(v, "$1=<redacted>")
	v = bearerRe.ReplaceAllString(v, "Bearer <redacted>")
	v = tokenRe.ReplaceAllString(v, "<redacted>")
	v = sshKeyRe.ReplaceAllString(v, "<redacted>")
	runes := []rune(v)
	if len(runes) > maxDebugFieldRunes {
		return string(runes[:maxDebugFieldRunes])
	}
	return v
}

func sanitizeErrCodeText(code string) string {
	code = sanitizeDebugField(code)
	if code == "" || code == "<redacted>" {
		return code
	}
	if !errCodeSafe.MatchString(code) {
		return "unknown_error"
	}
	return code
}
