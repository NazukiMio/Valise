package gui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/install"
	"valise/core/profile"
	"valise/core/valise"
	"valise/ui/service"
)

func hasAll(t *testing.T, texts []string, want ...string) {
	t.Helper()
	all := strings.Join(texts, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("missing %q in %q", w, texts)
		}
	}
}

// Servers are 在线, 离线, 需要处理 or 未知, each with its reason (UI.md 7.5).
func TestServerListStates(t *testing.T) {
	a, tt := world(t)
	a.servers = load[[]serverRow]{started: true, value: []serverRow{
		{Name: "home", Kind: "Valise 接入", Address: "agent@192.0.2.24", Valise: true},
		{Name: "cloud", Kind: "用你自己的 SSH 设置"}, {Name: "nas", Kind: "用你自己的 SSH 设置"}, {Name: "lab", Kind: "Valise 接入", Valise: true},
	}}
	a.agents.value.Observations[1].Error = &valise.UserError{Code: "ssh_auth_failed", Reason: "登录被拒"}
	a.agents.value.Observations[1].Status = "unknown"
	a.router.Push("/servers")
	tt.Frame()
	hasAll(t, tt.Texts(), "添加服务器", "home，在线", "nas，离线", "cloud，需要处理", "lab，未知", "agent@192.0.2.24 · 1 个 Agent", "登录被拒", "还没读取")
	click(t, tt, "添加服务器")
	if a.router.Path() != "/servers/add" || a.wiz == nil || a.wiz.fromHome {
		t.Fatalf("wizard not opened: %s", a.router.Path())
	}
}

// The connect step splits a pasted address and shows the card as typed;
// the identity step shows the fingerprints, and “不一致” writes nothing.
func TestWizardConnectAndIdentity(t *testing.T) {
	a, tt := world(t)
	a.startWizard(true)
	a.wiz.addr = "agent@192.0.2.24:2222"
	tt.Frame()
	hasAll(t, tt.Texts(), "连接服务器", "它在哪里？", "192-0-2-24", "agent@192.0.2.24:2222", "服务器密码登录 · 还没有 Agent", "接下来会发生什么")
	a.wiz.name = "家里"
	tt.Frame()
	hasAll(t, tt.Texts(), "名字只能用小写字母、数字和连字符")
	a.wiz.name = "home-vps"
	var asked string
	a.svc.Keyscan = func(_ context.Context, host string, port int) ([]byte, error) {
		asked = host + ":" + itoa(port)
		return []byte("[192.0.2.24]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"), nil
	}
	tt.Frame()
	click(t, tt, "下一步：确认身份")
	if asked != "192.0.2.24:2222" || a.wiz.step != wzIdentity {
		t.Fatalf("scan %q step %d err %v", asked, a.wiz.step, a.wiz.err)
	}
	hasAll(t, tt.Texts(), "这是你的服务器吗？", "192.0.2.24:2222 报出的指纹", "SHA256:", "ED25519")
	click(t, tt, "不一致，停止")
	hasAll(t, tt.Texts(), "已停止，没有记下任何东西")
	if tt.HasText("一致，继续") {
		t.Error("going on offered after a mismatch")
	}
}

