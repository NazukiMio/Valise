package seal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"valise/core/valise"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Automatic cloud backup (docs/ARCHITECTURE.md section 7): after every
// successful seal, targets with auto turned on and a last successful sync
// older than auto.every are synced by a detached `valise-porter autosync`
// child that exits when done. Nothing stays running.

// MinAutoEvery is the shortest allowed interval.
const MinAutoEvery = time.Hour

// ParseEvery accepts Go durations and whole days ("1d", "7d").
func ParseEvery(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("无效的间隔 %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("无效的间隔 %q", s)
	}
	return d, nil
}

func validateAuto(a *vaultrpc.AutoSync) error {
	if a == nil {
		return nil
	}
	d, err := ParseEvery(a.Every)
	if err != nil || d < MinAutoEvery {
		return valise.New("auto_every_invalid", "自动同步间隔无效："+a.Every, "至少 1 小时，例如 6h、1d")
	}
	return nil
}

// dueTargets lists auto targets whose last successful sync is older than
// their interval (or that never synced).
func dueTargets(p Paths) []vaultrpc.Target {
	ts, err := readTargets(p)
	if err != nil {
		return nil
	}
	var due []vaultrpc.Target
	for _, t := range ts {
		if t.Auto == nil {
			continue
		}
		every, err := ParseEvery(t.Auto.Every)
		if err != nil {
			continue
		}
		var st vaultrpc.TargetState
		if b, err := os.ReadFile(p.SyncState(t.Name)); err == nil {
			json.Unmarshal(b, &st)
		}
		if st.LastOK == nil || now().Sub(*st.LastOK) >= every {
			due = append(due, t)
		}
	}
	return due
}

// Spawn starts the detached autosync child; tests replace it.
var Spawn = func(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	cmd := exec.Command(exe, "autosync")
	cmd.Dir = home
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// startAutoSync is called after a successful seal; it returns the targets
// the child will sync.
func startAutoSync(p Paths) []string {
	due := dueTargets(p)
	if len(due) == 0 {
		return nil
	}
	if err := Spawn(p.Home); err != nil {
		p.AppendAudit(AuditEntry{Time: time.Now().UTC(), Op: "autosync", Initiator: "porter", Result: "failed", Reason: err.Error()})
		return nil
	}
	names := make([]string, len(due))
	for i, t := range due {
		names[i] = t.Name
	}
	return names
}

// lockWait is how long the child waits for the seal that started it to
// release the vault lock.
const lockWait = 2 * time.Minute

// AutoSync is the child's work: wait for the lock, then sync every due
// target. If another seal or sync keeps the lock, it gives up; the next
// seal tries again.
func AutoSync(ctx context.Context, e *porterenroll.Env, p Paths) error {
	deadline := time.Now().Add(lockWait)
	var unlock func()
	for {
		var err error
		if unlock, err = p.LockFor("autosync"); err == nil {
			break
		}
		if ue, ok := valise.As(err); !ok || ue.Code != "vault_busy" || time.Now().After(deadline) {
			p.AppendAudit(AuditEntry{Time: time.Now().UTC(), Op: "autosync", Initiator: "porter", Result: "skipped", Reason: "vault busy"})
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	defer unlock()
	st, err := Status(p)
	if err != nil {
		return err
	}
	var first error
	for _, t := range dueTargets(p) {
		if _, err := syncLocked(ctx, e, p, &t, st, vaultrpc.InitiatorAuto); err != nil && first == nil {
			first = err
		}
	}
	return first
}
