package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"time"

	"valise/client/config"
	"valise/client/ssh"
	"valise/core/kittools"
	"valise/core/login"
	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// The form login of a cloud drive (docs/ARCHITECTURE.md 7.3, 2026-11-10):
// rclone's questions come one at a time and are shown as a form; the ones
// with a fixed answer are answered here. The browser step runs through the
// login relay. The user's own app is passed to rclone once and not kept;
// nothing rclone holds is read. Drives without the loopback login, a busy
// port and an older porter use the terminal login instead.

// ErrFormUnavailable means this login has to use the terminal.
var ErrFormUnavailable = errors.New("the form login is not available here")

// CloudForm is how the form login reaches the person.
type CloudForm struct {
	App kittools.App
	// Ask shows one of rclone's questions and returns the answer.
	Ask         func(kittools.Question) (string, error)
	Status      func(LoginStatus)
	ConfirmOpen func(link, domain string) bool
	OpenBrowser func(link string) error
}

// maxSteps bounds a login: rclone asks a handful of questions.
const maxSteps = 40

// answerFixed is the answer the form gives by itself, or false.
func answerFixed(q kittools.Question) (string, bool) {
	switch q.Name {
	case kittools.QuestionAdvanced:
		return "false", true
	case kittools.QuestionSharedApp:
		// Asked only when no own app was given: the form already said the
		// shared one may stop working.
		return "true", true
	}
	return "", false
}

// CloudFormReady reports whether a server login of the drive can use the
// form.
func (s *Service) CloudFormReady(cloudID string) bool {
	cloud, err := s.cloudEntry(cloudID)
	return err == nil && cloud.UsesLoopback() && PortFree(CloudPort)
}

