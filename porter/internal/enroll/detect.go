package enroll

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
)

// versionTimeout bounds version_argv.
const versionTimeout = 15 * time.Second

// Detect finds instances of the top-level profiles. Child profiles are
// resolved by the client after scanning (their when conditions are checked
// by Scan).
func Detect(ctx context.Context, e *Env, profiles []profile.Profile) enroll.DetectResult {
	res := enroll.DetectResult{Candidates: []enroll.Candidate{}}
	for i := range profiles {
		p := &profiles[i]
		if p.Extends != "" || p.Detect == nil {
			continue
		}
		for _, c := range detectAll(ctx, e.For(p), p) {
			for j := range profiles {
				ch := &profiles[j]
				if ch.Extends == p.ID && ch.When != nil && ch.When.Exists != nil {
					if ch.When.Exists.Root != profile.StateRoot {
						c.Warnings = append(c.Warnings, "子 profile "+ch.ID+" 的生效条件不在 state root 下，暂不支持判断")
					} else if globExists(c.StateDir, ch.When.Exists.Glob) {
						c.Children = append(c.Children, ch.ID)
					}
				}
			}
			res.Candidates = append(res.Candidates, c)
		}
	}
	return res
}

// harnessHome is what ${HOME} stands for in p's detect templates: the
// harness's own home variable when set (a leading ~ is the user's home).
func harnessHome(e *Env, p *profile.Profile) string {
	if p.Detect.HomeEnv != "" {
		if v, ok := e.Lookup(p.Detect.HomeEnv); ok && strings.TrimSpace(v) != "" {
			v = strings.TrimSpace(v)
			if v == "~" {
				return e.Home
			}
			if rest, ok := strings.CutPrefix(v, "~/"); ok {
				return filepath.Join(e.Home, rest)
			}
			return v
		}
	}
	return e.Home
}

func (e *Env) expandHome(tmpl, home string) (string, bool) {
	return e.Expand(strings.ReplaceAll(tmpl, "${HOME}", home), nil)
}

// stateDirOK: dir exists and holds a marker (or, without markers, more than
// what a skills installer or old logs leave behind).
func stateDirOK(dir string, p *profile.Profile) ([]string, bool) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, false
	}
	if len(p.Detect.MarkersAny) == 0 {
		return []string{}, agentDir(dir)
	}
	var markers []string
	for _, m := range p.Detect.MarkersAny {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(m))); err == nil {
			markers = append(markers, m)
		}
	}
	return markers, len(markers) > 0
}

// leftovers are folders a harness writes as it runs and leaves behind; a
// folder holding only these and skills/ is no sign of an agent.
var leftovers = map[string]bool{"skills": true, "logs": true, "log": true, "debug": true, "diagnostics": true, "cache": true, ".DS_Store": true}

func agentDir(dir string) bool {
	es, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range es {
		if !leftovers[e.Name()] {
			return true
		}
	}
	return false
}

// namedEnv is what selects a named instance for the harness's commands.
func namedEnv(p *profile.Profile, name, dir string) map[string]string {
	for _, n := range p.Detect.Named {
		if len(n.Env) == 0 {
			continue
		}
		out := map[string]string{}
		for k, v := range n.Env {
			out[k] = strings.ReplaceAll(strings.ReplaceAll(v, "${NAME}", name), "${STATE_DIR}", dir)
		}
		return out
	}
	return nil
}

// NamedDirs lists p's named instances: name → state dir.
func NamedDirs(e *Env, p *profile.Profile) map[string]string {
	out := map[string]string{}
	if p.Detect == nil {
		return out
	}
	home := harnessHome(e, p)
	for _, n := range p.Detect.Named {
		g, ok := e.expandHome(n.Glob, home)
		if !ok {
			continue
		}
		matches, _ := filepath.Glob(filepath.Clean(g))
		before, after, _ := strings.Cut(filepath.Base(g), "*")
		for _, m := range matches {
			name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), before), after)
			if !namedRe.MatchString(name) {
				continue
			}
			if _, ok := stateDirOK(m, p); ok {
				out[name] = m
			}
		}
	}
	return out
}

var namedRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// NamedLocation reports whether dir is where the profile keeps a named
// instance (a detect.named glob with a valid name), whether or not the
// directory exists yet.
func NamedLocation(e *Env, p *profile.Profile, dir string) bool {
	if p.Detect == nil {
		return false
	}
	home := harnessHome(e, p)
	dir = filepath.Clean(dir)
	for _, n := range p.Detect.Named {
		g, ok := e.expandHome(n.Glob, home)
		if !ok {
			continue
		}
		g = filepath.Clean(g)
		if m, err := filepath.Match(g, dir); err != nil || !m {
			continue
		}
		before, after, _ := strings.Cut(filepath.Base(g), "*")
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(dir), before), after)
		if namedRe.MatchString(name) {
			return true
		}
	}
	return false
}

