package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"valise/client/porter"
	"valise/client/tunnel"
	"valise/core/enroll"
	"valise/core/install"
	"valise/core/login"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Two-sided port change (docs/LOGIN_RELAY.md section 4.2): with the user's
// consent the harness's entry port and the local port move together to a
// port free on both sides. Only the harness's own commands declared in the
// profile run; Valise edits no configuration file itself.

// portSearch bounds how far above the current port a free one is looked for.
const portSearch = 100

// defaultPortGrace is how long the harness may take to listen on the new
// port when the profile declares no health grace.
const defaultPortGrace = 20 * time.Second

// PortChangePlan is what the confirmation shows.
type PortChangePlan struct {
	Host      string `json:"host"`
	Instance  string `json:"instance"`
	ProfileID string `json:"profile_id"`
	Entry     string `json:"entry"`
	From      int    `json:"from"`
	To        int    `json:"to"`
	// Steps are the harness's commands with the new port filled in.
	Steps   [][]string `json:"steps"`
	Restart string     `json:"restart,omitempty"` // unit restarted after the steps
	Draft   bool       `json:"draft"`
	Grace   time.Duration

	binDirs []string
}

// PlanPortChange checks that the instance's entry supports the change and
// picks the new port: to when given (it must be free on both sides),
// otherwise the first port above the current one free on both sides.
func (s *Service) PlanPortChange(ctx context.Context, host, instance, entryID string, to int) (*PortChangePlan, error) {
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	var entry *profile.Entry
	for i := range target.Profile.Entries {
		if entryID == "" || target.Profile.Entries[i].ID == entryID {
			entry = &target.Profile.Entries[i]
			break
		}
	}
	if entry == nil {
		return nil, valise.New("entry_unknown", "profile "+target.Profile.ID+" 中没有这个入口", "不带 --entry 使用默认入口")
	}
	if entry.PortChange == nil {
		return nil, valise.New("port_change_unsupported", target.Profile.ID+" 的 harness 资料没有声明改端口的方法",
			fmt.Sprintf("关闭本机占用端口 %d 的程序后重试", entry.Port))
	}
	p := &PortChangePlan{Host: host, Instance: instance, ProfileID: target.Profile.ID, Entry: entry.ID, From: entry.Port,
		Draft: target.Profile.Status == profile.StatusDraft, Grace: defaultPortGrace}
	if target.Profile.Detect != nil {
		p.binDirs = target.Profile.Detect.BinDirs
	}
	if h := target.Profile.Health; h != nil && h.GraceSeconds > 0 {
		p.Grace = time.Duration(h.GraceSeconds) * time.Second
	}
	if entry.PortChange.RestartService {
		p.Restart = restartUnit(target.Record, &target.Profile)
		if p.Restart == "" {
			return nil, valise.New("port_change_no_service", "改端口后需要重启服务，但没有找到这个实例的服务", "在原生界面里自己改端口并重启")
		}
	}
	c, err := s.session(ctx, host)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	free := func(port int) (string, error) {
		if port == p.From {
			return "和现在的端口相同", nil
		}
		ln, err := tunnel.Bind(port)
		if err != nil {
			return err.Error(), nil
		}
		ln.Close()
		var res login.ListenResult
		if err := c.Call(ctx, login.MethodListen, login.ListenParams{Port: port}, &res); err != nil {
			return "", err
		}
		if len(res.Addrs) > 0 {
			return fmt.Sprintf("服务器上端口 %d 已有程序监听", port), nil
		}
		return "", nil
	}
	if to != 0 {
		if to < 1024 || to > 65535 {
			return nil, valise.New("entry_port_invalid", "端口必须在 1024 到 65535 之间", "")
		}
		why, err := free(to)
		if err != nil {
			return nil, err
		}
		if why != "" {
			return nil, valise.New("entry_port_taken", fmt.Sprintf("端口 %d 不能用：%s", to, why), "换一个端口，或不填让 Valise 推荐")
		}
		p.To = to
	} else {
		for port := p.From + 1; port <= p.From+portSearch && port <= 65535; port++ {
			why, err := free(port)
			if err != nil {
				return nil, err
			}
			if why == "" {
				p.To = port
				break
			}
		}
		if p.To == 0 {
			return nil, valise.New("entry_port_none_free", fmt.Sprintf("端口 %d 之后的 %d 个端口里没有两边都空闲的", p.From, portSearch), "用 --change-port <端口> 自己指定")
		}
	}
	vars := map[string]string{profile.VarPort: strconv.Itoa(p.To)}
	for _, st := range entry.PortChange.Steps {
		argv := make([]string, len(st))
		for i, a := range st {
			for k, v := range vars {
				a = strings.ReplaceAll(a, "${"+k+"}", v)
			}
			argv[i] = a // ${HOME} is expanded by porter exec
		}
		p.Steps = append(p.Steps, argv)
	}
	return p, nil
}

