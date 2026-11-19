package gui

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"valise/core/kittools"
	"valise/ui/service"
)

func rcloneTool(present, ours bool) service.KitTool {
	t := service.KitTool{Tool: kittools.Tool{ID: "rclone", Name: "rclone", Tier: kittools.TierEnhanced}, Present: present, Ours: ours}
	if present {
		t.Path = "/home/me/.local/bin/rclone"
	}
	return t
}

// fakeKit is a kitOps whose state the tests move by hand.
type fakeKit struct {
	rclone    map[string]service.KitTool // by host
	installed []string
	removed   []string
	logins    []string
	failLogin map[string]bool
}

func (f *fakeKit) ops() *kitOps {
	return &kitOps{
		tools: func(_ context.Context, h string) ([]service.KitTool, error) {
			t, ok := f.rclone[h]
			if !ok {
				return nil, errors.New("fixture unreachable")
			}
			return []service.KitTool{t}, nil
		},
		plan: func(_ context.Context, h, id string) (*service.KitBinaryPlan, error) {
			return &service.KitBinaryPlan{Tool: f.rclone[h], Arch: "arm64", Version: "v1.2.3",
				URL: "https://example.test/rclone.zip", SHA256: "abc123", InstallPath: "~/.local/bin/rclone"}, nil
		},
		install: func(_ context.Context, h, id string) (*service.KitTool, error) {
			f.installed = append(f.installed, h)
			t := rcloneTool(true, true)
			f.rclone[h] = t
			return &t, nil
		},
		remove: func(_ context.Context, h, id string) error {
			f.removed = append(f.removed, h)
			f.rclone[h] = rcloneTool(false, false)
			return nil
		},
		loginServer: func(_ context.Context, h, name, id string, _ service.CloudForm) error {
			f.logins = append(f.logins, h)
			if f.failLogin[h] {
				return errors.New("fixture login failed")
			}
			return nil
		},
	}
}

func TestServerToolsInstallNeedsConfirmationThenRemove(t *testing.T) {
	a, _ := settingsWorld(t)
	tt := ui.NewTester(a.View, 1180, 2400)
	f := &fakeKit{rclone: map[string]service.KitTool{"home": rcloneTool(false, false)}}
	a.kitHook = f.ops()
	a.serverTab = 1
	a.settings["home"].read = false // opening reads the tools next to the vault
	a.router.Push("/servers/home")
	tt.Frame()
	hasAll(t, tt.Texts(), T("tools.title"), "rclone", T("tools.prepare"))
	click(t, tt, T("tools.prepare"))
	hasAll(t, tt.Texts(), T("tools.plan_version", "version", "v1.2.3", "arch", "arm64"), "https://example.test/rclone.zip",
		"SHA-256 abc123", T("tools.plan_path", "path", "~/.local/bin/rclone"), T("tools.confirm_external"), T("tools.install_go"), T("tools.self_install_shell"))
	_ = tt.Click(T("tools.install_go")) // off until the person confirms
	if len(f.installed) != 0 {
		t.Fatal("installed without the confirmation")
	}
	click(t, tt, T("tools.confirm_external"))
	click(t, tt, T("tools.install_go"))
	if len(f.installed) != 1 {
		t.Fatalf("installed %v", f.installed)
	}
	hasAll(t, tt.Texts(), T("tools.installed", "path", "/home/me/.local/bin/rclone"), T("tools.uninstall"))
	click(t, tt, T("tools.uninstall"))
	if len(f.removed) != 0 {
		t.Fatal("removed before the second click")
	}
	click(t, tt, T("tools.uninstall_go"))
	if len(f.removed) != 1 || !tt.HasText(T("tools.prepare")) {
		t.Fatalf("removed %v %q", f.removed, tt.Texts())
	}
	click(t, tt, T("tools.prepare"))
	click(t, tt, T("tools.self_install_shell"))
	if a.router.Path() != "/servers/home/shell" {
		t.Fatalf("went to %s", a.router.Path())
	}
}

func TestServerToolsSystemCopyHasNoUninstallAndUnavailableIsSaid(t *testing.T) {
	a, _ := settingsWorld(t)
	tt := ui.NewTester(a.View, 1180, 2400)
	f := &fakeKit{rclone: map[string]service.KitTool{"home": rcloneTool(true, false)}}
	a.kitHook = f.ops()
	a.serverTab = 1
	a.router.Push("/servers/home")
	a.readKitTools("home")
	tt.Frame()
	hasAll(t, tt.Texts(), T("tools.system_present", "path", "/home/me/.local/bin/rclone"))
	if tt.HasText(T("tools.uninstall")) {
		t.Fatal("a system copy offers uninstall")
	}
	gone := rcloneTool(false, false)
	gone.Why = "no build for this machine"
	f.rclone["home"] = gone
	a.readKitTools("home")
	tt.Frame()
	hasAll(t, tt.Texts(), T("tools.not_available", "why", "no build for this machine"))
	if tt.HasText(T("tools.prepare")) {
		t.Fatal("an install that cannot run is offered")
	}
}
