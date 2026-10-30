package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"valise/core/valise"
	"valise/ui/service"
)

func (c *cli) doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	r, err := c.svc.Doctor(ctx, pos[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(r)
	}
	w := c.out
	target := r.SSH.HostName
	if r.SSH.User != "" {
		target = r.SSH.User + "@" + target
	}
	if r.SSH.Port != 0 {
		target = fmt.Sprintf("%s:%d", target, r.SSH.Port)
	}
	fmt.Fprintf(w, "服务器：%s（%s）\n", r.Host, target)
	if r.SSH.ProxyJump != "" {
		fmt.Fprintf(w, "跳板：%s\n", r.SSH.ProxyJump)
	}
	if r.ReusesUserControlMaster {
		fmt.Fprintln(w, "连接复用：沿用你在 SSH 配置中设置的 ControlMaster")
	} else {
		fmt.Fprintln(w, "连接复用：Valise 自带的 ControlMaster（空闲 60 秒后关闭）")
	}
	fmt.Fprintf(w, "系统与架构：%s\n", r.Platform)
	if !r.PorterFound {
		fmt.Fprintln(w, "porter：未部署")
	} else {
		fmt.Fprintf(w, "porter：%s（版本 %s，协议 %s）\n", r.PorterPath, r.PorterVersion, r.Protocol)
	}
	if r.Home != "" {
		fmt.Fprintf(w, "HOME：%s\n", r.Home)
	}
	if len(r.Tools) > 0 {
		fmt.Fprintln(w, "可用工具：")
		for _, t := range r.Tools {
			mark, note := "✗", "未找到"
			switch {
			case t.Available:
				mark, note = "✓", t.Path
			case t.Path != "":
				note = "不可用 " + t.Path
			}
			if t.Detail != "" {
				note += "（" + t.Detail + "）"
			}
			fmt.Fprintf(w, "  %s %-17s %s\n", mark, t.Name, note)
		}
	}
	if r.PorterFound && r.Problem == nil {
		if len(r.PendingOps) == 0 {
			fmt.Fprintln(w, "未完成的交换：无")
		} else {
			fmt.Fprintf(w, "未完成的交换：%d 个，需要你决定继续完成或退回：\n", len(r.PendingOps))
			for _, op := range r.PendingOps {
				fmt.Fprintf(w, "  %s\n", op.OpID)
			}
		}
	}
	if r.Problem != nil {
		fmt.Fprintln(w)
		c.printProblem(r.Problem)
	}
	return nil
}

func (c *cli) deploy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	var src service.DeploySource
	fs.StringVar(&src.LocalPath, "local", "", "")
	fs.StringVar(&src.ReleaseTag, "release", "", "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	r, err := c.svc.DeployPorter(ctx, pos[0], src)
	if err != nil {
		return err
	}
	from := "本地构建产物 " + src.LocalPath
	if src.ReleaseTag != "" {
		from = "Release " + src.ReleaseTag + "（签名与 SHA-256 已校验）"
	}
	fmt.Fprintf(c.out, "已部署 porter 到 %s：%s\n来源：%s\n版本：%s（协议 %s，%s）\nSHA-256：%s\n",
		r.Host, r.Porter.Path, from, r.Porter.Info.Version, r.Porter.Info.Protocol, r.Platform, r.Porter.Info.SelfSHA256)
	return nil
}

func (c *cli) key(sub string, args []string) error {
	fs := flag.NewFlagSet("key", flag.ContinueOnError)
	file := fs.String("file", "", "")
	want := map[string]int{"init": 0, "import": 0, "show": 0, "export-kit": 1, "import-kit": 1}
	n, ok := want[sub]
	if !ok {
		return errUsage
	}
	pos, err := parseArgs(fs, args, n)
	if err != nil {
		return err
	}
	var info *service.KeyInfo
	switch sub {
	case "init":
		pass, err := c.newPassphrase()
		if err != nil {
			return err
		}
		if info, err = c.svc.KeyInit(pass); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "已生成密钥。请立即导出离线恢复套件（valise key export-kit <目录>），并另外抄下指纹。")
	case "import":
		if *file == "" {
			return errUsage
		}
		id, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		pass, err := c.newPassphrase()
		if err != nil {
			return err
		}
		if info, err = c.svc.KeyImport(id, pass); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "已导入密钥。")
	case "show":
		if info, err = c.svc.KeyShow(); err != nil {
			return err
		}
	case "export-kit":
		if info, err = c.svc.ExportKit(pos[0]); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已导出离线恢复套件到 %s。请离线保存，并把下面的指纹另外抄写下来。\n", pos[0])
	case "import-kit":
		pass, err := c.passphrase("恢复口令：")
		if err != nil {
			return err
		}
		if info, err = c.svc.ImportKit(pos[0], pass); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "已从恢复套件导入。请核对下面的指纹与你的纸质记录完全一致。")
	}
	fmt.Fprintf(c.out, "公钥：%s\n指纹：%s\n", info.Recipient, info.Fingerprint)
	return nil
}

func (c *cli) newPassphrase() (string, error) {
	p1, err := c.passphrase("设置口令（至少 12 个字符）：")
	if err != nil {
		return "", err
	}
	if !c.interactive() {
		return p1, nil
	}
	p2, err := c.passphrase("再次输入口令：")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", valise.New("passphrase_mismatch", "两次输入的口令不一致", "重新运行命令并输入相同的口令")
	}
	return p1, nil
}
