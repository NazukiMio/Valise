package managedruntime

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Observation never equates process liveness with a working gateway. A query
// does not create files, reload units or execute an unfinished activation.
type Observation struct {
	Unit          string `json:"unit"`
	Definition    string `json:"definition"`
	PendingJob    bool   `json:"pending_job"`
	UnitFileState string `json:"unit_file_state"`
	ActiveState   string `json:"active_state"`
	KernelStart   string `json:"kernel_start,omitempty"`
	MainPID       int    `json:"main_pid"`
	ControlGroup  string `json:"control_group,omitempty"`
	Running       bool   `json:"running"`
	Stopped       bool   `json:"stopped"`
}

type ActionResult struct {
	Dispatched    bool        `json:"dispatched"`
	CommandExited bool        `json:"command_exited"`
	ExitCode      int         `json:"exit_code"`
	Observed      Observation `json:"observed"`
}

type Controller struct{ Home string }

const showFields = "Id,LoadState,ActiveState,SubState,MainPID,FragmentPath,DropInPaths,NeedDaemonReload,ControlGroup,UnitFileState,Job"

func (c Controller) call(ctx context.Context, args ...string) ([]byte, int, error) {
	if ctx == nil || runtime.GOOS != "linux" {
		return nil, -1, ErrState
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if st, err := os.Stat("/usr/bin/systemctl"); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0022 != 0 {
		return nil, -1, ErrState
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", append([]string{"--user"}, args...)...)
	uid := strconv.Itoa(os.Getuid())
	cmd.Env = []string{"HOME=" + c.Home, "PATH=/usr/bin:/bin", "LANG=C", "XDG_RUNTIME_DIR=/run/user/" + uid, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/" + uid + "/bus"}
	cmd.Dir = "/"
	var output boundedOutput
	cmd.Stdout = &output
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, -1, ctx.Err()
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return output.Bytes(), exit.ExitCode(), nil
		}
		return nil, -1, ErrAction
	}
	return output.Bytes(), 0, nil
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 32<<10 {
		return 0, ErrState
	}
	return b.Buffer.Write(p)
}

func (c Controller) properties(ctx context.Context, d Definition) (map[string]string, error) {
	b, code, err := c.call(ctx, "show", "--property="+showFields, "--", d.name)
	if err != nil {
		return nil, err
	}
	p, err := parseProperties(b)
	if err != nil {
		return nil, err
	}
	if p["LoadState"] == "not-found" && (code == 0 || code == 4) {
		return p, nil
	}
	if code != 0 {
		return nil, ErrState
	}
	return p, nil
}

func parseProperties(b []byte) (map[string]string, error) {
	if len(b) > 32<<10 {
		return nil, ErrState
	}
	allowed := map[string]bool{}
	for _, key := range strings.Split(showFields, ",") {
		allowed[key] = true
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !allowed[key] {
			return nil, ErrState
		}
		if _, duplicate := out[key]; duplicate {
			return nil, ErrState
		}
		if strings.ContainsAny(value, "\r\x00") {
			return nil, ErrState
		}
		out[key] = value
	}
	return out, nil
}

func (c Controller) Read(ctx context.Context, d Definition) (Observation, error) {
	var zero Observation
	if err := c.checkFile(d, d.digest); err != nil {
		return zero, err
	}
	p, err := c.properties(ctx, d)
	if err != nil {
		return zero, err
	}
	ob, err := classifyProperties(p, d, c.path(d))
	if err != nil {
		return zero, err
	}
	if ob.MainPID > 0 {
		if !ownedMainProcess(ob.MainPID, ob.ControlGroup, d.name) {
			return zero, ErrState
		}
		ob.KernelStart, err = processStart(ob.MainPID)
		if err != nil {
			return zero, ErrState
		}
		ob.Running = ob.ActiveState == "active" && p["SubState"] == "running"
	} else if ob.ActiveState == "inactive" || ob.ActiveState == "failed" {
		empty, err := emptyGroup(ob.ControlGroup, d.name)
		if err != nil {
			return zero, err
		}
		ob.Stopped = empty && !ob.PendingJob
	}
	if err := c.checkFile(d, d.digest); err != nil {
		return zero, err
	}
	return ob, nil
}

func classifyProperties(p map[string]string, d Definition, fragment string) (Observation, error) {
	if p["Id"] != d.name || p["LoadState"] != "loaded" || p["FragmentPath"] != fragment || p["DropInPaths"] != "" || p["NeedDaemonReload"] != "no" {
		return Observation{}, ErrState
	}
	for _, key := range strings.Split(showFields, ",") {
		if _, ok := p[key]; !ok {
			return Observation{}, ErrState
		}
	}
	pid, err := strconv.Atoi(p["MainPID"])
	if err != nil || pid < 0 {
		return Observation{}, ErrState
	}
	switch p["ActiveState"] {
	case "active", "inactive", "failed", "activating", "deactivating":
	default:
		return Observation{}, ErrState
	}
	return Observation{Unit: d.name, Definition: d.digest, ActiveState: p["ActiveState"], UnitFileState: p["UnitFileState"], PendingJob: p["Job"] != "" && p["Job"] != "0", MainPID: pid, ControlGroup: p["ControlGroup"]}, nil
}

// Start and Stop return the command outcome and independently read process
// facts. A zero exit code alone never proves running or stopped.
func (c Controller) Start(ctx context.Context, d Definition) (ActionResult, error) {
	return c.action(ctx, d, false)
}
func (c Controller) Stop(ctx context.Context, d Definition) (ActionResult, error) {
	return c.action(ctx, d, true)
}

func (c Controller) action(ctx context.Context, d Definition, stop bool) (ActionResult, error) {
	unlock, err := c.lock(d)
	if err != nil {
		return ActionResult{}, err
	}
	defer unlock()
	before, err := c.Read(ctx, d)
	if err != nil && stop {
		// Unknown process evidence must not authorize starting another slot. It
		// can still authorize stopping this exact, loaded, privately owned unit.
		if fileErr := c.checkFile(d, d.digest); fileErr != nil {
			return ActionResult{}, fileErr
		}
		properties, readErr := c.properties(ctx, d)
		if readErr != nil {
			return ActionResult{}, readErr
		}
		before, err = classifyProperties(properties, d, c.path(d))
		if err == nil {
			_, err = groupPath(before.ControlGroup, d.name)
		}
	}
	if err != nil {
		return ActionResult{}, err
	}
	result := ActionResult{Observed: before}
	if !stop && before.PendingJob {
		return result, ErrState
	}
	if stop && before.Stopped || !stop && before.Running {
		return result, nil
	}
	if !stop && !before.Stopped {
		return result, ErrState
	}
	verb := "start"
	if stop {
		verb = "stop"
	}
	_, code, actionErr := c.call(ctx, verb, "--", d.name)
	result.Dispatched, result.CommandExited, result.ExitCode = true, actionErr == nil, code
	result.Observed, err = c.Read(ctx, d)
	if stop && err == nil && before.ControlGroup != "" {
		empty, groupErr := emptyGroup(before.ControlGroup, d.name)
		if groupErr != nil || !empty {
			result.Observed.Stopped = false
			err = ErrState
		}
	}
	if actionErr != nil || code != 0 {
		return result, ErrAction
	}
	if err != nil || stop && !result.Observed.Stopped || !stop && !result.Observed.Running {
		return result, ErrState
	}
	return result, nil
}

func (c Controller) path(d Definition) string {
	return filepath.Join(c.Home, ".config", "systemd", "user", d.name)
}
func validDefinition(d Definition) bool {
	if !strings.HasPrefix(d.name, "valise-agent-") || !strings.HasSuffix(d.name, ".service") || filepath.Base(d.name) != d.name || d.content == "" || d.digest != digest(d.content) {
		return false
	}
	key := strings.TrimSuffix(strings.TrimPrefix(d.name, "valise-agent-"), ".service")
	raw, err := hex.DecodeString(key)
	return err == nil && len(raw) == 32 && strings.ToLower(key) == key
}

func groupPath(group, unit string) (string, error) {
	uid := os.Getuid()
	prefix := fmt.Sprintf("/user.slice/user-%d.slice/user@%d.service/", uid, uid)
	if group == "" {
		return "", nil
	}
	if !strings.HasPrefix(group, prefix) || !strings.HasSuffix(group, "/"+unit) || filepath.Clean(group) != group || strings.ContainsAny(group, "\x00\r\n\\") {
		return "", ErrState
	}
	return filepath.Join("/sys/fs/cgroup", group), nil
}
