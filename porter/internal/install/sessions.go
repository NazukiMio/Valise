package install

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	coreinstall "valise/core/install"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

// Terminal sessions (review F05): a program Valise starts in a terminal
// runs as porter's child and is recorded until it exits, so one left
// running after the connection dropped can be shown and, once the user
// confirms, ended. Only processes with a matching record are ever ended:
// the pid and the kernel's start time must both match.

// SessionsDir holds one record per running session.
func SessionsDir(home string) string {
	return filepath.Join(home, ".local", "state", "valise", "sessions")
}

type sessionRecord struct {
	coreinstall.Session
	Start   string `json:"start"`    // /proc/<pid>/stat field 22
	ConnPID int    `json:"conn_pid"` // porter's parent: the sshd session
}

// procStart is a process's start time in clock ticks since boot, "" when
// it is not running.
func procStart(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	// The command name (field 2) may hold spaces: count after its ')'.
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

// RunTracked runs argv as Exec does, but as porter's child, recorded while
// it runs. It returns the child's exit code.
func RunTracked(e *porterenroll.Env, argv []string) (int, error) {
	bin, out, err := resolve(e, argv)
	if err != nil {
		return 127, err
	}
	cmd := exec.Command(bin, out[1:]...)
	cmd.Env, cmd.Dir = e.Vars, e.Home
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// The terminal's signals reach the child (same process group); porter
	// catches them (ignoring would be inherited by the child) and stays to
	// remove the record when the child exits, even after a hangup.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		for range sigs {
		}
	}()
	if err := cmd.Start(); err != nil {
		return 127, err
	}
	rec := sessionRecord{Session: coreinstall.Session{PID: cmd.Process.Pid, Argv: out, StartedAt: time.Now().UTC()},
		Start: procStart(cmd.Process.Pid), ConnPID: os.Getppid()}
	path := filepath.Join(SessionsDir(e.Home), strconv.Itoa(rec.PID)+".json")
	if err := os.MkdirAll(SessionsDir(e.Home), 0o700); err == nil {
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(path, b, 0o600)
	}
	err = cmd.Wait()
	_ = os.Remove(path)
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	}
	return 1, err
}

// Sessions lists the recorded sessions still running; records of ended
// processes are removed.
func Sessions(home string) []coreinstall.Session {
	out := []coreinstall.Session{}
	ents, _ := os.ReadDir(SessionsDir(home))
	for _, ent := range ents {
		path := filepath.Join(SessionsDir(home), ent.Name())
		var r sessionRecord
		b, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(b, &r) != nil || r.Start == "" || procStart(r.PID) != r.Start {
			_ = os.Remove(path)
			continue
		}
		r.Detached = r.ConnPID > 0 && procStart(r.ConnPID) == ""
		out = append(out, r.Session)
	}
	return out
}

// EndSession ends a recorded session: SIGTERM, then SIGKILL after a few
// seconds. A pid without a matching record is refused.
func EndSession(home string, pid int) error {
	for _, s := range Sessions(home) {
		if s.PID != pid {
			continue
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		_ = p.Signal(syscall.SIGTERM)
		for range 50 {
			if procStart(pid) == "" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if procStart(pid) != "" {
			_ = p.Signal(syscall.SIGKILL)
		}
		_ = os.Remove(filepath.Join(SessionsDir(home), strconv.Itoa(pid)+".json"))
		return nil
	}
	return valise.New("session_unknown", "没有找到 Valise 启动的这个程序（可能已经结束）", "刷新后再看")
}
