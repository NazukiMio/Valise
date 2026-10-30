package enroll

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
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
		if c, ok := detectOne(ctx, e, p); ok {
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

func detectOne(ctx context.Context, e *Env, p *profile.Profile) (enroll.Candidate, bool) {
	c := enroll.Candidate{ProfileID: p.ID, Markers: []string{}}
	for _, tmpl := range p.Detect.StateDirCandidates {
		dir, ok := e.Expand(tmpl, nil)
		if !ok {
			continue
		}
		dir = filepath.Clean(dir)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		var markers []string
		for _, m := range p.Detect.MarkersAny {
			if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(m))); err == nil {
				markers = append(markers, m)
			}
		}
		if len(p.Detect.MarkersAny) > 0 && len(markers) == 0 {
			continue
		}
		c.StateDir = dir
		if markers != nil {
			c.Markers = markers
		}
		break
	}
	if c.StateDir == "" {
		return c, false
	}
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
	return c, true
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
