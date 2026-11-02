package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path"
	"slices"
	"strings"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/enroll"
	"valise/core/install"
	"valise/core/profile"
	"valise/core/valise"
)

// Installing a harness with its official plan (docs/INSTALL.md section 3).
// Scripts are downloaded here, checked against the profile's SHA-256 and
// shown in full before anything runs; on the server every step runs as a
// fixed command (`valise-porter exec`) in a terminal of its own admin
// connection. Nothing is piped from the network into an interpreter.

// maxScriptBytes bounds a downloaded install script.
const maxScriptBytes = 2 << 20

// PlanStep is one step as shown to the user and run on the server.
type PlanStep struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Command is the exact command run on the server.
	Command []string `json:"command"`
	Note    string   `json:"note,omitempty"`
	// Script steps: where it came from, its hash and full text.
	URL              string `json:"url,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	Script           []byte `json:"-"`
	RuntimeDownloads string `json:"runtime_downloads,omitempty"`
	// npm_global steps: the integrity the registry must report.
	Integrity string `json:"integrity,omitempty"`
	// Optional steps run only when Selected (docs/INSTALL.md section 3).
	Optional   bool   `json:"optional,omitempty"`
	ID         string `json:"id,omitempty"`
	SkipEffect string `json:"skip_effect,omitempty"`
	Selected   bool   `json:"selected,omitempty"`
	pkg        string
	// porterArgs are run by the porter itself (path_add), not through exec.
	porterArgs []string
}

// InstallPlan is an official plan checked against one server.
type InstallPlan struct {
	ID             string              `json:"id"`
	Host           string              `json:"host"`
	ProfileID      string              `json:"profile_id"`
	ProfileVersion int                 `json:"profile_version"`
	ProfileLocal   bool                `json:"profile_local"`
	Capability     string              `json:"install_capability"`
	Summary        string              `json:"summary"`
	Check          install.CheckResult `json:"check"`
	Steps          []PlanStep          `json:"steps"`
	// Missing are requirements that are not met; the plan cannot run until
	// they are (by the user, the server's admin, or advanced mode).
	Missing []install.Requirement `json:"missing,omitempty"`
	// Fixes are the profile's commands for Missing on this distribution,
	// run only in advanced mode.
	Fixes [][]string `json:"fixes,omitempty"`
	// Unfixable are missing requirements without a command for this
	// distribution.
	Unfixable []install.Requirement `json:"unfixable,omitempty"`
	// NativeCLI is the harness's own setup, to continue with natively.
	NativeCLI []string `json:"native_cli,omitempty"`
	// BinDirs are put on PATH for the steps (detect.bin_dirs).
	BinDirs []string `json:"bin_dirs,omitempty"`
}

// PlanInstall checks profileID's official plan against host and downloads
// and verifies its scripts.
func (s *Service) PlanInstall(ctx context.Context, host, profileID string) (*InstallPlan, error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	if err := needProfiles(set); err != nil {
		return nil, err
	}
	lp := set.ByID[profileID]
	if lp == nil {
		return nil, valise.New("profile_missing", "本机没有 harness 资料 "+profileID, "安装或更新 harness 资料包：valise profiles install")
	}
	prof := lp.Effective
	if prof.Install == nil {
		return nil, valise.New("install_no_plan", prof.DisplayName+" 还没有经过核实的官方安装方案",
			"按 "+prof.DisplayName+" 官方文档在服务器上自己安装，装好后用 valise enroll 接入 Valise")
	}
	var id [6]byte
	_, _ = rand.Read(id[:])
	plan := &InstallPlan{ID: "inst_" + hex.EncodeToString(id[:]), Host: host, ProfileID: prof.ID, ProfileVersion: prof.ProfileVersion,
		ProfileLocal: lp.Local, Capability: prof.CapabilityStatus("install"), Summary: prof.Install.Summary, NativeCLI: prof.NativeCLI}
	if prof.Detect != nil {
		plan.BinDirs = prof.Detect.BinDirs
	}
	err = s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, install.MethodCheck, install.CheckParams{Requires: prof.Install.Requires}, &plan.Check)
	})
	if err != nil {
		return nil, err
	}
	for i, q := range plan.Check.Requirement {
		if q.OK {
			continue
		}
		plan.Missing = append(plan.Missing, q)
		fixed := false
		for _, fix := range prof.Install.Requires[i].Install {
			if slices.Contains(fix.OSID, plan.Check.OSID) {
				plan.Fixes = append(plan.Fixes, fix.Argv)
				fixed = true
				break
			}
		}
		if !fixed {
			plan.Unfixable = append(plan.Unfixable, q)
		}
	}
	for _, st := range prof.Install.Steps {
		ps, err := s.planStep(ctx, plan.ID, prof.ID, st)
		if err != nil {
			return nil, err
		}
		plan.Steps = append(plan.Steps, ps)
	}
	return plan, nil
}

// installDir is the server directory holding a plan's uploaded scripts.
func installDir(planID string) string { return ".local/state/valise/install/" + planID }

func (s *Service) planStep(ctx context.Context, planID, profileID string, st profile.InstallStep) (PlanStep, error) {
	ps := PlanStep{Kind: st.Kind, Note: st.Note, Optional: st.Optional, ID: st.ID, SkipEffect: st.SkipEffect}
	switch st.Kind {
	case profile.StepNPMGlobal:
		spec := st.Package + "@" + st.Version
		ps.pkg, ps.Integrity = spec, st.Integrity
		ps.Summary = "用 npm 把 " + spec + " 装到你的用户目录（~/.local）"
		ps.Command = []string{"npm", "install", "-g", "--prefix", "${HOME}/.local", spec}
	case profile.StepScript:
		h := s.Release.HTTP
		if h == nil {
			h = http.DefaultClient
		}
		body, err := s.download(ctx, h, st.URL, maxScriptBytes)
		if err != nil {
			return ps, err
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != st.SHA256 {
			return ps, valise.New("install_script_changed", "官方安装脚本已经变了，这个版本的方案不再适用",
				"等 harness 资料包更新，或用原生方式按官方文档自己安装").WithDetail(fmt.Sprintf("%s\n期望 %s\n实际 %s", st.URL, st.SHA256, got))
		}
		name := path.Base(st.URL)
		ps.URL, ps.SHA256, ps.Script = st.URL, st.SHA256, body
		if st.RuntimeDownloads != nil {
			ps.RuntimeDownloads = *st.RuntimeDownloads
		}
		ps.Summary = fmt.Sprintf("运行官方安装脚本 %s（%d 字节，SHA-256 已核对）", name, len(body))
		ps.Command = append(append(append([]string{}, st.Interpreter...), "${HOME}/"+installDir(planID)+"/"+name), st.Args...)
	case profile.StepArgv:
		ps.Summary = "运行 " + strings.Join(st.Argv, " ")
		ps.Command = st.Argv
	case profile.StepPathAdd:
		ps.Summary = "把 " + st.Dir + " 加入你登录 shell 的 PATH（在登录文件末尾追加两行）"
		ps.porterArgs = []string{"path-add", "--dir", st.Dir, "--label", profileID}
		ps.Command = append([]string{"valise-porter"}, ps.porterArgs...)
	}
	return ps, nil
}

// InstallIO connects the steps' terminals to the user.
type InstallIO struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Step is told before each step runs.
	Step func(i int, st PlanStep)
}

// InstallResult is what a run achieved, judged by detecting the instance,
// not by exit codes.
type InstallResult struct {
	Exits     []int                `json:"exits"`
	Completed bool                 `json:"completed"`
	Found     *enroll.Candidate    `json:"found,omitempty"`
	Detect    *enroll.DetectResult `json:"detect,omitempty"`
}

// RunInstall uploads the plan's scripts and runs its steps on one admin
// connection. It stops at the first failing step.
func (s *Service) RunInstall(ctx context.Context, plan *InstallPlan, tio InstallIO) (*InstallResult, error) {
	if len(plan.Missing) > 0 {
		return nil, valise.New("install_requirements", "服务器还缺少方案需要的程序："+plan.Missing[0].Reason,
			"先装好这些程序（自己在原生界面里装、交给服务器管理员，或开启高级模式）")
	}
	if err := s.RequireGate(ctx, plan.Host, ""); err != nil {
		return nil, err
	}
	t, done, err := s.adminTransport(ctx, plan.Host)
	if err != nil {
		return nil, err
	}
	defer done()
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return nil, err
	}
	for _, st := range plan.Steps {
		if st.Kind == profile.StepScript {
			if err := uploadScript(ctx, t, plan.ID, path.Base(st.URL), st.Script); err != nil {
				return nil, err
			}
		}
	}
	res := &InstallResult{}
	audit := install.AuditParams{PlanID: plan.ID, ProfileID: plan.ProfileID, ProfileVersion: plan.ProfileVersion, Result: "failed"}
	defer func() {
		_ = s.withSession(context.WithoutCancel(ctx), plan.Host, func(c caller) error { return c.Call(ctx, install.MethodAudit, audit, nil) })
	}()
	for i, st := range plan.Steps {
		if tio.Step != nil {
			tio.Step(i, st)
		}
		if st.Optional && !st.Selected {
			res.Exits = append(res.Exits, -1)
			audit.Steps = append(audit.Steps, install.StepAudit{Kind: st.Kind + " (skipped)", Command: strings.Join(st.Command, " "), Exit: -1})
			continue
		}
		if st.Kind == profile.StepNPMGlobal {
			if err := s.checkIntegrity(ctx, t, found.Path, st); err != nil {
				return res, err
			}
		}
		line := execLine(found.Path, plan.BinDirs, st.Command)
		if st.porterArgs != nil {
			words := []string{ssh.Quote(found.Path)}
			for _, a := range st.porterArgs {
				words = append(words, ssh.Quote(a))
			}
			line = strings.Join(words, " ")
		}
		code, err := runLineTTY(ctx, t, line, tio)
		res.Exits = append(res.Exits, code)
		audit.Steps = append(audit.Steps, install.StepAudit{Kind: st.Kind, Command: strings.Join(st.Command, " "), Exit: code})
		if err != nil {
			return res, err
		}
		if code != 0 {
			return res, valise.New("install_step_failed", fmt.Sprintf("第 %d 步失败（退出码 %d）：%s", i+1, code, st.Summary),
				"看上面的输出找原因；也可以改用原生方式按官方文档继续")
		}
	}
	res.Completed = true
	audit.Result = "steps_done"
	det, err := s.detectProfile(ctx, plan.Host, plan.ProfileID)
	if err != nil {
		return res, err
	}
	res.Detect = det
	for i := range det.Candidates {
		if det.Candidates[i].ProfileID == plan.ProfileID {
			res.Found = &det.Candidates[i]
			audit.Result, audit.Instance = "installed", plan.ProfileID
			break
		}
	}
	return res, nil
}

func uploadScript(ctx context.Context, t *ssh.Transport, planID, name string, body []byte) error {
	dir := `"$HOME/` + installDir(planID) + `"`
	res, err := t.Run(ctx, "umask 077 && mkdir -p "+dir+" && cat > "+dir+"/"+ssh.Quote(name), strings.NewReader(string(body)))
	if err == nil && res.ExitCode != 0 {
		err = valise.New("install_upload_failed", "没能把安装脚本放到服务器的暂存目录", "检查服务器磁盘空间与 ~/.local/state 的权限").
			WithDetail(strings.TrimSpace(string(res.Stderr)))
	}
	return err
}

// runTTY runs a fixed command through `valise-porter exec` under a remote
// terminal, connected to the user's terminal. Valise reads none of it.
func runTTY(ctx context.Context, t *ssh.Transport, porterPath string, dirs, argv []string, tio InstallIO) (int, error) {
	return runLineTTY(ctx, t, execLine(porterPath, dirs, argv), tio)
}

// runLineTTY runs one remote command line under a remote terminal.
func runLineTTY(ctx context.Context, t *ssh.Transport, line string, tio InstallIO) (int, error) {
	cmd := exec.CommandContext(ctx, t.Binary(), t.TTYArgs(line)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tio.Stdin, tio.Stdout, tio.Stderr
	err := cmd.Run()
	if cmd.ProcessState == nil {
		return -1, err
	}
	code := cmd.ProcessState.ExitCode()
	if code == ssh.ExitConnFailed {
		return code, t.ConnError("")
	}
	return code, nil
}

// execLine is the remote command running argv through `valise-porter
// exec` with the harness's bin dirs on PATH.
func execLine(porterPath string, dirs, argv []string) string {
	words := []string{ssh.Quote(porterPath), "exec"}
	for _, d := range dirs {
		words = append(words, "--path", ssh.Quote(d))
	}
	words = append(words, "--")
	for _, a := range argv {
		words = append(words, ssh.Quote(a))
	}
	return strings.Join(words, " ")
}

