package gui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// settingsWorld has home (Valise passes) and nas (own SSH) with their
// reads already done, so nothing goes to a server.
func settingsWorld(t *testing.T) (*App, *ui.Tester) {
	a, tt := world(t)
	a.svc.Version = "0.9.2"
	a.servers = load[[]serverRow]{started: true, value: []serverRow{
		{Name: "home", Kind: "Valise 接入", Valise: true}, {Name: "nas", Kind: "用你自己的 SSH 设置"}}}
	for _, n := range []string{"home", "nas"} {
		a.passes[n] = &passState{doc: &service.DoctorReport{PorterFound: true, PorterVersion: "0.9.0", Platform: porter.Platform{OS: "linux", Arch: "arm64"}}}
		a.settings[n] = &settingsState{read: true, vault: &vaultrpc.StatusResult{ServerName: n}}
	}
	return a, tt
}

// The settings tab says how each thing is now; a row that needs doing has
// a button tagged with what it asks for; removing is its own block.
func TestServerSettingsTab(t *testing.T) {
	a, tt := settingsWorld(t)
	a.serverTab = 1 // the nav also has a “设置”
	a.router.Push("/servers/home")
	tt.Frame()
	hasAll(t, tt.Texts(), "服务器名", "也是备份位置里的文件夹名", "改名…", "Valise 口令", "Valise 通行证（用户通行证）",
		"0.9.0 · 比这台电脑上的 Valise（0.9.2）旧", "更新到 0.9.2…", "管理登录", "这台电脑的用户通行证",
		"这台电脑上没有（换了电脑或导入了配置）", "重新装…", "服务器密码", "还没登记", "登记", "删除服务器…")
	a.passes["home"].doc.PorterVersion = "0.9.2"
	a.settings["home"].vault.ServerName = ""
	tt.Frame()
	hasAll(t, tt.Texts(), "0.9.2 · 与这台电脑一致", "还没有名字", "起名…")
	if tt.HasText("更新到 0.9.2…") {
		t.Error("update offered for the same version")
	}
	click(t, tt, "起名…")
	hasAll(t, tt.Texts(), "起好这个名字")

	a.router.Push("/servers/nas")
	tt.Frame()
	hasAll(t, tt.Texts(), "用你自己的 SSH 设置（~/.ssh/config 里的 nas）", "改用 Valise 通行证…", "用你的 SSH 登录一次")
	a.passes["nas"].doc.SSH = ssh.HostConfig{ProxyJump: "bastion"}
	tt.Frame()
	hasAll(t, tt.Texts(), "经跳板机（ProxyJump）连接")
	if tt.HasText("改用 Valise 通行证…") {
		t.Error("migrating offered behind a jump host")
	}
	click(t, tt, "更新到 0.9.2…")
	if a.router.Path() != "/servers/nas/porter" {
		t.Fatalf("went to %s", a.router.Path())
	}
	hasAll(t, tt.Texts(), "更新 nas 上的搬运工", "服务器上", "0.9.0", "linux/arm64", "v0.9.2", "SHA-256", "不常驻")
}

// Switching to the Valise passes says what it will do first.
func TestMigratePage(t *testing.T) {
	a, tt := settingsWorld(t)
	a.router.Push("/servers/nas/migrate")
	tt.Frame()
	hasAll(t, tt.Texts(), "让 nas 改用 Valise 通行证", "用你自己的 SSH 设置登录 nas 一次", "拿不到 shell", "同时办一张管理员通行证", "Valise 不改 ~/.ssh/config", "开始")
}

