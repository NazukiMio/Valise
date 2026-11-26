package gui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/client/porter"
	"valise/core/enroll"
	"valise/core/install"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

type guiInstallWorld struct {
	a    *App
	tt   *ui.Tester
	mu   sync.Mutex
	home string
	ts   *httptest.Server
	svc  *service.Service
}

const cleanDemoInstallScript = `#!/bin/sh
mkdir -p "$HOME/.demo" "$HOME/.local/bin"
printf '{}' > "$HOME/.demo/demo.json"
printf '#!/bin/sh\necho demo 1.0\n' > "$HOME/.local/bin/demo"
chmod +x "$HOME/.local/bin/demo"
echo "gui-installed args=$*"
`

func scriptHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newGUIInstallWorld(t *testing.T, scriptBody []byte, recordedSHA string) *guiInstallWorld {
	t.Helper()
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(scriptBody)
	}))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	svc := &service.Service{
		Dirs:    config.Dirs{Data: filepath.Join(dir, "data"), Cache: filepath.Join(dir, "cache")},
		Config:  config.Config{SSHBinary: e.SSH},
		Version: "test",
		Home:    dir,
		Release: porter.Release{HTTP: ts.Client()},
	}
	if err := os.MkdirAll(filepath.Join(svc.Dirs.Data, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	prof := `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths:
  - { root: state, glob: "demo.json", category: config, verify: pending }
provenance: { kind: binary, binary: { name: demo } }
install:
  id: demo-script
  summary: "安装演示"
  where: "~/.demo"
  takes: "几秒"
  steps:
    - kind: script
      url: "` + ts.URL + `/install.sh"
      sha256: "` + recordedSHA + `"
      interpreter: [bash]
      args: [--ver, "1"]
      runtime_downloads: ""
`
	if err := os.WriteFile(filepath.Join(svc.Dirs.Data, "profiles", "demo.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddServers("box"); err != nil {
		t.Fatal(err)
	}

	w := &guiInstallWorld{home: home, ts: ts, svc: svc}
	w.a = New(svc)
	w.a.loadHarness()
	w.a.update = func(f func()) {
		w.mu.Lock()
		defer w.mu.Unlock()
		f()
	}
	w.a.scanTimeline = func(context.Context, service.TimelineScanRequest, func(service.TimelineUpdate)) error { return nil }
	w.tt = ui.NewTester(w.a.View, 1180, 900)
	return w
}

func (w *guiInstallWorld) do(f func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f()
	w.tt.Frame()
	w.tt.Advance(time.Second)
}

func (w *guiInstallWorld) wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var ok bool
		w.do(func() { ok = cond() })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			w.mu.Lock()
			defer w.mu.Unlock()
			t.Fatalf("timed out waiting for %s: %q", what, w.tt.Texts())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestInstallUpdatedScriptConfirmAndRunGUI(t *testing.T) {
	updated := []byte(cleanDemoInstallScript)
	oldSHA := strings.Repeat("0", 64)
	newSHA := scriptHex(updated)
	w := newGUIInstallWorld(t, updated, oldSHA)

	w.do(func() { w.a.router.Push("/servers/box/install/demo") })
	w.wait(t, "install plan", func() bool {
		ir := w.a.installs["box/demo"]
		return ir != nil && !ir.planning
	})
	w.mu.Lock()
	texts := w.tt.Texts()
	hasStartBefore := w.tt.HasText("开始安装")
	w.mu.Unlock()
	hasAll(t, texts, "官方安装脚本已更新", "资料包记录的 SHA-256："+oldSHA, "本次实际 SHA-256："+newSHA,
		"基础检查未发现疑点（不代表安全）", "我已核对最新官方脚本内容并确认使用当前版本继续", "gui-installed args=$*")
	if hasStartBefore {
		t.Fatal("start button offered before confirming updated script")
	}

	w.do(func() {
		if err := w.tt.Click("我已核对最新官方脚本内容并确认使用当前版本继续"); err != nil {
			t.Fatal(err)
		}
	})
	w.mu.Lock()
	hasStartAfter := w.tt.HasText("开始安装")
	w.mu.Unlock()
	if !hasStartAfter {
		t.Fatal("start button did not appear after checking updated script confirmation")
	}

	w.do(func() {
		if err := w.tt.Click("开始安装"); err != nil {
			t.Fatal(err)
		}
	})
	w.wait(t, "install completion", func() bool {
		ir := w.a.installs["box/demo"]
		return ir != nil && !ir.running && ir.conn != nil
	})
	w.mu.Lock()
	doneTexts := w.tt.Texts()
	planID := w.a.installs["box/demo"].plan.ID
	w.mu.Unlock()
	hasAll(t, doneTexts, "装好了，并在服务器上找到了它", "接入它", "gui-installed args=--ver 1")
	if _, err := os.Stat(filepath.Join(w.home, ".local/state/valise/install", planID, "install.sh")); err != nil {
		t.Fatalf("staged script missing: %v", err)
	}
}

func TestInstallUpdatedScriptRefusedGUI(t *testing.T) {
	evil := []byte("#!/bin/sh\nrm -rf / --no-preserve-root\n")
	w := newGUIInstallWorld(t, evil, strings.Repeat("1", 64))

	w.do(func() { w.a.router.Push("/servers/box/install/demo") })
	w.wait(t, "refused install plan", func() bool {
		ir := w.a.installs["box/demo"]
		return ir != nil && !ir.planning
	})
	w.mu.Lock()
	texts := w.tt.Texts()
	hasConfirm := w.tt.HasText("我已核对最新官方脚本内容并确认使用当前版本继续")
	hasStart := w.tt.HasText("开始安装")
	w.mu.Unlock()
	hasAll(t, texts, "官方安装脚本已更新，且包含 Valise 一律不执行的内容", "一律不执行：", "第 2 行：rm -rf / --no-preserve-root", "改用自定义官方安装地址或指令")
	if hasConfirm || hasStart {
		t.Fatalf("refused script must not offer confirmation or start: confirm=%v start=%v", hasConfirm, hasStart)
	}
}

func TestCustomInstallEntryAndCurlPipeBashGUI(t *testing.T) {
	script := []byte(cleanDemoInstallScript)
	w := newGUIInstallWorld(t, script, scriptHex(script))

	// Even when the server already has an agent, custom install stays reachable
	// under 安装新的 Agent.
	w.do(func() {
		w.a.scans["box"] = &serverScan{
			found: []service.DetectedInstance{{
				Candidate: enroll.Candidate{ProfileID: "demo", StateDir: "/home/me/.demo", Version: "1.0"},
				ID:        "demo",
			}},
			adopts: map[string]*adoption{},
		}
		w.a.router.Push("/servers/box/agents")
	})
	w.mu.Lock()
	onServerTexts := w.tt.Texts()
	w.mu.Unlock()
	hasAll(t, onServerTexts, "安装新的 Agent", "使用自定义官方安装地址或指令")

	w.do(func() {
		if err := w.tt.Click("使用自定义官方安装地址或指令"); err != nil {
			t.Fatal(err)
		}
	})
	if w.a.router.Path() != "/servers/box/install-custom" {
		t.Fatalf("went to %s", w.a.router.Path())
	}

	// Paste a curl | bash command: client parses, downloads locally, previews,
	// and runs staged file on the server.
	w.do(func() {
		ci := w.a.customInstalls["box"]
		ci.raw = "curl -fsSL " + w.ts.URL + "/setup.sh | bash -s -- --ver gui1"
		if err := w.tt.Click("下载并检查"); err != nil {
			t.Fatal(err)
		}
	})
	w.wait(t, "custom install plan", func() bool {
		ci := w.a.customInstalls["box"]
		return ci != nil && !ci.planning && ci.plan != nil
	})
	w.mu.Lock()
	planTexts := w.tt.Texts()
	w.mu.Unlock()
	hasAll(t, planTexts, "解析出的下载地址："+w.ts.URL+"/setup.sh", "本次下载 SHA-256："+scriptHex(script),
		"将在服务器暂存目录执行的实际命令", "setup.sh --ver gui1", "基础检查未发现疑点（不代表安全）", "gui-installed args=$*")

	w.do(func() {
		if err := w.tt.Click("上传并在终端执行"); err != nil {
			t.Fatal(err)
		}
	})
	w.wait(t, "custom install completion", func() bool {
		ci := w.a.customInstalls["box"]
		return ci != nil && !ci.running && ci.conn != nil
	})
	w.mu.Lock()
	runTexts := w.tt.Texts()
	w.mu.Unlock()
	hasAll(t, runTexts, "装好了，并在服务器上找到了它", "gui-installed args=--ver gui1")
}

func TestCustomInstallRiskSentenceAndRefusedGUI(t *testing.T) {
	warnScript := []byte(cleanDemoInstallScript + "echo fetch https://cdn.example.invalid/a.tgz\n")
	w := newGUIInstallWorld(t, warnScript, scriptHex(warnScript))

	w.do(func() {
		w.a.router.Push("/servers/box/install-custom")
		w.a.customInstalls["box"] = &customInstallRun{raw: w.ts.URL + "/warn.sh"}
	})
	w.do(func() {
		if err := w.tt.Click("下载并检查"); err != nil {
			t.Fatal(err)
		}
	})
	w.wait(t, "custom warn plan", func() bool {
		ci := w.a.customInstalls["box"]
		return ci != nil && !ci.planning && ci.plan != nil
	})
	w.mu.Lock()
	warnTexts := w.tt.Texts()
	w.mu.Unlock()
	hasAll(t, warnTexts, "发现 1 处需关注项", "会访问 https://cdn.example.invalid/a.tgz", install.RiskSentence)

	// Clicking run without typing RiskSentence shows mismatch error and does not run.
	w.do(func() {
		if err := w.tt.Click("上传并在终端执行"); err != nil {
			t.Fatal(err)
		}
	})
	w.mu.Lock()
	mismatchTexts := w.tt.Texts()
	ranEarly := w.a.customInstalls["box"].conn != nil
	w.mu.Unlock()
	if ranEarly {
		t.Fatal("custom install ran without RiskSentence")
	}
	hasAll(t, mismatchTexts, "请照原样输入确认句："+install.RiskSentence)

	// Typing RiskSentence allows execution.
	w.do(func() {
		w.a.customInstalls["box"].sentence = install.RiskSentence
		if err := w.tt.Click("上传并在终端执行"); err != nil {
			t.Fatal(err)
		}
	})
	w.wait(t, "custom warn run", func() bool {
		ci := w.a.customInstalls["box"]
		return ci != nil && !ci.running && ci.conn != nil
	})
	w.mu.Lock()
	doneTexts := w.tt.Texts()
	w.mu.Unlock()
	hasAll(t, doneTexts, "装好了，并在服务器上找到了它")

	// Destructive/security-disabling script is permanently refused in GUI without any run button.
	evil := []byte("#!/bin/sh\nufw disable\n")
	w2 := newGUIInstallWorld(t, evil, scriptHex(evil))
	w2.do(func() {
		w2.a.router.Push("/servers/box/install-custom")
		w2.a.customInstalls["box"] = &customInstallRun{raw: "curl -fsSL " + w2.ts.URL + "/evil.sh | bash"}
	})
	w2.do(func() {
		if err := w2.tt.Click("下载并检查"); err != nil {
			t.Fatal(err)
		}
	})
	w2.wait(t, "custom refused plan", func() bool {
		ci := w2.a.customInstalls["box"]
		return ci != nil && !ci.planning && ci.plan != nil
	})
	w2.mu.Lock()
	refusedTexts := w2.tt.Texts()
	hasRunBtn := w2.tt.HasText("上传并在终端执行")
	w2.mu.Unlock()
	hasAll(t, refusedTexts, "这段内容 Valise 不会执行，已拦下", "一律不执行：", "第 2 行：ufw disable", T("install.custom.open_shell"))
	if hasRunBtn {
		t.Fatal("refused custom script must not offer run button")
	}
}
