package enroll

import (
	"context"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

const (
	discoverTimeout = 15 * time.Second
	systemdTimeout  = 5 * time.Second
	dialTimeout     = time.Second
	maxUnreadable   = 50
)

// ExpandArgv expands variables in every argv element.
func (e *Env) ExpandArgv(argv []string, vars map[string]string) ([]string, bool) {
	out := make([]string, len(argv))
	for i, a := range argv {
		v, ok := e.Expand(a, vars)
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// Scan resolves roots, classifies every file, and records provenance,
// services and entries. It only reads; nothing on the server is modified.
func Scan(ctx context.Context, e *Env, params enroll.ScanParams, previous *enroll.Record) (*enroll.Report, error) {
	ap := params.Profile.ForAgent(params.Name)
	p := &ap
	e = e.For(p).With(params.Env)
	previous.ApplyEntryPorts(p)
	if err := enroll.ValidInstanceID(params.InstanceID); err != nil {
		return nil, valise.New("instance_id_invalid", err.Error(), "用 --id 指定一个由小写字母、数字和连字符组成的 id")
	}
	if st, err := os.Stat(params.StateDir); err != nil || !st.IsDir() || !filepath.IsAbs(params.StateDir) {
		return nil, valise.New("state_dir_missing", "状态目录不存在："+params.StateDir, "重新接入，让 Valise 重新探测")
	}
	var extraRootIDs []string
	if previous != nil {
		for _, lr := range previous.LocalRoots {
			if lr.ID != "" {
				extraRootIDs = append(extraRootIDs, lr.ID)
			}
		}
	}
	for _, o := range params.Overrides {
		if err := o.ValidateWithRoots(p, extraRootIDs); err != nil {
			return nil, valise.New("override_invalid", "归类指令无效："+err.Error(), "格式为 --assign <root>:<glob>=<类别>")
		}
	}
	mergedOverrides := mergeOverrides(p, previous, extraRootIDs, params.Overrides)
	r := &enroll.Report{
		InstanceID: params.InstanceID, Name: params.Name, Env: params.Env, ProfileID: p.ID, ProfileVersion: p.ProfileVersion, ProfileStatus: p.Status,
		Categories: map[string]enroll.Tally{}, Secrets: []enroll.FileRef{}, SQLite: []enroll.SQLiteFile{},
		UnclassGroups: []enroll.UnclassifiedGroup{}, Service: []enroll.ServiceState{}, Entries: []enroll.EntryState{},
		Overrides: mergedOverrides, ScannedAt: time.Now().UTC(), Previous: previous,
	}
	if previous != nil {
		r.EntryPorts = previous.EntryPorts
	}
	r.Warnings = append(r.Warnings, e.Warnings...)
	r.Roots = resolveRoots(ctx, e, p, params.StateDir, &r.Warnings)
	nested := NestedInstances(e, p, params.InstanceID, params.StateDir)
	for _, n := range nested {
		r.Warnings = append(r.Warnings, "状态目录里嵌着另一个 agent："+n+"；它不会装进这个 agent 的备份，要单独接入")
	}
	var localSQLite []profile.SQLiteRef
	if previous != nil {
		localSQLite = previous.LocalSQLite
	}
	classify(r, p, mergedOverrides, localSQLite, nested)
	r.Provenance = Provenance(ctx, e, p, r.Roots)
	r.Service = services(ctx, e, p, &r.Warnings)
	r.Entries = entries(p)
	r.HarnessName, r.HarnessAgents = identity(ctx, e, p, params.StateDir, &r.Warnings)
	if previous != nil {
		r.Changes = enroll.Diff(*previous, enroll.RecordFrom(r, r.ScannedAt))
	}
	return r, nil
}

// mergeOverrides preserves valid overrides from a previous enrollment record
// before applying newly supplied overrides (which replace matching Root+Glob
// entries). Any stale previous override that no longer validates against p is
// dropped.
func mergeOverrides(p *profile.Profile, previous *enroll.Record, extraRootIDs []string, current []profile.Override) []profile.Override {
	if previous == nil || len(previous.LocalOverrides) == 0 {
		return current
	}
	byKey := make(map[[2]string]profile.Override, len(current))
	for _, o := range current {
		byKey[[2]string{o.Root, o.Glob}] = o
	}
	out := make([]profile.Override, 0, len(previous.LocalOverrides)+len(current))
	seen := make(map[[2]string]bool, len(previous.LocalOverrides)+len(current))
	for _, o := range previous.LocalOverrides {
		if o.ValidateWithRoots(p, extraRootIDs) != nil {
			continue
		}
		k := [2]string{o.Root, o.Glob}
		if seen[k] {
			continue
		}
		seen[k] = true
		if repl, ok := byKey[k]; ok {
			out = append(out, repl)
		} else {
			out = append(out, o)
		}
	}
	for _, o := range current {
		k := [2]string{o.Root, o.Glob}
		if !seen[k] {
			seen[k] = true
			out = append(out, o)
		}
	}
	return out
}

func resolveRoots(ctx context.Context, e *Env, p *profile.Profile, stateDir string, warns *[]string) []enroll.Root {
	roots := []enroll.Root{resolved(enroll.Root{ID: profile.StateRoot, Path: stateDir, Source: "detect", Exists: true})}
	vars := map[string]string{"STATE_DIR": stateDir}
	for _, rt := range p.Roots {
		res := enroll.Root{ID: rt.ID}
		if len(rt.DiscoverArgv) > 0 {
			if path, ok := discover(ctx, e, rt.DiscoverArgv, vars, stateDir); ok {
				res.Path, res.Source = path, "discover"
			} else {
				*warns = append(*warns, "无法通过 harness 命令读出 "+rt.ID+" 的实际路径，改用 profile 中的默认路径")
			}
		}
		if res.Path == "" {
			path, ok := e.Expand(rt.Default, vars)
			if !ok {
				*warns = append(*warns, rt.ID+" 的默认路径依赖未设置的环境变量，已跳过")
				continue
			}
			res.Path, res.Source = filepath.Clean(path), "default"
		}
		st, err := os.Stat(res.Path)
		res.Exists = err == nil && st.IsDir()
		roots = append(roots, resolved(res))
	}
	return roots
}

// resolved follows symlinks in a root's own path; symlinks inside the root
// are never followed.
func resolved(r enroll.Root) enroll.Root {
	if real, err := filepath.EvalSymlinks(r.Path); err == nil && real != r.Path {
		r.Resolved = real
	}
	return r
}

// discover runs discover_argv; stdout must be exactly one absolute path.
func discover(ctx context.Context, e *Env, argv []string, vars map[string]string, dir string) (string, bool) {
	args, ok := e.ExpandArgv(argv, vars)
	if !ok {
		return "", false
	}
	out, err := e.Run(ctx, discoverTimeout, dir, args...)
	if err != nil || out.Code != 0 {
		return "", false
	}
	path := strings.TrimSpace(string(out.Stdout))
	if !filepath.IsAbs(path) || strings.Contains(path, "\n") {
		return "", false
	}
	return filepath.Clean(path), true
}

// Item is one entry found by Walk.
type Item struct {
	Root string
	Rel  string // slash-separated, relative to the root
	Abs  string
	Info fs.FileInfo
	// Symlink is set for symbolic links (never followed).
	Symlink bool
	// SidecarOf is the declared SQLite database this file belongs to.
	SidecarOf string
	Match     profile.Match
	SQLite    *profile.SQLiteRef
}

// Walk visits every regular file and symlink of every existing root; a file
// belongs only to the deepest root containing it. Directories in skip
// (other instances nested inside, see NestedInstances) are left out.
// Unreadable paths are passed to bad.
func Walk(roots []enroll.Root, skip []string, c *profile.Classifier, fn func(Item), bad func(root, rel string)) {
	// Roots are walked at their resolved path; compare skips the same way.
	for _, d := range append([]string{}, skip...) {
		if r := realPath(d); r != d {
			skip = append(skip, r)
		}
	}
	for _, root := range roots {
		if !root.Exists {
			continue
		}
		var nested []string
		for _, other := range roots {
			if other.ID != root.ID && other.Exists && strings.HasPrefix(other.Dir(), root.Dir()+string(filepath.Separator)) {
				nested = append(nested, other.Dir())
			}
		}
		_ = filepath.WalkDir(root.Dir(), func(path string, d fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(root.Dir(), path)
			rel = filepath.ToSlash(rel)
			if err != nil {
				bad(root.ID, rel)
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				for _, n := range nested {
					if path == n {
						return fs.SkipDir
					}
				}
				if len(skip) > 0 && slices.ContainsFunc(skip, func(n string) bool { return path == n || realPath(path) == n }) {
					return fs.SkipDir
				}
				return nil
			}
			it := Item{Root: root.ID, Rel: rel, Abs: path}
			if d.Type()&fs.ModeSymlink != 0 {
				it.Symlink = true
				it.Match = c.Classify(root.ID, rel)
				fn(it)
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // sockets, fifos, devices
			}
			info, err := d.Info()
			if err != nil {
				bad(root.ID, rel)
				return nil
			}
			it.Info = info
			if db, ok := c.Sidecar(root.ID, rel); ok {
				it.SidecarOf = db
				fn(it)
				return nil
			}
			it.Match = c.Classify(root.ID, rel)
			if decl, ok := c.SQLite(root.ID, rel); ok {
				it.SQLite = &decl
			}
			fn(it)
			return nil
		})
	}
}

// classify tallies every file into the report.
func classify(r *enroll.Report, p *profile.Profile, overrides []profile.Override, localSQLite []profile.SQLiteRef, skip []string) {
	c := profile.NewClassifier(p, overrides).WithSQLite(localSQLite)
	groups := map[[2]string]*enroll.UnclassifiedGroup{}
	sqlite := map[enroll.FileRef]*enroll.SQLiteFile{}
	sidecars := map[enroll.FileRef][]string{}
	Walk(r.Roots, skip, c, func(it Item) {
		ref := enroll.FileRef{Root: it.Root, Path: it.Rel}
		switch {
		case it.Symlink:
			r.Symlinks++
			return
		case it.SidecarOf != "":
			dbRef := enroll.FileRef{Root: it.Root, Path: it.SidecarOf}
			sidecars[dbRef] = append(sidecars[dbRef], strings.TrimPrefix(it.Rel, it.SidecarOf))
			return
		case it.Match.Category == "":
			r.Unclassified.Files++
			r.Unclassified.Bytes += it.Info.Size()
			key := [2]string{it.Root, groupPrefix(it.Rel)}
			g := groups[key]
			if g == nil {
				g = &enroll.UnclassifiedGroup{Root: it.Root, Prefix: key[1]}
				groups[key] = g
			}
			g.Files++
			g.Bytes += it.Info.Size()
			return
		}
		t := r.Categories[it.Match.Category]
		t.Files++
		t.Bytes += it.Info.Size()
		r.Categories[it.Match.Category] = t
		if it.Match.Category == profile.CategorySecrets {
			r.Secrets = append(r.Secrets, ref)
		}
		if it.SQLite != nil {
			sqlite[ref] = &enroll.SQLiteFile{FileRef: ref, Category: it.Match.Category, SecretTables: it.SQLite.SecretTables}
		}
	}, func(root, rel string) {
		if len(r.Unreadable) < maxUnreadable {
			r.Unreadable = append(r.Unreadable, enroll.FileRef{Root: root, Path: rel})
		}
	})
	for ref, s := range sqlite {
		sort.Strings(sidecars[ref])
		s.Sidecars = sidecars[ref]
		r.SQLite = append(r.SQLite, *s)
	}
	sort.Slice(r.SQLite, func(i, j int) bool { return r.SQLite[i].Root+r.SQLite[i].Path < r.SQLite[j].Root+r.SQLite[j].Path })
	for _, g := range groups {
		r.UnclassGroups = append(r.UnclassGroups, *g)
	}
	sort.Slice(r.UnclassGroups, func(i, j int) bool {
		a, b := r.UnclassGroups[i], r.UnclassGroups[j]
		return a.Root+"/"+a.Prefix < b.Root+"/"+b.Prefix
	})
	sort.Slice(r.Secrets, func(i, j int) bool { return r.Secrets[i].Root+r.Secrets[i].Path < r.Secrets[j].Root+r.Secrets[j].Path })
}

// groupPrefix keeps the first two path segments ("a/b/c/d" -> "a/b/").
func groupPrefix(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) <= 2 {
		if len(parts) == 2 {
			return parts[0] + "/"
		}
		return rel
	}
	return parts[0] + "/" + parts[1] + "/"
}

func services(ctx context.Context, e *Env, p *profile.Profile, warns *[]string) []enroll.ServiceState {
	out := []enroll.ServiceState{}
	if p.Service == nil || len(p.Service.Candidates) == 0 {
		return out
	}
	for _, u := range p.Service.Candidates {
		s := enroll.ServiceState{Scope: u.Scope, Unit: u.Unit}
		res, err := e.Run(ctx, systemdTimeout, "", "systemctl", "--user", "show", u.Unit,
			"-p", "LoadState", "-p", "ActiveState", "-p", "UnitFileState")
		if err != nil {
			*warns = append(*warns, "无法查询 systemd 用户服务："+err.Error())
			out = append(out, s)
			continue
		}
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			switch k {
			case "LoadState":
				s.LoadState = v
			case "ActiveState":
				s.Active = v
			case "UnitFileState":
				s.Enabled = v
			}
		}
		out = append(out, s)
	}
	return out
}

func entries(p *profile.Profile) []enroll.EntryState {
	out := []enroll.EntryState{}
	for _, en := range p.Entries {
		s := enroll.EntryState{ID: en.ID, DisplayName: en.DisplayName, Host: en.Host, Port: en.Port}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(en.Host, strconv.Itoa(en.Port)), dialTimeout)
		if err == nil {
			s.Listening = true
			conn.Close()
		}
		out = append(out, s)
	}
	return out
}

// identity reads the names the harness gives its agents (profile identity).
func identity(ctx context.Context, e *Env, p *profile.Profile, stateDir string, warnings *[]string) (string, []string) {
	c := p.Identity
	if c == nil {
		return "", nil
	}
	argv, ok := e.ExpandArgv(c.Argv, map[string]string{"STATE_DIR": stateDir})
	if !ok {
		return "", nil
	}
	out, err := e.Run(ctx, versionTimeout, stateDir, argv...)
	if err != nil || out.Code != 0 {
		*warnings = append(*warnings, "没能读出 harness 里 agent 的名字")
		return "", nil
	}
	return profile.EvalIdentity(c, out.Stdout)
}