// Renaming lists every place it changes before it changes anything, and
// says each place's result after.
func TestRenamePage(t *testing.T) {
	a, tt := settingsWorld(t)
	ss := a.settings["home"]
	ss.newName = "home-lab"
	ss.plan = &service.RenamePlan{Host: "home", Old: "home", New: "home-lab", Places: []service.RenamePlace{
		{Kind: "repo", Name: "usb", From: "/v/home", To: "/v/home-lab"}, {Kind: "station", Name: "gdrive", From: "home", To: "home-lab"},
		{Kind: "repo", Name: "nas", Missing: true}}}
	a.router.Push("/servers/home/rename")
	tt.Frame()
	hasAll(t, tt.Texts(), "给 home 改名", "会改动这些地方", "服务器上的备份库", "home → home-lab", "本机文件夹 usb", "/v/home → /v/home-lab",
		"远程备份位置 gdrive", "还没有复制过", "改名")
	ss.renamed = true
	ss.plan.Places[0].Done, ss.plan.Places[1].Pending, ss.plan.Places[1].Err = true, true, "云盘没有回应"
	tt.Frame()
	hasAll(t, tt.Texts(), "改好以后", "已改名", "待改名", "云盘没有回应")
}

// Removing: two layers; cleaning lists only what is there, skills
// unchecked and not advised; the button says what it will do.
func TestLeaveDialog(t *testing.T) {
	a, tt := settingsWorld(t)
	a.serverTab = 1
	left := &service.ServerLeftovers{Porter: true, Passes: []string{"daily", "admin"}, Timers: []string{"xiaoman"},
		Clouds: []string{"gdrive"}, Skills: []service.LeftSkills{{Instance: "xiaoman", Label: "小满", Skills: []string{"a", "b"}}}}
	ls := &leaveState{open: true, mode: "here", left: left, items: map[string]bool{service.LeaveTimers: true, service.LeaveClouds: true, service.LeavePorter: true, service.LeavePasses: true}}
	a.settings["home"].leave = ls
	a.router.Push("/servers/home")
	tt.Frame()
	hasAll(t, tt.Texts(), "删除 home", "只从这台电脑移除", "移除并清理 Valise 留在服务器上的文件", "备份库会保留", "从这台电脑移除")
	ls.mode = "clean"
	tt.Frame()
	hasAll(t, tt.Texts(), "定时备份任务", "停掉以后不再自动备份", "云盘授权", "gdrive", "搬运工", "用户通行证、管理员通行证",
		"Agent 的保护技能", "不建议删", "小满：2 个保护技能", "删除并清理")
	if tt.HasText("工具箱装的工具") {
		t.Error("an item with nothing to clean is listed")
	}

	// A failure stops; each item says what happened.
	ls.steps = []service.LeaveStep{{Item: service.LeaveTimers, Detail: "xiaoman"}}
	ls.j.err = errors.New("rclone.conf changed")
	tt.Frame()
	hasAll(t, tt.Texts(), "✓", "xiaoman", "✗", "没有完成", "没有执行", "home 还在列表里", "云盘授权、搬运工、用户通行证、管理员通行证", "重试")

	// Offline: only removing here, with what stays as last read.
	ls.steps, ls.j.err, ls.offline = nil, nil, errors.New("timeout")
	left.ReadAt = time.Now().Add(-3 * time.Hour)
	tt.Frame()
	hasAll(t, tt.Texts(), "home 现在连不上", "最后一次读到的情况", "porter（~/.local/bin/valise-porter）", "备份库（~/.local/share/valise/vaults，保留）")
	if tt.HasText("删除并清理") {
		t.Error("cleaning offered while offline")
	}
	click(t, tt, "从这台电脑移除")
	if a.router.Path() != "/servers" {
		t.Fatalf("still at %s", a.router.Path())
	}
	rm, _ := a.svc.RemovedServers()
	if len(rm) != 1 || rm[0].Name != "home" || rm[0].Cleaned {
		t.Fatalf("%+v", rm)
	}
	a.removed = rm
	tt.Frame()
	hasAll(t, tt.Texts(), "已删除的服务器 · 1")
	*a.fold("removed-servers") = true
	tt.Frame()
	tt.Advance(time.Second) // the removal dialog finishes closing
	click(t, tt, "忘记")
	hasAll(t, tt.Texts(), "忘记 home？", "不删任何备份")
	click(t, tt, "忘记这台服务器")
	if rm, _ := a.svc.RemovedServers(); len(rm) != 0 || a.forgetting != "" {
		t.Fatalf("not forgotten: %+v", rm)
	}
}

