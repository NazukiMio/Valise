package gui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/protection"
	"valise/ui/service"
)

const testPass = "a long enough passphrase"

// Settings › 钥匙与口令: without a key it offers to set one; setting it
// leads to backing the key up, and the page then says where things stand
// in one line each.
func TestKeySettings(t *testing.T) {
	a, tt := world(t)
	a.router.Push("/settings")
	tt.Frame()
	hasAll(t, tt.Texts(), "钥匙与口令", "这台电脑还没有加密钥匙", "设 Valise 口令…", "导入恢复套件…")
	click(t, tt, "设 Valise 口令…")
	hasAll(t, tt.Texts(), "设一个 Valise 口令", "同时留一把备用锁（忘了口令时用服务器的 sudo 找回）", "查看详情…")
	a.keyFlow.pass, a.keyFlow.pass2 = testPass, testPass
	tt.Frame()
	click(t, tt, "设好口令")
	if a.keyFlow == nil || a.keyFlow.step != kfGate || !a.svc.RecoveryEnabled() {
		t.Fatalf("not at the gate: %+v", a.keyFlow)
	}
	hasAll(t, tt.Texts(), "先把钥匙备份一份", "导出恢复套件", "我知道风险，稍后备份")
	a.keyFlow.dir = filepath.Join(t.TempDir(), "kit")
	tt.Frame()
	click(t, tt, "导出并继续")
	if a.keyFlow != nil || !a.svc.KeyBackupDecided() {
		t.Fatalf("export: %+v", a.keyFlow)
	}
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "加密钥匙由 Valise 口令保护", "已备份钥匙", "备份钥匙", "Valise 口令", "备用锁开着 · 0 台服务器可用来找回",
		"忘了口令…", "关闭备用锁…", "管理员通行证", "还没有服务器办管理员通行证")
	if tt.HasText("救不了的情况") {
		t.Error("details shown unfolded")
	}
	a.svc.DisableRecovery()
	tt.Frame()
	hasAll(t, tt.Texts(), "备用锁关着 · 忘了就找不回", "开启备用锁…")
	if tt.HasText("忘了口令…") {
		t.Error("recovery offered with the lock off")
	}
	click(t, tt, "备份")
	hasAll(t, tt.Texts(), "关掉 Valise 也继续备份", "开启后，进行中的备份交给服务器自己跑完。")
}

func TestPINSettingsAndWindow(t *testing.T) {
	a, tt := world(t)
	if _, err := a.svc.KeyInit(testPass); err != nil {
		t.Fatal(err)
	}
	a.router.Push("/settings")
	tt.Frame()
	hasAll(t, tt.Texts(), "本机快捷解锁", "PIN 码未开启", "免输解锁时长", "30 分钟")
	click(t, tt, "调整时长…")
	a.keyFlow.pass = "240"
	tt.Frame()
	click(t, tt, "保存")
	hasAll(t, tt.Texts(), "240 分钟")
	click(t, tt, "开启 PIN…")
	a.keyFlow.old, a.keyFlow.pass, a.keyFlow.pass2 = testPass, "012345", "012345"
	tt.Frame()
	click(t, tt, "保存")
	if !a.svc.PINEnabled() || tt.HasText("调整时长…") {
		t.Fatal("PIN settings did not hide the window control")
	}
	hasAll(t, tt.Texts(), "6 位 PIN 码已开启", "每次操作都要验证本机 PIN 码。", "修改 PIN…")
	click(t, tt, "关闭 PIN")
	click(t, tt, "关闭快捷解锁")
	if a.svc.PINEnabled() {
		t.Fatal("PIN remains enabled")
	}
	hasAll(t, tt.Texts(), "PIN 码未开启", "240 分钟", "调整时长…")
}

// The first backup waits for a key and for backing it up.
func TestNeedKeyGates(t *testing.T) {
	a, _ := world(t)
	ran := 0
	a.needKey(func() { ran++ })
	if ran != 0 || a.keyFlow == nil || a.keyFlow.step != kfSet {
		t.Fatal("no key: should ask for the passphrase")
	}
	a.keyFlow = nil
	if _, err := a.svc.KeyInit(testPass); err != nil {
		t.Fatal(err)
	}
	a.needKey(func() { ran++ })
	if ran != 0 || a.keyFlow.step != kfGate {
		t.Fatal("not backed up: should ask first")
	}
	a.svc.BackUpKeyLater()
	a.keyFlow = nil
	a.needKey(func() { ran++ })
	if ran != 1 || a.keyFlow != nil {
		t.Fatal("decided: should go on")
	}
}

