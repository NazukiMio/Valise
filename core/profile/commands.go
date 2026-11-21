package profile

import (
	"net/url"
	"path"
	"strings"
)

// CommandRef is one command or script a profile may make Valise run (or
// trigger) on a server, for audit and review.
type CommandRef struct {
	Field  string   `json:"field"`
	Label  string   `json:"label"`
	Tier   string   `json:"tier,omitempty"` // daily | owner | admin
	Argv   []string `json:"argv,omitempty"`
	URL    string   `json:"url,omitempty"`
	SHA256 string   `json:"sha256,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// Commands lists every command a profile can run on a server, in a stable
// order. agent_kit.mcp_*_argv are only printed to the user and never run, so
// they are not listed.
func Commands(p *Profile) []CommandRef {
	if p == nil {
		return nil
	}
	var out []CommandRef
	add := func(field, label, tier string, a []string) {
		if len(a) > 0 {
			out = append(out, CommandRef{Field: field, Label: label, Tier: tier, Argv: append([]string(nil), a...)})
		}
	}
	if p.Detect != nil {
		add("detect.version_argv", "检测版本", "daily", p.Detect.VersionArgv)
	}
	for i, r := range p.Roots {
		add(idx("roots", i, "discover_argv"), "查找目录 "+r.ID, "daily", r.DiscoverArgv)
	}
	if p.Body != nil {
		add("body.bootstrap_argv", "重建身体", "owner", p.Body.BootstrapArgv)
	}
	if p.Service != nil {
		add("service.install_argv", "安装服务", "admin", p.Service.InstallArgv)
	}
	for i, e := range p.Entries {
		if e.Auth != nil {
			add(idx("entries", i, "auth.url_argv"), "管理页令牌 "+e.ID, "owner", e.Auth.URLArgv)
			add(idx("entries", i, "auth.token_argv"), "管理页令牌 "+e.ID, "owner", e.Auth.TokenArgv)
		}
		if e.PortChange != nil {
			for j, st := range e.PortChange.Steps {
				add(idx("entries", i, "port_change.steps")+"["+itoa(j)+"]", "修改入口端口 "+e.ID, "admin", st)
			}
		}
	}
	if p.Health != nil {
		for i, c := range p.Health.Checks {
			add(idx("health.checks", i, "argv"), "健康检查", "daily", c.Argv)
		}
	}
	for i, l := range p.Login {
		add(idx("login", i, "argv"), "登录 "+l.ID, "owner", l.Argv)
	}
	if p.Native != nil {
		add("native.export_argv", "导出", "daily", p.Native.ExportArgv)
	}
	add("native_cli", "原生引导", "admin", p.NativeCLI)
	add("native_session_cli", "原生会话", "admin", p.NativeSessionCLI)
	for i, a := range p.Actions {
		add(idx("actions", i, "argv"), "管理动作 "+a.ID, a.AccessTier(), a.Argv)
		for j, st := range a.Steps {
			add(idx("actions", i, "steps")+"["+itoa(j)+"].argv", "管理动作 "+a.ID+"."+st.ID, a.AccessTier(), st.Argv)
		}
	}
	if p.ApprovalCheck != nil {
		add("approval_check.argv", "审批状态检查", "daily", p.ApprovalCheck.Argv)
	}
	if p.Identity != nil {
		add("identity.argv", "读取 Agent 名字", "daily", p.Identity.Argv)
	}
	if in := p.Install; in != nil {
		for i, r := range in.Requires {
			for j, ri := range r.Install {
				add(idx("install.requires", i, "install")+"["+itoa(j)+"].argv",
					"安装依赖 "+r.Binary+" ("+strings.Join(ri.OSID, ",")+")", "admin", ri.Argv)
			}
		}
		for i, st := range in.Steps {
			f := idx("install.steps", i, "")
			switch st.Kind {
			case StepArgv:
				add(f+".argv", "安装步骤", "admin", st.Argv)
			case StepNPMGlobal:
				out = append(out, CommandRef{Field: f, Label: "安装 npm 包", Tier: "admin",
					Argv: []string{"npm", "install", "-g", "--prefix", "${HOME}/.local", st.Package + "@" + st.Version},
					Note: st.Integrity})
			case StepScript:
				name := "script.sh"
				if u, err := url.Parse(st.URL); err == nil && u.Path != "" {
					if b := path.Base(u.Path); b != "/" && b != "." {
						name = b
					}
				}
				argv := append(append([]string{}, st.Interpreter...), "<downloaded:"+name+">")
				argv = append(argv, st.Args...)
				out = append(out, CommandRef{Field: f, Label: "安装脚本", Tier: "admin", Argv: argv, URL: st.URL, SHA256: st.SHA256})
			case StepPathAdd:
				out = append(out, CommandRef{Field: f, Label: "加入 PATH", Tier: "admin", Note: st.Dir})
			}
		}
	}
	return out
}

func idx(base string, i int, tail string) string {
	s := base + "[" + itoa(i) + "]"
	if tail != "" {
		s += "." + tail
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

var shellNames = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "csh": true, "tcsh": true}

// checkShellWrapper reports why argv runs a command string through a shell,
// or "".
func checkShellWrapper(argv Argv) string {
	for _, a := range argv {
		if strings.Contains(a, "$(") || strings.Contains(a, "`") {
			return "禁止在参数中使用命令替换（$( 或反引号）"
		}
	}
	rest := []string(argv)
	for len(rest) > 0 {
		switch path.Base(rest[0]) {
		case "env":
			rest = rest[1:]
			for len(rest) > 0 && (strings.HasPrefix(rest[0], "-") || strings.Contains(rest[0], "=")) {
				rest = rest[1:]
			}
		case "sudo", "doas", "su", "pkexec":
			rest = rest[1:]
			for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
				flag := rest[0]
				rest = rest[1:]
				if (flag == "-u" || flag == "-g") && len(rest) > 0 {
					rest = rest[1:]
				}
			}
		case "nohup", "nice", "time", "command":
			rest = rest[1:]
		default:
			goto done
		}
	}
done:
	if len(rest) == 0 || !shellNames[path.Base(rest[0])] {
		return ""
	}
	for _, a := range rest[1:] {
		if a == "-c" || (len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.Contains(a, "c")) {
			return "禁止通过 shell 解释器（sh -c 等）执行命令字符串"
		}
	}
	return ""
}
