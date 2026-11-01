// Package enroll implements detection, classification and provenance on the
// server. All harness knowledge comes from the profiles the client sends.
package enroll

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"valise/core/profile"
)

// Env is the environment harness commands run in: the user's login-shell
// environment, so user-level install prefixes are on PATH.
type Env struct {
	Home string
	// Vars is the login environment as KEY=VALUE.
	Vars     []string
	Warnings []string
}

// loginTimeout bounds reading the login environment.
const loginTimeout = 10 * time.Second

const envMarker = "__VALISE_ENV__"

// LoadEnv reads the login shell's environment once ($SHELL -lc). If that
// fails the porter's own environment is used and a warning is recorded.
// ~/.local/bin is always on PATH.
func LoadEnv(ctx context.Context, home string) *Env {
	e := &Env{Home: home}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-lc", `printf '\0`+envMarker+`\0'; env -0`)
	cmd.Dir = home
	cmd.Stdin = nil
	out, err := cmd.Output()
	if i := bytes.Index(out, []byte("\x00"+envMarker+"\x00")); err == nil && i >= 0 {
		for _, kv := range bytes.Split(out[i+len(envMarker)+2:], []byte{0}) {
			if s := string(kv); strings.Contains(s, "=") {
				e.Vars = append(e.Vars, s)
			}
		}
	} else {
		e.Vars = os.Environ()
		e.Warnings = append(e.Warnings, "无法读取登录 shell 的环境，改用非交互环境（用户级安装的命令可能找不到）")
	}
	local := filepath.Join(home, ".local", "bin")
	path := e.Get("PATH")
	if !containsPath(path, local) {
		e.Set("PATH", local+string(os.PathListSeparator)+path)
	}
	return e
}

// For returns the environment for running p's commands: p's own bin
// directories (detect.bin_dirs) in front of the login PATH.
func (e *Env) For(p *profile.Profile) *Env {
	if e == nil || p == nil || p.Detect == nil || len(p.Detect.BinDirs) == 0 {
		return e
	}
	c := &Env{Home: e.Home, Vars: append([]string{}, e.Vars...), Warnings: e.Warnings}
	var dirs []string
	for _, d := range p.Detect.BinDirs {
		if x, ok := e.Expand(d, map[string]string{"HOME": e.Home}); ok && filepath.IsAbs(x) {
			dirs = append(dirs, x)
		}
	}
	if len(dirs) > 0 {
		c.Set("PATH", strings.Join(append(dirs, e.Get("PATH")), string(os.PathListSeparator)))
	}
	return c
}

func containsPath(list, dir string) bool {
	for _, p := range filepath.SplitList(list) {
		if filepath.Clean(p) == dir {
			return true
		}
	}
	return false
}

// Lookup returns a variable and whether it is set.
func (e *Env) Lookup(key string) (string, bool) {
	for i := len(e.Vars) - 1; i >= 0; i-- {
		if k, v, _ := strings.Cut(e.Vars[i], "="); k == key {
			return v, true
		}
	}
	return "", false
}

// Get returns a variable or "".
func (e *Env) Get(key string) string {
	v, _ := e.Lookup(key)
	return v
}

// Set replaces a variable.
func (e *Env) Set(key, val string) {
	out := e.Vars[:0:0]
	for _, kv := range e.Vars {
		if k, _, _ := strings.Cut(kv, "="); k != key {
			out = append(out, kv)
		}
	}
	e.Vars = append(out, key+"="+val)
}

// LookPath searches the login PATH.
func (e *Env) LookPath(name string) (string, error) {
	if strings.Contains(name, "/") {
		return "", errors.New("command names must not contain /")
	}
	for _, dir := range filepath.SplitList(e.Get("PATH")) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// Output is the result of a command.
type Output struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// Run executes argv (argv[0] looked up on the login PATH) with a timeout.
// Commands come only from profile argv arrays or fixed tool invocations.
func (e *Env) Run(ctx context.Context, timeout time.Duration, dir string, argv ...string) (*Output, error) {
	bin := argv[0]
	if !filepath.IsAbs(bin) {
		p, err := e.LookPath(bin)
		if err != nil {
			return nil, err
		}
		bin = p
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Env = e.Vars
	cmd.Dir = dir
	if cmd.Dir == "" {
		cmd.Dir = e.Home
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return nil, err
	}
	return &Output{Stdout: out.Bytes(), Stderr: errb.Bytes(), Code: cmd.ProcessState.ExitCode()}, nil
}

var varRe = regexp.MustCompile(`\$\{([^}]*)\}`)

// Expand substitutes ${HOME}, ${STATE_DIR}, ${OUT} and ${env:NAME}. It
// returns false if an ${env:NAME} is unset, which makes the entry absent.
func (e *Env) Expand(s string, vars map[string]string) (string, bool) {
	ok := true
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if env, isEnv := strings.CutPrefix(name, "env:"); isEnv {
			v, set := e.Lookup(env)
			if !set || v == "" {
				ok = false
			}
			return v
		}
		if name == "HOME" {
			return e.Home
		}
		v, set := vars[name]
		if !set {
			ok = false
		}
		return v
	})
	return out, ok
}

// firstLine returns the first non-empty line, trimmed and bounded.
func firstLine(b []byte) string {
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 200 {
				l = l[:200]
			}
			return l
		}
	}
	return ""
}