// 管理与设置: the plan in force line by line, missed runs with 标记已读,
// and a backup's parts each with its own state.
func TestManageTab(t *testing.T) {
	a, tt := world(t)
	on, catch := true, true
	ref := identity.AgentRef{InstallID: "openclaw", InstanceID: "xiaoman"}
	a.agents.value.Items[0].IdentityState, a.agents.value.Items[0].AgentRef = identity.Bound, &ref
	now := time.Now()
	ps := &protState{ref: &ref, view: &protection.View{ServerPlan: &protection.ServerPlan{Revision: 4, Desired: protection.Desired{
		Schedule: protection.Schedule{Enabled: &on, DailyAt: "04:40", Timezone: "Asia/Shanghai", CatchUp: &catch},
		Period:   protection.Period{Unit: "month"}, ServerCopies: []protection.ServerCopy{{TargetRef: protection.LocationRef{ID: "gdrive"}}}}},
		MissedRuns: []protection.MissedRun{{ScheduledAt: now.Add(-48 * time.Hour), State: "missed"}, {ScheduledAt: now.Add(-24 * time.Hour), State: "caught_up"}}}}
	a.prots["home/xiaoman"] = ps
	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Frame()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "管理与设置", "保护计划", "已生效 · 第 4 版", "现在备份", "定时备份", "每天 04:40（Asia/Shanghai） · 错过时开机补一次",
		"备份分组", "按月", "备份后复制到", "gdrive", "错过了 2 次定时备份", "其中 1 次开机后已补", "标记已读")
	click(t, tt, "标记已读")
	if tt.HasText("错过了 2 次定时备份") {
		t.Error("still shown after 标记已读")
	}
	click(t, tt, "修改…")
	hasAll(t, tt.Texts(), "定时备份频率", "每天", "几点", "时区", "按周", "按季度", "保存")
	ps.sched = nil
	ps.backup = &backupRun{task: &service.BackupTask{Run: &protection.BackupRun{Components: []protection.BackupComponent{
		{Kind: "server_backup", State: "succeeded"}, {Kind: "server_copy", TargetRef: &protection.LocationRef{ID: "gdrive"}, State: "failed"}}}}}
	ps.backup.j.err = errors.New("x")
	tt.Frame()
	hasAll(t, tt.Texts(), "备份 小满", "服务器上的备份库", "已完成", "复制到 gdrive", "没有完成")
}

// Recovery: the list says each server's lock; in recovery mode a banner
// stands over the page; the command is shown in full.
func TestRecoverPage(t *testing.T) {
	a, tt := world(t)
	a.router.Push("/settings/recover")
	tt.Frame()
	hasAll(t, tt.Texts(), "找回 Valise 口令", "选一台“已做”备用锁的服务器。", "查看详情…")
	a.recover = &recoverState{host: "home", check: &service.PorterCheck{Version: "0.9.2", Match: true}}
	tt.Frame()
	hasAll(t, tt.Texts(), "找回模式", "取消找回", "用 home 找回 Valise 口令", "搬运工 0.9.2 与这台电脑记下的一致", "继续")
	a.recover.check.Match = false
	tt.Frame()
	hasAll(t, tt.Texts(), "搬运工与这台电脑记下的不一致，或者没有记录", "去更新搬运工")
	if tt.HasText("继续") {
		t.Error("going on with a porter that does not match")
	}
	a.recover.run = &service.RecoveryRun{Host: "home", Command: "sudo sh -c 'echo test'"}
	tt.Frame()
	hasAll(t, tt.Texts(), "在 home 上运行这条命令", "sudo sh -c 'echo test'", "验证码", "下一步")
	a.router.Push("/agents")
	tt.Frame()
	if a.router.Path() != "/settings/recover" {
		t.Fatalf("left recovery mode to %s", a.router.Path())
	}
	click(t, tt, "取消找回")
	if a.recovering() || a.svc.RecoveryMode() {
		t.Fatal("still recovering")
	}
}

func TestRebuildPage(t *testing.T) {
	a, tt := world(t)
	a.router.Push("/settings/admin-rebuild")
	tt.Frame()
	hasAll(t, tt.Texts(), "重办管理员通行证", "为已有通行证或待重办的服务器换发管理员通行证。", "新的管理口令", "开始重办")
}

