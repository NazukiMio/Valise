package backuprun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	porterenroll "valise/porter/internal/enroll"
	planstore "valise/porter/internal/protection"
)

const launchTimeout = 30 * time.Second

// UnitName is the transient user unit of one run. systemd refuses a second
// unit of the same name while the first is loaded.
func UnitName(runID string) string { return "valise-task-" + runID + ".service" }

// LaunchArgs is the fixed systemd-run invocation. The unit takes no input
// besides the run id; --user units carry only NoNewPrivileges=yes.
func LaunchArgs(porter, runID string) []string {
	return []string{"systemd-run", "--user", "--collect", "--unit=valise-task-" + runID, "--property=NoNewPrivileges=yes", porter, "task-run", "--id", runID}
}

// SystemdLauncher starts task-run in a transient user unit through the
// login environment. Any failure, including a missing systemd-run or user
// manager, makes the caller execute the run attached.
func SystemdLauncher(env *porterenroll.Env, home, porter string) func(context.Context, string) error {
	return func(ctx context.Context, runID string) error {
		if env == nil || porter == "" {
			return errors.New("launcher unavailable")
		}
		out, err := env.Run(ctx, launchTimeout, home, LaunchArgs(porter, runID)...)
		if err != nil {
			return err
		}
		if out.Code != 0 {
			return errors.New("systemd-run failed")
		}
		return nil
	}
}

// FormalPorter returns the deployed porter path when this process runs from
// it, and "" otherwise. A build run from elsewhere is not handed to a unit
// that would outlive it; such runs execute attached.
func FormalPorter(home string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	formal := planstore.DeployedPorter(home)
	resolved, err := filepath.EvalSymlinks(formal)
	if err != nil || resolved != real {
		return ""
	}
	return formal
}