// checkIntegrity makes sure the registry serves the profile's tarball for
// an npm_global step (npm then verifies the download against it).
func (s *Service) checkIntegrity(ctx context.Context, t *ssh.Transport, porterPath string, st PlanStep) error {
	res, err := t.Run(ctx, execLine(porterPath, nil, []string{"npm", "view", st.pkg, "dist.integrity"}), nil)
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(string(res.Stdout)); res.ExitCode != 0 || got != st.Integrity {
		return valise.New("install_integrity_mismatch", "npm registry 上 "+st.pkg+" 的校验值与方案不一致，已停止",
			"可能是 registry 被替换或包被重新发布；确认后再试，或按官方文档自己安装").WithDetail(fmt.Sprintf("期望 %s\n实际 %s", st.Integrity, got))
	}
	return nil
}

func (s *Service) detectProfile(ctx context.Context, host, profileID string) (*enroll.DetectResult, error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	var res enroll.DetectResult
	err = s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, enroll.MethodDetect, enroll.DetectParams{Profiles: []profile.Profile{*set.ByID[profileID].Effective}}, &res)
	})
	return &res, err
}

// NativeHints are the profile's notes shown before its native CLI opens.
func (s *Service) NativeHints(profileID string) []string {
	set, err := s.Profiles()
	if err != nil || set.ByID[profileID] == nil {
		return nil
	}
	return set.ByID[profileID].Effective.NativeHints
}

// NativeCommand returns an unstarted terminal session running profileID's
// own interactive CLI on host, over a fresh admin connection (docs/
// INSTALL.md section 2). Call done after it exits.
func (s *Service) NativeCommand(ctx context.Context, host, profileID string) (*exec.Cmd, func(), error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, nil, err
	}
	lp := set.ByID[profileID]
	if lp == nil || len(lp.Effective.NativeCLI) == 0 {
		return nil, nil, valise.New("native_cli_none", "profile "+profileID+" 没有声明原生界面", "改用 valise shell 打开服务器终端")
	}
	if err := s.RequireGate(ctx, host, ""); err != nil {
		return nil, nil, err
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return nil, nil, err
	}
	found, err := porter.Probe(ctx, t)
	if err != nil {
		done()
		return nil, nil, err
	}
	var dirs []string
	if lp.Effective.Detect != nil {
		dirs = lp.Effective.Detect.BinDirs
	}
	return exec.CommandContext(ctx, t.Binary(), t.TTYArgs(execLine(found.Path, dirs, lp.Effective.NativeCLI))...), done, nil
}