func TestVersionAndName(t *testing.T) {
	if !versionLess("0.9.0", "0.9.2") || versionLess("v0.10.0", "0.9.2") || versionLess("0.9.2", "0.9.2-dev") {
		t.Error("versionLess")
	}
	if !validName("home-vps") || validName("Home") || validName("-a") || validName("家里") {
		t.Error("validName")
	}
}

// TestSettingsShots renders the settings and removal screens.
func TestSettingsShots(t *testing.T) {
	if os.Getenv("VALISE_SHOTS") == "" {
		t.Skip("VALISE_SHOTS not set")
	}
	a, tt := settingsWorld(t)
	step := func(path, name string) {
		if path != "" {
			a.router.Push(path)
		}
		tt.Frame()
		tt.Advance(time.Second)
		shot(t, tt, name)
	}
	a.serverTab = 1
	step("/servers/home", "set-tab")
	step("/servers/nas", "set-tab-own")
	ss := a.settings["home"]
	ss.newName = "home-lab"
	ss.plan = &service.RenamePlan{Host: "home", Old: "home", New: "home-lab", Places: []service.RenamePlace{
		{Kind: "repo", Name: "usb", From: "/Volumes/usb/valise-vault/home", To: "/Volumes/usb/valise-vault/home-lab"},
		{Kind: "station", Name: "gdrive", From: "home", To: "home-lab"}, {Kind: "repo", Name: "nas", Missing: true}}}
	step("/servers/home/rename", "set-rename")
	step("/servers/nas/migrate", "set-migrate")
	step("/servers/home/porter", "set-porter")
	left := &service.ServerLeftovers{Porter: true, Passes: []string{"daily", "admin"}, Timers: []string{"xiaoman"},
		Clouds: []string{"gdrive"}, Tools: []service.LeftTool{{ID: "rclone", Name: "rclone"}},
		Skills: []service.LeftSkills{{Instance: "xiaoman", Label: "小满", Skills: []string{"a", "b"}}}, ReadAt: time.Now().Add(-3 * time.Hour)}
	ls := &leaveState{open: true, mode: "clean", left: left, items: map[string]bool{service.LeaveTimers: true, service.LeaveTools: true,
		service.LeaveClouds: true, service.LeavePorter: true, service.LeavePasses: true}}
	ss.leave = ls
	step("/servers/home", "leave-clean")
	ls.steps = []service.LeaveStep{{Item: service.LeaveTimers, Detail: "xiaoman"}, {Item: service.LeaveTools, Detail: "rclone"}}
	ls.j.err = errors.New("rclone.conf changed")
	step("", "leave-failed")
	ls.steps, ls.j.err, ls.offline = nil, nil, errors.New("timeout")
	step("", "leave-offline")
	ss.leave = nil
	a.removed = []service.RemovedServer{{Name: "debian-vm", Cleaned: true, RemovedAt: time.Now().Add(-time.Hour)}}
	step("/servers", "removed-list")
}

// A local build chosen on the update page stays chosen and enables 更新.
func TestPorterPagePickKeeps(t *testing.T) {
	a, tt := settingsWorld(t)
	src := filepath.Join(t.TempDir(), "valise-porter_linux_arm64")
	os.WriteFile(src, []byte("x"), 0o600)
	a.pickFile = func(string, string) (string, error) { return src, nil }
	a.router.Push("/servers/nas/porter")
	tt.Advance(time.Second)
	click(t, tt, "更多选项")
	click(t, tt, "选择文件…")
	tt.Frame()
	tt.Frame()
	hasAll(t, tt.Texts(), "已选择本地构建，将上传：", src)
	if w := a.settingsOf("nas").porterW; w == nil || w.localPorter != src {
		t.Fatalf("pick lost: %+v", w)
	}
}
