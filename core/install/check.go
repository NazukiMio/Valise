package install

import (
	"path"
	"regexp"
	"strings"
)

// Basic command checks (docs/INSTALL.md section 7). Commands are argv, never
// shell text. The checks only catch obvious problems; a command without
// findings is "未发现基础疑点", never "safe".

// Finding is one hit of the checks.
type Finding struct {
	// Refuse marks a command that is never run, in any mode.
	Refuse bool   `json:"refuse"`
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// Finding rules.
const (
	RuleHidden      = "hidden" // command substitution, encoded content
	RulePipeToShell = "pipe_to_shell"
	RuleDestroy     = "destroy" // wide deletion, system paths, disks
	RuleSecurityOff = "security_off"
	RulePrivilege   = "privilege"
	RuleSensitive   = "sensitive_config"
	RuleDownload    = "download"
	RuleInline      = "inline_code"
	RuleDelete      = "delete"
)

// NoFindings is what the plan shows when nothing was found.
const NoFindings = "未发现基础疑点"

var (
	reDecode     = regexp.MustCompile(`base64\s+(-d|--decode|-D)|xxd\s+-r|openssl\s+(enc\s+)?-d|\\x[0-9a-fA-F]{2}`)
	rePipeShell  = regexp.MustCompile(`(curl|wget|fetch)\b[^|]*\|\s*(sudo\s+)?(env\s+)?(ba|z|da|k|c|tc|fi)?sh\b|(curl|wget)\b[^|]*\|\s*(sudo\s+)?(python\d*|perl|ruby|node|php)\b`)
	reInlineBad  = regexp.MustCompile(`\b(curl|wget|eval|exec)\b|base64|\|\s*(ba|z|da)?sh\b`)
	reForkBomb   = regexp.MustCompile(`:\(\)\s*\{`)
	reURL        = regexp.MustCompile(`https?://[^\s'"]+`)
	reSensitive  = regexp.MustCompile(`authorized_keys|/etc/sudoers|\.bashrc|\.bash_profile|\.profile\b|\.zshrc|/etc/profile|/etc/systemd/system|/etc/ssh/sshd_config|/etc/passwd|/etc/shadow`)
	shells       = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "csh": true, "tcsh": true}
	interpreters = map[string]string{"python": "-c", "python3": "-c", "perl": "-e", "ruby": "-e", "node": "-e", "php": "-r"}
	elevators    = map[string]bool{"sudo": true, "doas": true, "su": true, "pkexec": true}
	diskTools    = map[string]bool{"wipefs": true, "fdisk": true, "sfdisk": true, "parted": true, "sgdisk": true, "gdisk": true, "cfdisk": true, "blkdiscard": true}
	securitySvc  = map[string]bool{"ssh": true, "sshd": true, "ufw": true, "firewalld": true, "apparmor": true, "auditd": true, "nftables": true, "iptables": true}
	systemDirs   = []string{"/", "/*", "~", "~/", "$HOME", "${HOME}", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/var", "/home", "/root", "/opt", "/srv", "/dev", "/proc", "/sys"}
)

// CheckCommand checks one argv. home is the server's home directory (it
// counts as a protected path), "" when unknown.
func CheckCommand(argv []string, home string) []Finding {
	var out []Finding
	add := func(refuse bool, rule, reason string) {
		for _, f := range out {
			if f.Rule == rule && f.Reason == reason {
				return
			}
		}
		out = append(out, Finding{Refuse: refuse, Rule: rule, Reason: reason})
	}
	joined := strings.Join(argv, " ")
	for _, a := range argv {
		if strings.Contains(a, "$(") || strings.Contains(a, "`") {
			add(true, RuleHidden, "参数里有命令替换，实际执行的内容无法事先展示")
		}
	}
	if reDecode.MatchString(joined) {
		add(true, RuleHidden, "命令会解码隐藏的内容再使用，实际执行的内容无法事先展示")
	}
	if rePipeShell.MatchString(joined) {
		add(true, RulePipeToShell, "网络内容经管道直接交给解释器执行")
	}
	if reForkBomb.MatchString(joined) {
		add(true, RuleDestroy, "fork 炸弹，会耗尽服务器资源")
	}
	for _, u := range reURL.FindAllString(joined, -1) {
		add(false, RuleDownload, "会访问 "+u)
	}
	if reSensitive.MatchString(joined) {
		add(false, RuleSensitive, "涉及敏感配置："+reSensitive.FindString(joined))
	}

	// Peel elevation and env wrappers to find the command itself.
	rest := argv
	for len(rest) > 0 {
		name := path.Base(rest[0])
		switch {
		case elevators[name]:
			add(false, RulePrivilege, "以管理员身份运行（"+name+"）")
			rest = skipOptions(rest[1:])
		case name == "env":
			rest = rest[1:]
			for len(rest) > 0 && (strings.Contains(rest[0], "=") || strings.HasPrefix(rest[0], "-")) {
				rest = rest[1:]
			}
		case name == "nohup" || name == "nice" || name == "time" || name == "command":
			rest = rest[1:]
		default:
			checkTool(name, rest[1:], home, add)
			return out
		}
	}
	return out
}

// skipOptions drops sudo-style options (and the values of -u/-g).
func skipOptions(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if args[0] == "--" {
			return args[1:]
		}
		if args[0] == "-u" || args[0] == "-g" {
			args = args[1:]
		}
		if len(args) > 0 {
			args = args[1:]
		}
	}
	return args
}

