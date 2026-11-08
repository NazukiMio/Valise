package gui

import (
	"context"
	"os"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	"valise/ui/service"
)

// TestServerShots renders the servers segment (docs/UI.md 7.5).
func TestServerShots(t *testing.T) {
	if os.Getenv("VALISE_SHOTS") == "" {
		t.Skip("VALISE_SHOTS not set")
	}
	a, tt := world(t)
	a.servers = load[[]serverRow]{started: true, value: []serverRow{
		{Name: "home", Kind: "Valise 接入", Address: "agent@192.0.2.24", Valise: true},
		{Name: "cloud", Kind: "用你自己的 SSH 设置"}, {Name: "nas", Kind: "用你自己的 SSH 设置"}, {Name: "lab", Kind: "Valise 接入", Valise: true}}}
	a.agents.value.Observations[1].Error = &valise.UserError{Code: "ssh_auth_failed", Reason: "登录被拒：服务器上的授权列表可能被改过"}
	a.agents.value.Observations[1].Status = "unknown"
	step := func(path, name string) {
		if path != "" {
			a.router.Push(path)
		}
		tt.Frame()
		tt.Advance(time.Second)
		shot(t, tt, name)
	}
	step("/servers", "srv-list")
	a.startWizard(false)
	a.wiz.addr = "agent@192.0.2.24:22"
	step("", "srv-connect")
	a.svc.Keyscan = func(context.Context, string, int) ([]byte, error) {
		return []byte("192.0.2.24 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"), nil
	}
	a.scanKeys(a.wiz)
	step("", "srv-identity")
	a.wiz.step, a.wiz.admin, a.wiz.adminPass = wzLogin, "yes", "short"
	step("", "srv-login")
	a.wiz.step, a.wiz.host, a.wiz.busy = wzPrepare, "192-0-2-24", true
	a.wiz.steps = []service.SetupStep{{Step: service.SetupKeys, Detail: "daily"}, {Step: service.SetupCheck, Detail: "linux/arm64"}}
	step("", "srv-prepare")
	a.installable = []installable{{ID: "openclaw", Name: "OpenClaw", Takes: "3 分钟", Needs: "node >=22"}, {ID: "hermes", Name: "Hermes", Takes: "5 分钟"}}
	d := service.DetectedInstance{Candidate: enroll.Candidate{ProfileID: "openclaw", StateDir: "/home/agent/.openclaw", Version: "2026.10.9"}, ID: "openclaw"}
	a.scans["home"] = &serverScan{found: []service.DetectedInstance{d}, adopts: map[string]*adoption{"openclaw": {assign: map[string]string{},
		res: &service.EnrollResult{Report: &enroll.Report{Categories: map[string]enroll.Tally{"memory": {Files: 40}},
			Unclassified: enroll.Tally{Files: 2}, UnclassGroups: []enroll.UnclassifiedGroup{{Root: "state", Prefix: "plugins/", Tally: enroll.Tally{Files: 2, Bytes: 1000}}}}}}}}
	*a.fold("assign-homeopenclaw") = true
	step("/servers/home/agents", "srv-found")
	a.scans["lab"] = &serverScan{found: []service.DetectedInstance{}, adopts: map[string]*adoption{}}
	step("/servers/lab/agents", "srv-none")
	a.installs["lab/openclaw"] = &installRun{plan: &service.InstallPlan{Host: "lab", ProfileID: "openclaw", Where: "~/.openclaw", Takes: "3 分钟", Steps: []service.PlanStep{
		{Kind: profile.StepScript, Summary: "下载官方安装脚本", URL: "https://example.invalid/install.sh", SHA256: "3b7f…a91c", Script: []byte("#!/bin/sh\necho hi\n")},
		{Kind: "exec", Summary: "在服务器上运行", Command: []string{"bash", "install.sh", "--no-onboard"}},
		{Kind: "exec", Summary: "加入 PATH", Optional: true, SkipEffect: "之后要用完整路径运行 openclaw", Command: []string{"true"}}}}}
	step("/servers/lab/install/openclaw", "srv-plan")
	ir := a.installs["lab/openclaw"]
	ir.conn, ir.step, ir.text = newInstallConn(), "第 2 步 · 在服务器上运行", "==> Installing OpenClaw 2026.10.9\n==> Node 22.11.0 found\n"
	ir.res = &service.InstallResult{Exits: []int{0, 0, -1}, Completed: true, Found: &enroll.Candidate{StateDir: "/home/agent/.openclaw"}}
	step("", "srv-installed")
	step("/servers/home", "srv-detail")
	step("/servers/cloud", "srv-detail-trouble")
}
