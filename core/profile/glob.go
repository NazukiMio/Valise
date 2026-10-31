package profile

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

// ValidateGlob checks a root-relative glob: segments separated by "/", "**"
// matches zero or more segments, other segments use path.Match syntax.
func ValidateGlob(g string) error {
	if g == "" {
		return errors.New("glob 不能为空")
	}
	if strings.HasPrefix(g, "/") {
		return errors.New("glob 必须相对于 root，不能是绝对路径")
	}
	if strings.Contains(g, "${") {
		return errors.New("glob 中不能使用变量")
	}
	for _, seg := range strings.Split(g, "/") {
		switch {
		case seg == "":
			return errors.New("glob 中不能有空路径段")
		case seg == "." || seg == "..":
			return errors.New("glob 中不能出现 . 或 ..")
		case seg == "**":
		case strings.Contains(seg, "**"):
			return errors.New("** 必须单独构成一个路径段")
		default:
			if _, err := path.Match(seg, ""); err != nil {
				return errors.New("glob 语法错误")
			}
		}
	}
	return nil
}

// IsLiteral reports whether g contains no wildcard.
func IsLiteral(g string) bool {
	return !strings.ContainsAny(g, `*?[\`)
}

// MatchGlob reports whether the slash-separated relative path p matches g.
// g must be valid per ValidateGlob.
func MatchGlob(g, p string) bool {
	var ps []string
	if p != "" {
		ps = strings.Split(p, "/")
	}
	return matchSegs(strings.Split(g, "/"), ps)
}

func matchSegs(g, p []string) bool {
	for len(g) > 0 {
		if g[0] == "**" {
			for len(g) > 0 && g[0] == "**" {
				g = g[1:]
			}
			if len(g) == 0 {
				return true
			}
			for i := 0; i <= len(p); i++ {
				if matchSegs(g, p[i:]) {
					return true
				}
			}
			return false
		}
		if len(p) == 0 {
			return false
		}
		if ok, err := path.Match(g[0], p[0]); err != nil || !ok {
			return false
		}
		g, p = g[1:], p[1:]
	}
	return len(p) == 0
}

var (
	varRe     = regexp.MustCompile(`\$\{([^}]*)\}`)
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Template variables, see docs/PROFILES.md section 2.
const (
	VarHome     = "HOME"
	VarStateDir = "STATE_DIR"
	VarOut      = "OUT"
	VarPorter   = "PORTER"
	VarInstance = "INSTANCE"
	VarToken    = "TOKEN"
)

// checkVars validates every ${...} in s against the allowed names. env:NAME
// is always allowed.
func checkVars(s string, allowed ...string) error {
	rest := varRe.ReplaceAllString(s, "")
	if strings.Contains(rest, "${") {
		return errors.New("变量缺少右花括号")
	}
	for _, m := range varRe.FindAllStringSubmatch(s, -1) {
		name := m[1]
		if env, ok := strings.CutPrefix(name, "env:"); ok {
			if !envNameRe.MatchString(env) {
				return errors.New("环境变量名不合法：" + env)
			}
			continue
		}
		ok := false
		for _, a := range allowed {
			if name == a {
				ok = true
			}
		}
		if !ok {
			return errors.New("此处不允许使用变量 ${" + name + "}")
		}
	}
	return nil
}

// validateBaseTemplate checks a path template that must start with one of
// the allowed base variables (or ${env:NAME}) followed by an optional
// relative path without "..".
func validateBaseTemplate(s string, bases ...string) error {
	if s == "" {
		return errors.New("路径不能为空")
	}
	if err := checkVars(s, bases...); err != nil {
		return err
	}
	loc := varRe.FindStringIndex(s)
	if loc == nil || loc[0] != 0 {
		return errors.New("路径必须以 ${" + strings.Join(bases, "}、${") + "} 或 ${env:…} 开头")
	}
	rest := s[loc[1]:]
	if strings.Contains(rest, "${") {
		return errors.New("变量只能出现在路径开头")
	}
	if rest == "" {
		return nil
	}
	if !strings.HasPrefix(rest, "/") {
		return errors.New("变量之后必须是 / 分隔的相对路径")
	}
	return validateRelPath(strings.TrimPrefix(rest, "/"), true)
}

// validateRelPath checks a plain relative path. A trailing slash is allowed
// when dirOK is set.
func validateRelPath(p string, dirOK bool) error {
	if p == "" {
		return errors.New("路径不能为空")
	}
	if strings.HasPrefix(p, "/") {
		return errors.New("路径必须是相对路径")
	}
	if dirOK {
		p = strings.TrimSuffix(p, "/")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return errors.New("路径中不能有空段、. 或 ..")
		}
	}
	return nil
}