// restartUnit is the instance's loaded service unit, or the profile's first
// candidate.
func restartUnit(rec *enroll.Record, p *profile.Profile) string {
	if rec != nil {
		for _, sv := range rec.Service {
			if sv.LoadState == "loaded" {
				return sv.Unit
			}
		}
	}
	if p.Service != nil && len(p.Service.Candidates) > 0 {
		return p.Service.Candidates[0].Unit
	}
	return ""
}

// PortChangeOptions control ChangeEntryPort.
type PortChangeOptions struct {
	// SkipSeal skips the escape pod taken before the configuration changes.
	SkipSeal bool
	IO       InstallIO
	Progress func(string)
}

// ChangeEntryPort runs a confirmed plan: escape pod, the harness's own
// commands on an admin connection, service restart, wait for the new port,
// then the new port is recorded on the instance. It stops at the first
// failure and says where things stand.
func (s *Service) ChangeEntryPort(ctx context.Context, p *PortChangePlan, o PortChangeOptions) error {
	progress := func(msg string) {
		if o.Progress != nil {
			o.Progress(msg)
		}
	}
	changed := func(err error, what string) error {
		ue, ok := valise.As(err)
		if !ok {
			ue = valise.New("port_change_failed", err.Error(), "")
		}
		hint := what + "。可以在原生界面里检查 harness 的设置"
		if !o.SkipSeal {
			hint += "，或从刚才的逃生舱原地着陆，回到改动之前"
		}
		return valise.New(ue.Code, ue.Reason, hint).WithDetail(ue.Detail)
	}
	if !o.SkipSeal {
		progress("建立逃生舱（改动之前的配置）")
		rep, err := s.Seal(ctx, p.Host, vaultrpc.SealParams{InstanceID: p.Instance, Tier: vault.TierSoulSecrets,
			Reason: fmt.Sprintf("改端口之前（%s %d → %d）", p.Entry, p.From, p.To)})
		if err != nil {
			return err
		}
		progress("逃生舱已建立：" + rep.SnapshotID)
	}
	t, done, err := s.adminTransport(ctx, p.Host)
	if err != nil {
		return err
	}
	defer done()
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return err
	}
	audit := install.AuditParams{Op: install.AuditPortChange, PlanID: "port-change-" + p.Entry, ProfileID: p.ProfileID, Instance: p.Instance, Result: "failed"}
	defer func() {
		_ = s.withSession(context.WithoutCancel(ctx), p.Host, func(c caller) error { return c.Call(ctx, install.MethodAudit, audit, nil) })
	}()
	for i, st := range p.Steps {
		progress(fmt.Sprintf("第 %d/%d 条命令：%s", i+1, len(p.Steps), strings.Join(st, " ")))
		code, err := runTTY(ctx, t, found.Path, p.binDirs, st, o.IO)
		audit.Steps = append(audit.Steps, install.StepAudit{Kind: "port_change", Command: strings.Join(st, " "), Exit: code})
		if err != nil {
			return err
		}
		if code != 0 {
			e := valise.New("port_change_step_failed", fmt.Sprintf("改端口命令失败（退出码 %d）：%s", code, strings.Join(st, " ")), "")
			if i == 0 {
				return valise.New(e.Code, e.Reason, "第一条命令就失败了，配置可能没有改动；看上面的输出找原因")
			}
			return changed(e, "前面的命令已经执行，配置可能已部分改动")
		}
	}
	c, err := s.session(ctx, p.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if p.Restart != "" {
		progress("重启服务 " + p.Restart)
		if err := c.Call(ctx, revive.MethodService, revive.ServiceParams{Unit: p.Restart, Action: "stop"}, nil); err != nil {
			return changed(err, "配置已经改动，但服务没能停止")
		}
		if err := c.Call(ctx, revive.MethodService, revive.ServiceParams{Unit: p.Restart, Action: "start"}, nil); err != nil {
			return changed(err, "配置已经改动，但服务没能启动")
		}
	}
	progress(fmt.Sprintf("等待服务在端口 %d 上监听", p.To))
	deadline := time.Now().Add(p.Grace)
	for {
		var res login.ListenResult
		if err := c.Call(ctx, login.MethodListen, login.ListenParams{Port: p.To}, &res); err != nil {
			return err
		}
		if len(res.Addrs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			return changed(valise.New("port_change_not_listening", fmt.Sprintf("命令都已执行，但 %s 秒内服务器上没有程序监听端口 %d", strconv.Itoa(int(p.Grace.Seconds())), p.To), ""),
				"配置已经改动")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	var set enroll.EntryPortSetResult
	if err := c.Call(ctx, enroll.MethodEntryPortSet, enroll.EntryPortSetParams{InstanceID: p.Instance, Entry: p.Entry, Port: p.To}, &set); err != nil {
		return changed(err, fmt.Sprintf("服务已经改用端口 %d，但 Valise 没能记下新端口", p.To))
	}
	audit.Result = "done"
	progress(fmt.Sprintf("已改用端口 %d，以后打开管理页都用这个端口", p.To))
	return nil
}
