// Package install runs the server side of installing a harness
// (docs/INSTALL.md): checking requirements, and running fixed commands with
// the login environment.
package install

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"time"

	"valise/core/install"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/runtimeguard"
)

// Check reports each requirement as found on the login PATH.
func Check(ctx context.Context, e *porterenroll.Env, p install.CheckParams) install.CheckResult {
	res := install.CheckResult{Requirement: []install.Requirement{}}
	res.OSID, res.OSVersion = OSRelease("/etc/os-release")
	var st syscall.Statfs_t
	if syscall.Statfs(e.Home, &st) == nil {
		res.FreeBytes = uint64(st.Bavail) * uint64(st.Bsize)
	}
	for _, r := range p.Requires {
		q := install.Requirement{Binary: r.Binary, Need: r.Version}
		path, err := e.LookPath(r.Binary)
		if err != nil {
			q.Reason = "没有找到 " + r.Binary
			res.Requirement = append(res.Requirement, q)
			continue
		}
		q.Path = path
		if out, err := e.Run(ctx, 15*time.Second, "", path, "--version"); err == nil {
			q.Version = install.ParseVersion(string(out.Stdout) + " " + string(out.Stderr))
		}
		q.OK = install.Satisfies(q.Version, r.Version)
		if !q.OK {
			q.Reason = r.Binary + " 的版本是 " + orUnknown(q.Version) + "，需要 " + r.Version
		}
		res.Requirement = append(res.Requirement, q)
	}
	return res
}

func orUnknown(v string) string {
	if v == "" {
		return "未知"
	}
	return v
}

// OSRelease reads ID and VERSION_ID.
func OSRelease(path string) (id, version string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			id = v
		case "VERSION_ID":
			version = v
		}
	}
	return id, version
}

// Exec replaces the porter with argv, run in HOME with the login
// environment ("${HOME}" and "${USER}" expanded). It is used for a profile's native CLI,
// install steps and port changes, over an admin connection only (the daily
// key's forced command never allows it). It returns only on failure.
func Exec(ctx context.Context, e *porterenroll.Env, argv []string) error {
	if err := runtimeguard.Check(ctx, e.Home, runtimeguard.Scope{All: true}); err != nil {
		return err
	}
	bin, out, err := resolve(e, argv)
	if err != nil {
		return err
	}
	if err := os.Chdir(e.Home); err != nil {
		return err
	}
	return syscall.Exec(bin, out, e.Vars)
}

// resolve expands argv and finds its program on the login PATH.
func resolve(e *porterenroll.Env, argv []string) (string, []string, error) {
	if len(argv) == 0 {
		return "", nil, errors.New("nothing to run")
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(a, "${HOME}", e.Home), "${USER}", e.Get("USER"))
	}
	bin := out[0]
	if !strings.HasPrefix(bin, "/") {
		p, err := e.LookPath(bin)
		if err != nil {
			return "", nil, errors.New("在登录环境的 PATH 里找不到 " + bin)
		}
		bin = p
	}
	return bin, out, nil
}
