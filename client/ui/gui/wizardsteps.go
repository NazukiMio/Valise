package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/valise"
	"valise/ui/service"
)

// The wizard's later steps: identity, login, preparing, and the path from
// ~/.ssh/config (docs/UI.md 7.5).

func (a *App) openImport(w *wizard) {
	w.importing, w.hosts, w.hostsErr = true, nil, nil
	a.run(func() {
		hosts, err := a.svc.ImportableHosts()
		a.update(func() { w.hosts, w.hostsErr = hosts, err })
	})
}

// importStep lists the hosts of ~/.ssh/config not added yet; choosing one
// goes straight to preparing it, logging in with the user's own setup.
func (a *App) importStep(c *ui.Context, w *wizard) {
	card(ui.Column(c)).Gap(12).Padding(22).Children(func() {
		ui.Text(c, "从已有 SSH 配置导入").FontSize(20).Bold().Font(cur.Fonts.Display)
		muted(ui.Text(c, "这些是 ~/.ssh/config 里还没加入的主机。登录用你自己的 SSH 设置，Valise 不另办通行证。")).Wrap()
		switch {
		case w.hostsErr != nil:
			errorBox(c, w.hostsErr, a.fold("wiz-hosts"))
		case w.hosts == nil:
			ui.Spinner(c)
		case len(w.hosts) == 0:
			muted(ui.Text(c, "没有可以导入的主机。")).FontSize(13)
		}
		for _, h := range w.hosts {
			ui.Row(c.Key(h)).Gap(10).AlignItems(ui.Center).Padding(8, 0).Children(func() {
				ui.Icon(c, icons.server).FontSize(16).TextColor(palette.TextSub)
				mono(ui.Text(c, h)).Grow(1)
				ui.Button(c, "导入并准备").OnClick(func() { a.runImport(w, h) })
			})
		}
		a.porterChoice(c, w)
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "‹ 改为手动填写").OnClick(func() { w.importing = false })
		})
	})
}

func (a *App) identityStep(c *ui.Context, w *wizard) {
	srv := w.server()
	card(ui.Column(c)).Gap(14).Padding(22).Children(func() {
		ui.Text(c, "这是你的服务器吗？").FontSize(20).Bold().Font(cur.Fonts.Display)
		muted(ui.Text(c, "第一次连接，先核对它的指纹，防止连到别人冒充的机器。")).FontSize(13).Wrap()
		ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
			muted(ui.Text(c, knownHostsName(srv.HostName, srv.Port)+" 报出的指纹"))
			for _, k := range w.keys {
				ui.Row(c.Key(k.Fingerprint)).Gap(12).Children(func() {
					ui.Text(c, strings.ToUpper(strings.TrimPrefix(k.Type, "ssh-"))).FontSize(12).Bold().Width(90)
					mono(ui.Text(c, k.Fingerprint)).FontSize(12).Wrap().Grow(1)
				})
			}
		})
		ui.Text(c, "怎么核对？在服务器上（云服务商的网页控制台也可以）运行 ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub，看结果是否和上面一致。").
			FontSize(12).Wrap()
		if w.mismatch {
			errorBox(c, valise.New("host_key_rejected", "已停止，没有记下任何东西",
				"可能是服务器重装过系统、地址填错，或有人冒充；查清后再重新添加"), a.fold("wiz-mm"))
		}
		if w.err != nil {
			errorBox(c, w.err, a.fold("wiz-err"))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "返回").OnClick(func() { w.step, w.err, w.mismatch = wzConnect, nil, false })
			if w.mismatch {
				return
			}
			ui.Button(c, "不一致，停止").OnClick(func() { w.mismatch = true })
			if w.busy {
				ui.Spinner(c)
				return
			}
			ui.PrimaryButton(c, "一致，继续").OnClick(func() { a.trustKeys(w) })
		})
	})
}

func knownHostsName(host string, port int) string {
	if port == 0 {
		port = 22
	}
	return host + ":" + itoa(port)
}

func (a *App) trustKeys(w *wizard) {
	srv, keys := w.server(), w.keys
	w.busy, w.err = true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := a.svc.TrustHostKeys(ctx, srv, keys)
		a.update(func() {
			w.busy, w.err = false, err
			if err == nil {
				w.step = wzLogin
			}
		})
	})
}

// loginStep says what the login will do, offers the admin pass, and starts
// adding; OpenSSH's own question then comes up as a dialog.
func (a *App) loginStep(c *ui.Context, w *wizard) {
	srv := w.server()
	card(ui.Column(c)).Gap(14).Padding(22).Children(func() {
		ui.Text(c, "登录 "+srv.HostName).FontSize(20).Bold().Font(cur.Fonts.Display)
		how := "点“登录”后，OpenSSH 会问服务器密码。密码直接交给它，只登录这一次，Valise 不保存。"
		if w.login != "password" {
			how = "点“登录”后，OpenSSH 用你选的私钥登录；私钥有口令时会问你。Valise 不保存口令。"
		}
		ui.Text(c, how).FontSize(13).Wrap()
		passLine(c, "用户通行证", "一定会办。只能查看和备份，不用密码，所以定时备份不用你在场。")
		want := w.admin == "yes"
		if ui.Checkbox(c, &want, "同时办一张管理员通行证").Changed() {
			w.admin = map[bool]string{true: "yes"}[want]
		}
		muted(ui.Text(c, "以后改动服务器（启动服务、安装、修复）时输管理口令就行，不用每次输服务器密码。不办的话，每次改动服务器时用服务器密码临时登录一次。")).Wrap()
		if want {
			labeled(c, "管理口令", func() {
				ui.TextInput(c, &w.adminPass).Password().Label("管理口令").Placeholder("至少 12 个字符，和 Valise 口令不同")
			})
			labeled(c, "再输一次", func() {
				ui.TextInput(c, &w.adminPass2).Password().Label("再输一次").Placeholder("再输一次管理口令")
			})
			muted(ui.Text(c, "忘了管理口令，可以在服务器详情里注销再重办，不影响备份。")).Wrap()
		}
		a.porterChoice(c, w)
		why := ""
		switch {
		case want && len([]rune(w.adminPass)) < 12:
			why = "管理口令至少 12 个字符"
		case want && w.adminPass != w.adminPass2:
			why = "两次输入的管理口令不一样"
		}
		if why != "" && w.adminPass != "" {
			ui.Text(c, why).FontSize(12).TextColor(palette.Warn)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "取消").OnClick(func() { a.closeWizard() })
			b := ui.PrimaryButton(c, "登录").OnClick(func() { a.runAdd(w) })
			if why != "" {
				b.Disabled(true)
			}
		})
	})
}