func checkTool(name string, args []string, home string, add func(bool, string, string)) {
	has := func(flags ...string) bool {
		for _, a := range args {
			for _, f := range flags {
				if a == f || (len(f) == 2 && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], f[1:])) {
					return true
				}
			}
		}
		return false
	}
	protected := func(a string) bool {
		c := strings.TrimRight(a, "/")
		if c == "" {
			c = "/"
		}
		for _, d := range systemDirs {
			if a == d || c == strings.TrimRight(d, "/") {
				return true
			}
		}
		return home != "" && (c == strings.TrimRight(home, "/") || c == path.Dir(strings.TrimRight(home, "/")))
	}
	inline := func(flag string) {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				if reInlineBad.MatchString(args[i+1]) {
					add(true, RuleHidden, "解释器执行的内联代码里有下载、解码或 eval，实际执行的内容无法事先展示")
				} else {
					add(false, RuleInline, "交给解释器执行一段内联代码")
				}
			}
		}
	}
	switch {
	case shells[name]:
		inline("-c")
	case interpreters[name] != "":
		inline(interpreters[name])
	case name == "rm":
		recursive := has("-r", "-R", "--recursive")
		if has("--no-preserve-root") {
			add(true, RuleDestroy, "要求删除根目录")
		}
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && recursive && protected(a) {
				add(true, RuleDestroy, "递归删除系统目录或家目录："+a)
			}
		}
		if recursive {
			add(false, RuleDelete, "递归删除")
		}
	case name == "chmod" || name == "chown" || name == "chgrp":
		if has("-R", "--recursive") {
			for _, a := range args {
				if !strings.HasPrefix(a, "-") && protected(a) {
					add(true, RuleDestroy, "递归改动系统目录或家目录的权限："+a)
				}
			}
		}
	case strings.HasPrefix(name, "mkfs") || name == "mke2fs" || name == "mkswap":
		add(true, RuleDestroy, "格式化磁盘")
	case diskTools[name]:
		add(true, RuleDestroy, "改动磁盘分区")
	case name == "dd" || name == "shred":
		for _, a := range args {
			if strings.HasPrefix(a, "of=/dev/") || strings.HasPrefix(a, "/dev/") {
				add(true, RuleDestroy, "直接写磁盘设备："+a)
			}
		}
	case name == "setenforce" && len(args) > 0 && (args[0] == "0" || strings.EqualFold(args[0], "permissive")):
		add(true, RuleSecurityOff, "关闭 SELinux")
	case name == "ufw" && len(args) > 0 && args[0] == "disable":
		add(true, RuleSecurityOff, "关闭防火墙")
	case name == "systemctl":
		stop := false
		for _, a := range args {
			if a == "stop" || a == "disable" || a == "mask" || a == "kill" {
				stop = true
			}
		}
		for _, a := range args {
			if stop && securitySvc[strings.TrimSuffix(a, ".service")] {
				add(true, RuleSecurityOff, "停用安全相关的服务："+a)
			}
		}
	case name == "passwd" && has("-d", "--delete"):
		add(true, RuleSecurityOff, "删除用户密码")
	}
}

// Refused reports whether any finding forbids running the command.
func Refused(fs []Finding) bool {
	for _, f := range fs {
		if f.Refuse {
			return true
		}
	}
	return false
}

// NeedsRiskSentence reports whether findings beyond privilege require the
// section 7 confirmation sentence.
func NeedsRiskSentence(fs []Finding) bool {
	for _, f := range fs {
		if !f.Refuse && f.Rule != RulePrivilege {
			return true
		}
	}
	return false
}

// RiskSentence is the section 7 confirmation sentence.
const RiskSentence = "我知道自己可能会面临安全风险，确认"
