package managedruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"valise/core/profile"
)

// NativeRef is frozen evidence of an accepted profile's existing user unit.
// It is not a command request or a grant; the preparation controller binds it
// to the accepted Agent, immutable original materials and human plan.
type NativeRef struct {
	Unit             string `json:"unit"`
	Definition       string `json:"definition"`
	Executable       string `json:"executable"`
	ExecutableSHA256 string `json:"executable_sha256"`
	MainPID          int    `json:"main_pid"`
	KernelStart      string `json:"kernel_start"`
	ControlGroup     string `json:"control_group"`
	EnableOnBoot     bool   `json:"enable_on_boot"`
}

var nativeUnitName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._:-]*\.service$`)

func (r NativeRef) Validate(p profile.Profile) error {
	if p.Service == nil || !slices.Contains(p.Service.Candidates, profile.ServiceUnit{Scope: "user", Unit: r.Unit}) ||
		!nativeUnitName.MatchString(r.Unit) || strings.HasPrefix(r.Unit, "valise-") || len(r.Unit) > 240 ||
		!filepath.IsAbs(r.Executable) || filepath.Clean(r.Executable) != r.Executable || strings.ContainsAny(r.Executable, "\x00\r\n") ||
		r.MainPID <= 1 || r.KernelStart == "" || r.ControlGroup == "" {
		return ErrDefinition
	}
	if start, err := strconv.ParseUint(r.KernelStart, 10, 64); err != nil || start == 0 {
		return ErrDefinition
	}
	if p.Environments != nil && p.Environments.RuntimeExecutable != nil {
		exe := p.Environments.RuntimeExecutable
		if exe.Service != (profile.ServiceUnit{Scope: "user", Unit: r.Unit}) || filepath.Base(r.Executable) != exe.Name {
			return ErrDefinition
		}
	}
	for _, hash := range []string{r.Definition, r.ExecutableSHA256} {
		b, err := hex.DecodeString(hash)
		if err != nil || len(b) != 32 || strings.ToLower(hash) != hash {
			return ErrDefinition
		}
	}
	if _, err := groupPath(r.ControlGroup, r.Unit); err != nil {
		return err
	}
	return nil
}

func nativeHash(path string, limit int64) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", ErrDefinition
	}
	defer f.Close()
	st, err := f.Stat()
	owner, ok := stSys(st)
	if err != nil || !ok || !st.Mode().IsRegular() || owner.Nlink != 1 || owner.Uid != uint32(os.Getuid()) && owner.Uid != 0 || st.Mode().Perm()&0022 != 0 || st.Size() < 0 || st.Size() > limit {
		return "", ErrDefinition
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, limit+1))
	after, statErr := os.Lstat(path)
	if err != nil || statErr != nil || n != st.Size() || !os.SameFile(st, after) || !st.ModTime().Equal(after.ModTime()) || st.Mode() != after.Mode() || st.Size() != after.Size() {
		return "", ErrDefinition
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c Controller) nativeFile(unit string) (string, error) {
	if !nativeUnitName.MatchString(unit) || strings.HasPrefix(unit, "valise-") || len(unit) > 240 || !filepath.IsAbs(c.Home) || filepath.Clean(c.Home) != c.Home {
		return "", ErrDefinition
	}
	current := c.Home
	for _, part := range []string{"", ".config", "systemd", "user"} {
		if part != "" {
			current = filepath.Join(current, part)
		}
		st, err := os.Lstat(current)
		owner, ok := stSys(st)
		if err != nil || !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 {
			return "", ErrDefinition
		}
	}
	return filepath.Join(current, unit), nil
}

func (c Controller) nativeProperties(ctx context.Context, unit string) (map[string]string, error) {
	b, code, err := c.call(ctx, "show", "--property="+showFields, "--", unit)
	if err != nil || code != 0 {
		return nil, ErrState
	}
	return parseProperties(b)
}

// InspectNative is read-only and accepts only an explicit unit declared by
// the trusted profile, never a user-supplied arbitrary service name.
func (c Controller) InspectNative(ctx context.Context, p profile.Profile, unit profile.ServiceUnit) (NativeRef, Observation, error) {
	var ref NativeRef
	if p.Service == nil || unit.Scope != "user" || !slices.Contains(p.Service.Candidates, unit) {
		return ref, Observation{}, ErrDefinition
	}
	path, err := c.nativeFile(unit.Unit)
	if err != nil {
		return ref, Observation{}, err
	}
	hash, err := nativeHash(path, 1<<20)
	if err != nil {
		return ref, Observation{}, err
	}
	props, err := c.nativeProperties(ctx, unit.Unit)
	if err != nil {
		return ref, Observation{}, err
	}
	ob, err := classifyProperties(props, Definition{name: unit.Unit, digest: hash}, path)
	if err != nil || ob.PendingJob || ob.ActiveState != "active" || props["SubState"] != "running" || ob.MainPID <= 1 || ob.UnitFileState != "enabled" && ob.UnitFileState != "disabled" {
		return ref, ob, ErrState
	}
	exe, start, err := nativeProcess(ob.MainPID, ob.ControlGroup, unit.Unit)
	if err != nil {
		return ref, ob, err
	}
	exeHash, err := nativeHash(exe, 1<<30)
	if err != nil {
		return ref, ob, err
	}
	ref = NativeRef{unit.Unit, hash, exe, exeHash, ob.MainPID, start, ob.ControlGroup, ob.UnitFileState == "enabled"}
	if err := ref.Validate(p); err != nil {
		return NativeRef{}, ob, err
	}
	ob, err = c.ReadNative(ctx, ref)
	if err != nil || !sameNativeProcess(ref, ob) {
		return NativeRef{}, ob, ErrState
	}
	return ref, ob, nil
}

func sameNativeProcess(ref NativeRef, ob Observation) bool {
	return ob.Running && !ob.PendingJob && ob.MainPID == ref.MainPID && ob.KernelStart == ref.KernelStart && ob.ControlGroup == ref.ControlGroup
}

func (c Controller) ReadNative(ctx context.Context, ref NativeRef) (Observation, error) {
	var zero Observation
	path, err := c.nativeFile(ref.Unit)
	if err != nil {
		return zero, err
	}
	hash, err := nativeHash(path, 1<<20)
	if err != nil || hash != ref.Definition {
		return zero, ErrDefinition
	}
	props, err := c.nativeProperties(ctx, ref.Unit)
	if err != nil {
		return zero, err
	}
	ob, err := classifyProperties(props, Definition{name: ref.Unit, digest: ref.Definition}, path)
	if err != nil {
		return zero, err
	}
	if ob.MainPID > 0 {
		exeHash, err := nativeHash(ref.Executable, 1<<30)
		if err != nil || exeHash != ref.ExecutableSHA256 {
			return zero, ErrDefinition
		}
		exe, start, err := nativeProcess(ob.MainPID, ob.ControlGroup, ref.Unit)
		if err != nil || exe != ref.Executable {
			return zero, ErrState
		}
		ob.KernelStart, ob.Running = start, ob.ActiveState == "active" && props["SubState"] == "running"
	} else if ob.ActiveState == "inactive" || ob.ActiveState == "failed" {
		empty, err := emptyGroup(ref.ControlGroup, ref.Unit)
		if err != nil {
			return zero, err
		}
		if ob.ControlGroup != "" && ob.ControlGroup != ref.ControlGroup {
			other, e := emptyGroup(ob.ControlGroup, ref.Unit)
			if e != nil {
				return zero, e
			}
			empty = empty && other
		}
		ob.Stopped = empty && !ob.PendingJob
	}
	final, err := nativeHash(path, 1<<20)
	if err != nil || final != ref.Definition {
		return zero, ErrDefinition
	}
	return ob, nil
}

func (c Controller) DisableNativeStartup(ctx context.Context, ref NativeRef) (ActionResult, error) {
	return c.nativeStartup(ctx, ref, false, profile.Profile{})
}

// RestoreNativeStartup never starts an original service. A preparation-only
// cancellation preserves the exact original process and its boot policy.
func (c Controller) RestoreNativeStartup(ctx context.Context, ref NativeRef, p profile.Profile) (ActionResult, error) {
	return c.nativeStartup(ctx, ref, ref.EnableOnBoot, p)
}

func (c Controller) nativeStartup(ctx context.Context, ref NativeRef, enable bool, p profile.Profile) (ActionResult, error) {
	before, err := c.ReadNative(ctx, ref)
	result := ActionResult{Observed: before}
	if err != nil || !sameNativeProcess(ref, before) {
		return result, ErrState
	}
	verb, want := "disable", "disabled"
	if enable {
		proof, err := c.CheckNativeHTTP(ctx, ref, p)
		if err != nil || !proof.GatewayPassed {
			return result, ErrState
		}
		verb, want = "enable", "enabled"
	}
	if before.UnitFileState == want {
		return result, nil
	}
	_, code, callErr := c.call(ctx, verb, "--", ref.Unit)
	result.Dispatched, result.CommandExited, result.ExitCode = true, callErr == nil, code
	result.Observed, err = c.ReadNative(ctx, ref)
	if callErr != nil || code != 0 {
		return result, ErrAction
	}
	if err != nil || !sameNativeProcess(ref, result.Observed) || result.Observed.UnitFileState != want {
		return result, ErrState
	}
	return result, nil
}

func (c Controller) StopNative(ctx context.Context, ref NativeRef) (ActionResult, error) {
	before, err := c.ReadNative(ctx, ref)
	result := ActionResult{Observed: before}
	if err != nil {
		return result, err
	}
	if before.Stopped {
		return result, nil
	}
	if !sameNativeProcess(ref, before) || before.UnitFileState != "disabled" {
		return result, ErrState
	}
	_, code, callErr := c.call(ctx, "stop", "--", ref.Unit)
	result.Dispatched, result.CommandExited, result.ExitCode = true, callErr == nil, code
	result.Observed, err = c.ReadNative(ctx, ref)
	if callErr != nil || code != 0 {
		return result, ErrAction
	}
	if err != nil || !result.Observed.Stopped {
		return result, ErrState
	}
	return result, nil
}