// TestKeyShots renders the keys and backups screens.
func TestKeyShots(t *testing.T) {
	if os.Getenv("VALISE_SHOTS") == "" {
		t.Skip("VALISE_SHOTS not set")
	}
	a, tt := world(t)
	step := func(path, name string) {
		if path != "" {
			a.router.Push(path)
		}
		tt.Frame()
		tt.Advance(time.Second)
		shot(t, tt, name)
	}
	a.keyFlow = &keyFlow{step: kfSet, lock: true, pass: testPass, pass2: testPass}
	step("/settings", "key-set")
	a.keyFlow = &keyFlow{step: kfGate, choice: "export", dir: "/Volumes/USB/valise-kit"}
	step("", "key-gate")
	a.keyFlow = nil
	a.svc.KeyInit(testPass)
	a.svc.EnableRecovery(testPass)
	a.svc.ExportKit(filepath.Join(t.TempDir(), "kit"))
	step("/settings", "key-settings")
	on, catch := true, true
	ref := identity.AgentRef{InstallID: "openclaw", InstanceID: "xiaoman"}
	ps := &protState{ref: &ref, view: &protection.View{ServerPlan: &protection.ServerPlan{Revision: 4, Desired: protection.Desired{
		Schedule: protection.Schedule{Enabled: &on, DailyAt: "04:40", Timezone: "Asia/Shanghai", CatchUp: &catch},
		Period:   protection.Period{Unit: "month"}, ServerCopies: []protection.ServerCopy{{TargetRef: protection.LocationRef{ID: "gdrive"}}}}},
		MissedRuns: []protection.MissedRun{{ScheduledAt: time.Now().Add(-48 * time.Hour), State: "missed"}}}}
	a.prots["home/xiaoman"] = ps
	a.agentTab = tabManage
	step("/agents/home/xiaoman", "manage")
	ps.backup = &backupRun{task: &service.BackupTask{Run: &protection.BackupRun{Components: []protection.BackupComponent{
		{Kind: "server_backup", State: "succeeded"}, {Kind: "server_copy", TargetRef: &protection.LocationRef{ID: "gdrive"}, State: "running"}}}}}
	ps.backup.j.busy = true
	step("", "backup-running")
	ps.backup = nil
	a.recover = &recoverState{host: "home", run: &service.RecoveryRun{Host: "home", Command: "sudo sh -c 'f=$(mktemp) && cat /home/me/.local/bin/valise-porter > \"$f\" && echo \"45d82e759cd2b1819dfc66123db3e71710df9aee0b191a05ff3a12af2ceac6cd  $f\" | sha256sum -c --quiet && chmod 700 \"$f\" && \"$f\" recovery-code --for me; r=$?; rm \"$f\"; exit $r'"}}
	step("/settings/recover", "recover-command")
	a.recover = nil
	step("/settings/recover", "recover-list")
	step("/settings/admin-rebuild", "rebuild")
}

// An earlier generation's plan is shown as no longer in force, with one
// button to set it again; backing up waits for a plan in force.
func TestManageTabSuperseded(t *testing.T) {
	a, tt := world(t)
	on := true
	ref := identity.AgentRef{ServerUID: "u", InstallID: "openclaw", InstanceID: "xiaoman", Generation: "new"}
	old := ref
	old.Generation = "old"
	a.agents.value.Items[0].IdentityState, a.agents.value.Items[0].AgentRef = identity.Bound, &ref
	a.prots["home/xiaoman"] = &protState{ref: &ref, view: &protection.View{Mode: "superseded", SupersededPlan: &protection.ServerPlan{AgentRef: old, Revision: 3,
		Desired: protection.Desired{Schedule: protection.Schedule{Enabled: &on, DailyAt: "04:40", Timezone: "Asia/Shanghai"}, Period: protection.Period{Unit: "month"},
			ServerCopies: []protection.ServerCopy{{TargetRef: protection.LocationRef{ID: "gdrive", AuthorityID: "u"}}, {TargetRef: protection.LocationRef{ID: "elsewhere", AuthorityID: "other"}}}}}}}
	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Frame()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "已失效", "重新接入后，原来的计划已失效，定时备份已经停了。", "按原设置重新生效", "每天 04:40（Asia/Shanghai）", "gdrive")
	if tt.HasText("现在备份") || tt.HasText("elsewhere") {
		t.Errorf("backup offered or another server's copy kept: %q", tt.Texts())
	}
}