func passLine(c *ui.Context, title, sub string) {
	ui.Row(c.Key(title)).Gap(10).Padding(10, 12).Radius(10).Background(palette.Board).Children(func() {
		ui.Text(c, title).Bold().Width(96)
		muted(ui.Text(c, sub)).Wrap().Grow(1)
	})
}

// releaseTag is this client's own release, "" for a development build.
func (a *App) releaseTag() string {
	v := a.svc.Version
	if v == "" || strings.Contains(v, "dev") {
		return ""
	}
	return "v" + strings.TrimPrefix(v, "v")
}

func (a *App) deploySource(w *wizard) service.DeploySource {
	if w.localPorter != "" {
		return service.DeploySource{LocalPath: w.localPorter}
	}
	return service.DeploySource{ReleaseTag: a.releaseTag()}
}

// porterChoice says where porter comes from when the server has none: the
// release of this client's version, or under 更多选项 a local build.
func (a *App) porterChoice(c *ui.Context, w *wizard) {
	ui.Column(c).Gap(6).Children(func() {
		muted(ui.Text(c, "porter 是 Valise 在服务器上的小帮手，用到时由 SSH 唤起，不常驻、不监听端口。")).Wrap()
		switch tag := a.releaseTag(); {
		case w.localPorter != "":
			ui.Text(c, "服务器上没有 porter 时，上传本地构建：").FontSize(12)
			mono(muted(ui.Text(c, w.localPorter))).Wrap()
		case tag != "":
			ui.Text(c, "服务器上没有 porter 时，从 GitHub Release 下载 "+tag+"（与这台电脑上的 Valise 同版本），核对签名与 SHA-256 后上传。").FontSize(12).Wrap()
		default:
			ui.Text(c, "这是开发版 Valise，没有对应的 Release；服务器上没有 porter 时，要在“更多选项”里选本地构建。").FontSize(12).Wrap()
		}
		ui.Collapsible(c, "更多选项", &w.more, func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				muted(ui.Text(c, "上传这台电脑上的本地构建（开发与演练用）")).Grow(1)
				ui.Button(c, "选择文件…").OnClick(func() { a.pickPorter(w) })
				if w.localPorter != "" {
					ui.Button(c, "改回 Release").OnClick(func() { w.localPorter = "" })
				}
			})
		})
	})
}

func (a *App) pickPorter(w *wizard) {
	if a.pickFile == nil {
		return
	}
	a.run(func() {
		p, err := a.pickFile("选择 porter 文件", "你自己构建的 valise-porter（Linux，与服务器同架构）。")
		a.update(func() {
			if err == nil && p != "" {
				w.localPorter = p
			}
		})
	})
}

// runAdd adds the server: the passes, then preparing it, step by step.
func (a *App) runAdd(w *wizard) {
	srv := w.server()
	opts := service.AddServerOptions{Server: srv, Setup: service.SetupOptions{Name: srv.Name, Deploy: a.deploySource(w)}}
	if w.admin == "yes" {
		opts.AdminPassphrase = w.adminPass
	}
	a.prepare(w, srv.Name, func(ctx context.Context, step func(service.SetupStep)) error {
		opts.Setup.Step = step
		return a.svc.AddServer(ctx, opts)
	})
}

func (a *App) runImport(w *wizard, alias string) {
	opts := service.SetupOptions{Name: defaultName(alias), Deploy: a.deploySource(w)}
	a.prepare(w, alias, func(ctx context.Context, step func(service.SetupStep)) error {
		opts.Step = step
		return a.svc.SetupServer(ctx, alias, opts)
	})
}

// prepare runs one way of adding and shows its steps as they come.
func (a *App) prepare(w *wizard, host string, add func(context.Context, func(service.SetupStep)) error) {
	w.step, w.host, w.busy, w.err, w.steps = wzPrepare, host, true, nil, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		err := add(ctx, func(s service.SetupStep) { a.update(func() { w.steps = append(w.steps, s) }) })
		a.update(func() {
			w.busy, w.err = false, err
			a.refreshServers()
			if err != nil {
				return
			}
			w.adminPass, w.adminPass2 = "", ""
			a.refreshAgents()
			if w.fromHome {
				a.router.Push("/servers/" + host + "/agents")
			}
		})
	})
}

func (a *App) closeWizard() {
	a.wiz = nil
	a.router.Push("/servers")
}
