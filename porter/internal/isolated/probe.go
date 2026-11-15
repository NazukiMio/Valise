package isolated

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"valise/core/environment"
	"valise/core/profile"
)

const probeConfigPath = "/valise/control/probe.json"

// ProbeCheck comes from the validated profile's gateway entry and HTTP check.
// It has no headers, tokens, request body, redirects, proxy or model messages.
type ProbeCheck struct {
	Host     string                    `json:"host"`
	Port     int                       `json:"port"`
	Path     string                    `json:"path"`
	Statuses []int                     `json:"statuses"`
	Timeout  time.Duration             `json:"timeout"`
	Offline  *profile.OfflineReadiness `json:"offline,omitempty"`
}

type ProbeResult = environment.ProbeResult

type probeDescriptor struct {
	Spec     Spec       `json:"spec"`
	Check    ProbeCheck `json:"check"`
	Deadline time.Time  `json:"deadline"`
}

func (p ProbeCheck) valid() bool {
	if p.Offline != nil {
		return p.validOffline()
	}
	return (p.Host == "127.0.0.1" || p.Host == "::1") && p.Port > 0 && p.Port <= 65535 &&
		strings.HasPrefix(p.Path, "/") && !strings.ContainsAny(p.Path, "\r\n\x00?#") && p.Timeout > 0 && p.Timeout <= 2*time.Minute &&
		len(p.Statuses) > 0 && len(p.Statuses) <= 16 && !slices.ContainsFunc(p.Statuses, func(s int) bool { return s < 200 || s > 599 })
}

// CheckFor requires an explicit HTTP readiness declaration for the selected
// entry. A TCP listener or an absent health declaration is not gateway proof.
func CheckFor(p profile.Profile) (ProbeCheck, error) {
	if p.Environments == nil || p.Health == nil {
		return ProbeCheck{}, ErrWorkspace
	}
	var check ProbeCheck
	for _, e := range p.Entries {
		if e.ID == p.Environments.GatewayEntry && e.Kind == "web" {
			check = ProbeCheck{Host: e.Host, Port: e.Port, Path: e.Path, Timeout: 2 * time.Minute}
		}
	}
	for _, h := range p.Health.Checks {
		if h.Kind == "http" && h.Entry == p.Environments.GatewayEntry {
			check.Statuses = append(check.Statuses, h.ExpectStatus...)
		}
	}
	if !check.valid() {
		return ProbeCheck{}, ErrWorkspace
	}
	return check, nil
}

// GatewayProbe runs this executable's fixed internal probe entry in the same
// private namespace as the candidate. The executable/config are mounted read
// only outside HOME; no code is installed into the harness. The porter helper
// only starts the profile program and acts as an HTTP client, never a listener.
func GatewayProbe(ctx context.Context, w *Workspace, spec Spec, check ProbeCheck, track ...func(os.FileInfo) error) (ProbeResult, error) {
	var result ProbeResult
	if ctx == nil {
		return result, ErrDeadline
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < 2500*time.Millisecond {
		return result, ErrDeadline
	}
	if !check.validAt(spec.home()) || !privatePathAt(spec.home(), spec.WorkingDir) || len(spec.Argv) == 0 || !privatePathAt(spec.home(), spec.Argv[0]) {
		return result, ErrWorkspace
	}
	probeDeadline := time.Now().Add(check.Timeout)
	if last := deadline.Add(-2 * time.Second); probeDeadline.After(last) {
		probeDeadline = last
	}
	// Validate the complete workspace and mount policy before adding control
	// files. A second attempt in this workspace is deliberately refused.
	cmd, err := command(ctx, w, Spec{Home: spec.Home, Argv: []string{"/valise/porter", "environment-probe"}, WorkingDir: spec.WorkingDir, Path: spec.Path})
	if err != nil {
		return result, err
	}
	config, err := json.Marshal(probeDescriptor{spec, check, probeDeadline})
	if err != nil || len(config) > 64<<10 {
		return result, ErrWorkspace
	}
	f, err := os.OpenFile(filepath.Join(w.dir, "control", "probe.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return result, ErrWorkspace
	}
	_, err = f.Write(config)
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		return result, ErrWorkspace
	}
	exe, err := os.Executable()
	if err != nil {
		return result, ErrUnsupported
	}
	i := slices.Index(cmd.Args, "--remount-ro")
	if i < 0 {
		return result, ErrWorkspace
	}
	mounts := []string{"--ro-bind", exe, "/valise/porter", "--ro-bind", filepath.Join(w.dir, "control"), "/valise/control"}
	cmd.Args = append(append(append([]string{}, cmd.Args[:i]...), mounts...), cmd.Args[i:]...)
	lease, err := probeLifetime(w)
	if err != nil {
		return result, ErrWorkspace
	}
	defer lease.Close()
	if len(track) > 1 {
		return result, ErrWorkspace
	}
	if len(track) == 1 && track[0] != nil {
		st, err := lease.Stat()
		if err != nil {
			return result, err
		}
		if err := track[0](st); err != nil {
			return result, err
		}
	}

	// fd 3 stays in bwrap's private namespace init, and is closed before the
	// helper/harness exec. It is not an inherited credential or command channel.
	cmd.ExtraFiles = []*os.File{lease}
	cmd.Args = append([]string{cmd.Args[0], "--sync-fd", "3"}, cmd.Args[1:]...)
	var output limitedResult
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, ErrTask
	}
	d := json.NewDecoder(bytes.NewReader(output.Bytes()))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || !result.Completed {
		return ProbeResult{}, ErrTask
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ProbeResult{}, ErrTask
	}
	return result, nil
}

type limitedResult struct{ bytes.Buffer }

func (w *limitedResult) Write(p []byte) (int, error) {
	if w.Len()+len(p) > 4096 {
		return 0, errors.New("probe result exceeded limit")
	}
	return w.Buffer.Write(p)
}