// splitAddress reads the forms people paste.
func TestSplitAddress(t *testing.T) {
	for in, want := range map[string]string{
		"agent@192.0.2.24:22": "agent|192.0.2.24|22", "example.com": "|example.com|",
		"root@[2001:db8::1]:2200": "root|2001:db8::1|2200", " vps:22 ": "|vps|22",
	} {
		u, h, p := splitAddress(in)
		if got := u + "|" + h + "|" + p; got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// The login step offers the admin pass, unchecked; preparing shows each
// step done, running or failed, apart.
func TestWizardLoginAndPrepare(t *testing.T) {
	a, tt := world(t)
	a.startWizard(false)
	a.wiz.addr, a.wiz.step = "agent@192.0.2.24", wzLogin
	tt.Frame()
	hasAll(t, tt.Texts(), "用户通行证", "同时办一张管理员通行证", "OpenSSH 会问服务器密码", "这是开发版 Valise")
	if tt.HasText("再输一次") {
		t.Error("admin passphrase fields shown before asking for the pass")
	}
	a.wiz.step, a.wiz.host = wzPrepare, "home-vps"
	a.wiz.steps = []service.SetupStep{{Step: service.SetupKeys, OK: true, Detail: "daily、admin"}, {Step: service.SetupCheck, OK: true, Detail: "linux/arm64"}}
	a.wiz.err = valise.New("porter_signature", "签名不对，没有上传", "换个网络重试")
	tt.Frame()
	hasAll(t, tt.Texts(), "正在准备「home-vps」", "用户通行证（只能查看和备份）、管理员通行证", "linux/arm64", "这一步没完成，后面的步骤没有执行。",
		"签名不对，没有上传", "重试", "更多选项")
}

// No agent is presumed: found ones are adopted after the report, a harness
// the profiles do not know leads to the guide, and with none the first
// agent can be installed.
func TestAgentsOnServer(t *testing.T) {
	a, tt := world(t)
	a.installable = []installable{{ID: "openclaw", Name: "OpenClaw", Takes: "3 分钟", Needs: "node >=22"}}
	d := service.DetectedInstance{Candidate: enroll.Candidate{ProfileID: "openclaw", StateDir: "/home/me/.openclaw", Version: "2026.10.9"}, ID: "openclaw"}
	a.scans["lab"] = &serverScan{found: []service.DetectedInstance{d}, adopts: map[string]*adoption{"openclaw": {assign: map[string]string{},
		res: &service.EnrollResult{Report: &enroll.Report{Categories: map[string]enroll.Tally{"memory": {Files: 4}},
			Unclassified: enroll.Tally{Files: 2}, UnclassGroups: []enroll.UnclassifiedGroup{{Root: "state", Prefix: "plugins/", Tally: enroll.Tally{Files: 2, Bytes: 10}}}}}}}}
	a.router.Push("/servers/lab/agents")
	tt.Frame()
	hasAll(t, tt.Texts(), "这台服务器上的 Agent", "找到了认识的 Agent", "OpenClaw", "找到 4 个文件，已按资料归类；2 个文件用途尚未识别，默认按敏感内容保留",
		"逐项改归类", "确认接入", "怎么为它写一份资料 ›", "重新扫描")
	// Installing stays offered under the agents found, as another one,
	// never as "there is no agent here".
	hasAll(t, tt.Texts(), T("onserver.more_title"), T("onserver.more_sub"), "安装 OpenClaw", "官方安装方案 · 约 3 分钟 · 需要 node >=22")
	if tt.HasText("这台服务器上还没有 Agent") {
		t.Error("an agent was found and the page says there is none")
	}
	found, _ := tt.Find("找到了认识的 Agent")
	more, _ := tt.Find(T("onserver.more_title"))
	if more.Y <= found.Y {
		t.Errorf("the install block is not under the agents: %v above %v", more.Y, found.Y)
	}
	// With nothing installable there is no empty block beside the agents.
	plans := a.installable
	a.installable = nil
	tt.Frame()
	if tt.HasText(T("onserver.more_title")) || tt.HasText(T("onserver.no_plans")) {
		t.Error("an empty install block beside found agents")
	}
	a.installable = plans
	tt.Frame()
	click(t, tt, "安装 OpenClaw")
	if a.router.Path() != "/servers/lab/install/openclaw" {
		t.Fatalf("install beside a found agent went to %s", a.router.Path())
	}
	a.router.Push("/servers/lab/agents")
	tt.Frame()
	if got := assignRule("state:plugins/", "config"); got != "state:plugins/**=config" {
		t.Errorf("assign %q", got)
	}
	a.scans["lab"] = &serverScan{found: []service.DetectedInstance{}, adopts: map[string]*adoption{}}
	tt.Frame()
	hasAll(t, tt.Texts(), "这台服务器上还没有 Agent", "安装 OpenClaw", "官方安装方案 · 约 3 分钟 · 需要 node >=22")
	click(t, tt, "安装 OpenClaw")
	if a.router.Path() != "/servers/lab/install/openclaw" {
		t.Fatalf("went to %s", a.router.Path())
	}
}

// The install plan is shown in full before anything runs; missing
// requirements and an existing agent stop it; the result keeps exit codes
// and what detecting found apart.
func TestInstallScreen(t *testing.T) {
	a, tt := world(t)
	plan := &service.InstallPlan{Host: "lab", ProfileID: "openclaw", Where: "~/.openclaw", Takes: "3 分钟", Steps: []service.PlanStep{
		{Kind: profile.StepScript, Summary: "下载官方安装脚本", URL: "https://example.invalid/install.sh", SHA256: "3b7f", Script: []byte("#!/bin/sh\necho hi\n")},
		{Kind: "exec", Summary: "加入 PATH", Optional: true, SkipEffect: "之后要用完整路径", Command: []string{"true"}},
	}, Missing: []install.Requirement{{Binary: "node", Reason: "需要 Node 22，服务器上是 18"}}}
	a.installs["lab/openclaw"] = &installRun{plan: plan}
	a.router.Push("/servers/lab/install/openclaw")
	tt.Frame()
	hasAll(t, tt.Texts(), "安装 OpenClaw", "会做这些事", "下载官方安装脚本", "SHA-256 3b7f · 已核对", "查看脚本全文（3 行）",
		"可选：加入 PATH", "不选的话：之后要用完整路径", "装在 ~/.openclaw · 大约 3 分钟", "不代表安全", "需要 Node 22，服务器上是 18")
	if tt.HasText("开始安装") {
		t.Error("install offered with requirements missing")
	}
	plan.Missing = nil
	tt.Frame()
	hasAll(t, tt.Texts(), "前提都满足", "开始安装")
	plan.Existing = &service.DetectedInstance{Candidate: enroll.Candidate{StateDir: "/home/me/.openclaw"}}
	tt.Frame()
	hasAll(t, tt.Texts(), "服务器上已经有 OpenClaw", "去接入它")
	plan.Existing = nil
	a.installs["lab/openclaw"] = &installRun{plan: plan, conn: newInstallConn(), text: tailText{s: "==> done\n"},
		res: &service.InstallResult{Exits: []int{0, -1}, Completed: true, Found: &enroll.Candidate{StateDir: "/home/me/.openclaw"}}}
	tt.Frame()
	hasAll(t, tt.Texts(), "装好了，并在服务器上找到了它", "命令：第 1 步退出码 0，第 2 步没选，跳过", "接入它", "==> done")
	a.installs["lab/openclaw"].res = &service.InstallResult{Exits: []int{0, 0}, Completed: true}
	a.installs["lab/openclaw"].err = errors.New("x")
	tt.Frame()
	hasAll(t, tt.Texts(), "命令执行完了，但重新检测没有找到它")
}

// The server detail: state, source and the two passes; a server from
// ~/.ssh/config logs in with the user's own setup.
func TestServerDetail(t *testing.T) {
	a, tt := world(t)
	a.servers = load[[]serverRow]{started: true, value: []serverRow{
		{Name: "home", Kind: "Valise 接入", Valise: true}, {Name: "nas", Kind: "用你自己的 SSH 设置"}}}
	a.router.Push("/servers/home")
	tt.Frame()
	hasAll(t, tt.Texts(), "在线", "Valise 接入", "通行证", "用户通行证 · 这台电脑上没有", "管理员通行证 · 未办理",
		"办一张管理员通行证", "这台服务器上的 Agent · 1", "小满", "查找、接入或安装")
	click(t, tt, "办一张管理员通行证")
	hasAll(t, tt.Texts(), "管理口令", "再输一次", "办理")
	a.router.Push("/servers/nas")
	tt.Frame()
	hasAll(t, tt.Texts(), "离线", "来自 SSH 配置", "用你自己的 SSH 设置登录", "原因", "连不上", "重新连接")
}

// While preparing, the running step says what it is doing and for how
// long; OpenSSH's open question is said as such; 停止 ends the work and
// closes the question.
func TestPrepareSaysWhatItDoes(t *testing.T) {
	a, tt := world(t)
	a.startWizard(false)
	w := a.wiz
	stopped := false
	w.step, w.host, w.busy = wzPrepare, "lab", true
	w.cancel = func() { stopped = true }
	w.doing, w.doingAt = "用 SSH 连接 192.0.2.24，登录后写入通行证", time.Now().Add(-12*time.Second)
	tt.Frame()
	hasAll(t, tt.Texts(), "用 SSH 连接 192.0.2.24，登录后写入通行证…（已等 1", "停止", "可以随时停止")
	reply := make(chan passReply, 1)
	a.asks = append(a.asks, &passPrompt{host: "agent@192.0.2.24's password:", kind: askServer, open: true, reply: reply})
	tt.Frame()
	hasAll(t, tt.Texts(), "等你在弹框里回答 OpenSSH 的提问")
	a.stopPrepare(w) // the dialog covers the page; 停止 does this
	if !stopped || len(a.asks) != 0 {
		t.Fatalf("stop: %v, %d questions left", stopped, len(a.asks))
	}
	if r := <-reply; r.err == nil {
		t.Fatal("the question was not answered as stopped")
	}
	stopped = false
	tt.Frame()
	click(t, tt, "停止")
	if !stopped {
		t.Fatal("停止 did not stop")
	}
}

// An agent enrolled the old way has no identity: the scan offers to accept
// it again, and once the list has its identity the plan is read with it.
func TestReenrollUnbound(t *testing.T) {
	a, tt := world(t)
	a.agents.value.Items[0].IdentityState = identity.LegacyUnbound
	d := service.DetectedInstance{Candidate: enroll.Candidate{ProfileID: "openclaw", StateDir: "/home/me/.openclaw"}, ID: "openclaw", Enrolled: "xiaoman"}
	a.scans["home"] = &serverScan{found: []service.DetectedInstance{d}, adopts: map[string]*adoption{}}
	a.router.Push("/servers/home/agents")
	tt.Frame()
	hasAll(t, tt.Texts(), "缺身份记录", "重新接入…")
	row := &agentRow{Server: "home", Instance: "xiaoman"}
	if a.protOf(row).ref != nil {
		t.Fatal("ref without identity")
	}
	ref := identity.AgentRef{ServerUID: "u", InstallID: "openclaw", InstanceID: "xiaoman", Generation: "g"}
	a.agents.value.Items[0].IdentityState, a.agents.value.Items[0].AgentRef = identity.Bound, &ref
	tt.Frame()
	hasAll(t, tt.Texts(), "已接入")
	if p := a.protOf(row).ref; p == nil || *p != ref {
		t.Fatalf("kept the old reading: %+v", p)
	}
}
