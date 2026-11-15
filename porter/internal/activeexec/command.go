// Package activeexec derives daily commands from accepted instance metadata.
// It is not a supplied argv, mount or execution RPC.
package activeexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/install"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/porter/internal/activeview"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/runtimeguard"
)

var ErrUnmanaged = errors.New("instance has no managed environment")

type Controller struct {
	Home string
	// Only synthetic lab tests inject a service-controller home.
	Runtime managedruntime.Controller
}

type invocation struct {
	record  *enroll.Record
	profile profile.Profile
	current *activeview.Binding
}

func unavailable() error {
	return valise.New("native_current_unverified", "无法核实这个 Agent 当前使用的程序，没有启动命令", "重新检查这个 Agent；若正在恢复，请先查看原恢复操作的结果")
}

func Info(home, id string) (*install.NativeInfo, error) {
	d, p, err := porterenroll.ReadInstallation(home, id)
	if err != nil {
		return nil, err
	}
	if d == nil || p == nil || d.Record(id) == nil {
		return nil, valise.New("instance_not_enrolled", "没有找到这个 Agent 已接受的接入资料", "重新检查这个 Agent 的接入状态")
	}
	r := d.Record(id)
	effective := p.ForAgent(r.Name)
	return &install.NativeInfo{InstanceID: id, ProfileID: effective.ID, Argv: effective.NativeCLI, Hints: effective.NativeHints}, nil
}

func (c Controller) bind(ctx context.Context, id string) (*invocation, error) {
	if enroll.ValidInstanceID(id) != nil {
		return nil, unavailable()
	}
	idNow, err := porteridentity.Read(c.Home, id)
	if err != nil {
		return nil, unavailable()
	}
	var current *activeview.Binding
	if idNow.IdentityState == identity.Bound && idNow.Agent != nil {
		current, err = (activeview.Reader{Home: c.Home, Control: c.Runtime}).Read(ctx, idNow.Agent.Ref)
		if err != nil {
			return nil, err
		}
	}
	if current != nil {
		return &invocation{record: current.Record, profile: current.Program.Profile, current: current}, nil
	}
	if err := runtimeguard.CheckInstance(ctx, c.Home, id); err != nil {
		return nil, err
	}
	d, p, err := porterenroll.ReadInstallation(c.Home, id)
	if err == nil && d == nil && p == nil {
		return nil, ErrUnmanaged // A separate legacy-compatible consumer may read it.
	}
	if err != nil || d == nil || p == nil || d.Record(id) == nil || d.Record(id).Superseded != nil {
		return nil, unavailable()
	}
	r := d.Record(id)
	selected := p.ForAgent(r.Name)
	r.ApplyEntryPorts(&selected)
	return &invocation{record: r, profile: selected}, nil
}

// Native takes no command arguments. lock is the caller's actual server
// mutation lock; a request cannot supply an execution grant in its place.
func (c Controller) Native(ctx context.Context, id string, lock *os.File) (*exec.Cmd, []string, error) {
	b, err := c.bind(ctx, id)
	if err != nil {
		if errors.Is(err, ErrUnmanaged) {
			return nil, nil, unavailable()
		}
		return nil, nil, err
	}
	if len(b.profile.NativeCLI) == 0 {
		return nil, nil, valise.New("native_cli_none", "这个 Agent 的接入资料没有声明原生界面", "使用它已支持的管理页或登录方式")
	}
	cmd, err := c.command(ctx, b, b.profile.NativeCLI, lock)
	return cmd, b.profile.NativeCLI, err
}

// Entry derives the token command from the accepted entry, rejecting a stale
// or altered client declaration. Nothing from child output is recorded here.
func (c Controller) Entry(ctx context.Context, id string, requested profile.Entry, lock *os.File) (*exec.Cmd, *profile.EntryAuth, error) {
	b, err := c.bind(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if b.current == nil {
		return nil, nil, ErrUnmanaged
	}
	p := b.profile
	b.record.ApplyEntryPorts(&p)
	for _, entry := range p.Entries {
		if entry.ID != requested.ID {
			continue
		}
		if environments.Digest(entry) != environments.Digest(requested) {
			return nil, nil, valise.New("entry_auth_changed", "管理页资料已经变化，没有运行旧的取令牌命令", "重新读取这个 Agent 后再打开管理页")
		}
		if entry.Auth == nil {
			break
		}
		argv := entry.Auth.URLArgv
		if len(argv) == 0 {
			argv = entry.Auth.TokenArgv
		}
		cmd, err := c.command(ctx, b, argv, lock)
		return cmd, entry.Auth, err
	}
	return nil, nil, valise.New("entry_auth_none", "这个 Agent 的接入资料没有声明取令牌的方法", "按管理页提示使用它的原生登录方式")
}

func (c Controller) command(ctx context.Context, b *invocation, argv []string, lock *os.File) (*exec.Cmd, error) {
	return c.commandMode(ctx, b, argv, lock, false)
}

func (c Controller) commandMode(ctx context.Context, b *invocation, argv []string, lock *os.File, readonly bool) (*exec.Cmd, error) {
	if lock == nil || len(argv) == 0 || ctx == nil {
		return nil, unavailable()
	}
	if readonly && b.current == nil {
		return nil, ErrUnmanaged
	}
	if b.current == nil {
		e := porterenroll.LoadEnv(ctx, c.Home).ForInstance(&b.profile, b.record)
		args, ok := e.ExpandArgv(argv, map[string]string{"STATE_DIR": b.record.Roots["state"]})
		if !ok || len(args) == 0 {
			return nil, unavailable()
		}
		program := args[0]
		if !filepath.IsAbs(program) {
			var err error
			program, err = e.LookPath(program)
			if err != nil {
				return nil, unavailable()
			}
		}
		cmd := exec.CommandContext(ctx, program, args[1:]...)
		cmd.Env, cmd.Dir, cmd.ExtraFiles = e.Vars, c.Home, []*os.File{lock}
		return cmd, nil
	}
	if runtime.GOOS != "linux" {
		return nil, isolated.ErrUnsupported
	}
	current := b.current
	spec, err := environments.Command(current.Program, argv)
	if err != nil {
		return nil, unavailable()
	}
	w, _, err := (environments.ProgramStore{Home: c.Home}).Open(ctx, current.Registry.Agent, current.Ref, environments.Digest(current.Program))
	if err != nil {
		return nil, unavailable()
	}
	roots, err := revive.CanonicalRoots(b.record.Roots)
	if err != nil {
		return nil, unavailable()
	}
	var data []isolated.DataBinding
	for _, root := range roots {
		if root.SwapRoot == "" {
			data = append(data, isolated.DataBinding{Host: root.Path, Guest: current.Program.Layout.Roots[root.ID]})
		}
	}
	var args []string
	if readonly {
		args, err = isolated.ReadonlyAttachedArguments(w, spec, data)
	} else {
		args, err = isolated.AttachedArguments(w, spec, data)
	}
	if err != nil {
		return nil, unavailable()
	}
	// The namespace init keeps this open file description until all private
	// children exit; it closes it before starting the harness itself.
	args = append([]string{"--sync-fd", "3"}, args...)
	cmd := exec.CommandContext(ctx, "/usr/bin/bwrap", args...)
	cmd.Env, cmd.Dir, cmd.ExtraFiles = []string{"HOME=/nonexistent", "PATH=/usr/bin:/bin", "LANG=C"}, "/", []*os.File{lock}
	return cmd, nil
}
