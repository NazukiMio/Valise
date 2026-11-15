package revive

import (
	"errors"
	"os"
	"path/filepath"

	"valise/core/revive"
)

// Only this operation's fixed private directory is removable. Never accept a
// path from the request, candidate receipt or an existing journal root.
func (r *Runner) testDirectory(op string, create bool) (string, error) {
	if !revive.ValidOpID(op) {
		return "", testOnlyError()
	}
	p := r.Store.Home
	for _, part := range []string{".local", "state", "valise", "environment-tests", op} {
		p = filepath.Join(p, part)
		st, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) && create {
			if err = os.Mkdir(p, 0o700); err != nil {
				return "", err
			}
			st, err = os.Lstat(p)
		}
		if errors.Is(err, os.ErrNotExist) && !create {
			return "", nil
		}
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || part == op && st.Mode().Perm()&0o077 != 0 {
			return "", testFailure("environment_workspace")
		}
	}
	return p, nil
}

func (r *Runner) discardEnvironmentTest(j *revive.Journal) error {
	if j.Plan.IsolatedTest == nil {
		return nil
	}
	if !j.Plan.TestOnly || validateTestPlan(j.Plan) != nil || j.State != revive.StatePlanned {
		return testOnlyError()
	}
	if t := j.EnvironmentTest; t != nil && t.ProbeAttempted && (!t.Probe.Completed || !t.Probe.ProcessesStopped) {
		return testFailure("environment_stop_unverified")
	}
	p, err := r.testDirectory(j.Plan.OpID, false)
	if err != nil || p == "" {
		return err
	}
	return os.RemoveAll(p)
}
