package install

import (
	"context"
	"os"
	"os/exec"

	"valise/core/enroll"
	"valise/core/install"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

func nativeUnavailable() error {
	return valise.New("native_current_unverified", "无法核实这个 Agent 当前使用的程序，没有启动命令", "重新检查这个 Agent；若正在恢复，请先查看原恢复操作的结果")
}

// NativeInfo returns the accepted native CLI metadata for one enrolled instance.
func NativeInfo(home, id string) (*install.NativeInfo, error) {
	d, p, err := porterenroll.ReadInstallation(home, id)
	if err != nil {
		return nil, err
	}
	if d == nil || p == nil || d.Record(id) == nil {
		return nil, valise.New("instance_not_enrolled", "没有找到这个 Agent 已接受的接入资料", "重新检查这个 Agent 的接入状态")
	}
	r := d.Record(id)
	effective := p.ForAgent(r.Name)
	return &install.NativeInfo{
		InstanceID: id,
		ProfileID:  effective.ID,
		Argv:       effective.NativeCLI,
		Hints:      effective.NativeHints,
	}, nil
}

// NativeCommand resolves the accepted native CLI command for one enrolled instance.
func NativeCommand(ctx context.Context, home, id string, lock *os.File) (*exec.Cmd, []string, error) {
	if enroll.ValidInstanceID(id) != nil || lock == nil {
		return nil, nil, nativeUnavailable()
	}
	d, p, err := porterenroll.ReadInstallation(home, id)
	if err != nil || d == nil || p == nil || d.Record(id) == nil || d.Record(id).Superseded != nil {
		return nil, nil, nativeUnavailable()
	}
	r := d.Record(id)
	selected := p.ForAgent(r.Name)
	r.ApplyEntryPorts(&selected)
	if len(selected.NativeCLI) == 0 {
		return nil, nil, valise.New("native_cli_none", "这个 Agent 的接入资料没有声明原生界面", "使用它已支持的管理页或登录方式")
	}
	env := porterenroll.LoadEnv(ctx, home).ForInstance(&selected, r)
	bin, out, err := resolve(env, selected.NativeCLI)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(bin, out[1:]...)
	cmd.Env, cmd.Dir, cmd.ExtraFiles = env.Vars, env.Home, []*os.File{lock}
	return cmd, selected.NativeCLI, nil
}
