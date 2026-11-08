package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"valise/client/ssh"
	"valise/core/kittools"
	"valise/core/login"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// Cloud drives (docs/ARCHITECTURE.md 7.3, docs/LOGIN_RELAY.md section 7).

// CloudPort is rclone's loopback login port.
const CloudPort = 53682

// cloudFolder is the folder Valise uses inside a drive.
const cloudFolder = "valise-vault/"

// Clouds lists the signed list's cloud drives.
func (s *Service) Clouds() ([]kittools.Cloud, error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	if set.Kit == nil || set.Kit.ByID("rclone") == nil {
		return nil, valise.New("kit_tools_none", "已安装的资料包里没有云盘清单", "安装或更新资料包：valise profiles install")
	}
	return set.Kit.ByID("rclone").Clouds, nil
}

func (s *Service) cloudEntry(id string) (*kittools.Cloud, error) {
	list, err := s.Clouds()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, valise.New("cloud_unknown", "云盘清单里没有 "+id, "运行 valise cloud list 查看可选的云盘")
}

// CloudTarget is the saved location of a cloud drive.
func CloudTarget(name string) vaultrpc.Target {
	return vaultrpc.Target{Name: name, Kind: "rclone", Dest: vaultrpc.CloudRemote(name) + ":" + cloudFolder}
}

// CloudOptions control a cloud login.
type CloudOptions struct {
	// AllowPending uses a cloud not yet tested with this version.
	AllowPending bool
	// Headless uses rclone's own headless login.
	Headless bool
}

// CloudLoginIO is a login relay plus the headless step: ConfirmAuthorize
// is asked before Valise runs `rclone authorize` on this computer.
type CloudLoginIO struct {
	LoginIO
	ConfirmAuthorize func(cmdline string) bool
}

// CloudResult is a finished server login.
type CloudResult struct {
	Status   *LoginStatus `json:"status"`
	Headless bool         `json:"headless"`
	Saved    bool         `json:"saved"`
}

// PortFree reports whether a loopback port can be bound here.
func PortFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// cloudLinkPattern is the only link a cloud login relays: rclone's own
// loopback page, which then redirects to the drive's sign-in. Other links
// rclone prints (help pages) are left on the screen.
var cloudLinkPattern = `(http://127\.0\.0\.1:` + strconv.Itoa(CloudPort) + `/auth\?state=[A-Za-z0-9_-]+)`

var authorizeRe = regexp.MustCompile(`rclone authorize "([a-z0-9]+)"(?: "([A-Za-z0-9_=-]+)")?`)

// CloudLoginServer logs host in to a cloud drive (the server keeps the
// authorization in Valise's rclone config) and saves the location after a
// read-only test. A failed login leaves nothing behind on the server.
func (s *Service) CloudLoginServer(ctx context.Context, host, name, cloudID string, o CloudOptions, lio CloudLoginIO) (*CloudResult, error) {
	cloud, err := s.cloudEntry(cloudID)
	if err != nil {
		return nil, err
	}
	if cloud.Pending() && !o.AllowPending {
		return nil, valise.New("cloud_pending", cloud.Name+" 的登录与复制还没有在这个版本上测试", "确实要试用时加 --allow-pending；出现问题请报告")
	}
	headless := o.Headless || !cloud.UsesLoopback() || !PortFree(CloudPort)
	var local *LocalRclone
	if headless {
		if local = s.FindLocalRclone(ctx); local == nil {
			return nil, valise.New("rclone_local_missing", "这次要在这台电脑上完成授权，但这台电脑还没有 rclone", "先运行 valise cloud rclone 准备 rclone，再重试")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
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
	if err := n2Compatibility(hello, vaultrpc.MethodCloudPrepare, nil); err != nil {
		return nil, err
	}
	if s.Tiered(host) && s.server(host).DailyTTY {
		if err := s.proveOwner(ctx, c, host); err != nil {
			return nil, err
		}
	}
	var prep login.PrepareResult
	if err := c.Call(ctx, vaultrpc.MethodCloudPrepare, vaultrpc.CloudPrepareParams{Name: name, Cloud: *cloud, Fallback: headless}, &prep); err != nil {
		return nil, err
	}
	res := &CloudResult{Headless: headless}
	var once sync.Once
	watch := func(recent string) {
		m := authorizeRe.FindStringSubmatch(recent)
		if m == nil || m[1] != cloud.RcloneType || local == nil {
			return
		}
		once.Do(func() {
			args := []string{"authorize", m[1]}
			if m[2] != "" {
				args = append(args, m[2])
			}
			line := "rclone " + strings.Join(args, " ")
			if lio.ConfirmAuthorize != nil && !lio.ConfirmAuthorize(line) {
				return
			}
			go s.authorizeHere(ctx, local, args, lio.Stdout)
		})
	}
	status, err := s.relaySession(ctx, c, t, porterPath, prep, relayOpts{kind: login.KindLoopbackOAuth, pattern: cloudLinkPattern, timeout: 10 * time.Minute, watch: watch}, lio.LoginIO)
	res.Status = status
	forget := func() {
		fctx, fcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer fcancel()
		c.Call(fctx, vaultrpc.MethodCloudForget, vaultrpc.CloudForgetParams{Name: name}, nil)
	}
	if err != nil || status == nil || status.ExitCode != 0 {
		forget()
		if err == nil {
			err = valise.New("cloud_login_failed", "云盘登录没有完成（rclone 退出码 "+fmt.Sprint(exitOf(status))+"）", "重新登录；浏览器里的授权要在 10 分钟内完成")
		}
		return res, err
	}
	target := CloudTarget(name)
	chk, err := s.CheckTarget(ctx, host, vaultrpc.TargetCheckParams{Target: &target})
	if err == nil && !chk.Reachable {
		err = valise.New("cloud_unreachable", "登录完成了，但服务器读不了这个云盘："+chk.Reason, "重新登录；"+chk.Suggestion).WithDetail(chk.Detail)
	}
	if err != nil {
		forget()
		return res, err
	}
	if err := s.TargetSet(ctx, host, target); err != nil {
		forget()
		return res, err
	}
	res.Saved = true
	return res, nil
}

func exitOf(st *LoginStatus) int {
	if st == nil {
		return -1
	}
	return st.ExitCode
}

// authorizeHere runs rclone's headless authorization step on this
// computer; its output goes to the user only.
func (s *Service) authorizeHere(ctx context.Context, local *LocalRclone, args []string, out io.Writer) {
	if err := s.ensureLocalRcloneConfig(); err != nil {
		fmt.Fprintln(out, "\r\n[Valise] 无法准备 rclone 配置："+err.Error())
		return
	}
	fmt.Fprintln(out, "\r\n[Valise] 在这台电脑上运行 rclone 授权；完成后把它打印的结果复制、粘贴到上面服务器的提示里。")
	cmd := exec.CommandContext(ctx, local.Path, append(args, "--config", s.LocalRcloneConfig())...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(out, "\r\n[Valise] 这台电脑上的 rclone 授权没有完成："+err.Error())
	}
}

// CloudRemove removes a cloud location from host together with the
// server's login. The drive's own authorization must be revoked by the
// user in the account settings; backups on the drive stay.
func (s *Service) CloudRemove(ctx context.Context, host, name string) error {
	return s.call(ctx, host, vaultrpc.MethodCloudForget, vaultrpc.CloudForgetParams{Name: name, RemoveTarget: true}, nil)
}