// CloudLoginServerForm logs host in to a cloud drive with the form, then
// tests and saves the location like CloudLoginServer.
func (s *Service) CloudLoginServerForm(ctx context.Context, host, name, cloudID string, f CloudForm) (*CloudResult, error) {
	cloud, err := s.cloudEntry(cloudID)
	if err != nil {
		return nil, err
	}
	if cloud.Pending() {
		return nil, valise.New("cloud_pending", cloud.Name+" 的登录与复制还没有在这个版本上测试", "选择已核实的云盘")
	}
	if !cloud.UsesLoopback() || !PortFree(CloudPort) {
		return nil, ErrFormUnavailable
	}
	if err := kittools.CheckApp(f.App); err != nil {
		return nil, valise.New("app_invalid", "自己的应用填写有误", "client_id 与 client_secret 各填一行；不用自己的应用就都留空")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	t, porterPath, done, err := s.loginTransport(ctx, host)
	if err != nil {
		return nil, err
	}
	defer done()
	c, hello, err := ssh.Dial(ctx, t, porterPath, s.Version)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if hello == nil || !slices.Contains(hello.Capabilities, vaultrpc.CloudFormCapability) {
		return nil, ErrFormUnavailable
	}
	if s.Tiered(host) && s.server(host).DailyTTY {
		if err := s.proveOwner(ctx, c, host); err != nil {
			return nil, err
		}
	}
	forget := func() {
		fctx, fcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer fcancel()
		c.Call(fctx, vaultrpc.MethodCloudForget, vaultrpc.CloudForgetParams{Name: name}, nil)
	}
	step := func(state, result string) (*kittools.Question, error) {
		var q kittools.Question
		p := vaultrpc.CloudStepParams{Name: name, Cloud: *cloud, State: state, Result: result}
		if state == "" {
			p.App = f.App
		}
		err := c.Call(ctx, vaultrpc.MethodCloudStep, p, &q)
		return &q, err
	}
	browser := func(state string) (*kittools.Question, *LoginStatus, error) {
		var prep login.PrepareResult
		if err := c.Call(ctx, vaultrpc.MethodCloudPrepare, vaultrpc.CloudPrepareParams{Name: name, Cloud: *cloud, State: state}, &prep); err != nil {
			return nil, nil, err
		}
		in, inW := io.Pipe()
		defer inW.Close()
		var out bytes.Buffer
		st, err := s.relaySession(ctx, c, t, porterPath, prep, relayOpts{kind: login.KindLoopbackOAuth, pattern: cloudLinkPattern, timeout: 10 * time.Minute},
			LoginIO{Stdin: in, Stdout: &out, Cols: 200, Rows: 40, Status: f.Status, ConfirmOpen: f.ConfirmOpen, OpenBrowser: f.OpenBrowser})
		if err != nil {
			return nil, st, err
		}
		if st == nil || st.ExitCode != 0 {
			return nil, st, valise.New("cloud_login_failed", "云盘登录没有完成（rclone 退出码 "+strconv.Itoa(exitOf(st))+"）", "重新登录；浏览器里的授权要在 10 分钟内完成")
		}
		q, err := kittools.ParseQuestion(out.Bytes())
		if err != nil {
			return nil, st, valise.New("rclone_failed", "授权完成了，但 rclone 没有给出下一步", "重新登录")
		}
		return q, st, nil
	}
	res := &CloudResult{}
	err = runForm(ctx, f, step, func(state string) (*kittools.Question, error) {
		q, st, err := browser(state)
		res.Status = st
		return q, err
	})
	if err != nil {
		forget()
		return res, err
	}
	target := CloudTarget(name)
	chk, err := s.CheckTarget(ctx, host, vaultrpc.TargetCheckParams{Target: &target})
	if err == nil && !chk.Reachable {
		err = valise.New("cloud_unreachable", "登录完成了，但服务器读不了这个云盘："+chk.Reason, "重新登录；"+chk.Suggestion).WithDetail(chk.Detail)
	}
	if err == nil {
		err = s.TargetSet(ctx, host, target)
	}
	if err != nil {
		forget()
		return res, err
	}
	res.Saved = true
	return res, nil
}

// runForm walks rclone's questions until it is done.
func runForm(ctx context.Context, f CloudForm, step func(state, result string) (*kittools.Question, error), browser func(state string) (*kittools.Question, error)) error {
	q, err := step("", "")
	for n := 0; err == nil && q.State != ""; n++ {
		if n >= maxSteps {
			return valise.New("rclone_failed", "rclone 的提问没有结束", "重新登录；仍不行请报告")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if q.Name == kittools.QuestionIsLocal {
			q, err = browser(q.State)
			continue
		}
		answer, fixed := answerFixed(*q)
		if !fixed || q.Error != "" {
			if f.Ask == nil {
				return ErrFormUnavailable
			}
			if answer, err = f.Ask(*q); err != nil {
				return err
			}
		}
		q, err = step(q.State, answer)
	}
	return err
}

// CloudLoginHereForm logs this computer in to a cloud drive with the
// form: rclone runs here and opens this computer's browser itself.
func (s *Service) CloudLoginHereForm(ctx context.Context, name, cloudID string, f CloudForm) error {
	cloud, err := s.cloudEntry(cloudID)
	if err != nil {
		return err
	}
	if cloud.Pending() {
		return valise.New("cloud_pending", cloud.Name+" 的登录与复制还没有在这个版本上测试", "选择已核实的云盘")
	}
	if !cloud.UsesLoopback() || !PortFree(CloudPort) {
		return ErrFormUnavailable
	}
	if err := kittools.CheckApp(f.App); err != nil {
		return valise.New("app_invalid", "自己的应用填写有误", "client_id 与 client_secret 各填一行；不用自己的应用就都留空")
	}
	if !repoNameRe.MatchString(name) || s.cloudHere(name) != nil || s.IsStation(name) {
		return valise.New("cloud_name_invalid", "名字无效或已被使用："+name, "换一个只含小写字母、数字和连字符的名字")
	}
	local := s.FindLocalRclone(ctx)
	if local == nil {
		return valise.New("rclone_local_missing", "这台电脑没有 rclone", "先准备 rclone")
	}
	if err := s.ensureLocalRcloneConfig(); err != nil {
		return err
	}
	remote := vaultrpc.CloudRemote(name)
	if remotes, err := s.localRemotes(ctx, local.Path); err == nil && slices.Contains(remotes, remote) {
		s.forgetHere(local.Path, name)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	run := func(timeout time.Duration, args []string) (*kittools.Question, error) {
		rctx, rcancel := context.WithTimeout(ctx, timeout)
		defer rcancel()
		cmd := exec.CommandContext(rctx, local.Path, append(args, "--config", s.LocalRcloneConfig())...)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return nil, valise.New("cloud_login_failed", "这台电脑上的 rclone 没能继续登录", "重新登录").WithDetail(err.Error())
		}
		return kittools.ParseQuestion(out.Bytes())
	}
	step := func(state, result string) (*kittools.Question, error) {
		if state == "" {
			return run(time.Minute, cloud.FormArgs(remote, f.App))
		}
		return run(time.Minute, kittools.ContinueArgs(remote, state, result))
	}
	browser := func(state string) (*kittools.Question, error) {
		if f.Status != nil {
			f.Status(LoginStatus{State: LoginBrowserOpened})
		}
		return run(10*time.Minute, kittools.ContinueArgs(remote, state, "true"))
	}
	if err := runForm(ctx, f, step, browser); err != nil {
		s.forgetHere(local.Path, name)
		return err
	}
	st, err := s.hereStation(ctx, name)
	if err == nil {
		_, _, err = stationsync.With(ctx, localRunner{}, st, func(r stationsync.Remote) error { return r.Probe(ctx, st.Dest(cloudFolder)) })
	}
	if err != nil {
		s.forgetHere(local.Path, name)
		return err
	}
	clouds := append(append([]config.Cloud{}, s.Config.Clouds...), config.Cloud{Name: name, Cloud: cloudID})
	if err := config.SetClouds(s.Dirs.Data, clouds); err != nil {
		s.forgetHere(local.Path, name)
		return err
	}
	s.Config.Clouds = clouds
	return nil
}