func isLink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// realPath resolves symlinks so one directory is one instance.
func realPath(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}

// detectAll finds every instance of p: the first candidate directory that
// holds a marker, then each named instance; one directory is one instance.
func detectAll(ctx context.Context, e *Env, p *profile.Profile) []enroll.Candidate {
	home := harnessHome(e, p)
	var out []enroll.Candidate
	seen := map[string]bool{}
	add := func(name, dir string, markers []string) {
		if seen[realPath(dir)] {
			return
		}
		seen[realPath(dir)] = true
		if markers == nil {
			markers = []string{}
		}
		c := enroll.Candidate{ProfileID: p.ID, Name: name, StateDir: dir, Markers: markers}
		if name != "" {
			c.Env = namedEnv(p, name, dir)
		}
		out = append(out, c)
	}
	for _, tmpl := range p.Detect.StateDirCandidates {
		dir, ok := e.expandHome(tmpl, home)
		if !ok {
			continue
		}
		dir = filepath.Clean(dir)
		if markers, ok := stateDirOK(dir, p); ok {
			add("", dir, markers)
			break
		}
	}
	named := NamedDirs(e, p)
	names := make([]string, 0, len(named))
	for n := range named {
		names = append(names, n)
	}
	// Real directories first, so a symlink to one is the duplicate.
	sort.Slice(names, func(i, j int) bool {
		li, lj := isLink(named[names[i]]), isLink(named[names[j]])
		if li != lj {
			return !li
		}
		return names[i] < names[j]
	})
	for _, n := range names {
		markers, _ := stateDirOK(named[n], p)
		add(n, named[n], markers)
	}
	for i := range out {
		describe(ctx, e.With(out[i].Env), p, &out[i])
	}
	return out
}

// describe adds the binary and version to a candidate.
func describe(ctx context.Context, e *Env, p *profile.Profile, c *enroll.Candidate) {
	for _, b := range p.Detect.Binaries {
		if path, err := e.LookPath(b); err == nil {
			c.Binary = path
			break
		}
	}
	if c.Binary == "" && len(p.Detect.Binaries) > 0 {
		c.Warnings = append(c.Warnings, "在登录 PATH 中找不到 "+p.Detect.Binaries[0]+"，无法读取版本与来源")
	}
	if c.Binary != "" && len(p.Detect.VersionArgv) > 0 {
		argv, ok := e.ExpandArgv(p.Detect.VersionArgv, map[string]string{"STATE_DIR": c.StateDir})
		var out *Output
		var err error
		if ok {
			out, err = e.Run(ctx, versionTimeout, c.StateDir, argv...)
		}
		switch {
		case !ok:
			c.Warnings = append(c.Warnings, "version_argv 依赖未设置的环境变量")
		case err != nil:
			c.Warnings = append(c.Warnings, "读取版本失败："+err.Error())
		case out.Code != 0:
			c.Warnings = append(c.Warnings, "读取版本的命令以非零状态退出")
		default:
			c.Version = firstLine(out.Stdout)
		}
	}
}

// globExists reports whether any file under dir matches glob. The walk
// starts at the glob's leading literal segments.
func globExists(dir, glob string) bool {
	segs := strings.Split(glob, "/")
	base := dir
	for len(segs) > 1 && profile.IsLiteral(segs[0]) && segs[0] != "**" {
		base = filepath.Join(base, segs[0])
		segs = segs[1:]
	}
	found := false
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return fs.SkipDir
		}
		if rel, rerr := filepath.Rel(dir, path); rerr == nil && !d.IsDir() && profile.MatchGlob(glob, filepath.ToSlash(rel)) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// NestedInstances lists the other instances inside stateDir: p's named
// instances and other enrolled instances whose state dir lies under it.
// They are never sealed into this instance (docs/PROFILES.md 2, 多实例).
func NestedInstances(e *Env, p *profile.Profile, self, stateDir string) []string {
	base := realPath(filepath.Clean(stateDir)) + string(filepath.Separator)
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		if dir == "" {
			return
		}
		dir = realPath(filepath.Clean(dir))
		if strings.HasPrefix(dir, base) && !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	for _, dir := range NamedDirs(e, p) {
		add(dir)
	}
	if recs, err := ListRecords(e.Home); err == nil {
		for _, r := range recs {
			if r.InstanceID != self && r.Superseded == nil {
				if dir := r.Roots[profile.StateRoot]; dir != "" {
					if _, err := os.Lstat(dir); err == nil {
						add(dir)
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
